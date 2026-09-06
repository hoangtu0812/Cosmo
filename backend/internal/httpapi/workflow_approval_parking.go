package httpapi

import (
	"context"
	"cosmo/backend/internal/tools"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type workflowParkKey struct{}

var errWorkflowSuspended = errors.New("workflow waiting for approval")

// The node checkpoint already contains every preceding output. Persist the
// exact consent and release ownership atomically before returning the worker.
func (s *Server) parkWorkflowApproval(ctx context.Context, execution *workflowExecution, approval toolApproval) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	tag, err := tx.Exec(ctx, `UPDATE workflow_executions SET status='waiting_approval',approval_id=$3,lease_owner='',lease_until=NOW() WHERE id=$1 AND lease_owner=$2 AND status='running' AND lease_until>NOW() AND active_node<>''`, execution.ID, execution.owner, approval.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errWorkflowResume
	}
	tag, err = tx.Exec(ctx, `UPDATE tool_approvals SET lease_until=expires_at WHERE id=$1 AND status='pending' AND expires_at>NOW()`, approval.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errApprovalClosed
	}
	raw, err := json.Marshal(approval)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO workflow_execution_events(execution_id,frame) VALUES($1,$2)`, execution.ID, fmt.Sprintf("event: approval\ndata: %s\n\n", raw)); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	execution.parked = true
	return errWorkflowSuspended
}

func (s *Server) resumeWorkflowApproval(ctx context.Context, execution *workflowExecution, toolID, actionID string) (tools.CallResult, error) {
	var status string
	var live bool
	var raw []byte
	err := s.db.QueryRow(ctx, `SELECT a.status,a.expires_at>NOW(),a.request FROM tool_approvals a JOIN workflow_executions e ON e.approval_id=a.id WHERE e.id=$1 AND e.lease_owner=$2 AND e.status='running' AND e.lease_until>NOW() AND a.id=$3 AND a.tool_id=$4 AND a.actor_id=e.actor_id AND a.workspace_id=e.workspace_id`, execution.ID, execution.owner, execution.ApprovalID, toolID).Scan(&status, &live, &raw)
	if err != nil {
		return tools.CallResult{}, err
	}
	defer func() {
		finish, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = s.db.Exec(finish, `UPDATE tool_approvals SET status='expired',lease_until=NOW() WHERE id=$1 AND status IN ('pending','approved')`, execution.ApprovalID)
	}()
	if status != "approved" || !live {
		return tools.CallResult{}, errApprovalClosed
	}
	caller, ok := tools.CallerFrom(ctx)
	if !ok {
		return tools.CallResult{}, tools.ErrApprovalRequired
	}
	tool, err := s.tools.Get(ctx, toolID, caller.UserID, caller.WorkspaceID)
	if err != nil {
		return tools.CallResult{}, err
	}
	action, err := s.tools.Action(ctx, toolID, actionID)
	if err != nil {
		return tools.CallResult{}, err
	}
	var review struct {
		Arguments  map[string]any `json:"arguments"`
		Definition string         `json:"definition"`
	}
	if err = json.Unmarshal(raw, &review); err != nil {
		return tools.CallResult{}, err
	}
	if err = s.checkWorkflowExecution(ctx); err != nil {
		return tools.CallResult{}, err
	}
	result, operation, callErr := s.tools.InvokeConfirmed(ctx, tool, action, review.Arguments, true, execution.ApprovalID, review.Definition)
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
	_, err = s.db.Exec(finish, `UPDATE tool_approvals SET status=$2,operation_id=$3 WHERE id=$1 AND status='approved'`, execution.ApprovalID, status, operationID)
	if err != nil {
		return result, tools.ErrWriteUncertain
	}
	return result, callErr
}
