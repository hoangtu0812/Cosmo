package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cosmo/backend/internal/tools"
	"cosmo/backend/internal/workflows"
	"github.com/go-chi/chi/v5"
)

func TestWorkflowQueueDisconnectReplayAndAccessChecks(t *testing.T) {
	for _, mode := range []string{"normal", "changed", "revoked", "claimed_crash", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			s, agent, owner, member := agentAccessFixture(t)
			ctx := context.Background()
			s.workflows = workflows.NewRepository(s.db, slog.Default())
			s.tools = tools.NewRepository(s.db, slog.Default(), nil, tools.EgressPolicy{AllowedHosts: []string{"127.0.0.1"}}, tools.SearchBackend{})
			var calls atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, `{"ok":true}`) }))
			defer target.Close()
			toolID, actionID, wfID := "tol_"+randomID(18), "act_"+randomID(18), "wf_"+randomID(18)
			graph := workflows.Graph{Nodes: []workflows.Node{{ID: "start", Kind: workflows.KindStart}, {ID: "read", Kind: workflows.KindTool, Config: map[string]any{"tool_id": toolID, "action_id": actionID}}, {ID: "end", Kind: workflows.KindEnd}}, Edges: []workflows.Edge{{ID: "a", Source: "start", Target: "read"}, {ID: "b", Source: "read", Target: "end"}}}
			raw, _ := json.Marshal(graph)
			for _, q := range []struct {
				query string
				args  []any
			}{
				{`INSERT INTO workspace_llm_configs(workspace_id,base_url,model) VALUES($1,'http://example.invalid','fixture')`, []any{agent.WorkspaceID}},
				{`INSERT INTO tools(id,name,owner_user_id,owner_workspace_id,kind,base_url) VALUES($1,'Queue read',$2,$3,'http',$4)`, []any{toolID, owner.ID, agent.WorkspaceID, target.URL}},
				{`INSERT INTO tool_actions(id,tool_id,name,method,path) VALUES($1,$2,'read','GET','/')`, []any{actionID, toolID}},
				{`INSERT INTO workflows(id,name,owner_user_id,owner_workspace_id,graph) VALUES($1,'Queue test',$2,$3,$4)`, []any{wfID, owner.ID, agent.WorkspaceID, raw}},
			} {
				if _, err := s.db.Exec(ctx, q.query, q.args...); err != nil {
					t.Fatal(err)
				}
			}
			router := chi.NewRouter()
			router.Post("/workflows/{workflowID}/run", s.runWorkflow)
			router.Post("/workflows/{workflowID}/executions/{executionID}/cancel", s.cancelWorkflowExecution)
			router.Get("/workflows/{workflowID}/executions/{executionID}/events", s.workflowEvents)
			requestCtx, cancel := context.WithCancel(context.WithValue(ctx, userContextKey, owner))
			done := make(chan struct{})
			go func() {
				defer close(done)
				router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/workflows/"+wfID+"/run?workspace="+agent.WorkspaceID, strings.NewReader(`{"input":"saved","request_id":"request-one"}`)).WithContext(requestCtx))
			}()
			var id string
			deadline := time.Now().Add(4 * time.Second)
			for time.Now().Before(deadline) {
				if s.db.QueryRow(ctx, `SELECT id FROM workflow_executions WHERE workflow_id=$1 AND status='queued'`, wfID).Scan(&id) == nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if id == "" {
				t.Fatal("request was not durably queued")
			}
			cancel()
			<-done
			if calls.Load() != 0 {
				t.Fatal("HTTP request executed work")
			}
			item, err := s.workflows.Get(ctx, wfID, owner.ID, agent.WorkspaceID)
			if err != nil {
				t.Fatal(err)
			}
			queueCtx := context.WithValue(ctx, workflowQueueKey{}, "request-one")
			duplicate, err := s.admitWorkflowExecution(queueCtx, item, owner.ID, "saved", "fixture", "")
			if err != nil || duplicate.ID != id {
				t.Fatalf("lost request replay: %v", err)
			}
			if _, mismatch := s.admitWorkflowExecution(queueCtx, item, owner.ID, "different", "fixture", ""); mismatch == nil {
				t.Fatal("changed payload accepted")
			}
			if mode == "cancelled" {
				stopRequest := func(user User) int {
					w := httptest.NewRecorder()
					r := httptest.NewRequest("POST", "/workflows/"+wfID+"/executions/"+id+"/cancel?workspace="+agent.WorkspaceID, nil).WithContext(context.WithValue(ctx, userContextKey, user))
					router.ServeHTTP(w, r)
					return w.Code
				}
				if stopRequest(member) == 204 {
					t.Fatal("another actor cancelled execution")
				}
				if stopRequest(owner) != 204 || stopRequest(owner) != 409 {
					t.Fatal("cancellation state is incorrect")
				}
			}
			if mode == "changed" {
				_, err = s.db.Exec(ctx, `UPDATE tools SET updated_at=NOW() WHERE id=$1`, toolID)
			}
			if mode == "revoked" {
				_, err = s.db.Exec(ctx, `DELETE FROM workspace_memberships WHERE user_id=$1 AND workspace_id=$2`, owner.ID, agent.WorkspaceID)
			}
			if mode == "claimed_crash" {
				job, e := s.claimWorkflowJob(ctx)
				if e != nil || job == nil {
					t.Fatal(e)
				}
				_, err = s.db.Exec(ctx, `UPDATE workflow_executions SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, id)
			}
			if err != nil {
				t.Fatal(err)
			}
			workerCtx, stop := context.WithCancel(ctx)
			var wg sync.WaitGroup
			// Two workers compete for the same persisted request.
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); s.RunWorkflowWorker(workerCtx) }()
			}
			defer func() { stop(); wg.Wait() }()
			want := "interrupted"
			if mode == "cancelled" {
				want = "cancelled"
			}
			if mode == "normal" {
				want = "succeeded"
			}
			deadline = time.Now().Add(5 * time.Second)
			status := ""
			for time.Now().Before(deadline) {
				_ = s.db.QueryRow(ctx, `SELECT status FROM workflow_executions WHERE id=$1`, id).Scan(&status)
				if status == want {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if status != want {
				t.Fatalf("status=%s want=%s", status, want)
			}
			wantCalls := int32(0)
			if mode == "normal" {
				wantCalls = 1
			}
			if calls.Load() != wantCalls {
				t.Fatalf("dispatch=%d", calls.Load())
			}
			if mode == "claimed_crash" {
				stop()
				wg.Wait()
				resumed, err := s.admitWorkflowExecution(context.WithValue(ctx, workflowQueueKey{}, "resume-one"), item, owner.ID, "ignored", "fixture", id)
				if err != nil || resumed.ID != id || resumed.Input != "saved" {
					t.Fatalf("manual resume: %v", err)
				}
				oldRequest, err := s.admitWorkflowExecution(queueCtx, item, owner.ID, "saved", "fixture", "")
				if err != nil || oldRequest.ID != id {
					t.Fatalf("old request key lost after resume: %v", err)
				}
				var total int
				if err = s.db.QueryRow(ctx, `SELECT count(*) FROM workflow_executions WHERE workflow_id=$1`, wfID).Scan(&total); err != nil || total != 1 {
					t.Fatalf("resume duplicated execution: count=%d error=%v", total, err)
				}
				// Stop the fixture queue before the next case starts its workers.
				if _, err = s.db.Exec(ctx, `UPDATE workflow_executions SET status='failed' WHERE id=$1`, id); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "normal" {
				read := func(user User, cursor string) *httptest.ResponseRecorder {
					r := httptest.NewRequest("GET", "/workflows/"+wfID+"/executions/"+id+"/events?workspace="+agent.WorkspaceID, nil).WithContext(context.WithValue(ctx, userContextKey, user))
					r.Header.Set("Last-Event-ID", cursor)
					w := httptest.NewRecorder()
					router.ServeHTTP(w, r)
					return w
				}
				w := read(owner, "")
				if w.Code != 200 || !strings.Contains(w.Body.String(), "event: done") || !strings.Contains(w.Body.String(), "event: step") {
					t.Fatalf("missing persisted events: %s", w.Body.String())
				}
				first := strings.Split(strings.Split(w.Body.String(), "id: ")[1], "\n")[0]
				if replay := read(owner, first); replay.Code != 200 || strings.Contains(replay.Body.String(), "id: "+first+"\n") {
					t.Fatal("cursor replay repeated event")
				}
				if read(member, "").Code == 200 {
					t.Fatal("another actor read execution output")
				}
				duplicate, err = s.admitWorkflowExecution(queueCtx, item, owner.ID, "saved", "fixture", "")
				if err != nil || duplicate.ID != id || calls.Load() != 1 {
					t.Fatal("terminal retry admitted another run")
				}
			}
		})
	}
}
