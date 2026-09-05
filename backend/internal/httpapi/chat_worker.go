package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"cosmo/backend/internal/modelgateway"
	"cosmo/backend/internal/runs"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

type chatExecutionKey struct{}
type chatExecution struct {
	Conversation string
	Identity     chatTurnIdentity
	Question     Message
	Run          runs.Run
	First        bool
	Owner        string
}

func currentChatExecution(ctx context.Context) *chatExecution {
	execution, _ := ctx.Value(chatExecutionKey{}).(*chatExecution)
	return execution
}

type ChatWorkerOptions struct{ Poll, Lease, Timeout time.Duration }

// RunChatWorker executes one conversation at a time per worker. Multiple
// processes/workers share the queue; row locking and sequence protect FIFO.
func (s *Server) RunChatWorker(ctx context.Context, options ChatWorkerOptions) error {
	if options.Poll <= 0 {
		options.Poll = 200 * time.Millisecond
	}
	if options.Lease <= 0 {
		options.Lease = 30 * time.Second
	}
	if options.Timeout <= 0 {
		options.Timeout = 10 * time.Minute
	}
	owner := "chat_" + randomID(18)
	ticker := time.NewTicker(options.Poll)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		if err := s.recoverChatTurns(ctx); err != nil && ctx.Err() == nil {
			s.logger.Error("recover chat queue", "error", err)
		}
		execution, err := s.claimChatTurn(ctx, owner, options.Lease)
		if err != nil && ctx.Err() == nil {
			s.logger.Error("claim chat queue", "error", err)
		}
		if execution != nil {
			s.executeChatTurn(ctx, execution, options)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (s *Server) claimChatTurn(ctx context.Context, owner string, lease time.Duration) (*chatExecution, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var execution chatExecution
	execution.Owner = owner
	err = tx.QueryRow(ctx, `SELECT t.conversation_id,t.client_message_id,t.request_hash,t.user_message_id,t.assistant_message_id,t.run_id,t.request_payload,t.runtime_hash,t.readable_ids,t.is_first_turn
	FROM chat_turns t JOIN runs r ON r.id=t.run_id
	WHERE t.status='queued' AND r.status='queued' AND NOT EXISTS(
	 SELECT 1 FROM chat_turns earlier WHERE earlier.conversation_id=t.conversation_id AND earlier.sequence<t.sequence AND earlier.status IN ('queued','executing'))
	ORDER BY t.sequence FOR UPDATE OF t SKIP LOCKED LIMIT 1`).Scan(&execution.Conversation, &execution.Identity.ClientMessageID, &execution.Identity.RequestHash, &execution.Question.ID, &execution.Identity.AssistantID, &execution.Run.ID, &execution.Identity.Payload, &execution.Identity.RuntimeHash, &execution.Identity.ReadableIDs, &execution.First)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE chat_turns SET status='executing',lease_owner=$3,lease_expires_at=NOW()+$4::interval WHERE conversation_id=$1 AND client_message_id=$2`, execution.Conversation, execution.Identity.ClientMessageID, owner, lease.String()); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &execution, nil
}

// Queued work survives restart. Started work with an expired lease is never
// executed a second time: its external tool outcome may be unknown.
func (s *Server) recoverChatTurns(ctx context.Context) error {
	// A saved answer is a durable completion checkpoint even if the process
	// died before recording the run's terminal status or the final SSE event.
	if _, err := s.db.Exec(ctx, `WITH completed AS (
	 UPDATE runs r SET status='succeeded',finished_at=COALESCE(r.finished_at,NOW()),output=jsonb_build_object('message_id',t.assistant_message_id)
	 FROM chat_turns t WHERE t.run_id=r.id AND t.status='succeeded' AND t.lease_expires_at<NOW() AND r.status IN ('queued','running')
	 AND EXISTS(SELECT 1 FROM messages m WHERE m.id=t.assistant_message_id AND m.conversation_id=t.conversation_id)
	 RETURNING r.id
	) UPDATE run_steps SET status='succeeded',finished_at=COALESCE(finished_at,NOW()) WHERE run_id IN (SELECT id FROM completed) AND node_id='generation' AND status='running'`); err != nil {
		return err
	}
	_, err := s.db.Exec(ctx, `WITH stale AS (
	 SELECT t.conversation_id,t.client_message_id,t.run_id FROM chat_turns t LEFT JOIN runs r ON r.id=t.run_id
	 WHERE (t.status='executing' AND t.lease_expires_at<NOW()) OR
	 (t.status='queued' AND (r.id IS NULL OR r.status IN ('cancelled','failed','timed_out','succeeded')))
	 FOR UPDATE OF t SKIP LOCKED
	), closed AS (
	 UPDATE chat_turns t SET status='interrupted',finished_at=NOW(),lease_owner='',lease_expires_at=NULL FROM stale
	 WHERE t.conversation_id=stale.conversation_id AND t.client_message_id=stale.client_message_id RETURNING t.run_id
	), failed AS (
	 UPDATE runs SET status='failed',finished_at=NOW(),error_code='chat_execution_interrupted',error_message='Execution interrupted; external effects require reconciliation'
	 WHERE id IN (SELECT run_id FROM closed) AND status IN ('queued','running','waiting_approval') RETURNING id
	) UPDATE run_steps SET status='failed',finished_at=NOW(),error_code='chat_execution_interrupted',error_message='Execution interrupted'
	WHERE run_id IN (SELECT run_id FROM closed) AND status IN ('queued','running','waiting_approval')`)
	return err
}

func (s *Server) executeChatTurn(parent context.Context, execution *chatExecution, options ChatWorkerOptions) {
	ctx, cancel := context.WithTimeout(parent, options.Timeout)
	defer cancel()
	ctx = context.WithValue(ctx, chatExecutionKey{}, execution)
	done := make(chan struct{})
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(min(options.Lease/3, 500*time.Millisecond))
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				heartbeatCtx, stop := context.WithTimeout(ctx, min(options.Lease/3, 2*time.Second))
				tag, err := s.db.Exec(heartbeatCtx, `UPDATE chat_turns t SET lease_expires_at=NOW()+$4::interval FROM runs r WHERE t.conversation_id=$1 AND t.client_message_id=$2 AND t.lease_owner=$3 AND t.status IN ('executing','succeeded') AND t.lease_expires_at>NOW() AND r.id=t.run_id AND r.status IN ('queued','running','succeeded')`, execution.Conversation, execution.Identity.ClientMessageID, execution.Owner, options.Lease.String())
				stop()
				if err != nil || tag.RowsAffected() != 1 {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { close(done); cancel(); <-heartbeatDone; s.finishChatExecution(execution) }()
	defer func() {
		if recovered := recover(); recovered != nil {
			s.logger.Error("chat worker panic", "run_id", execution.Run.ID, "panic", fmt.Sprint(recovered))
		}
	}()
	run, err := s.runs.Get(ctx, execution.Run.ID)
	if err != nil {
		return
	}
	execution.Run = run
	var user User
	if err = s.db.QueryRow(ctx, `SELECT u.id,u.email,u.name,u.role FROM users u JOIN workspace_memberships m ON m.user_id=u.id WHERE u.id=$1 AND m.workspace_id=$2`, run.ActorUserID, run.WorkspaceID).Scan(&user.ID, &user.Email, &user.Name, &user.Role); err != nil {
		return
	}
	if err = s.db.QueryRow(ctx, `SELECT id,conversation_id,role,content,created_at FROM messages WHERE id=$1 AND conversation_id=$2`, execution.Question.ID, execution.Conversation).Scan(&execution.Question.ID, &execution.Question.ConversationID, &execution.Question.Role, &execution.Question.Content, &execution.Question.CreatedAt); err != nil {
		return
	}
	ctx = context.WithValue(ctx, userContextKey, user)
	route := chi.NewRouteContext()
	route.URLParams.Add("conversationID", execution.Conversation)
	ctx = context.WithValue(ctx, chi.RouteCtxKey, route)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "/api/conversations/"+execution.Conversation+"/messages", strings.NewReader(string(execution.Identity.Payload)))
	if err != nil {
		return
	}
	writer := &chatEventWriter{server: s, ctx: ctx, execution: execution, cancel: cancel, header: make(http.Header)}
	s.chat(writer, request)
}

func (s *Server) finishChatExecution(execution *chatExecution) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := s.db.Exec(ctx, `WITH closed AS (
	UPDATE chat_turns SET status='interrupted',finished_at=NOW(),lease_owner='',lease_expires_at=NULL
	WHERE conversation_id=$1 AND client_message_id=$2 AND lease_owner=$3 AND status='executing' RETURNING run_id
	), failed AS (
	UPDATE runs SET status='failed',finished_at=NOW(),error_code='chat_execution_interrupted',error_message='Execution interrupted'
	WHERE id IN (SELECT run_id FROM closed) AND status IN ('queued','running','waiting_approval') RETURNING id
	) UPDATE run_steps SET status='failed',finished_at=NOW(),error_code='chat_execution_interrupted',error_message='Execution interrupted'
	WHERE run_id IN (SELECT run_id FROM closed) AND status IN ('queued','running','waiting_approval')`, execution.Conversation, execution.Identity.ClientMessageID, execution.Owner)
	if err != nil {
		s.logger.Error("finish chat worker", "run_id", execution.Run.ID, "error", err)
	}
}

func (s *Server) checkChatExecution(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	execution := currentChatExecution(ctx)
	if execution == nil {
		return nil
	}
	var valid bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM chat_turns t JOIN runs r ON r.id=t.run_id WHERE t.conversation_id=$1 AND t.client_message_id=$2 AND t.lease_owner=$3 AND t.lease_expires_at>NOW() AND t.status='executing' AND r.status='running')`, execution.Conversation, execution.Identity.ClientMessageID, execution.Owner).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return errors.New("chat execution lease lost or run cancelled")
	}
	return nil
}

func (s *Server) executionHistory(ctx context.Context, execution *chatExecution) ([]modelgateway.Message, error) {
	// Order known turns by admission sequence and user/assistant role. An
	// earlier answer can be created after this queued question, so timestamps
	// alone are insufficient. Exclude this and all future turns explicitly.
	rows, err := s.db.Query(ctx, `WITH current_turn AS (SELECT sequence,created_at FROM chat_turns WHERE conversation_id=$1 AND client_message_id=$2), recent AS (
	SELECT m.role,m.content,COALESCE(t.sequence,0) AS turn_sequence,m.created_at,m.id,
	CASE WHEN t.sequence IS NOT NULL AND m.role='assistant' THEN 1 ELSE 0 END AS role_order
	FROM messages m LEFT JOIN chat_turns t ON t.conversation_id=m.conversation_id AND (t.user_message_id=m.id OR t.assistant_message_id=m.id)
	CROSS JOIN current_turn c WHERE m.conversation_id=$1 AND ((t.sequence<c.sequence) OR (t.sequence IS NULL AND m.created_at<c.created_at))
	ORDER BY turn_sequence DESC,role_order DESC,m.created_at DESC,m.id DESC LIMIT 39)
	SELECT role,content FROM recent ORDER BY turn_sequence,role_order,created_at,id`, execution.Conversation, execution.Identity.ClientMessageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	history := []modelgateway.Message{}
	for rows.Next() {
		var message modelgateway.Message
		if err := rows.Scan(&message.Role, &message.Content); err != nil {
			return nil, err
		}
		history = append(history, message)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return append(history, modelgateway.Message{Role: "user", Content: execution.Question.Content}), nil
}
