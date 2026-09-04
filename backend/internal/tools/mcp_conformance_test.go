package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cosmo/backend/internal/mcpdemo"
)

// This test crosses the real HTTP transport between Cosmo's MCP client and a
// server built with the official SDK. The server is domain-neutral on purpose:
// passing it proves protocol compatibility without coupling Cosmo to SAP.
func TestMCPConformanceAgainstOfficialSDKServer(t *testing.T) {
	server := httptest.NewServer(mcpdemo.Handler())
	t.Cleanup(server.Close)

	repository := repositoryFor(t, server)
	tool := Tool{
		ID:       "tol_conformance",
		BaseURL:  server.URL + "/mcp",
		Kind:     KindMCP,
		AuthType: AuthNone,
	}
	actions, err := repository.DiscoverMCP(context.Background(), tool)
	if err != nil {
		t.Fatalf("discover official SDK server: %v", err)
	}
	if len(actions) != 4 {
		t.Fatalf("expected all four conformance tools, got %d: %#v", len(actions), actions)
	}

	catalog := conformanceAction(t, actions, "catalog.lookup-v2")
	assertComplexContract(t, catalog)

	result, err := repository.Invoke(context.Background(), tool, catalog, map[string]any{
		"ids": []any{101, 202},
		"filters": map[string]any{
			"warehouse":        "WH_01",
			"include_inactive": false,
		},
	})
	if err != nil {
		t.Fatalf("invoke structured tool: %v", err)
	}
	if result.Status != http.StatusOK {
		t.Fatalf("structured tool returned status %d: %s", result.Status, result.Body)
	}
	var envelope struct {
		Structured struct {
			Items []struct {
				ID        int    `json:"id"`
				Warehouse string `json:"warehouse"`
			} `json:"items"`
			Total int `json:"total"`
		} `json:"structuredContent"`
	}
	if err := json.Unmarshal([]byte(result.Body), &envelope); err != nil {
		t.Fatalf("structured MCP response was not preserved as JSON: %v\n%s", err, result.Body)
	}
	if envelope.Structured.Total != 2 || len(envelope.Structured.Items) != 2 ||
		envelope.Structured.Items[0].ID != 101 || envelope.Structured.Items[0].Warehouse != "WH_01" {
		t.Fatalf("structured MCP response was changed: %#v", envelope.Structured)
	}

	words := conformanceAction(t, actions, "count_words")
	plain, err := repository.Invoke(context.Background(), tool, words, map[string]any{
		"text": "one two three four",
	})
	if err != nil || plain.Status != http.StatusOK || plain.Body != "4" {
		t.Fatalf("plain text compatibility failed: result=%#v err=%v", plain, err)
	}

	failure := conformanceAction(t, actions, "always_fail")
	failed, err := repository.Invoke(context.Background(), tool, failure, map[string]any{})
	if err != nil {
		t.Fatalf("MCP tool errors must remain visible results: %v", err)
	}
	if failed.Status != http.StatusBadGateway || !strings.Contains(failed.Body, "intentional conformance error") {
		t.Fatalf("MCP tool error was not mapped correctly: %#v", failed)
	}
}

func conformanceAction(t *testing.T, actions []Action, name string) Action {
	t.Helper()
	for _, action := range actions {
		if action.Name == name {
			return action
		}
	}
	t.Fatalf("conformance tool %q was not discovered", name)
	return Action{}
}

func assertComplexContract(t *testing.T, action Action) {
	t.Helper()
	if action.ResultType != "object" {
		t.Fatalf("output schema type was lost: %#v", action)
	}
	var contract map[string]any
	if err := json.Unmarshal(action.MCPTool, &contract); err != nil {
		t.Fatalf("MCP contract is not JSON: %v", err)
	}
	input, ok := contract["inputSchema"].(map[string]any)
	if !ok {
		t.Fatalf("input schema was lost: %#v", contract)
	}
	properties, _ := input["properties"].(map[string]any)
	filters, _ := properties["filters"].(map[string]any)
	filterProperties, _ := filters["properties"].(map[string]any)
	warehouse, _ := filterProperties["warehouse"].(map[string]any)
	if warehouse["pattern"] != "^[A-Z0-9_-]{2,12}$" {
		t.Fatalf("nested JSON Schema constraint was lost: %#v", warehouse)
	}
	if contract["outputSchema"] == nil || contract["annotations"] == nil {
		t.Fatalf("output schema or tool annotations were lost: %#v", contract)
	}
}
