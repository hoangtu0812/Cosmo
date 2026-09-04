package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cosmo/backend/internal/knowledge"
	"cosmo/backend/internal/runs"
	"cosmo/backend/internal/tools"
	"github.com/go-chi/chi/v5"
)

func TestChatKnowledgeEvidenceHandling(t *testing.T) {
	for _, mode := range []string{"empty", "failed", "partial", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			partial := mode == "partial" || mode == "timeout"
			s, agent, owner, _ := agentAccessFixture(t)
			ctx := context.Background()
			var modelCalls atomic.Int32
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				call := modelCalls.Add(1)
				body, _ := io.ReadAll(r.Body)
				content := "CO"
				if call == 2 {
					if !strings.Contains(string(body), "Some knowledge sources could not be searched") || !strings.Contains(string(body), "Available evidence") {
						t.Errorf("generation lost evidence or partial-source instruction: %s", body)
					}
					content = "Theo nguồn hiện có [1]."
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\ndata: [DONE]\n\n", content)
			}))
			defer gateway.Close()
			kb := createRetrievalKB(t, s, agent.WorkspaceID, agent.WorkspaceID, "workspace", "test-embed")
			if partial {
				createRetrievalKB(t, s, agent.WorkspaceID, agent.WorkspaceID, "workspace", "test-embed")
			}
			rag := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var input struct {
					KBIDs []string `json:"kb_ids"`
				}
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil || len(input.KBIDs) != 1 {
					t.Error("invalid search request")
					w.WriteHeader(400)
					return
				}
				if partial && input.KBIDs[0] != kb {
					_ = json.NewEncoder(w).Encode(map[string]any{"results": []knowledge.Passage{{KBID: input.KBIDs[0], DocumentID: "doc-evidence", DocumentTitle: "Policy", Text: "Available evidence"}}})
					return
				}
				if mode == "timeout" {
					<-r.Context().Done()
					return
				}
				if mode != "empty" {
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
					return
				}
				fmt.Fprint(w, `{"results":[]}`)
			}))
			defer rag.Close()
			s.cfg.LLMRequestTimeout = time.Second
			s.cfg.RetrievalKBTimeout = 200 * time.Millisecond
			if mode == "timeout" {
				// Exercise the overall retrieval deadline through the real chat
				// orchestrator; the per-source timer is deliberately longer.
				s.cfg.RetrievalKBTimeout = 2 * time.Second
				s.cfg.RetrievalTimeout = 200 * time.Millisecond
			}
			s.knowledge = knowledge.New(rag.URL, time.Second)
			s.runs = runs.NewRepository(s.db)
			s.tools = tools.NewRepository(s.db, slog.Default(), nil, tools.EgressPolicy{}, tools.SearchBackend{})
			if _, err := s.db.Exec(ctx, `INSERT INTO workspace_llm_configs(workspace_id,base_url,model) VALUES($1,$2,'test-model')`, agent.WorkspaceID, gateway.URL); err != nil {
				t.Fatal(err)
			}
			conversation := "con_" + randomID(18)
			if _, err := s.db.Exec(ctx, `INSERT INTO conversations(id,user_id,workspace_id,title) VALUES($1,$2,$3,'Test')`, conversation, owner.ID, agent.WorkspaceID); err != nil {
				t.Fatal(err)
			}
			router := chi.NewRouter()
			router.Post("/conversations/{conversationID}/messages", s.chat)
			r := httptest.NewRequest(http.MethodPost, "/conversations/"+conversation+"/messages", strings.NewReader(`{"content":"Quy định nội bộ áp dụng như thế nào?"}`))
			r = r.WithContext(context.WithValue(r.Context(), userContextKey, owner))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "event: done") {
				t.Fatalf("chat failed: %d %s", w.Code, w.Body.String())
			}
			if !partial && modelCalls.Load() != 1 {
				t.Fatalf("expected planner only, got %d model calls", modelCalls.Load())
			}
			var answer string
			if err := s.db.QueryRow(ctx, `SELECT content FROM messages WHERE conversation_id=$1 AND role='assistant'`, conversation).Scan(&answer); err != nil {
				t.Fatal(err)
			}
			if !partial && answer != missingKnowledgeAnswer(true, 0, mode == "failed") {
				t.Fatalf("unsupported answer persisted: %q", answer)
			}
			if partial {
				if modelCalls.Load() < 2 || answer != partialKnowledgeNotice+"Theo nguồn hiện có [1]." || !strings.Contains(w.Body.String(), "retrieval_partial") {
					t.Fatalf("partial answer lost warning/evidence: %q", answer)
				}
				var output string
				if err := s.db.QueryRow(ctx, `SELECT steps.output::text FROM run_steps steps JOIN runs ON runs.id=steps.run_id WHERE runs.resource_id=$1 AND steps.node_id='retrieval'`, conversation).Scan(&output); err != nil {
					t.Fatal(err)
				}
				wantStatus := "failed"
				if mode == "timeout" {
					wantStatus = "timed_out"
				}
				if !strings.Contains(output, `"partial": true`) || !strings.Contains(output, wantStatus) || !strings.Contains(output, "ready") {
					t.Fatalf("missing per-source diagnostics: %s", output)
				}
			}
		})
	}
}
