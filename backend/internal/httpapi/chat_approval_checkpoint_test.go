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

	"cosmo/backend/internal/runs"
	"cosmo/backend/internal/tools"
	"github.com/go-chi/chi/v5"
)

func TestChatParkSurvivesWorkerRestartAndPreservesFIFO(t *testing.T) {
	for _, decision := range []string{"approved", "rejected", "expired", "changed", "revoked", "cancelled", "claimed_crash", "two_approvals"} {
		t.Run(decision, func(t *testing.T) {
			s, agent, owner, _ := agentAccessFixture(t)
			ctx := context.Background()
			s.runs = runs.NewRepository(s.db)
			s.tools = tools.NewRepository(s.db, slog.Default(), nil, tools.EgressPolicy{AllowedHosts: []string{"127.0.0.1"}}, tools.SearchBackend{})
			var writes, reads, decisions atomic.Int32

			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					writes.Add(1)
					w.WriteHeader(200)
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
					Stream   bool `json:"stream"`
					Messages []struct {
						Role    string `json:"role"`
						Content string `json:"content"`
					} `json:"messages"`
					Tools []struct {
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
				lastQuestion := ""
				for _, m := range payload.Messages {
					if m.Role == "user" {
						lastQuestion = m.Content
					}
				}
				if lastQuestion != "Submit once and check status" {
					_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "Independent answer"}}}})
					return
				}
				n := decisions.Add(1)

				for _, d := range payload.Tools {
					if strings.HasSuffix(d.Function.Name, "__submit") {
						submit = d.Function.Name

					}
					if strings.HasSuffix(d.Function.Name, "__probe") {
						probe = d.Function.Name
					}
				}
				message := map[string]any{"role": "assistant", "content": "Finished with reconciliation instructions"}
				if n < 2 {
					// Deliberately ignore the removed definition and change arguments.
					calls := []any{}
					names := []string{probe, submit}
					if decision == "two_approvals" {
						names = append(names, submit)
					}
					for i, name := range names {
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
			router.Get("/conversations/{conversationID}/messages", s.listMessages)
			router.Post("/approvals/{approvalID}", s.decideToolApproval)
			workerCtx, cancelWorker := context.WithCancel(ctx)
			var workers sync.WaitGroup
			startWorker := func() {
				workers.Add(1)
				go func() {
					defer workers.Done()
					_ = s.RunChatWorker(workerCtx, ChatWorkerOptions{Poll: 10 * time.Millisecond, Lease: 3 * time.Second, Timeout: 20 * time.Second})
				}()
			}
			stopWorker := func() { cancelWorker(); workers.Wait() }
			defer stopWorker()
			startWorker()
			send := func(con, key, question string) <-chan struct{} {
				done := make(chan struct{})
				go func() {
					defer close(done)
					req := httptest.NewRequest("POST", "/conversations/"+con+"/messages", strings.NewReader(fmt.Sprintf(`{"content":%q,"client_message_id":%q}`, question, key))).WithContext(context.WithValue(ctx, userContextKey, owner))
					router.ServeHTTP(httptest.NewRecorder(), req)
				}()
				return done
			}
			done := send(conversation, "park-test", "Submit once and check status")
			awaitChatStatus(t, s, conversation, "park-test", "waiting_approval")
			var id, definition, runID string
			if err := s.db.QueryRow(ctx, `SELECT a.id,a.request->>'definition',c.run_id FROM tool_approvals a JOIN chat_approval_checkpoints c ON c.approval_id=a.id WHERE a.source_id=$1`, conversation).Scan(&id, &definition, &runID); err != nil {
				t.Fatal(err)
			}
			loadMessages := func() []Message {
				req := httptest.NewRequest("GET", "/conversations/"+conversation+"/messages", nil).WithContext(context.WithValue(ctx, userContextKey, owner))
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				var result struct {
					Messages []Message `json:"messages"`
				}
				if w.Code != 200 {
					t.Fatalf("transcript: %d %s", w.Code, w.Body.String())
				}
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				return result.Messages
			}
			loaded := loadMessages()
			if len(loaded) != 3 || !loaded[2].IsPending || len(loaded[2].ToolCalls) != 2 || loaded[2].ToolCalls[1].ApprovalID != id {
				t.Fatalf("missing checkpoint transcript: %+v", loaded)
			}
			var savedBeforeApproval int
			if err := s.db.QueryRow(ctx, `SELECT count(*) FROM messages WHERE conversation_id=$1 AND role='assistant'`, conversation).Scan(&savedBeforeApproval); err != nil || savedBeforeApproval != 0 {
				t.Fatal("checkpoint was saved as completed answer")
			}
			if writes.Load() != 0 || reads.Load() != 1 {
				t.Fatal("effects before approval or missing prior read")
			}
			// One worker must be available for another conversation while FIFO
			// keeps this conversation's next question behind the parked turn.
			later := send(conversation, "later", "Later question")
			awaitChatStatus(t, s, conversation, "later", "queued")
			other := "con_" + randomID(18)
			if _, err := s.db.Exec(ctx, `INSERT INTO conversations(id,user_id,workspace_id,title) VALUES($1,$2,$3,'Other')`, other, owner.ID, agent.WorkspaceID); err != nil {
				t.Fatal(err)
			}
			independent := send(other, "independent", "Independent question")
			select {
			case <-independent:
			case <-time.After(5 * time.Second):
				t.Fatal("waiting approval occupied the only worker")
			}
			awaitChatStatus(t, s, other, "independent", "succeeded")
			awaitChatStatus(t, s, conversation, "later", "queued")
			stopWorker()
			approve := func() int {
				req := httptest.NewRequest("POST", "/approvals/"+id+"?workspace="+agent.WorkspaceID, strings.NewReader(fmt.Sprintf(`{"decision":%q,"definition":%q}`, map[bool]string{true: "rejected", false: "approved"}[decision == "rejected"], definition))).WithContext(context.WithValue(ctx, userContextKey, owner))
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				return w.Code
			}
			var err error
			switch decision {
			case "expired":
				_, err = s.db.Exec(ctx, `UPDATE tool_approvals SET expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, id)
				if approve() != 409 {
					t.Fatal("expired approval accepted")
				}
			case "cancelled":
				_, err = s.runs.Transition(ctx, runID, runs.Cancelled, nil, "", "")
				if approve() != 409 {
					t.Fatal("cancelled approval accepted")
				}
			default:
				if approve() != 204 {
					t.Fatal("parked approval unavailable after worker shutdown")
				}
				if approve() != 409 {
					t.Fatal("duplicate decision accepted")
				}
				if decision == "changed" {
					_, err = s.db.Exec(ctx, `UPDATE tools SET updated_at=NOW() WHERE id=$1`, toolID)
				}
				if decision == "revoked" {
					_, err = s.db.Exec(ctx, `DELETE FROM workspace_memberships WHERE user_id=$1 AND workspace_id=$2`, owner.ID, agent.WorkspaceID)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if decision == "claimed_crash" {
				claimed, err := s.claimChatTurn(ctx, "dead-worker", time.Second)
				if err != nil || claimed == nil || claimed.Run.ID != runID {
					t.Fatalf("claim: %v", err)
				}
				// Once claimed the outcome might be unknown; never requeue it.
				if _, err = s.db.Exec(ctx, `UPDATE chat_turns SET lease_expires_at=NOW()-INTERVAL '1 second' WHERE run_id=$1`, runID); err != nil {
					t.Fatal(err)
				}
			}
			workerCtx, cancelWorker = context.WithCancel(ctx)
			startWorker()
			if decision == "two_approvals" {
				firstID := id
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) {
					if s.db.QueryRow(ctx, `SELECT a.id,a.request->>'definition' FROM tool_approvals a JOIN chat_approval_checkpoints c ON c.approval_id=a.id JOIN chat_turns t ON t.run_id=c.run_id WHERE a.source_id=$1 AND a.id<>$2 AND t.status='waiting_approval'`, conversation, firstID).Scan(&id, &definition) == nil {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				if id == firstID || writes.Load() != 1 {
					t.Fatal("second checkpoint did not retain first write")
				}
				stopWorker()
				if approve() != 204 {
					t.Fatal("second parked approval unavailable")
				}
				workerCtx, cancelWorker = context.WithCancel(ctx)
				startWorker()
			}
			select {
			case <-done:
			case <-time.After(6 * time.Second):
				t.Fatal("parked turn did not settle")
			}
			terminal := "succeeded"
			if decision == "changed" || decision == "revoked" || decision == "cancelled" || decision == "claimed_crash" {
				terminal = "interrupted"
			}
			awaitChatStatus(t, s, conversation, "park-test", terminal)
			select {
			case <-later:
			case <-time.After(5 * time.Second):
				t.Fatal("FIFO never released")
			}
			want := int32(0)
			if decision == "approved" {
				want = 1
			}
			if decision == "two_approvals" {
				want = 2
			}
			if writes.Load() != want || reads.Load() != 1 {
				t.Fatalf("replayed effects: writes=%d reads=%d", writes.Load(), reads.Load())
			}
			if terminal == "succeeded" {
				for _, m := range loadMessages() {
					if m.IsPending {
						t.Fatal("terminal transcript still pending")
					}
				}
				var calls []byte
				if err := s.db.QueryRow(ctx, `SELECT m.tool_calls FROM messages m JOIN chat_turns t ON m.id=t.assistant_message_id WHERE t.run_id=$1`, runID).Scan(&calls); err != nil {
					t.Fatal(err)
				}
				var saved []ToolCall
				wantCalls := 2
				if decision == "two_approvals" {
					wantCalls = 3
				}
				if err = json.Unmarshal(calls, &saved); err != nil || len(saved) != wantCalls || saved[len(saved)-1].ApprovalID != id {
					t.Fatalf("lost tool transcript: %s %v", calls, err)
				}
				if decisions.Load() != 2 {
					t.Fatalf("repeated model decision: %d", decisions.Load())
				}
			}
		})
	}
}
