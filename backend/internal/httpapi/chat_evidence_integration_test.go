package httpapi

import (
	"context"
	"fmt"
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

func TestChatMissingEvidenceDoesNotGenerateUnsupportedAnswer(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprint(failed), func(t *testing.T) {
			s, agent, owner, _ := agentAccessFixture(t)
			ctx := context.Background()
			var modelCalls atomic.Int32
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				modelCalls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"CO\"}}]}\n\ndata: [DONE]\n\n")
			}))
			defer gateway.Close()
			rag := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if failed {
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
					return
				}
				fmt.Fprint(w, `{"results":[]}`)
			}))
			defer rag.Close()
			s.cfg.LLMRequestTimeout = time.Second
			s.knowledge = knowledge.New(rag.URL, time.Second)
			s.runs = runs.NewRepository(s.db)
			s.tools = tools.NewRepository(s.db, slog.Default(), nil, tools.EgressPolicy{}, tools.SearchBackend{})
			if _, err := s.db.Exec(ctx, `INSERT INTO workspace_llm_configs(workspace_id,base_url,model) VALUES($1,$2,'test-model')`, agent.WorkspaceID, gateway.URL); err != nil {
				t.Fatal(err)
			}
			kb := "kb_" + randomID(18)
			if _, err := s.db.Exec(ctx, `INSERT INTO knowledge_bases(id,name,owner_workspace_id,version,embedding_model,rerank_enabled) VALUES($1,'Policies',$2,1,'test-embed',false)`, kb, agent.WorkspaceID); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = s.db.Exec(ctx, `DELETE FROM knowledge_bases WHERE id=$1`, kb) })
			if _, err := s.db.Exec(ctx, `INSERT INTO knowledge_mounts(kb_id,target_type,target_id) VALUES($1,'workspace',$2)`, kb, agent.WorkspaceID); err != nil {
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
			if modelCalls.Load() != 1 {
				t.Fatalf("expected planner only, got %d model calls", modelCalls.Load())
			}
			var answer string
			if err := s.db.QueryRow(ctx, `SELECT content FROM messages WHERE conversation_id=$1 AND role='assistant'`, conversation).Scan(&answer); err != nil {
				t.Fatal(err)
			}
			if answer != missingKnowledgeAnswer(true, 0, failed) {
				t.Fatalf("unsupported answer persisted: %q", answer)
			}
		})
	}
}
