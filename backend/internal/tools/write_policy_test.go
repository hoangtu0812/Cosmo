package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestUntrustedMCPHintsCannotPermitAutomaticWrites(t *testing.T) {
	repo := &Repository{}
	action := Action{ID: "action", Name: "submit", Method: "POST", Path: "/", MCPTool: json.RawMessage(`{"name":"submit","inputSchema":{"type":"object"},"annotations":{"readOnlyHint":true,"idempotentHint":true}}`)}
	_, err := repo.invokeAutomatic(context.Background(), Tool{Kind: KindMCP}, action, nil)
	if !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("untrusted annotation authorized call: %v", err)
	}
	_, err = repo.invokeAutomatic(context.Background(), Tool{Kind: KindHTTP}, Action{Method: "POST"}, nil)
	if !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("HTTP write automatically allowed: %v", err)
	}
}

func writeFixture(t *testing.T) (*Repository, Tool, Action, context.Context) {
	t.Helper()
	repo, tool, user := mcpDatabaseFixture(t, AuthNone, "")
	tool.Kind = KindHTTP
	repo.db.QueryRow(context.Background(), `SELECT owner_workspace_id FROM tools WHERE id=$1`, tool.ID).Scan(&tool.WorkspaceID)
	action, err := repo.SaveAction(context.Background(), tool.ID, "", Action{Name: "submit", Method: "POST", Path: "/", Parameters: []Parameter{{Name: "value", Type: "string", In: "body"}}})
	if err != nil {
		t.Fatal(err)
	}
	return repo, tool, action, WithCaller(context.Background(), Caller{UserID: user, WorkspaceID: tool.WorkspaceID})
}

func TestConfirmedWriteIsSingleDispatchAcrossConcurrentAndRepeatedCalls(t *testing.T) {
	repo, tool, action, ctx := writeFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		close(entered)
		<-release
		w.Write([]byte(`{"written":true}`))
	}))
	defer server.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	tool = prepareWriteTool(t, repo, tool, ctx, server.URL)
	key := strings.Repeat("a", 32)
	args := map[string]any{"value": "one"}
	review := repo.PolicyReview(tool, action)
	if _, _, err := repo.InvokeConfirmed(ctx, tool, action, args, false, key, review); !errors.Is(err, ErrApprovalRequired) {
		t.Fatal("confirmation not required")
	}
	if _, _, err := repo.InvokeConfirmed(ctx, tool, action, args, true, key, "old-review"); !errors.Is(err, ErrApprovalRequired) {
		t.Fatal("stale review accepted")
	}
	if count.Load() != 0 {
		t.Fatal("request sent before approval")
	}
	stopped := make(chan error, 1)
	caller, cancel := context.WithCancel(ctx)
	go func() {
		_, _, err := repo.InvokeConfirmed(caller, tool, action, args, true, key, review)
		stopped <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("write did not start")
	}
	cancel() // Subscriber disappearance must not discard completion accounting.
	_, op, err := repo.InvokeConfirmed(ctx, tool, action, args, true, key, review)
	if !errors.Is(err, ErrWriteUncertain) || op == nil {
		t.Fatal("in-flight write replayed")
	}
	if _, _, err = repo.InvokeConfirmed(ctx, tool, action, args, true, strings.Repeat("b", 32), review); !errors.Is(err, ErrWriteUncertain) {
		t.Fatal("fresh key bypassed unresolved write")
	}
	close(release)
	if err = <-stopped; err != nil {
		t.Fatal(err)
	}
	result, op, err := repo.InvokeConfirmed(ctx, tool, action, args, true, key, review)
	if err != nil || op.Status != "succeeded" || result.Body != `{"written":true}` || count.Load() != 1 {
		t.Fatalf("not replayed from ledger: %v count=%d", err, count.Load())
	}
	if _, _, err = repo.InvokeConfirmed(ctx, tool, action, map[string]any{"value": "two"}, true, key, review); !errors.Is(err, ErrWriteKey) {
		t.Fatal("key reused with changed arguments")
	}
}

func TestUncertainWriteRequiresReconciliationAndNeverReplays(t *testing.T) {
	repo, tool, action, ctx := writeFixture(t)
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count.Add(1); w.WriteHeader(503) }))
	defer server.Close()
	tool = prepareWriteTool(t, repo, tool, ctx, server.URL)
	key := strings.Repeat("a", 32)
	review := repo.PolicyReview(tool, action)
	_, op, err := repo.InvokeConfirmed(ctx, tool, action, nil, true, key, review)
	if !errors.Is(err, ErrWriteUncertain) || op.Status != "uncertain" {
		t.Fatal("ambiguous outcome treated as safe failure")
	}
	_, _, _ = repo.InvokeConfirmed(ctx, tool, action, nil, true, key, review)
	if count.Load() != 1 {
		t.Fatal("unknown write retried")
	}
	caller, _ := CallerFrom(ctx)
	if err = repo.ReconcileWrite(ctx, tool.ID, "another-user", caller.WorkspaceID, op.ID, "reconciled_no_effect", "Checked target transaction log"); !errors.Is(err, ErrWriteUncertain) {
		t.Fatal("other actor reconciled write")
	}
	if err = repo.ReconcileWrite(ctx, tool.ID, caller.UserID, caller.WorkspaceID, op.ID, "reconciled_no_effect", "Checked target transaction log"); err != nil {
		t.Fatal(err)
	}
	_, _, _ = repo.InvokeConfirmed(ctx, tool, action, nil, true, key, review)
	if count.Load() != 1 {
		t.Fatal("reconciliation replayed old intent")
	}
	if err = repo.SetActionEffect(ctx, tool, action, caller.UserID, EffectRead); err != nil {
		t.Fatal(err)
	}
	effect, err := repo.ActionEffect(ctx, tool, action)
	if err != nil || effect != EffectRead {
		t.Fatal("reviewed policy not applied")
	}
	changed := action
	changed.Path = "/different"
	effect, err = repo.ActionEffect(ctx, tool, changed)
	if err != nil || effect != EffectApproval {
		t.Fatal("changed contract retained read grant")
	}
	if err = repo.SetActionEffect(ctx, tool, action, caller.UserID, EffectBlocked); err != nil {
		t.Fatal(err)
	}
	_, _, err = repo.InvokeConfirmed(ctx, tool, action, nil, true, strings.Repeat("c", 32), review)
	if !errors.Is(err, ErrActionBlocked) {
		t.Fatal("blocked action was manually dispatched")
	}
}

func TestConfirmedWriteDoesNotFollowRedirect(t *testing.T) {
	repo, tool, action, ctx := writeFixture(t)
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		http.Redirect(w, r, "/next", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	tool = prepareWriteTool(t, repo, tool, ctx, server.URL)
	_, op, err := repo.InvokeConfirmed(ctx, tool, action, nil, true, strings.Repeat("r", 32), repo.PolicyReview(tool, action))
	if !errors.Is(err, ErrWriteUncertain) || op == nil || count.Load() != 1 {
		t.Fatalf("write redirect replay: %v count=%d", err, count.Load())
	}
}

func TestInterruptedWriteBecomesReviewableWithoutDispatch(t *testing.T) {
	repo, tool, action, ctx := writeFixture(t)
	caller, _ := CallerFrom(ctx)
	id := newID("two_")
	if _, err := repo.db.Exec(ctx, `INSERT INTO tool_write_operations(id,tool_id,action_id,actor_id,workspace_id,idempotency_key,request_hash,status,created_at) VALUES($1,$2,$3,$4,$5,'test-key','hash','executing',NOW()-INTERVAL '2 minutes')`, id, tool.ID, action.ID, caller.UserID, caller.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	// Newer completed calls must not push an unresolved intent off the review page.
	if _, err := repo.db.Exec(ctx, `INSERT INTO tool_write_operations(id,tool_id,action_id,actor_id,workspace_id,idempotency_key,request_hash,status) SELECT $1||i,$2,$3,$4,$5,'done-'||i,'hash','succeeded' FROM generate_series(1,55) AS i`, id, tool.ID, action.ID, caller.UserID, caller.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	list, err := repo.WriteOperations(ctx, tool.ID, caller.UserID, caller.WorkspaceID)
	if err != nil || len(list) != 50 || list[0].ID != id || list[0].Key != "test-key" || list[0].Status != "uncertain" {
		t.Fatal("interrupted write not reviewable")
	}
	if err = repo.ReconcileWrite(ctx, tool.ID, caller.UserID, caller.WorkspaceID, id, "reconciled_succeeded", "Verified target transaction ID 123"); err != nil {
		t.Fatal(err)
	}
}

func prepareWriteTool(t *testing.T, repo *Repository, tool Tool, ctx context.Context, base string) Tool {
	t.Helper()
	caller, _ := CallerFrom(ctx)
	if _, err := repo.db.Exec(ctx, `UPDATE tools SET kind='http',base_url=$2,updated_at=NOW() WHERE id=$1`, tool.ID, base); err != nil {
		t.Fatal(err)
	}
	current, err := repo.Get(ctx, tool.ID, caller.UserID, caller.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	return current
}
