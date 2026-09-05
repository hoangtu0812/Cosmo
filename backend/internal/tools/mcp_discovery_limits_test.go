package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestMCPDiscoveryRejectsIncompleteRegistry(t *testing.T) {
	for _, mode := range []string{"invalid", "duplicate", "limit", "cycle", "pages"} {
		t.Run(mode, func(t *testing.T) {
			f := newMCPFixture(t, false)
			f.modern = true
			page := 0
			f.customPage = func(cursor string) map[string]any {
				page++
				entry := map[string]any{"name": fmt.Sprintf("tool_%d", page), "inputSchema": map[string]any{"type": "object"}}
				list := []any{entry}
				next := ""
				switch mode {
				case "invalid":
					entry["name"] = "invalid name"
				case "duplicate":
					list = append(list, entry)
				case "limit":
					for i := 1; i <= MaxActions; i++ {
						list = append(list, map[string]any{"name": fmt.Sprintf("extra_%d", i), "inputSchema": map[string]any{"type": "object"}})
					}
				case "cycle":
					next = "repeated"
				case "pages":
					next = fmt.Sprint(page)
				}
				return map[string]any{"tools": list, "nextCursor": next}
			}
			actions, err := repositoryFor(t, f.server).DiscoverMCP(context.Background(), mcpTool(f))
			if !errors.Is(err, ErrMCPDiscoveryIncomplete) || len(actions) != 0 {
				t.Fatalf("partial registry returned: %d %v", len(actions), err)
			}
		})
	}
}

func TestMCPDiscoveryLongPropertyDescriptionPreservesContract(t *testing.T) {
	f := newMCPFixture(t, false)
	f.modern = true
	description := strings.Repeat("x", MaxDescriptionRunes+1)
	f.customPage = func(string) map[string]any {
		return map[string]any{"tools": []any{map[string]any{"name": "valid_tool", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string", "description": description}}}}}}
	}
	actions, err := repositoryFor(t, f.server).DiscoverMCP(context.Background(), mcpTool(f))
	if err != nil || len(actions) != 1 {
		t.Fatalf("valid schema dropped: %v", err)
	}
	var contract map[string]any
	json.Unmarshal(actions[0].MCPTool, &contract)
	original := contract["inputSchema"].(map[string]any)["properties"].(map[string]any)["query"].(map[string]any)["description"]
	if original != description || len(actions[0].Parameters[0].Description) != MaxDescriptionRunes {
		t.Fatal("contract or bounded projection incorrect")
	}
}
