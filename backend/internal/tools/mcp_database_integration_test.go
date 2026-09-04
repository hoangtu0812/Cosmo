package tools

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestMCPContractDatabaseRoundTrip is opt-in because unit tests do not require
// Postgres. It creates an isolated workspace/tool and removes it afterwards.
// Set COSMO_TEST_DATABASE_URL to exercise the real JSONB and version snapshot
// paths against a migrated database.
func TestMCPContractDatabaseRoundTrip(t *testing.T) {
	databaseURL := os.Getenv("COSMO_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("database integration environment is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	defer pool.Close()

	userID, workspaceID, toolID := newID("usr_"), newID("wsp_"), newID("tol_")
	if _, err := pool.Exec(ctx, `INSERT INTO users(id, email, name) VALUES($1, $2, 'MCP contract test')`, userID, userID+"@test.invalid"); err != nil {
		t.Fatalf("create test user: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	if _, err := pool.Exec(ctx, `INSERT INTO workspaces(id, name, slug, type) VALUES($1, 'MCP contract test', $2, 'personal')`, workspaceID, workspaceID); err != nil {
		t.Fatalf("create test workspace: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM workspaces WHERE id = $1`, workspaceID)
	if _, err := pool.Exec(ctx, `
		INSERT INTO tools(id, name, owner_user_id, owner_workspace_id, kind)
		VALUES($1, 'Contract fixture', $2, $3, 'mcp')`, toolID, userID, workspaceID); err != nil {
		t.Fatalf("create test tool: %v", err)
	}

	repository := NewRepository(pool, nil, nil, EgressPolicy{}, SearchBackend{})
	contract := json.RawMessage(`{
		"name":"inventory.lookup-v2",
		"description":"Find stock",
		"inputSchema":{"type":"object","properties":{"ids":{"type":"array","items":{"type":"integer"}}}},
		"outputSchema":{"type":"object","properties":{"items":{"type":"array"}}},
		"annotations":{"readOnlyHint":true},
		"_meta":{"owner":"warehouse"}
	}`)
	saved, err := repository.SaveAction(ctx, toolID, "", Action{
		Name:       "inventory.lookup-v2",
		Method:     "POST",
		Path:       "/",
		Parameters: []Parameter{},
		MCPTool:    contract,
	})
	if err != nil {
		t.Fatalf("save MCP action: %v", err)
	}
	if string(saved.MCPTool) == "" || mcpRemoteName(saved) != "inventory.lookup-v2" {
		t.Fatalf("database lost MCP contract: %#v", saved)
	}
	version, err := repository.Publish(ctx, toolID, userID, "contract test")
	if err != nil {
		t.Fatalf("publish MCP action: %v", err)
	}
	if len(version.Actions) != 1 || len(version.Actions[0].MCPTool) == 0 {
		t.Fatalf("published version lost MCP contract: %#v", version.Actions)
	}
}
