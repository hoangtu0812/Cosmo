package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// An MCP server is a tool whose actions are discovered from the server rather
// than entered by hand. MCP is kept behind this boundary so the rest of Cosmo
// does not depend on one particular MCP server or its business domain.
const (
	KindHTTP = "http"
	KindMCP  = "mcp"
)

// mcpAuthorisingTransport attaches the credential configured for this Cosmo
// tool to every HTTP request made by the MCP SDK. This preserves Cosmo's
// existing bearer, custom-header, OAuth client-credentials and OBO providers
// without putting provider-specific logic into the MCP protocol client.
type mcpAuthorisingTransport struct {
	repository *Repository
	tool       Tool
	base       http.RoundTripper
}

func (transport *mcpAuthorisingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	copy.Header = request.Header.Clone()
	if err := transport.repository.authorise(copy.Context(), transport.tool, copy); err != nil {
		return nil, err
	}

	response, err := transport.base.RoundTrip(copy)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		response.Body.Close()
		return nil, ErrToolUnauthorized
	}
	return response, nil
}

// openMCP uses the official SDK for protocol negotiation. The SDK starts with
// the current 2026-07-28 stateless server/discover flow and falls back to the
// initialize/initialized lifecycle for older servers, including 2025-06-18.
func (repository *Repository) openMCP(ctx context.Context, tool Tool) (*mcpsdk.ClientSession, error) {
	if err := repository.egress.CheckEgress(tool.BaseURL); err != nil {
		return nil, err
	}

	httpClient := repository.client()
	base := httpClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	httpClient.Transport = &mcpAuthorisingTransport{
		repository: repository,
		tool:       tool,
		base:       base,
	}

	client := mcpsdk.NewClient(&mcpsdk.Implementation{
		Name:    "cosmo",
		Title:   "Cosmo",
		Version: "1",
	}, nil)
	transport := &mcpsdk.StreamableClientTransport{
		Endpoint:             tool.BaseURL,
		HTTPClient:           httpClient,
		DisableStandaloneSSE: true,
		// A tool invocation is already bounded by CallTimeout. Avoid hiding a
		// failure behind the SDK's default reconnect loop.
		MaxRetries: -1,
	}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		if errors.Is(err, ErrToolUnauthorized) {
			return nil, ErrToolUnauthorized
		}
		return nil, err
	}
	return session, nil
}

// DiscoverMCP asks the server what tools it offers. The complete MCP tool is
// retained, while Parameters remains a small projection for the existing test
// panel and older API clients.
func (repository *Repository) DiscoverMCP(ctx context.Context, tool Tool) ([]Action, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	session, err := repository.openMCP(ctx, tool)
	if err != nil {
		return nil, err
	}
	defer session.Close()

	discovered := []Action{}
	cursor := ""
	for page := 0; page < 20; page++ {
		listed, err := session.ListTools(ctx, &mcpsdk.ListToolsParams{Cursor: cursor})
		if err != nil {
			if errors.Is(err, ErrToolUnauthorized) {
				return nil, ErrToolUnauthorized
			}
			return nil, err
		}

		for _, entry := range listed.Tools {
			name, err := ValidateMCPToolName(entry.Name)
			if err != nil {
				continue
			}
			description := strings.TrimSpace(entry.Description)
			if runes := []rune(description); len(runes) > MaxDescriptionRunes {
				description = string(runes[:MaxDescriptionRunes])
			}
			parameters, err := mcpParameters(entry.InputSchema)
			if err != nil {
				continue
			}
			mcpTool, err := json.Marshal(entry)
			if err != nil {
				continue
			}
			discovered = append(discovered, Action{
				Name:        name,
				Description: description,
				Method:      http.MethodPost,
				Path:        "/",
				Parameters:  parameters,
				MCPTool:     mcpTool,
				ResultType:  mcpSchemaType(entry.OutputSchema),
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

// mcpParameters builds the bounded compatibility projection shown by the
// action editor. It is never used to describe an MCP call to the model; that
// path reads the complete inputSchema from MCPTool.
func mcpParameters(inputSchema any) ([]Parameter, error) {
	encoded, err := json.Marshal(inputSchema)
	if err != nil {
		return nil, err
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(encoded, &schema); err != nil {
		return nil, err
	}

	required := map[string]bool{}
	for _, field := range schema.Required {
		required[field] = true
	}
	fields := make([]string, 0, len(schema.Properties))
	for field := range schema.Properties {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	if len(fields) > MaxParameters {
		fields = fields[:MaxParameters]
	}
	parameters := make([]Parameter, 0, len(fields))
	for _, field := range fields {
		var property struct {
			Type        any    `json:"type"`
			Description string `json:"description"`
		}
		_ = json.Unmarshal(schema.Properties[field], &property)
		parameters = append(parameters, Parameter{
			Name:        field,
			Description: property.Description,
			Type:        mcpPropertyType(property.Type),
			In:          "body",
			IsRequired:  required[field],
		})
	}
	return CleanParameters(parameters)
}

// ValidateMCPToolName follows the MCP name grammar. It is intentionally
// separate from ValidateActionName: dots and hyphens are valid MCP names but
// are not accepted by every hand-authored HTTP integration or model gateway.
func ValidateMCPToolName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" || len(name) > 128 {
		return "", ErrMCPToolName
	}
	for _, r := range name {
		letter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		digit := r >= '0' && r <= '9'
		if !letter && !digit && r != '_' && r != '-' && r != '.' {
			return "", ErrMCPToolName
		}
	}
	return name, nil
}

// cleanMCPTool validates a complete tools/list entry and returns its remote
// name. The raw object remains intact so future MCP fields do not require a
// database migration merely to survive a discovery/publish cycle.
func cleanMCPTool(raw json.RawMessage) (json.RawMessage, string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("{}")) || bytes.Equal(trimmed, []byte("null")) {
		return nil, "", nil
	}
	var wire struct {
		Name        string          `json:"name"`
		InputSchema json.RawMessage `json:"inputSchema"`
	}
	if err := json.Unmarshal(trimmed, &wire); err != nil {
		return nil, "", ErrMCPContract
	}
	name, err := ValidateMCPToolName(wire.Name)
	if err != nil {
		return nil, "", ErrMCPContract
	}
	var schema map[string]any
	if err := json.Unmarshal(wire.InputSchema, &schema); err != nil || schema == nil {
		return nil, "", ErrMCPContract
	}
	return append(json.RawMessage(nil), trimmed...), name, nil
}

func decodeMCPTool(raw []byte) json.RawMessage {
	cleaned, _, err := cleanMCPTool(raw)
	if err != nil {
		return nil
	}
	return cleaned
}

func mcpInputSchema(action Action) (map[string]any, bool) {
	var wire struct {
		InputSchema map[string]any `json:"inputSchema"`
	}
	if len(action.MCPTool) == 0 || json.Unmarshal(action.MCPTool, &wire) != nil || wire.InputSchema == nil {
		return nil, false
	}
	return wire.InputSchema, true
}

func mcpRemoteName(action Action) string {
	_, name, err := cleanMCPTool(action.MCPTool)
	if err == nil && name != "" {
		return name
	}
	return action.Name
}

func mcpSchemaType(schema any) string {
	encoded, err := json.Marshal(schema)
	if err != nil || string(encoded) == "null" {
		return ""
	}
	var root struct {
		Type any `json:"type"`
	}
	if json.Unmarshal(encoded, &root) != nil {
		return ""
	}
	return ValidateResultType(mcpPropertyType(root.Type))
}

func mcpPropertyType(value any) string {
	switch typed := value.(type) {
	case string:
		if typed != "null" {
			return typed
		}
	case []any:
		for _, item := range typed {
			if kind, ok := item.(string); ok && kind != "null" {
				return kind
			}
		}
	}
	return "string"
}

// invokeMCP calls one server tool through the negotiated SDK session. A plain
// text-only result stays convenient for existing callers; any structured,
// metadata or non-text content is returned as the complete MCP JSON envelope.
func (repository *Repository) invokeMCP(ctx context.Context, tool Tool, action Action, arguments map[string]any) (CallResult, error) {
	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, CallTimeout)
	defer cancel()

	session, err := repository.openMCP(ctx, tool)
	if err != nil {
		return CallResult{}, err
	}
	defer session.Close()

	result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      mcpRemoteName(action),
		Arguments: arguments,
	})
	if err != nil {
		if errors.Is(err, ErrToolUnauthorized) {
			return CallResult{}, ErrToolUnauthorized
		}
		return CallResult{}, err
	}

	var parts []string
	onlyText := result.StructuredContent == nil && len(result.Meta) == 0 &&
		len(result.InputRequests) == 0 && result.RequestState == ""
	for _, item := range result.Content {
		if text, ok := item.(*mcpsdk.TextContent); ok {
			parts = append(parts, text.Text)
		} else {
			onlyText = false
		}
	}
	body, isJSON := strings.Join(parts, "\n"), false
	if len(parts) == 0 || !onlyText {
		encoded, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return CallResult{}, ErrCallFailed
		}
		body = string(encoded)
		isJSON = true
	}

	status := http.StatusOK
	if result.IsError {
		status = http.StatusBadGateway
	}
	truncated := len(body) > MaxResponseBytes
	if truncated {
		body = boundedMCPBody(body, isJSON)
	}
	return CallResult{
		Status:      status,
		DurationMS:  time.Since(started).Milliseconds(),
		Body:        body,
		IsTruncated: truncated,
	}, nil
}

// boundedMCPBody never returns broken UTF-8 or a half JSON document. Large
// responses are still bounded for model safety, but the caller receives a
// valid envelope that says exactly what happened.
func boundedMCPBody(body string, isJSON bool) string {
	limit := MaxResponseBytes
	if isJSON {
		limit /= 2
	}
	preview := body[:limit]
	for !utf8.ValidString(preview) {
		preview = preview[:len(preview)-1]
	}
	if !isJSON {
		return preview
	}
	encoded, _ := json.Marshal(map[string]any{
		"isTruncated":   true,
		"originalBytes": len(body),
		"preview":       preview,
	})
	return string(encoded)
}
