package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestMCPArgumentsAreValidatedBeforeConnecting(t *testing.T) {
	action := Action{MCPTool: json.RawMessage(`{"name":"lookup","inputSchema":{"type":"object","properties":{"ids":{"type":"array","maxItems":2,"items":{"type":"integer","minimum":1}},"mode":{"type":"string","enum":["read"]}},"required":["ids","mode"],"additionalProperties":false}}`)}
	repo := &Repository{}
	for _, raw := range []string{`{}`, `{"ids":[0],"mode":"read"}`, `{"ids":[1,2,3],"mode":"read"}`, `{"ids":[1.5],"mode":"read"}`, `{"ids":[1],"mode":"write"}`, `{"ids":[1],"mode":"read","extra":true}`} {
		var arguments map[string]any
		json.Unmarshal([]byte(raw), &arguments)
		_, err := repo.Invoke(context.Background(), Tool{Kind: KindMCP}, action, arguments)
		if !errors.Is(err, ErrArguments) {
			t.Fatalf("invalid args passed to transport: %s %v", raw, err)
		}
	}
	if err := validateMCPArguments(action, map[string]any{"ids": []int{1, 2}, "mode": "read"}); err != nil {
		t.Fatal(err)
	}
	if err := validateMCPArguments(action, map[string]any{"mode": strings.Repeat("x", MaxArgumentBytes)}); !errors.Is(err, ErrArguments) {
		t.Fatal(err)
	}
	action.MCPTool = json.RawMessage(`{"name":"external","inputSchema":{"$ref":"https://example.com/schema"}}`)
	if err := validateMCPArguments(action, nil); !errors.Is(err, ErrMCPContract) {
		t.Fatal("external schema reference accepted")
	}
}
