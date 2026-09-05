package httpapi

import (
	"context"
	"cosmo/backend/internal/runs"
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
)

func TestChatUsesDecisionAnswerWithoutAnotherGeneration(t *testing.T) {
	s, agent, owner, _ := agentAccessFixture(t)
	ctx := context.Background()
	s.runs = runs.NewRepository(s.db)
	s.tools = tools.NewRepository(s.db, slog.Default(), nil, tools.EgressPolicy{}, tools.SearchBackend{})
	toolID := "tol_" + randomID(18)
	if _, err := s.db.Exec(ctx, `INSERT INTO tools(id,name,owner_user_id,owner_workspace_id,visibility,kind,auth_type) VALUES($1,'Test tool',$2,$3,'workspace','http','none')`, toolID, owner.ID, agent.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.tools.SaveAction(ctx, toolID, "", tools.Action{Name: "probe", Method: "GET", Path: "/"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO workspace_tools(workspace_id,tool_id,auto_call) VALUES($1,$2,true)`, agent.WorkspaceID, toolID); err != nil {
		t.Fatal(err)
	}
	var decisions, generations atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/model/info" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["tools"]; ok {
			decisions.Add(1)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"choices":[{"message":{"content":"The existing final answer"}}]}`)
			return
		}
		generations.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"unexpected rewrite\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer gateway.Close()
	if _, err := s.db.Exec(ctx, `INSERT INTO workspace_llm_configs(workspace_id,base_url,model) VALUES($1,$2,'test')`, agent.WorkspaceID, gateway.URL); err != nil {
		t.Fatal(err)
	}
	conversation := "con_" + randomID(18)
	if _, err := s.db.Exec(ctx, `INSERT INTO conversations(id,user_id,workspace_id,title) VALUES($1,$2,$3,'Existing')`, conversation, owner.ID, agent.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	// Avoid title generation; suggestions are auxiliary and use the same stream
	// API, so count them separately from the saved answer below.
	if _, err := s.db.Exec(ctx, `INSERT INTO messages(id,conversation_id,role,content) VALUES($1,$2,'user','Previous question')`, "msg_"+randomID(18), conversation); err != nil {
		t.Fatal(err)
	}
	stop := startTestChatWorkers(t, s)
	defer stop()
	router := chi.NewRouter()
	router.Post("/conversations/{conversationID}/messages", s.chat)
	req := httptest.NewRequest("POST", "/conversations/"+conversation+"/messages", strings.NewReader(`{"content":"Question","client_message_id":"decision-answer"}`)).WithContext(context.WithValue(ctx, userContextKey, owner))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	awaitChatStatus(t, s, conversation, "decision-answer", "succeeded")
	var answer string
	s.db.QueryRow(ctx, `SELECT content FROM messages WHERE conversation_id=$1 AND role='assistant'`, conversation).Scan(&answer)
	if answer != "The existing final answer" || decisions.Load() != 1 || generations.Load() > 1 {
		t.Fatalf("answer was regenerated: %q decisions=%d other=%d", answer, decisions.Load(), generations.Load())
	}
	var decisionAccounting int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM run_steps s JOIN chat_turns t ON t.run_id=s.run_id WHERE t.conversation_id=$1 AND s.type='model_call' AND s.output->>'phase'='tool_decision' AND s.output->'usage'='null'::jsonb`, conversation).Scan(&decisionAccounting); err != nil || decisionAccounting != 1 {
		t.Fatalf("missing per-call accounting: count=%d err=%v", decisionAccounting, err)
	}
}
