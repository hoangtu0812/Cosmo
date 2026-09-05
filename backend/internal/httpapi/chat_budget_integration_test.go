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

	"cosmo/backend/internal/modelgateway"
	"cosmo/backend/internal/runs"
	"cosmo/backend/internal/tools"
	"github.com/go-chi/chi/v5"
)

func TestChatWorkerEnforcesContextBudgetAndPersistsAccounting(t *testing.T) {
	for _, overflow := range []bool{false, true} {
		t.Run(fmt.Sprint(overflow), func(t *testing.T) {
			s, agent, owner, _ := agentAccessFixture(t)
			s.runs = runs.NewRepository(s.db)
			s.tools = tools.NewRepository(s.db, slog.Default(), nil, tools.EgressPolicy{}, tools.SearchBackend{})
			s.cfg.LLMRequestTimeout = time.Second
			ctx := context.Background()
			question := "Current question"
			if overflow {
				question = strings.TrimSpace(strings.Repeat("current ", 1300))
			}
			var requests atomic.Int32
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/model/info" {
					fmt.Fprint(w, `{"data":[{"model_name":"test","model_info":{"max_input_tokens":12000}}]}`)
					return
				}
				requests.Add(1)
				var body struct {
					Messages []modelgateway.Message `json:"messages"`
				}
				json.NewDecoder(r.Body).Decode(&body)
				// Auxiliary calls may follow success; only the answer carries the current question.
				if len(body.Messages) > 0 && body.Messages[len(body.Messages)-1].Content == question {
					for _, message := range body.Messages {
						if strings.Contains(message.Content, "oldest-marker") {
							t.Error("oldest history was not trimmed")
						}
					}
				}
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Budgeted answer\"}}]}\n\ndata: [DONE]\n\n")
			}))
			defer gateway.Close()
			if _, err := s.db.Exec(ctx, `INSERT INTO workspace_llm_configs(workspace_id,base_url,model) VALUES($1,$2,'test')`, agent.WorkspaceID, gateway.URL); err != nil {
				t.Fatal(err)
			}
			conversation := "con_" + randomID(16)
			if _, err := s.db.Exec(ctx, `INSERT INTO conversations(id,user_id,workspace_id,title) VALUES($1,$2,$3,'Existing')`, conversation, owner.ID, agent.WorkspaceID); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 16; i++ {
				role := "user"
				if i%2 == 1 {
					role = "assistant"
				}
				content := fmt.Sprintf("old-%d ", i) + strings.Repeat("history ", 220)
				if i == 0 {
					content = "oldest-marker " + content
				}
				if _, err := s.db.Exec(ctx, `INSERT INTO messages(id,conversation_id,role,content,created_at) VALUES($1,$2,$3,$4,NOW()+$5*INTERVAL '1 millisecond')`, "msg_"+randomID(16), conversation, role, content, i); err != nil {
					t.Fatal(err)
				}
			}
			stop := startTestChatWorkers(t, s)
			defer stop()
			router := chi.NewRouter()
			router.Post("/conversations/{conversationID}/messages", s.chat)
			payload, _ := json.Marshal(map[string]any{"content": question, "client_message_id": "budget"})
			req := httptest.NewRequest("POST", "/conversations/"+conversation+"/messages", strings.NewReader(string(payload))).WithContext(context.WithValue(ctx, userContextKey, owner))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			want := "succeeded"
			if overflow {
				want = "interrupted"
			}
			awaitChatStatus(t, s, conversation, "budget", want)
			if overflow && (requests.Load() != 0 || !strings.Contains(response.Body.String(), "Ngữ cảnh bắt buộc vượt giới hạn")) {
				t.Fatalf("oversized context reached gateway or lost error: calls=%d %s", requests.Load(), response.Body.String())
			}
			var counted int
			if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM run_steps s JOIN chat_turns t ON t.run_id=s.run_id WHERE t.conversation_id=$1 AND s.type='model_call' AND (s.output->'budget'->>'dropped_messages')::int > 0 AND (s.output->'budget'->>'context_window')::int=12000`, conversation).Scan(&counted); err != nil || counted < 1 {
				t.Fatalf("budget accounting missing: %d %v", counted, err)
			}
		})
	}
}
