package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

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

// DiscoverMCP asks the server what tools it offers. Phase 1 changes only the
// protocol implementation; the conversion to Cosmo's existing Action model is
// deliberately retained until the lossless MCP contract lands in Phase 2.
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
			name, err := ValidateActionName(entry.Name)
			if err != nil {
				continue
			}
			description, err := ValidateDescription(entry.Description)
			if err != nil {
				description = ""
			}
			parameters, err := mcpParameters(entry.InputSchema)
			if err != nil {
				continue
			}
			discovered = append(discovered, Action{
				Name:        name,
				Description: description,
				Method:      http.MethodPost,
				Path:        "/",
				Parameters:  parameters,
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

// mcpParameters retains the legacy Action projection used by the rest of
// Cosmo today. Keeping it isolated makes the lossy compatibility layer
// explicit and removable when Phase 2 stores the complete JSON Schema.
func mcpParameters(inputSchema any) ([]Parameter, error) {
	encoded, err := json.Marshal(inputSchema)
	if err != nil {
		return nil, err
	}
	var schema struct {
		Properties map[string]struct {
			Type        string `json:"type"`
			Description string `json:"description"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(encoded, &schema); err != nil {
		return nil, err
	}

	required := map[string]bool{}
	for _, field := range schema.Required {
		required[field] = true
	}
	parameters := make([]Parameter, 0, len(schema.Properties))
	for field, property := range schema.Properties {
		parameters = append(parameters, Parameter{
			Name:        field,
			Description: property.Description,
			Type:        property.Type,
			In:          "body",
			IsRequired:  required[field],
		})
	}
	return CleanParameters(parameters)
}

// invokeMCP calls one server tool through the negotiated SDK session. Result
// projection remains backward-compatible for Phase 1: text blocks are joined
// for the model, while a result without text is returned as MCP JSON.
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
		Name:      action.Name,
		Arguments: arguments,
	})
	if err != nil {
		if errors.Is(err, ErrToolUnauthorized) {
			return CallResult{}, ErrToolUnauthorized
		}
		return CallResult{}, err
	}

	var parts []string
	for _, item := range result.Content {
		if text, ok := item.(*mcpsdk.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	body := strings.Join(parts, "\n")
	if len(parts) == 0 {
		encoded, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return CallResult{}, ErrCallFailed
		}
		body = string(encoded)
	}

	status := http.StatusOK
	if result.IsError {
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
