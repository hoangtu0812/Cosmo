package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cosmo/backend/internal/tools"
	"github.com/go-chi/chi/v5"
)

func TestInlineApprovalRequiresExactLiveActorDecision(t *testing.T) {
	for _, mode := range []string{"approve", "reject", "expire", "cancel", "changed", "revoked", "crash_ledger"} {
		t.Run(mode, func(t *testing.T) {
			s, agent, owner, member := agentAccessFixture(t)
			var calls atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, `{"ok":true}`) }))
			defer target.Close()
			parsed, _ := url.Parse(target.URL)
			s.tools = tools.NewRepository(s.db, slog.Default(), nil, tools.EgressPolicy{AllowedHosts: []string{parsed.Hostname()}}, tools.SearchBackend{})
			base := context.Background()
			id := "tol_" + randomID(18)
			_, err := s.db.Exec(base, `INSERT INTO tools(id,name,owner_user_id,owner_workspace_id,kind,base_url) VALUES($1,'Approval test',$2,$3,'http',$4)`, id, owner.ID, agent.WorkspaceID, target.URL)
			if err != nil {
				t.Fatal(err)
			}
			action, err := s.tools.SaveAction(base, id, "", tools.Action{Name: "submit", Method: "POST", Path: "/", Parameters: []tools.Parameter{{Name: "value", In: "body", Type: "string"}}})
			if err != nil {
				t.Fatal(err)
			}
			tool, err := s.tools.Get(base, id, owner.ID, agent.WorkspaceID)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(tools.WithCaller(base, tools.Caller{UserID: owner.ID, WorkspaceID: agent.WorkspaceID}), 5*time.Second)
			defer cancel()
			notice := make(chan toolApproval, 3)
			done := make(chan error, 1)
			go func() {
				_, err := s.awaitToolApproval(ctx, "workflow", "test", tool, action, map[string]any{"value": "reviewed"}, func(a toolApproval) { notice <- a })
				done <- err
			}()
			var approval toolApproval
			select {
			case approval = <-notice:
			case err := <-done:
				t.Fatalf("no approval: %v", err)
			case <-time.After(3 * time.Second):
				t.Fatal("no approval")
			}
			if calls.Load() != 0 {
				t.Fatal("dispatch before consent")
			}
			var review map[string]any
			if err = json.Unmarshal(approval.Request, &review); err != nil {
				t.Fatal(err)
			}
			router := chi.NewRouter()
			router.Post("/approval/{approvalID}", s.decideToolApproval)
			router.Get("/approvals", s.listToolApprovals)
			decide := func(user User, definition, decision string) int {
				r := httptest.NewRequest("POST", "/approval/"+approval.ID+"?workspace="+agent.WorkspaceID, strings.NewReader(fmt.Sprintf(`{"decision":%q,"definition":%q}`, decision, definition))).WithContext(context.WithValue(base, userContextKey, user))
				w := httptest.NewRecorder()
				router.ServeHTTP(w, r)
				return w.Code
			}
			definition := review["definition"].(string)
			if decide(member, definition, "approved") != 409 {
				t.Fatal("other actor can decide")
			}
			if decide(owner, "stale", "approved") != 409 {
				t.Fatal("wrong review can decide")
			}
			switch mode {
			case "expire":
				_, err = s.db.Exec(base, `UPDATE tool_approvals SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, approval.ID)
			case "cancel", "crash_ledger":
				cancel()
			case "changed":
				_, err = s.db.Exec(base, `UPDATE tools SET updated_at=NOW() WHERE id=$1`, id)
			case "revoked":
				_, err = s.db.Exec(base, `DELETE FROM workspace_memberships WHERE user_id=$1 AND workspace_id=$2`, owner.ID, agent.WorkspaceID)
			}
			if err != nil {
				t.Fatal(err)
			}
			decision := "approved"
			if mode == "reject" {
				decision = "rejected"
			}
			if mode != "cancel" && mode != "crash_ledger" && mode != "revoked" {
				code := decide(owner, definition, decision)
				want := 204
				if mode == "expire" {
					want = 409
				}
				if code != want {
					t.Fatalf("decision: %d want %d", code, want)
				}
				if decide(owner, definition, decision) != 409 {
					t.Fatal("decision replay accepted")
				}
			}
			if mode == "revoked" {
				cancel()
			}
			select {
			case err = <-done:
			case <-time.After(4 * time.Second):
				t.Fatal("executor did not settle")
			}
			if mode == "approve" {
				if err != nil || calls.Load() != 1 {
					t.Fatalf("approved write: %v calls %d", err, calls.Load())
				}
			} else if err == nil || calls.Load() != 0 {
				t.Fatalf("unsafe dispatch: %v calls %d", err, calls.Load())
			}
			if mode == "crash_ledger" {
				// A crash can occur after dispatch but before the approval row is
				// updated. The ledger, not the expired wait lease, owns the outcome.
				_, err = s.db.Exec(base, `INSERT INTO tool_write_operations(id,tool_id,action_id,actor_id,workspace_id,idempotency_key,request_hash,status,created_at) VALUES($1,$2,$3,$4,$5,$6,'test','executing',NOW()-INTERVAL '2 minutes')`, "two_"+randomID(18), id, action.ID, owner.ID, agent.WorkspaceID, approval.ID)
				if err != nil {
					t.Fatal(err)
				}
				r := httptest.NewRequest("GET", "/approvals?workspace="+agent.WorkspaceID+"&kind=workflow&source=test", nil).WithContext(context.WithValue(base, userContextKey, owner))
				w := httptest.NewRecorder()
				router.ServeHTTP(w, r)
				if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"uncertain"`) {
					t.Fatalf("lost operation hidden: %s", w.Body.String())
				}
			}
		})
	}
}
