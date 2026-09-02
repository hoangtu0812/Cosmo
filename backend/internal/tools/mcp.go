package tools

import (
	"bytes"
	"context"
	"encoding/json"
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

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// call sends one JSON-RPC request and returns the raw result. The same egress
// guard applies as for a plain HTTP tool: an MCP endpoint is still a URL a
// user chose.
func (repository *Repository) callMCP(ctx context.Context, tool Tool, method string, params any, sessionID string) (json.RawMessage, string, error) {
	if err := repository.egress.CheckEgress(tool.BaseURL); err != nil {
		return nil, "", err
	}
	payload, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: 1, Method: method, Params: params})
	if err != nil {
		return nil, "", ErrCallFailed
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, tool.BaseURL, bytes.NewReader(payload))
	if err != nil {
		return nil, "", ErrCallFailed
	}
	request.Header.Set("Content-Type", "application/json")
	// A server may answer either as JSON or as a one-event stream; saying we
	// take both is what the transport asks for.
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", mcpProtocolVersion)
	if sessionID != "" {
		request.Header.Set("Mcp-Session-Id", sessionID)
	}
	if tool.AuthType != AuthNone {
		secret, err := repository.secretFor(ctx, tool.ID)
		if err != nil {
			return nil, "", err
		}
		if secret != "" {
			switch tool.AuthType {
			case AuthBearer:
				request.Header.Set("Authorization", "Bearer "+secret)
			case AuthHeader:
				request.Header.Set(tool.AuthHeaderName, secret)
			}
		}
	}

	response, err := repository.client().Do(request)
	if err != nil {
		return nil, "", ErrCallFailed
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", ErrCallFailed
	}

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

// openMCP performs the handshake and returns the session the server wants
// subsequent calls to carry. Servers that do not use sessions return nothing,
// which is fine: the header is then simply not sent.
func (repository *Repository) openMCP(ctx context.Context, tool Tool) (string, error) {
	_, session, err := repository.callMCP(ctx, tool, "initialize", map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "cosmo", "version": "1"},
	}, "")
	return session, err
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
	result, _, err := repository.callMCP(ctx, tool, "tools/list", map[string]any{}, session)
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
	}
	if err := json.Unmarshal(result, &listed); err != nil {
		return nil, ErrCallFailed
	}

	discovered := []Action{}
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
		return CallResult{}, ErrCallFailed
	}
	result, _, err := repository.callMCP(ctx, tool, "tools/call", map[string]any{
		"name":      action.Name,
		"arguments": arguments,
	}, session)
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
