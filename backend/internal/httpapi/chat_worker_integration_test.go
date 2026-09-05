package httpapi

import (
	"context"
	"fmt"
	"io"
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

func startTestChatWorkers(t *testing.T, s *Server) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.RunChatWorker(ctx, ChatWorkerOptions{Poll: 10 * time.Millisecond, Lease: 3 * time.Second, Timeout: 10 * time.Second})
		}()
	}
	stop := func() { cancel(); wg.Wait() }
	t.Cleanup(stop)
	return stop
}

func TestChatQueueSurvivesSubscriberDisconnectAndRechecksAccess(t *testing.T) {
	for _, mode := range []string{"normal", "revoked", "changed"} {
		t.Run(mode, func(t *testing.T) {
			s, agent, owner, _ := agentAccessFixture(t)
			s.runs = runs.NewRepository(s.db)
			s.tools = tools.NewRepository(s.db, slog.Default(), nil, tools.EgressPolicy{}, tools.SearchBackend{})
			s.cfg.LLMRequestTimeout = time.Second
			ctx := context.Background()
			var calls atomic.Int32
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/model/info" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Detached answer\"}}]}\n\ndata: [DONE]\n\n")
			}))
			defer gateway.Close()
			if _, err := s.db.Exec(ctx, `INSERT INTO workspace_llm_configs(workspace_id,base_url,model) VALUES($1,$2,'test')`, agent.WorkspaceID, gateway.URL); err != nil {
				t.Fatal(err)
			}
			conversation := "con_" + randomID(18)
			if _, err := s.db.Exec(ctx, `INSERT INTO conversations(id,user_id,workspace_id,title) VALUES($1,$2,$3,'Test')`, conversation, owner.ID, agent.WorkspaceID); err != nil {
				t.Fatal(err)
			}
			router := chi.NewRouter()
			router.Post("/conversations/{conversationID}/messages", s.chat)
			subscriber, cancel := context.WithCancel(context.WithValue(ctx, userContextKey, owner))
			defer cancel()
			r := httptest.NewRequest(http.MethodPost, "/conversations/"+conversation+"/messages", strings.NewReader(`{"content":"Question","client_message_id":"detached"}`)).WithContext(subscriber)
			done := make(chan struct{})
			go func() { router.ServeHTTP(httptest.NewRecorder(), r); close(done) }()
			awaitChatStatus(t, s, conversation, "detached", "queued")
			cancel()
			<-done
			if calls.Load() != 0 {
				t.Fatal("HTTP subscriber executed the model")
			}
			if mode == "revoked" {
				if _, err := s.db.Exec(ctx, `DELETE FROM workspace_memberships WHERE workspace_id=$1 AND user_id=$2`, agent.WorkspaceID, owner.ID); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "changed" {
				s.cfg.LLMSystemPrompt = "Changed instructions while queued"
			}
			stop := startTestChatWorkers(t, s)
			want := "succeeded"
			if mode != "normal" {
				want = "interrupted"
			}
			awaitChatStatus(t, s, conversation, "detached", want)
			stop()
			if mode != "normal" && calls.Load() != 0 {
				t.Fatal("worker ran after membership revocation")
			}
			if mode == "normal" && calls.Load() == 0 {
				t.Fatal("queued turn did not execute after disconnect")
			}
		})
	}
}

func TestChatWorkerCancelsGatewayWhenRunIsCancelled(t *testing.T) {
	s, agent, owner, _ := agentAccessFixture(t)
	s.runs = runs.NewRepository(s.db)
	s.tools = tools.NewRepository(s.db, slog.Default(), nil, tools.EgressPolicy{}, tools.SearchBackend{})
	s.cfg.LLMRequestTimeout = 10 * time.Second
	ctx := context.Background()
	started, stopped := make(chan struct{}), make(chan struct{})
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/model/info" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.ReadAll(r.Body)
		close(started)
		<-r.Context().Done()
		close(stopped)
	}))
	defer gateway.Close()
	if _, err := s.db.Exec(ctx, `INSERT INTO workspace_llm_configs(workspace_id,base_url,model) VALUES($1,$2,'test')`, agent.WorkspaceID, gateway.URL); err != nil {
		t.Fatal(err)
	}
	conversation := "con_" + randomID(18)
	if _, err := s.db.Exec(ctx, `INSERT INTO conversations(id,user_id,workspace_id,title) VALUES($1,$2,$3,'Test')`, conversation, owner.ID, agent.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	stop := startTestChatWorkers(t, s)
	defer stop()
	router := chi.NewRouter()
	router.Post("/conversations/{conversationID}/messages", s.chat)
	r := httptest.NewRequest(http.MethodPost, "/conversations/"+conversation+"/messages", strings.NewReader(`{"content":"Question","client_message_id":"cancel-me"}`)).WithContext(context.WithValue(ctx, userContextKey, owner))
	done := make(chan struct{})
	go func() { router.ServeHTTP(httptest.NewRecorder(), r); close(done) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("gateway did not start")
	}
	var runID string
	if err := s.db.QueryRow(ctx, `SELECT run_id FROM chat_turns WHERE conversation_id=$1`, conversation).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.runs.Cancel(ctx, runID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not reach gateway")
	}
	awaitChatStatus(t, s, conversation, "cancel-me", "interrupted")
	<-done
	var answers int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM messages WHERE conversation_id=$1 AND role='assistant'`, conversation).Scan(&answers); err != nil || answers != 0 {
		t.Fatalf("cancelled run saved an answer: %d %v", answers, err)
	}
	run, err := s.runs.Get(ctx, runID)
	if err != nil || run.Status != runs.Cancelled {
		t.Fatalf("cancellation overwritten: %+v %v", run, err)
	}
}

func TestChatQueueRecoveryFencesDeadWorkerAndAdvancesFIFO(t *testing.T) {
	s, agent, owner, _ := agentAccessFixture(t)
	s.runs = runs.NewRepository(s.db)
	ctx := context.Background()
	conversation := "con_" + randomID(18)
	if _, err := s.db.Exec(ctx, `INSERT INTO conversations(id,user_id,workspace_id,title) VALUES($1,$2,$3,'Queue')`, conversation, owner.ID, agent.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"first", "second"} {
		_, _, _, err := s.acceptChatQuestion(ctx, Message{ID: "msg_" + randomID(18), ConversationID: conversation, Content: key, CreatedAt: time.Now()}, runs.NewRun{WorkspaceID: agent.WorkspaceID, ActorUserID: owner.ID, ResourceType: "conversation", ResourceID: conversation}, nil, chatTurnIdentity{ClientMessageID: key, RequestHash: key, AssistantID: "msg_" + randomID(18)})
		if err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.claimChatTurn(ctx, "dead-worker", time.Minute)
	if err != nil || first == nil || first.Identity.ClientMessageID != "first" {
		t.Fatalf("claim first: %+v %v", first, err)
	}
	if next, err := s.claimChatTurn(ctx, "other-worker", time.Minute); err != nil || next != nil {
		t.Fatalf("ran two turns in same conversation: %+v %v", next, err)
	}
	if _, err = s.db.Exec(ctx, `UPDATE chat_turns SET lease_expires_at=NOW()-interval '1 second' WHERE conversation_id=$1 AND client_message_id='first'`, conversation); err != nil {
		t.Fatal(err)
	}
	// Simulate a replacement process reading the persisted queue.
	replacement := *s
	if err = replacement.recoverChatTurns(ctx); err != nil {
		t.Fatal(err)
	}
	awaitChatStatus(t, s, conversation, "first", "interrupted")
	oldRun, err := s.runs.Get(ctx, first.Run.ID)
	if err != nil || oldRun.Status != runs.Failed {
		t.Fatalf("dead run stayed active: %+v %v", oldRun, err)
	}
	staleCtx, cancel := context.WithCancel(context.WithValue(ctx, chatExecutionKey{}, first))
	defer cancel()
	writer := chatEventWriter{server: s, ctx: staleCtx, execution: first, cancel: cancel, header: make(http.Header)}
	if _, err = writer.Write([]byte("event: delta\ndata: {}\n\n")); err == nil {
		t.Fatal("dead worker wrote a late event")
	}
	if s.checkChatExecution(staleCtx) == nil {
		t.Fatal("dead worker retained invocation permission")
	}
	next, err := replacement.claimChatTurn(ctx, "replacement", time.Minute)
	if err != nil || next == nil || next.Identity.ClientMessageID != "second" {
		t.Fatalf("queue did not resume: %+v %v", next, err)
	}
	if _, _, _, err = s.acceptChatQuestion(ctx, Message{ID: "msg_" + randomID(18), ConversationID: conversation, Content: "first", CreatedAt: time.Now()}, runs.NewRun{WorkspaceID: agent.WorkspaceID, ActorUserID: owner.ID, ResourceType: "conversation", ResourceID: conversation}, nil, first.Identity); err == nil {
		t.Fatal("recovery allowed dead turn to rerun")
	}
}

func TestChatEventsResumeAfterCursor(t *testing.T) {
	s, agent, owner, _ := agentAccessFixture(t)
	s.runs = runs.NewRepository(s.db)
	ctx := context.Background()
	conversation := "con_" + randomID(18)
	if _, err := s.db.Exec(ctx, `INSERT INTO conversations(id,user_id,workspace_id,title) VALUES($1,$2,$3,'Replay')`, conversation, owner.ID, agent.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := s.acceptChatQuestion(ctx, Message{ID: "msg_" + randomID(18), ConversationID: conversation, Content: "Question", CreatedAt: time.Now()}, runs.NewRun{WorkspaceID: agent.WorkspaceID, ActorUserID: owner.ID, ResourceType: "conversation", ResourceID: conversation}, nil, chatTurnIdentity{ClientMessageID: "replay", RequestHash: "hash", AssistantID: "msg_" + randomID(18)})
	if err != nil {
		t.Fatal(err)
	}
	execution, err := s.claimChatTurn(ctx, "live-worker", time.Minute)
	if err != nil || execution == nil {
		t.Fatal(err)
	}
	writerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	writer := chatEventWriter{server: s, ctx: writerCtx, execution: execution, cancel: cancel, header: make(http.Header)}
	if _, err = writer.Write([]byte("event: delta\ndata: {\"content\":\"first-fragment\"}\n\n")); err != nil {
		t.Fatal(err)
	}
	var cursor int64
	if err = s.db.QueryRow(ctx, `SELECT max(id) FROM chat_turn_events WHERE conversation_id=$1`, conversation).Scan(&cursor); err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Write([]byte("event: delta\ndata: {\"content\":\"second-fragment\"}\n\n")); err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Write([]byte("event: done\ndata: {\"message\":{\"content\":\"saved\"}}\n\n")); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set("Last-Event-ID", fmt.Sprint(cursor))
	w := httptest.NewRecorder()
	s.followChatTurn(w, r, conversation, "replay", "hash")
	if strings.Contains(w.Body.String(), "first-fragment") || !strings.Contains(w.Body.String(), "second-fragment") || !strings.Contains(w.Body.String(), "event: done") {
		t.Fatalf("bad cursor replay: %s", w.Body.String())
	}
}

func awaitChatStatus(t *testing.T, s *Server, conversation, key, status string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var got string
		err := s.db.QueryRow(ctx, `SELECT status FROM chat_turns WHERE conversation_id=$1 AND client_message_id=$2`, conversation, key).Scan(&got)
		if err == nil && got == status {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("turn %s status=%s want %s: %v", key, got, status, err)
		case <-ticker.C:
		}
	}
}

func TestChatWorkerDoesNotSaveTruncatedModelStream(t *testing.T) {
	s, agent, owner, _ := agentAccessFixture(t)
	s.runs = runs.NewRepository(s.db)
	s.tools = tools.NewRepository(s.db, slog.Default(), nil, tools.EgressPolicy{}, tools.SearchBackend{})
	ctx := context.Background()
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/model/info" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"unfinished answer\"}}]}\n\n")
	}))
	defer gateway.Close()
	if _, err := s.db.Exec(ctx, `INSERT INTO workspace_llm_configs(workspace_id,base_url,model) VALUES($1,$2,'test')`, agent.WorkspaceID, gateway.URL); err != nil {
		t.Fatal(err)
	}
	conversation := "con_" + randomID(18)
	if _, err := s.db.Exec(ctx, `INSERT INTO conversations(id,user_id,workspace_id,title) VALUES($1,$2,$3,'Truncated')`, conversation, owner.ID, agent.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	stop := startTestChatWorkers(t, s)
	defer stop()
	router := chi.NewRouter()
	router.Post("/conversations/{conversationID}/messages", s.chat)
	r := httptest.NewRequest(http.MethodPost, "/conversations/"+conversation+"/messages", strings.NewReader(`{"content":"Question","client_message_id":"truncated"}`)).WithContext(context.WithValue(ctx, userContextKey, owner))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	awaitChatStatus(t, s, conversation, "truncated", "interrupted")
	var answers int
	s.db.QueryRow(ctx, `SELECT count(*) FROM messages WHERE conversation_id=$1 AND role='assistant'`, conversation).Scan(&answers)
	if answers != 0 || strings.Contains(w.Body.String(), "event: done") {
		t.Fatal("truncated response was saved as completed")
	}
}
