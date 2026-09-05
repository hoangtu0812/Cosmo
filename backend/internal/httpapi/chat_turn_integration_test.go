package httpapi

import (
	"context"
	"encoding/json"
	"errors"
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

func TestChatTurnRetriesReplayWithoutExecutingAgain(t *testing.T) {
	s, agent, owner, member := agentAccessFixture(t)
	s.runs = runs.NewRepository(s.db)
	s.tools = tools.NewRepository(s.db, slog.Default(), nil, tools.EgressPolicy{}, tools.SearchBackend{})
	s.cfg.LLMRequestTimeout = 5 * time.Second
	ctx := context.Background()
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []modelgateway.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if len(payload.Messages) > 0 && payload.Messages[len(payload.Messages)-1].Content == "Next question" {
			found := false
			for _, m := range payload.Messages {
				if m.Role == "assistant" && m.Content == "Saved answer" {
					found = true
				}
			}
			if !found {
				t.Error("queued question lost the previous answer created after admission")
			}
		}
		if calls.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Saved answer\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer gateway.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	if _, err := s.db.Exec(ctx, `INSERT INTO workspace_llm_configs(workspace_id,base_url,model) VALUES($1,$2,'test-model')`, agent.WorkspaceID, gateway.URL); err != nil {
		t.Fatal(err)
	}
	conversation := "con_" + randomID(18)
	if _, err := s.db.Exec(ctx, `INSERT INTO conversations(id,user_id,workspace_id,title) VALUES($1,$2,$3,'Test')`, conversation, owner.ID, agent.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	stopWorkers := startTestChatWorkers(t, s)
	router.Post("/conversations/{conversationID}/messages", s.chat)
	router.Get("/conversations/{conversationID}/messages", s.listMessages)
	request := func(user User, key, content string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/conversations/"+conversation+"/messages", strings.NewReader(fmt.Sprintf(`{"client_message_id":%q,"content":%q}`, key, content)))
		r = r.WithContext(context.WithValue(r.Context(), userContextKey, user))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- request(owner, "request-1", "Question") }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first execution never started")
	}
	for _, test := range []struct{ key, content, code string }{{"request-1", "Changed", "chat_turn_mismatch"}} {
		w := request(owner, test.key, test.content)
		if w.Code != 409 || !strings.Contains(w.Body.String(), test.code) {
			t.Fatalf("expected %s: %d %s", test.code, w.Code, w.Body.String())
		}
	}
	duplicate := make(chan *httptest.ResponseRecorder, 1)
	next := make(chan *httptest.ResponseRecorder, 1)
	go func() { duplicate <- request(owner, "request-1", "Question") }()
	go func() { next <- request(owner, "request-2", "Next question") }()
	awaitChatStatus(t, s, conversation, "request-2", "queued")
	if w := request(member, "request-1", "Question"); w.Code != 404 {
		t.Fatalf("another user read turn identity: %d", w.Code)
	}
	if calls.Load() != 1 {
		t.Fatalf("duplicate model execution: %d", calls.Load())
	}
	close(release)
	w := <-first
	if w.Code != 200 || !strings.Contains(w.Body.String(), "event: done") {
		t.Fatalf("first failed: %d %s", w.Code, w.Body.String())
	}
	for _, result := range []<-chan *httptest.ResponseRecorder{duplicate, next} {
		w := <-result
		if w.Code != 200 || !strings.Contains(w.Body.String(), "event: done") {
			t.Fatalf("queued/subscribed request failed: %d %s", w.Code, w.Body.String())
		}
	}
	stopWorkers()
	after := calls.Load()
	w = request(owner, "request-1", "Question")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"replayed":true`) || !strings.Contains(w.Body.String(), "Saved answer") || calls.Load() != after {
		t.Fatalf("retry did not replay: %d %s calls=%d", w.Code, w.Body.String(), calls.Load())
	}
	var messages, runCount, turns int
	if err := s.db.QueryRow(ctx, `SELECT (SELECT count(*) FROM messages WHERE conversation_id=$1),(SELECT count(*) FROM runs WHERE resource_id=$1),(SELECT count(*) FROM chat_turns WHERE conversation_id=$1)`, conversation).Scan(&messages, &runCount, &turns); err != nil {
		t.Fatal(err)
	}
	if messages != 4 || runCount != 2 || turns != 2 {
		t.Fatalf("duplicate persistent state: messages=%d runs=%d turns=%d", messages, runCount, turns)
	}
	transcriptRequest := httptest.NewRequest(http.MethodGet, "/conversations/"+conversation+"/messages", nil).WithContext(context.WithValue(ctx, userContextKey, owner))
	transcriptResponse := httptest.NewRecorder()
	router.ServeHTTP(transcriptResponse, transcriptRequest)
	var transcript struct {
		Messages []Message `json:"messages"`
	}
	if err := json.Unmarshal(transcriptResponse.Body.Bytes(), &transcript); err != nil {
		t.Fatal(err)
	}
	if len(transcript.Messages) != 4 {
		t.Fatalf("bad transcript: %s", transcriptResponse.Body.String())
	}
	for i, want := range []string{"Question", "Saved answer", "Next question", "Saved answer"} {
		if transcript.Messages[i].Content != want {
			t.Fatalf("queued transcript order: %+v", transcript.Messages)
		}
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM messages WHERE conversation_id=$1`, conversation); err != nil {
		t.Fatal(err)
	}
	w = request(owner, "request-1", "Question")
	if w.Code != 409 || calls.Load() != after {
		t.Fatalf("deleting transcript allowed rerun: %d calls=%d", w.Code, calls.Load())
	}
}

func TestChatTurnConcurrentAdmissionAndInterruptedIdentity(t *testing.T) {
	s, agent, owner, _ := agentAccessFixture(t)
	s.runs = runs.NewRepository(s.db)
	ctx := context.Background()
	conversation := "con_" + randomID(18)
	if _, err := s.db.Exec(ctx, `INSERT INTO conversations(id,user_id,workspace_id,title) VALUES($1,$2,$3,'Test')`, conversation, owner.ID, agent.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	identity := chatTurnIdentity{ClientMessageID: "same-request", RequestHash: chatRequestHash("Question", "", ""), AssistantID: "msg_" + randomID(18)}
	input := runs.NewRun{WorkspaceID: agent.WorkspaceID, ActorUserID: owner.ID, ResourceType: "conversation", ResourceID: conversation}
	start := make(chan struct{})
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			<-start
			_, _, _, err := s.acceptChatQuestion(ctx, Message{ID: "msg_" + randomID(18), ConversationID: conversation, Content: "Question", CreatedAt: time.Now()}, input, nil, identity)
			results <- err
		}()
	}
	close(start)
	winners := 0
	for i := 0; i < 8; i++ {
		err := <-results
		if err == nil {
			winners++
		} else {
			var turn *chatTurn
			if !errors.As(err, &turn) {
				t.Fatal(err)
			}
		}
	}
	if winners != 1 {
		t.Fatalf("expected one accepted turn: %d", winners)
	}
	before, err := lookupChatTurn(ctx, s.db, conversation, identity.ClientMessageID, identity.RequestHash)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.claimChatTurn(ctx, "test-owner", time.Second)
	if err != nil || claimed == nil {
		t.Fatalf("claim turn: %v", err)
	}
	s.finishChatExecution(claimed)
	_, _, _, err = s.acceptChatQuestion(ctx, Message{ID: "msg_" + randomID(18), ConversationID: conversation, Content: "Question", CreatedAt: time.Now()}, input, nil, identity)
	var old *chatTurn
	if !errors.As(err, &old) || old.Status != "interrupted" {
		t.Fatalf("interrupted turn reran: %v", err)
	}
	identity.ClientMessageID = "new-request"
	if _, _, _, err = s.acceptChatQuestion(ctx, Message{ID: "msg_" + randomID(18), ConversationID: conversation, Content: "Question", CreatedAt: time.Now()}, input, nil, identity); err != nil {
		t.Fatal(err)
	}
	after, err := lookupChatTurn(ctx, s.db, conversation, identity.ClientMessageID, identity.RequestHash)
	if err != nil {
		t.Fatal(err)
	}
	if after.Sequence <= before.Sequence {
		t.Fatalf("turn sequence did not increase: %d then %d", before.Sequence, after.Sequence)
	}
}
