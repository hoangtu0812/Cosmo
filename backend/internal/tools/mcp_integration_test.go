package tools

import (
	"context"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"cosmo/backend/internal/secrets"
)

// TestLiveConfiguredMCP is an opt-in smoke test for a real MCP registration in
// Cosmo. It deliberately knows nothing about SAP: any configured MCP server
// can be selected, which keeps that server from becoming Cosmo's test oracle.
//
// Required environment variables:
//   - COSMO_MCP_LIVE_DATABASE_URL
//   - COSMO_MCP_LIVE_SESSION_SECRET
//   - COSMO_MCP_LIVE_TOOL_ID
//
// COSMO_MCP_LIVE_ENDPOINT can override the stored endpoint when the test runs
// outside the deployment network. COSMO_MCP_LIVE_ACTION optionally calls one
// no-argument tool after discovery.
func TestLiveConfiguredMCP(t *testing.T) {
	databaseURL := os.Getenv("COSMO_MCP_LIVE_DATABASE_URL")
	sessionSecret := os.Getenv("COSMO_MCP_LIVE_SESSION_SECRET")
	toolID := os.Getenv("COSMO_MCP_LIVE_TOOL_ID")
	if databaseURL == "" || sessionSecret == "" || toolID == "" {
		t.Skip("live MCP environment is not configured")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	defer pool.Close()

	var tool Tool
	err = pool.QueryRow(ctx, `
		SELECT id, base_url, kind, auth_type, auth_header_name
		FROM tools WHERE id = $1`, toolID).Scan(
		&tool.ID, &tool.BaseURL, &tool.Kind, &tool.AuthType, &tool.AuthHeaderName,
	)
	if err != nil {
		t.Fatalf("load configured MCP tool: %v", err)
	}
	if endpointOverride := os.Getenv("COSMO_MCP_LIVE_ENDPOINT"); endpointOverride != "" {
		tool.BaseURL = endpointOverride
	}
	endpoint, err := url.Parse(tool.BaseURL)
	if err != nil || endpoint.Hostname() == "" {
		t.Fatalf("invalid configured endpoint %q", tool.BaseURL)
	}
	box, err := secrets.New(sessionSecret)
	if err != nil {
		t.Fatalf("configure secret box: %v", err)
	}
	repository := NewRepository(pool, nil, box, EgressPolicy{
		AllowedHosts: []string{endpoint.Hostname()},
	}, SearchBackend{})

	actions, err := repository.DiscoverMCP(ctx, tool)
	if err != nil {
		t.Fatalf("live MCP discovery: %v", err)
	}
	if len(actions) == 0 {
		t.Fatal("live MCP server returned no callable tools")
	}
	for _, action := range actions {
		if len(action.MCPTool) == 0 {
			t.Fatalf("live MCP tool %q lost its contract", action.Name)
		}
		if _, ok := mcpInputSchema(action); !ok {
			t.Fatalf("live MCP tool %q has no usable input schema", action.Name)
		}
	}

	if actionName := os.Getenv("COSMO_MCP_LIVE_ACTION"); actionName != "" {
		result, err := repository.Invoke(ctx, tool, Action{Name: actionName}, nil)
		if err != nil {
			t.Fatalf("live MCP call %q: %v", actionName, err)
		}
		if result.Status < 200 || result.Status >= 300 {
			t.Fatalf("live MCP call %q returned status %d: %s", actionName, result.Status, result.Body)
		}
	}
}
