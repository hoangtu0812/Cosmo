package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

// A version exists so that what an agent was built on stops moving. The
// credential is the one thing that must keep moving: a snapshot carrying one
// would put a revoked key back into service the moment somebody rolled back to
// it, and would copy the key to anyone allowed to read the version.
func TestVersionFreezesNoCredential(t *testing.T) {
	for _, forbidden := range []string{"auth_secret", "secret"} {
		if strings.Contains(versionColumns, forbidden) {
			t.Errorf("a version freezes %q; a credential is current state, not a description", forbidden)
		}
	}
	for _, expected := range []string{"base_url", "auth_type", "actions"} {
		if !strings.Contains(versionColumns, expected) {
			t.Errorf("a version does not freeze %q, so a published agent would still drift with the draft", expected)
		}
	}
}

func TestVersionSnapshotKeepsMCPContract(t *testing.T) {
	actions := []Action{{
		Name: "inventory.lookup-v2",
		MCPTool: json.RawMessage(`{
			"name":"inventory.lookup-v2",
			"inputSchema":{"type":"object","properties":{"ids":{"type":"array","items":{"type":"integer"}}}},
			"outputSchema":{"type":"array"},
			"_meta":{"owner":"warehouse"}
		}`),
	}}
	frozen, err := json.Marshal(actions)
	if err != nil {
		t.Fatalf("freeze actions: %v", err)
	}
	restored := decodeActions(frozen)
	if len(restored) != 1 || len(restored[0].MCPTool) == 0 {
		t.Fatalf("MCP contract did not survive snapshot: %#v", restored)
	}
	var contract map[string]any
	if err := json.Unmarshal(restored[0].MCPTool, &contract); err != nil || contract["outputSchema"] == nil || contract["_meta"] == nil {
		t.Fatalf("MCP output schema or metadata was lost: %#v, %v", contract, err)
	}
}

// The changelog is stored on every version, so it has to stop somewhere.
func TestChangelogIsCapped(t *testing.T) {
	long := strings.Repeat("a", MaxChangelogRunes+50)
	if got := len([]rune(CapChangelog(long))); got != MaxChangelogRunes {
		t.Errorf("changelog capped to %d runes, want %d", got, MaxChangelogRunes)
	}
}
