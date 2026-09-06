package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cosmo/backend/internal/runs"
	"cosmo/backend/internal/tools"
	"github.com/go-chi/chi/v5"
)

func TestChatDoesNotRepeatClosedApprovalButContinuesReads(t *testing.T) {
	for _, decision := range []string{"rejected", "approved"} {
		t.Run(decision, func(t *testing.T) {
			s, agent, owner, _ := agentAccessFixture(t)
			ctx := context.Background()
			s.runs = runs.NewRepository(s.db)
			s.tools = tools.NewRepository(s.db, slog.Default(), nil, tools.EgressPolicy{AllowedHosts: []string{"127.0.0.1"}}, tools.SearchBackend{})
			var writes, reads, decisions atomic.Int32
			var removed atomic.Bool
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					writes.Add(1)
					w.WriteHeader(503)
				} else {
					reads.Add(1)
				}
				fmt.Fprint(w, `{"fixture":true}`)
			}))
			defer target.Close()
			toolID := "tol_" + randomID(18)
			if _, err := s.db.Exec(ctx, `INSERT INTO tools(id,name,owner_user_id,owner_workspace_id,kind,base_url) VALUES($1,'Retry fixture',$2,$3,'http',$4)`, toolID, owner.ID, agent.WorkspaceID, target.URL); err != nil {
				t.Fatal(err)
			}
			for _, a := range []tools.Action{{Name: "submit", Method: "POST", Path: "/"}, {Name: "probe", Method: "GET", Path: "/"}} {
				if _, err := s.tools.SaveAction(ctx, toolID, "", a); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.db.Exec(ctx, `INSERT INTO workspace_tools(workspace_id,tool_id,auto_call) VALUES($1,$2,true)`, agent.WorkspaceID, toolID); err != nil {
				t.Fatal(err)
			}
			var submit, probe string
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/model/info" {
					w.WriteHeader(404)
					return
				}
				var payload struct {
					Stream bool `json:"stream"`
					Tools  []struct {
						Function struct {
							Name string `json:"name"`
						} `json:"function"`
					} `json:"tools"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
					return
				}
				if payload.Stream {
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Done\"}}]}\n\ndata: [DONE]\n\n")
					return
				}
				n := decisions.Add(1)
				hasSubmit := false
				for _, d := range payload.Tools {
					if strings.HasSuffix(d.Function.Name, "__submit") {
						submit = d.Function.Name
						hasSubmit = true
					}
					if strings.HasSuffix(d.Function.Name, "__probe") {
						probe = d.Function.Name
					}
				}
				if n == 2 {
					removed.Store(!hasSubmit && probe != "")
				}
				message := map[string]any{"role": "assistant", "content": "Finished with reconciliation instructions"}
				if n < 3 {
					// Deliberately ignore the removed definition and change arguments.
					calls := []any{}
					for i, name := range []string{submit, submit, probe} {
						calls = append(calls, map[string]any{"id": fmt.Sprintf("call_%d_%d", n, i), "type": "function", "function": map[string]any{"name": name, "arguments": fmt.Sprintf(`{"value":%d}`, n)}})
					}
					message["content"] = ""
					message["tool_calls"] = calls
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message}}})
			}))
			defer gateway.Close()
			if _, err := s.db.Exec(ctx, `INSERT INTO workspace_llm_configs(workspace_id,base_url,model) VALUES($1,$2,'fixture')`, agent.WorkspaceID, gateway.URL); err != nil {
				t.Fatal(err)
			}
			conversation := "con_" + randomID(18)
			if _, err := s.db.Exec(ctx, `INSERT INTO conversations(id,user_id,workspace_id,title) VALUES($1,$2,$3,'Retry test')`, conversation, owner.ID, agent.WorkspaceID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(ctx, `INSERT INTO messages(id,conversation_id,role,content) VALUES($1,$2,'user','Previous')`, "msg_"+randomID(18), conversation); err != nil {
				t.Fatal(err)
			}
			router := chi.NewRouter()
			router.Post("/conversations/{conversationID}/messages", s.chat)
			router.Post("/approvals/{approvalID}", s.decideToolApproval)
			stop := startTestChatWorkers(t, s)
			defer stop()
			done := make(chan struct{})
			go func() {
				defer close(done)
				req := httptest.NewRequest("POST", "/conversations/"+conversation+"/messages", strings.NewReader(`{"content":"Submit once and check status","client_message_id":"retry-test"}`)).WithContext(context.WithValue(ctx, userContextKey, owner))
				router.ServeHTTP(httptest.NewRecorder(), req)
			}()
			var id, definition string
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if s.db.QueryRow(ctx, `SELECT id,request->>'definition' FROM tool_approvals WHERE source_id=$1 AND status='pending'`, conversation).Scan(&id, &definition) == nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if id == "" {
				t.Fatal("no pending approval")
			}
			req := httptest.NewRequest("POST", "/approvals/"+id+"?workspace="+agent.WorkspaceID, strings.NewReader(fmt.Sprintf(`{"decision":%q,"definition":%q}`, decision, definition))).WithContext(context.WithValue(ctx, userContextKey, owner))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != 204 {
				t.Fatalf("decision %d: %s", w.Code, w.Body.String())
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("repeated approval blocked completion")
			}
			awaitChatStatus(t, s, conversation, "retry-test", "succeeded")
			wantWrites := int32(0)
			if decision == "approved" {
				wantWrites = 1
			}
			var count int
			if err := s.db.QueryRow(ctx, `SELECT count(*) FROM tool_approvals WHERE source_id=$1`, conversation).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 1 || writes.Load() != wantWrites || reads.Load() != 2 || !removed.Load() {
				t.Fatalf("approval loop: approvals=%d writes=%d reads=%d removed=%v", count, writes.Load(), reads.Load(), removed.Load())
			}
		})
	}
}
