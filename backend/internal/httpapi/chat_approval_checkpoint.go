package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"cosmo/backend/internal/modelgateway"
	"cosmo/backend/internal/runs"
	"cosmo/backend/internal/tools"
)

var errChatCheckpoint = errors.New("Không thể lưu điểm chờ xác nhận. Lượt chat đã dừng trước khi gửi lệnh.")

var errChatSuspended = errors.New("chat saved while waiting for approval")

type chatParkKey struct{}
type chatParkHandler func(toolApproval) error

// Only a parked approval is resumable. Once claimed, a crash remains an
// interrupted execution: the remote side may already have received a write.
type chatApprovalCheckpoint struct {
	ReadableHash string
	Version      int
	Deadline     time.Time
	Citations    []Citation
	ContextParts map[string]int
	Tools        chatToolCheckpoint
}

type chatToolCheckpoint struct {
	Round      int
	Calls      []modelgateway.ToolCall
	Index      int
	History    []modelgateway.Message
	Answer     string
	Reported   []ToolCall
	Blocked    map[string]string
	Attempts   map[string]int
	ApprovalID string
	ToolID     string
	ActionID   string
	StepID     string
}

func (s *Server) parkChatApproval(ctx context.Context, checkpoint *chatApprovalCheckpoint, approval toolApproval, shown ToolCall) error {
	execution := currentChatExecution(ctx)
	if execution == nil {
		return errors.New("missing chat execution")
	}
	checkpoint.Version = 1
	checkpoint.Deadline = execution.Deadline
	checkpoint.Tools.ApprovalID = approval.ID
	shown.ApprovalID = approval.ID
	raw, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	if len(raw) > 3*1024*1024 {
		return errors.New("Trạng thái Chat vượt giới hạn lưu điểm chờ xác nhận.")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var valid string
	err = tx.QueryRow(ctx, `SELECT t.run_id FROM chat_turns t JOIN runs r ON r.id=t.run_id WHERE t.run_id=$1 AND t.lease_owner=$2 AND t.status='executing' AND t.lease_expires_at>NOW() AND r.status='running' FOR UPDATE OF t,r`, execution.Run.ID, execution.Owner).Scan(&valid)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO chat_approval_checkpoints(run_id,conversation_id,approval_id,state) VALUES($1,$2,$3,$4) ON CONFLICT(run_id) DO UPDATE SET approval_id=EXCLUDED.approval_id,state=EXCLUDED.state`, execution.Run.ID, execution.Conversation, approval.ID, raw)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE tool_approvals SET lease_until=expires_at WHERE id=$1 AND status='pending' AND lease_until>NOW() AND expires_at>NOW()`, approval.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errApprovalClosed
	}
	tag, err = tx.Exec(ctx, `UPDATE run_steps SET status='waiting_approval' WHERE id=$1 AND run_id=$2 AND status='running'`, checkpoint.Tools.StepID, execution.Run.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("tool step no longer running")
	}
	if _, err = tx.Exec(ctx, `UPDATE runs SET status='waiting_approval' WHERE id=$1;`, execution.Run.ID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE chat_turns SET status='waiting_approval',lease_owner='',lease_expires_at=NULL WHERE run_id=$1`, execution.Run.ID); err != nil {
		return err
	}
	for _, event := range []struct{ Type, Step string }{{"run.waiting_approval", ""}, {"step.waiting_approval", checkpoint.Tools.StepID}} {
		_, err = tx.Exec(ctx, `WITH seq AS (UPDATE runs SET next_event_sequence=next_event_sequence+1 WHERE id=$1 RETURNING next_event_sequence-1 AS n) INSERT INTO run_events(run_id,step_id,sequence,type,payload) SELECT $1,NULLIF($2,''),n,$3,jsonb_build_object('approval_id',$4::text) FROM seq`, execution.Run.ID, event.Step, event.Type, approval.ID)
		if err != nil {
			return err
		}
	}
	// Persist the anchor and approval notice in the same commit as the park.
	for _, event := range []struct {
		Name string
		Data any
	}{{"tool", shown}, {"approval", approval}, {"status", map[string]string{"stage": "approval", "message": "Chờ xác nhận thao tác. Phiên đã được lưu."}}} {
		payload, err := json.Marshal(event.Data)
		if err != nil {
			return err
		}
		frame := fmt.Sprintf("event: %s\ndata: %s\n\n", event.Name, payload)
		if _, err = tx.Exec(ctx, `INSERT INTO chat_turn_events(conversation_id,client_message_id,frame) VALUES($1,$2,$3)`, execution.Conversation, execution.Identity.ClientMessageID, frame); err != nil {
			return err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	return errChatSuspended
}

func (s *Server) resumeChatApproval(ctx context.Context, state *chatToolCheckpoint) (tools.CallResult, error) {
	execution := currentChatExecution(ctx)
	if execution == nil {
		return tools.CallResult{}, errApprovalClosed
	}
	var status string
	var live bool
	var request []byte
	err := s.db.QueryRow(ctx, `SELECT a.status,a.expires_at>NOW(),a.request FROM tool_approvals a JOIN chat_approval_checkpoints c ON c.approval_id=a.id JOIN chat_turns t ON t.run_id=c.run_id JOIN runs r ON r.id=t.run_id WHERE a.id=$1 AND c.run_id=$2 AND t.lease_owner=$3 AND t.status='executing' AND t.lease_expires_at>NOW() AND r.status='running' AND a.actor_id=r.actor_user_id AND a.workspace_id=r.workspace_id`, state.ApprovalID, execution.Run.ID, execution.Owner).Scan(&status, &live, &request)
	if err != nil {
		return tools.CallResult{}, err
	}
	defer func() {
		finish, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = s.db.Exec(finish, `UPDATE tool_approvals SET status='expired' WHERE id=$1 AND status IN ('pending','approved')`, state.ApprovalID)
	}()
	if status != "approved" || !live {
		return tools.CallResult{}, errApprovalClosed
	}
	caller, ok := tools.CallerFrom(ctx)
	if !ok {
		return tools.CallResult{}, tools.ErrApprovalRequired
	}
	tool, err := s.tools.Get(ctx, state.ToolID, caller.UserID, caller.WorkspaceID)
	if err != nil {
		return tools.CallResult{}, err
	}
	action, err := s.tools.Action(ctx, state.ToolID, state.ActionID)
	if err != nil {
		return tools.CallResult{}, err
	}
	var review struct {
		Arguments  map[string]any `json:"arguments"`
		Definition string         `json:"definition"`
	}
	if err = json.Unmarshal(request, &review); err != nil {
		return tools.CallResult{}, err
	}
	if err = s.checkChatExecution(ctx); err != nil {
		return tools.CallResult{}, err
	}
	result, operation, callErr := s.tools.InvokeConfirmed(ctx, tool, action, review.Arguments, true, state.ApprovalID, review.Definition)
	status = "failed"
	operationID := ""
	if operation != nil {
		status = "uncertain"
		operationID = operation.ID
	}
	if callErr == nil {
		status = "completed"
	}
	finish, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = s.db.Exec(finish, `UPDATE tool_approvals SET status=$2,operation_id=$3 WHERE id=$1 AND status='approved'`, state.ApprovalID, status, operationID)
	if err != nil {
		return result, tools.ErrWriteUncertain
	}
	return result, callErr
}

func (s *Server) loadChatCheckpoint(ctx context.Context, execution *chatExecution) error {
	var raw []byte
	if err := s.db.QueryRow(ctx, `SELECT state FROM chat_approval_checkpoints WHERE run_id=$1`, execution.Run.ID).Scan(&raw); err != nil {
		return err
	}
	var cp chatApprovalCheckpoint
	if err := json.Unmarshal(raw, &cp); err != nil {
		return err
	}
	if cp.Version != 1 || cp.Deadline.IsZero() || cp.Tools.Index < 0 || cp.Tools.Index >= len(cp.Tools.Calls) || cp.Tools.ApprovalID == "" {
		return errors.New("invalid chat checkpoint")
	}
	execution.Checkpoint = &cp
	execution.Deadline = cp.Deadline
	return nil
}

// Keep bookkeeping independent of the HTTP subscriber while preserving the
// original execution deadline across every park/resume cycle.
func (s *Server) resumeChatStep(ctx context.Context, state *chatToolCheckpoint) (runs.Step, error) {
	return s.runs.TransitionStep(ctx, state.StepID, runs.Running, nil, "", "", "")
}

func (s *Server) cleanupChatCheckpoints(ctx context.Context, runID string) error {
	_, err := s.db.Exec(ctx, `WITH done AS (
 SELECT c.run_id,c.approval_id FROM chat_approval_checkpoints c JOIN chat_turns t ON t.run_id=c.run_id WHERE ($1='' OR c.run_id=$1) AND t.status IN ('succeeded','interrupted') FOR UPDATE OF c SKIP LOCKED LIMIT 100
 ), expired AS (UPDATE tool_approvals a SET status='expired' FROM done WHERE a.id=done.approval_id AND a.status IN ('pending','approved'))
 DELETE FROM chat_approval_checkpoints c USING done WHERE c.run_id=done.run_id`, runID)
	return err
}
