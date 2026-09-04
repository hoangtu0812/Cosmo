// Package mcpdemo provides a domain-neutral MCP server for local conformance
// testing. It uses the official SDK, so it verifies Cosmo against an
// independently implemented protocol peer rather than a JSON-RPC imitation.
package mcpdemo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	countWordsSchema = json.RawMessage(`{
		"type":"object",
		"properties":{"text":{"type":"string","description":"The text to count"}},
		"required":["text"],
		"additionalProperties":false
	}`)
	celsiusSchema = json.RawMessage(`{
		"type":"object",
		"properties":{"celsius":{"type":"number","description":"Temperature in degrees Celsius"}},
		"required":["celsius"],
		"additionalProperties":false
	}`)
	catalogInputSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"ids":{"type":"array","description":"Numeric item identifiers","items":{"type":"integer","minimum":1},"minItems":1},
			"filters":{"type":"object","description":"Optional catalog filters","properties":{
				"warehouse":{"type":"string","pattern":"^[A-Z0-9_-]{2,12}$"},
				"include_inactive":{"type":"boolean","default":false}
			},"required":["warehouse"],"additionalProperties":false}
		},
		"required":["ids","filters"],
		"additionalProperties":false
	}`)
	catalogOutputSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"items":{"type":"array","items":{"type":"object","properties":{
				"id":{"type":"integer"},"label":{"type":"string"},"warehouse":{"type":"string"}
			},"required":["id","label","warehouse"]}},
			"total":{"type":"integer","minimum":0}
		},
		"required":["items","total"]
	}`)
	emptyObjectSchema = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
)

// Handler exposes Streamable HTTP at /mcp and a process health endpoint. The
// tools are deliberately generic and deterministic, so this fixture works
// offline and cannot send user data to a third party.
func Handler() http.Handler {
	server := newServer()
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, nil))
	mux.HandleFunc("/health/live", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

func newServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "cosmo-mcp-conformance",
		Title:   "Cosmo MCP Conformance Server",
		Version: "2.0.0",
	}, &mcp.ServerOptions{
		Instructions: "Domain-neutral tools for MCP client conformance testing.",
	})

	server.AddTool(&mcp.Tool{
		Name:        "count_words",
		Description: "Count the words in a piece of text",
		InputSchema: countWordsSchema,
	}, countWords)
	server.AddTool(&mcp.Tool{
		Name:        "celsius_to_fahrenheit",
		Description: "Convert a temperature from Celsius to Fahrenheit",
		InputSchema: celsiusSchema,
	}, celsiusToFahrenheit)
	server.AddTool(&mcp.Tool{
		Name:         "catalog.lookup-v2",
		Description:  "Look up generic catalog items using a nested filter",
		InputSchema:  catalogInputSchema,
		OutputSchema: catalogOutputSchema,
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: true,
		},
	}, catalogLookup)
	server.AddTool(&mcp.Tool{
		Name:        "always_fail",
		Description: "Return a visible MCP tool error for client error-path testing",
		InputSchema: emptyObjectSchema,
	}, alwaysFail)
	return server
}

func countWords(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var input struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(request.Params.Arguments, &input); err != nil {
		return toolError("text must be a string"), nil
	}
	count := len(strings.FieldsFunc(input.Text, unicode.IsSpace))
	return textResult(fmt.Sprintf("%d", count)), nil
}

func celsiusToFahrenheit(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var input struct {
		Celsius *float64 `json:"celsius"`
	}
	if err := json.Unmarshal(request.Params.Arguments, &input); err != nil || input.Celsius == nil {
		return toolError("celsius must be a number"), nil
	}
	return textResult(fmt.Sprintf("%.1f", *input.Celsius*9/5+32)), nil
}

func catalogLookup(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var input struct {
		IDs     []int `json:"ids"`
		Filters struct {
			Warehouse string `json:"warehouse"`
		} `json:"filters"`
	}
	if err := json.Unmarshal(request.Params.Arguments, &input); err != nil {
		return toolError("invalid catalog lookup arguments"), nil
	}
	items := make([]map[string]any, 0, len(input.IDs))
	for _, id := range input.IDs {
		items = append(items, map[string]any{
			"id":        id,
			"label":     fmt.Sprintf("Item %d", id),
			"warehouse": input.Filters.Warehouse,
		})
	}
	structured := map[string]any{"items": items, "total": len(items)}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Found %d item(s)", len(items))}},
		StructuredContent: structured,
	}, nil
}

func alwaysFail(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return toolError("intentional conformance error"), nil
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func toolError(message string) *mcp.CallToolResult {
	result := textResult(message)
	result.IsError = true
	return result
}
