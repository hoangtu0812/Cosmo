package httpapi

import (
	"context"
	"cosmo/backend/internal/tools"
	"encoding/json"
	"fmt"
	"github.com/go-chi/chi/v5"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSharedToolRequiresOwnerOptInAndActorConsent(t *testing.T) {
	for _, mode := range []string{"approved", "policy_revoked", "uninstalled", "membership_revoked", "changed"} {
		t.Run(mode, func(t *testing.T) {
			s, agent, owner, member := agentAccessFixture(t)
			base := context.Background()
			var calls atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, `{"ok":true}`) }))
			defer target.Close()
			s.tools = tools.NewRepository(s.db, slog.Default(), nil, tools.EgressPolicy{AllowedHosts: []string{"127.0.0.1"}}, tools.SearchBackend{})
			id := "tol_" + randomID(18)
			if _, err := s.db.Exec(base, `INSERT INTO tools(id,name,owner_user_id,owner_workspace_id,visibility,kind,base_url) VALUES($1,'Shared approval',$2,$3,'workspace','http',$4)`, id, owner.ID, agent.WorkspaceID, target.URL); err != nil {
				t.Fatal(err)
			}
			action, err := s.tools.SaveAction(base, id, "", tools.Action{Name: "submit", Method: "POST", Path: "/"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec(base, `INSERT INTO workspace_tools(workspace_id,tool_id,installed_by,auto_call) VALUES($1,$2,$3,true)`, agent.WorkspaceID, id, owner.ID); err != nil {
				t.Fatal(err)
			}
			ownTool, err := s.tools.Get(base, id, owner.ID, agent.WorkspaceID)
			if err != nil {
				t.Fatal(err)
			}
			shared, err := s.tools.Get(base, id, member.ID, agent.WorkspaceID)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(tools.WithCaller(base, tools.Caller{UserID: member.ID, WorkspaceID: agent.WorkspaceID}), 3*time.Second)
			defer cancel()
			if _, err = s.awaitToolApproval(ctx, "conversation", "shared-test", shared, action, nil, func(toolApproval) {}); err == nil {
				t.Fatal("sharing implicitly allowed consent")
			}
			if err = s.tools.SetActionEffect(base, shared, action, member.ID, tools.EffectSharedApproval); err == nil {
				t.Fatal("member changed owner policy")
			}
			if err = s.tools.SetActionEffect(base, ownTool, action, owner.ID, tools.EffectSharedApproval); err != nil {
				t.Fatal(err)
			}
			notice := make(chan toolApproval, 3)
			done := make(chan error, 1)
			go func() {
				_, err := s.awaitToolApproval(ctx, "conversation", "shared-test", shared, action, map[string]any{}, func(a toolApproval) { notice <- a })
				done <- err
			}()
			var approval toolApproval
			select {
			case approval = <-notice:
			case err := <-done:
				t.Fatalf("no shared approval: %v", err)
			case <-ctx.Done():
				t.Fatal("no shared approval")
			}
			var review struct {
				Definition string `json:"definition"`
			}
			if err = json.Unmarshal(approval.Request, &review); err != nil {
				t.Fatal(err)
			}
			router := chi.NewRouter()
			router.Post("/approval/{approvalID}", s.decideToolApproval)
			router.Get("/approvals", s.listToolApprovals)
			decide := func(user User) int {
				w := httptest.NewRecorder()
				r := httptest.NewRequest("POST", "/approval/"+approval.ID+"?workspace="+agent.WorkspaceID, strings.NewReader(fmt.Sprintf(`{"decision":"approved","definition":%q}`, review.Definition))).WithContext(context.WithValue(base, userContextKey, user))
				router.ServeHTTP(w, r)
				return w.Code
			}
			if decide(owner) != 409 {
				t.Fatal("owner decided for another actor")
			}
			if decide(member) != 204 {
				t.Fatal("actor consent rejected")
			}
			switch mode {
			case "policy_revoked":
				err = s.tools.SetActionEffect(base, ownTool, action, owner.ID, tools.EffectApproval)
			case "uninstalled":
				_, err = s.db.Exec(base, `DELETE FROM workspace_tools WHERE workspace_id=$1 AND tool_id=$2`, agent.WorkspaceID, id)
			case "membership_revoked":
				_, err = s.db.Exec(base, `DELETE FROM workspace_memberships WHERE workspace_id=$1 AND user_id=$2`, agent.WorkspaceID, member.ID)
			case "changed":
				_, err = s.db.Exec(base, `UPDATE tools SET base_url=base_url || '/changed', updated_at=NOW() WHERE id=$1`, id)
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case err = <-done:
			case <-ctx.Done():
				t.Fatal("approval did not settle")
			}
			if mode == "approved" {
				if err != nil || calls.Load() != 1 {
					t.Fatalf("approved: %v calls=%d", err, calls.Load())
				}
			} else if err == nil || calls.Load() != 0 {
				t.Fatalf("revocation dispatch: %v calls=%d", err, calls.Load())
			}
			if mode == "approved" {
				w := httptest.NewRecorder()
				r := httptest.NewRequest("GET", "/approvals?workspace="+agent.WorkspaceID+"&kind=conversation&source=shared-test", nil).WithContext(context.WithValue(base, userContextKey, member))
				router.ServeHTTP(w, r)
				if w.Code != 200 || !strings.Contains(w.Body.String(), approval.ID) {
					t.Fatal("actor cannot read own receipt")
				}
			}
		})
	}
}
