package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// An MCP server is a tool whose actions are not typed in but asked for. The
// server is the authority on what it offers, so the actions here are a cached
// copy of its answer to tools/list, refreshed on demand rather than guessed.
//
// This speaks Streamable HTTP - one endpoint, JSON-RPC in the body - because
// that is what a server reachable over the network offers. A stdio server runs
// as a child process and is a different problem entirely; it is not supported.
const (
	KindHTTP = "http"
	KindMCP  = "mcp"

	mcpProtocolVersion = "2025-06-18"
)

// The versions this client can speak. The message set it uses - initialize,
// initialized, tools/list, tools/call - is identical across all three, so an
// older server is talked to in its own version rather than refused.
//
// Anything outside this set is refused rather than attempted: the spec asks a
// client that does not support the server's version to disconnect, and a
// hopeful guess against an unknown protocol fails later and less clearly.
var mcpSupportedVersions = map[string]bool{
	"2024-11-05": true,
	"2025-03-26": true,
	"2025-06-18": true,
}

// errMCPSessionGone is a 404 answered against a session id. The spec says a
// server may expire a session at any time and that the client must then start a
// new one, which is a different thing from the call having failed.
var errMCPSessionGone = errors.New("mcp session expired")

// mcpSession is one initialised conversation with a server: the id it wants
// carried, and the version it agreed to speak. A stateless server returns no
// id, and then the header is simply not sent.
type mcpSession struct {
	id      string
	version string
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	// A request carries an id and a notification does not, and the difference
	// is not cosmetic: a server answers the first and must not answer the
	// second. Pointer, so that "no id" can be expressed at all.
	ID     *int   `json:"id,omitempty"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// mcpRequest builds and sends one message. Whether a reply is read is the
// caller's business: a notification is acknowledged with 202 and an empty body.
func (repository *Repository) mcpRequest(ctx context.Context, tool Tool, session *mcpSession, message rpcRequest) (*http.Response, error) {
	if err := repository.egress.CheckEgress(tool.BaseURL); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(message)
	if err != nil {
		return nil, ErrCallFailed
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, tool.BaseURL, bytes.NewReader(payload))
	if err != nil {
		return nil, ErrCallFailed
	}
	request.Header.Set("Content-Type", "application/json")
	// A server may answer either as JSON or as a one-event stream; saying we
	// take both is what the transport asks for.
	request.Header.Set("Accept", "application/json, text/event-stream")
	if session != nil {
		// The negotiated version, not the one we asked for: the server may have
		// answered with an older one, and every later message is in that.
		if session.version != "" {
			request.Header.Set("MCP-Protocol-Version", session.version)
		}
		if session.id != "" {
			request.Header.Set("Mcp-Session-Id", session.id)
		}
	}
	if err := repository.authorise(ctx, tool, request); err != nil {
		return nil, err
	}

	response, err := repository.client().Do(request)
	if err != nil {
		return nil, ErrCallFailed
	}
	if response.StatusCode == http.StatusNotFound && session != nil && session.id != "" {
		response.Body.Close()
		return nil, errMCPSessionGone
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, ErrCallFailed
	}
	return response, nil
}

// callMCP sends one JSON-RPC request and returns the raw result.
func (repository *Repository) callMCP(ctx context.Context, tool Tool, session *mcpSession, method string, params any) (json.RawMessage, string, error) {
	id := 1
	response, err := repository.mcpRequest(ctx, tool, session, rpcRequest{
		JSONRPC: "2.0", ID: &id, Method: method, Params: params,
	})
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(response.Body, MaxResponseBytes+1))
	if err != nil {
		return nil, "", ErrCallFailed
	}
	body := extractRPCBody(string(raw))

	var decoded struct {
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		return nil, "", ErrCallFailed
	}
	if decoded.Error != nil {
		return nil, "", fmt.Errorf("%s", decoded.Error.Message)
	}
	return decoded.Result, response.Header.Get("Mcp-Session-Id"), nil
}

// notifyMCP sends a message the server must not answer. The body is drained and
// discarded: a notification is acknowledged with 202 and nothing in it.
func (repository *Repository) notifyMCP(ctx context.Context, tool Tool, session *mcpSession, method string, params any) error {
	response, err := repository.mcpRequest(ctx, tool, session, rpcRequest{
		JSONRPC: "2.0", Method: method, Params: params,
	})
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	return nil
}

// extractRPCBody unwraps the answer when it arrives as an event stream rather
// than plain JSON. Both are allowed by the transport, and which one you get
// depends on the server.
func extractRPCBody(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "{") {
		return trimmed
	}
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if after, found := strings.CutPrefix(line, "data:"); found {
			return strings.TrimSpace(after)
		}
	}
	return trimmed
}

// openMCP performs the handshake: initialize, check what version came back,
// then say we are initialized.
//
// That last message is not optional, and leaving it out was this client's bug.
// The spec requires it before any other request, and a server built on the
// official SDKs enforces it - so every tools/list arrived before the server
// considered itself open for business, and was refused.
func (repository *Repository) openMCP(ctx context.Context, tool Tool) (*mcpSession, error) {
	result, id, err := repository.callMCP(ctx, tool, &mcpSession{version: mcpProtocolVersion}, "initialize", map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "cosmo", "version": "1"},
	})
	if err != nil {
		return nil, err
	}

	var handshake struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(result, &handshake); err != nil {
		return nil, ErrCallFailed
	}
	// An empty answer is read as the version we asked for: some servers omit it
	// when they agree, and refusing them would be stricter than the spec.
	agreed := strings.TrimSpace(handshake.ProtocolVersion)
	if agreed == "" {
		agreed = mcpProtocolVersion
	}
	if !mcpSupportedVersions[agreed] {
		return nil, fmt.Errorf("MCP server dùng phiên bản %s, Cosmo chưa hỗ trợ", agreed)
	}

	session := &mcpSession{id: id, version: agreed}
	if err := repository.notifyMCP(ctx, tool, session, "notifications/initialized", nil); err != nil {
		return nil, err
	}
	return session, nil
}

// closeMCP ends the session. A server that keeps per-session state has no other
// way to learn we are done; one that does not allow this answers 405, which is
// why the outcome is not checked.
func (repository *Repository) closeMCP(ctx context.Context, tool Tool, session *mcpSession) {
	if session == nil || session.id == "" {
		return
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, tool.BaseURL, nil)
	if err != nil {
		return
	}
	request.Header.Set("Mcp-Session-Id", session.id)
	if session.version != "" {
		request.Header.Set("MCP-Protocol-Version", session.version)
	}
	if err := repository.authorise(ctx, tool, request); err != nil {
		return
	}
	response, err := repository.client().Do(request)
	if err != nil {
		return
	}
	response.Body.Close()
}

// DiscoverMCP asks the server what it offers and turns the answer into actions.
// The server's own schema is kept as the parameter list, flattened to what an
// action can express: a name, a type, and whether it is required.
func (repository *Repository) DiscoverMCP(ctx context.Context, tool Tool) ([]Action, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	session, err := repository.openMCP(ctx, tool)
	if err != nil {
		return nil, err
	}
	defer repository.closeMCP(ctx, tool, session)

	discovered := []Action{}
	cursor := ""
	// tools/list is paginated. Reading only the first page loses whatever a
	// server chose to put on the second, and says nothing about having done so.
	for page := 0; page < 20; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		result, _, err := repository.callMCP(ctx, tool, session, "tools/list", params)
		if err != nil {
			return nil, err
		}

		var listed struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				InputSchema struct {
					Properties map[string]struct {
						Type        string `json:"type"`
						Description string `json:"description"`
					} `json:"properties"`
					Required []string `json:"required"`
				} `json:"inputSchema"`
			} `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(result, &listed); err != nil {
			return nil, ErrCallFailed
		}

		for _, entry := range listed.Tools {
			name, err := ValidateActionName(entry.Name)
			if err != nil {
				continue
			}
			description, err := ValidateDescription(entry.Description)
			if err != nil {
				description = ""
			}
			required := map[string]bool{}
			for _, field := range entry.InputSchema.Required {
				required[field] = true
			}
			parameters := []Parameter{}
			for field, schema := range entry.InputSchema.Properties {
				parameters = append(parameters, Parameter{
					Name:        field,
					Description: schema.Description,
					Type:        schema.Type,
					// Everything an MCP server takes travels in the arguments
					// object, so nothing is a query or a path parameter here.
					In:         "body",
					IsRequired: required[field],
				})
			}
			cleaned, err := CleanParameters(parameters)
			if err != nil {
				continue
			}
			discovered = append(discovered, Action{
				Name:        name,
				Description: description,
				Method:      "POST",
				Path:        "/",
				Parameters:  cleaned,
			})
			if len(discovered) >= MaxActions {
				return discovered, nil
			}
		}
		cursor = listed.NextCursor
		if cursor == "" {
			break
		}
	}
	return discovered, nil
}

// invokeMCP calls one of the server's tools. The reply is text content, which
// is what a model can read; anything else the server returns is passed through
// as its JSON so nothing is silently dropped.
func (repository *Repository) invokeMCP(ctx context.Context, tool Tool, action Action, arguments map[string]any) (CallResult, error) {
	started := time.Now()
	session, err := repository.openMCP(ctx, tool)
	if err != nil {
		return CallResult{}, err
	}
	defer func() { repository.closeMCP(ctx, tool, session) }()

	params := map[string]any{"name": action.Name, "arguments": arguments}
	result, _, err := repository.callMCP(ctx, tool, session, "tools/call", params)
	if errors.Is(err, errMCPSessionGone) {
		// The server dropped the session between the handshake and the call.
		// One fresh session, one retry; a second failure is a real failure.
		session, err = repository.openMCP(ctx, tool)
		if err != nil {
			return CallResult{}, err
		}
		result, _, err = repository.callMCP(ctx, tool, session, "tools/call", params)
	}
	if err != nil {
		return CallResult{}, err
	}

	var content struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	body := string(result)
	if err := json.Unmarshal(result, &content); err == nil && len(content.Content) > 0 {
		var parts []string
		for _, item := range content.Content {
			if item.Type == "text" {
				parts = append(parts, item.Text)
			}
		}
		if len(parts) > 0 {
			body = strings.Join(parts, "\n")
		}
	}
	status := http.StatusOK
	if content.IsError {
		status = http.StatusBadGateway
	}
	truncated := len(body) > MaxResponseBytes
	if truncated {
		body = body[:MaxResponseBytes]
	}
	return CallResult{
		Status:      status,
		DurationMS:  time.Since(started).Milliseconds(),
		Body:        body,
		IsTruncated: truncated,
	}, nil
}
