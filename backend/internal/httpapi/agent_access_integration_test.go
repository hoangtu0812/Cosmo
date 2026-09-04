package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"cosmo/backend/internal/agents"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func agentAccessFixture(t *testing.T) (*Server, agents.Agent, User, User) {
	t.Helper()
	url := os.Getenv("COSMO_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set COSMO_TEST_DATABASE_URL to test agent execution access")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	owner, member := User{ID: "usr_" + randomID(18)}, User{ID: "usr_" + randomID(18)}
	for _, user := range []User{owner, member} {
		if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,name) VALUES($1,$2,'Access test')`, user.ID, user.ID+"@test.invalid"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, user.ID) })
	}
	workspace := "wsp_" + randomID(18)
	if _, err := pool.Exec(ctx, `INSERT INTO workspaces(id,name,slug,type) VALUES($1,'Access test',$1,'personal')`, workspace); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM workspaces WHERE id=$1`, workspace) })
	if _, err := pool.Exec(ctx, `INSERT INTO workspace_memberships VALUES($1,$3,'owner',NOW()),($2,$3,'member',NOW())`, owner.ID, member.ID, workspace); err != nil {
		t.Fatal(err)
	}
	repo := agents.NewRepository(pool, slog.Default())
	id, err := repo.Create(ctx, agents.NewAgent{Name: "Access test", OwnerUserID: owner.ID, WorkspaceID: workspace, Visibility: agents.Shared})
	if err != nil {
		t.Fatal(err)
	}
	item, err := repo.Get(ctx, id, owner.ID, workspace)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{db: pool, agents: repo, logger: slog.Default()}, item, owner, member
}

func TestStartAgentConversationRequiresReleaseOrEditor(t *testing.T) {
	s, item, owner, member := agentAccessFixture(t)
	router := chi.NewRouter()
	router.Post("/agents/{agentID}/conversations", s.startAgentConversation)
	request := func(user User, target string, want int) {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/agents/"+item.ID+"/conversations?workspace="+item.WorkspaceID, strings.NewReader(`{"target":"`+target+`"}`))
		r = r.WithContext(context.WithValue(r.Context(), userContextKey, user))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("target %q user %s: status %d want %d: %s", target, user.ID, w.Code, want, w.Body.String())
		}
	}
	request(member, "draft", http.StatusForbidden)
	request(member, "published", http.StatusConflict)
	request(owner, "", http.StatusConflict)
	request(owner, "typo", http.StatusBadRequest)
	var count int
	if err := s.db.QueryRow(context.Background(), `SELECT count(*) FROM conversations WHERE agent_id=$1`, item.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("denied requests created conversations: %d, %v", count, err)
	}
	request(owner, "draft", http.StatusCreated)
	if _, err := s.agents.Publish(context.Background(), item.ID, owner.ID, "test"); err != nil {
		t.Fatal(err)
	}
	request(member, "published", http.StatusCreated)
	request(member, "", http.StatusCreated)
}

func TestAgentRuntimeRechecksPermissionsAndVersionOwnership(t *testing.T) {
	s, item, owner, member := agentAccessFixture(t)
	ctx := context.Background()
	version, err := s.agents.Publish(ctx, item.ID, owner.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.agentRuntime(ctx, member, item.WorkspaceID, item.ID, ""); !errors.Is(err, agents.ErrDraftForbidden) {
		t.Fatalf("member ran existing draft: %v", err)
	}
	if _, err := s.agentRuntime(ctx, owner, item.WorkspaceID, item.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.agentRuntime(ctx, member, item.WorkspaceID, item.ID, version.ID); err != nil {
		t.Fatal(err)
	}
	other, err := s.agents.Create(ctx, agents.NewAgent{Name: "Other", OwnerUserID: owner.ID, WorkspaceID: item.WorkspaceID, Visibility: agents.Shared})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.agentRuntime(ctx, owner, item.WorkspaceID, other, version.ID); err == nil {
		t.Fatal("accepted another agent's version")
	}
	if _, err := s.db.Exec(ctx, `UPDATE agents SET visibility='private' WHERE id=$1`, item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.agentRuntime(ctx, member, item.WorkspaceID, item.ID, version.ID); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("continued after visibility revoked: %v", err)
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM workspace_memberships WHERE user_id=$1 AND workspace_id=$2`, owner.ID, item.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.agentRuntime(ctx, owner, item.WorkspaceID, item.ID, version.ID); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("continued after membership revoked: %v", err)
	}
}
