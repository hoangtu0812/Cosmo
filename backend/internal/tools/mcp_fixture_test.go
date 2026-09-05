package tools

import (
	"context"
	"cosmo/backend/internal/secrets"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
)

func mcpDatabaseFixture(t *testing.T, auth, secret string) (*Repository, Tool, string) {
	t.Helper()
	if os.Getenv("COSMO_TEST_DATABASE_URL") == "" {
		t.Skip("database integration environment is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("COSMO_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	box, err := secrets.New("isolated-mcp-audit-secret-20260905")
	if err != nil {
		t.Fatal(err)
	}
	user, workspace, toolID := newID("usr_"), newID("wsp_"), newID("tol_")
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,email,name) VALUES($1,$2,'audit')`, user, user+"@test.invalid"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, user) })
	if _, err = pool.Exec(ctx, `INSERT INTO workspaces(id,name,slug,type) VALUES($1,'audit',$1,'personal')`, workspace); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM workspaces WHERE id=$1`, workspace) })
	sealed, err := box.Seal(secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO tools(id,name,owner_user_id,owner_workspace_id,kind,auth_type,auth_secret) VALUES($1,'audit',$2,$3,'mcp',$4,$5)`, toolID, user, workspace, auth, sealed); err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(pool, nil, box, EgressPolicy{AllowedHosts: []string{"127.0.0.1", "localhost"}}, SearchBackend{})
	return repo, Tool{ID: toolID, Kind: KindMCP, AuthType: auth}, user
}
