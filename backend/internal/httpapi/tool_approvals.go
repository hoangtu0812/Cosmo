package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"cosmo/backend/internal/tools"
	"github.com/go-chi/chi/v5"
)

var errApprovalClosed = errors.New("Yêu cầu xác nhận đã bị từ chối, hết hạn hoặc phiên chạy đã dừng.")

type toolApproval struct {
	ID          string          `json:"id"`
	ToolID      string          `json:"tool_id"`
	Status      string          `json:"status"`
	Request     json.RawMessage `json:"request"`
	ExpiresAt   time.Time       `json:"expires_at"`
	OperationID string          `json:"operation_id"`
}

// The waiting executor alone may dispatch. The decision endpoint only records
// consent; it never recreates an executor or replays a terminated run.
func (s *Server) awaitToolApproval(ctx context.Context, kind, source string, tool tools.Tool, action tools.Action, args map[string]any, report func(toolApproval)) (tools.CallResult, error) {
	caller, ok := tools.CallerFrom(ctx)
	if !ok {
		return tools.CallResult{}, tools.ErrApprovalRequired
	}
	current, err := s.tools.Get(ctx, tool.ID, caller.UserID, caller.WorkspaceID)
	if err != nil {
		return tools.CallResult{}, err
	}
	if !current.IsEditable || s.tools.PolicyReview(current, action) != s.tools.PolicyReview(tool, action) {
		return tools.CallResult{}, tools.ErrApprovalRequired
	}
	// Fixed values are part of the exact request the user reviews.
	effective := make(map[string]any, len(args))
	for k, v := range args {
		effective[k] = v
	}
	for _, p := range action.Parameters {
		if p.IsFixed() {
			effective[p.Name] = p.Value
		}
	}
	raw, err := json.Marshal(effective)
	if err != nil || len(raw) > tools.MaxArgumentBytes {
		return tools.CallResult{}, tools.ErrArguments
	}
	definition := s.tools.PolicyReview(tool, action)
	review, err := json.Marshal(map[string]any{"destination": tool.BaseURL, "action": action.Name, "method": action.Method, "path": action.Path, "arguments": effective, "definition": definition})
	if err != nil {
		return tools.CallResult{}, err
	}
	expires := time.Now().Add(90 * time.Second)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(expires) {
		expires = deadline
	}
	approval := toolApproval{ID: "tap_" + randomID(18), ToolID: tool.ID, Status: "pending", Request: review, ExpiresAt: expires}
	_, err = s.db.Exec(ctx, `INSERT INTO tool_approvals(id,actor_id,workspace_id,tool_id,source_kind,source_id,request,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, approval.ID, caller.UserID, caller.WorkspaceID, tool.ID, kind, source, string(review), expires)
	if err != nil {
		return tools.CallResult{}, err
	}
	defer func() {
		finish, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = s.db.Exec(finish, `UPDATE tool_approvals SET status='expired' WHERE id=$1 AND status IN ('pending','approved')`, approval.ID)
	}()
	report(approval)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return tools.CallResult{}, ctx.Err()
		case <-ticker.C:
		}
		if err = s.checkWorkflowExecution(ctx); err != nil {
			return tools.CallResult{}, err
		}
		if err = s.checkChatExecution(ctx); err != nil {
			return tools.CallResult{}, err
		}
		// A dead executor's lease cannot be renewed, even by an old goroutine.
		err = s.db.QueryRow(ctx, `UPDATE tool_approvals SET lease_until=NOW()+INTERVAL '5 seconds' WHERE id=$1 AND status IN ('pending','approved','rejected') AND lease_until>NOW() AND expires_at>NOW() RETURNING status`, approval.ID).Scan(&approval.Status)
		if err != nil {
			return tools.CallResult{}, errApprovalClosed
		}
		if approval.Status == "rejected" {
			report(approval)
			return tools.CallResult{}, errApprovalClosed
		}
		if approval.Status != "approved" {
			continue
		}
		// Recheck membership immediately before admission, including workflow
		// callers whose streaming request can outlive workspace access.
		var member bool
		err = s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workspace_memberships WHERE user_id=$1 AND workspace_id=$2)`, caller.UserID, caller.WorkspaceID).Scan(&member)
		if err != nil || !member {
			return tools.CallResult{}, tools.ErrApprovalRequired
		}
		result, operation, callErr := s.tools.InvokeConfirmed(ctx, tool, action, effective, true, approval.ID, definition)
		approval.Status = "failed"
		if operation != nil {
			approval.OperationID = operation.ID
			approval.Status = "uncertain"
		}
		if callErr == nil {
			approval.Status = "completed"
		}
		finish, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, saveErr := s.db.Exec(finish, `UPDATE tool_approvals SET status=$2,operation_id=$3 WHERE id=$1 AND status='approved'`, approval.ID, approval.Status, approval.OperationID)
		cancel()
		if saveErr != nil {
			return result, tools.ErrWriteUncertain
		}
		report(approval)
		return result, callErr
	}
}

func (s *Server) listToolApprovals(w http.ResponseWriter, r *http.Request) {
	user, workspace, ok := s.agentWorkspace(w, r, r.URL.Query().Get("workspace"))
	if !ok {
		return
	}
	kind, source := r.URL.Query().Get("kind"), r.URL.Query().Get("source")
	if (kind != "conversation" && kind != "workflow") || source == "" {
		writeError(w, 400, "Thiếu phạm vi xác nhận.")
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT a.id,a.tool_id,CASE WHEN o.status IN ('succeeded','reconciled_succeeded') THEN 'completed' WHEN o.status='reconciled_no_effect' THEN 'failed' WHEN o.status='uncertain' OR (o.status='executing' AND o.created_at<NOW()-INTERVAL '1 minute') THEN 'uncertain' WHEN o.status='executing' THEN 'approved' WHEN a.status IN ('pending','approved') AND (a.expires_at<=NOW() OR a.lease_until<=NOW()) THEN 'expired' ELSE a.status END,a.request,a.expires_at,COALESCE(o.id,a.operation_id) FROM tool_approvals a JOIN tools t ON t.id=a.tool_id LEFT JOIN tool_write_operations o ON o.actor_id=a.actor_id AND o.workspace_id=a.workspace_id AND o.idempotency_key=a.id WHERE a.actor_id=$1 AND a.workspace_id=$2 AND a.source_kind=$3 AND a.source_id=$4 AND t.owner_user_id=$1 ORDER BY a.created_at DESC LIMIT 50`, user.ID, workspace, kind, source)
	if err != nil {
		writeToolError(w, err)
		return
	}
	defer rows.Close()
	items := []toolApproval{}
	for rows.Next() {
		var item toolApproval
		if err = rows.Scan(&item.ID, &item.ToolID, &item.Status, &item.Request, &item.ExpiresAt, &item.OperationID); err != nil {
			writeToolError(w, err)
			return
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		writeToolError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"approvals": items})
}

func (s *Server) decideToolApproval(w http.ResponseWriter, r *http.Request) {
	user, workspace, ok := s.agentWorkspace(w, r, r.URL.Query().Get("workspace"))
	if !ok {
		return
	}
	var input struct {
		Decision   string `json:"decision"`
		Definition string `json:"definition"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Decision != "approved" && input.Decision != "rejected" {
		writeError(w, 400, "Quyết định không hợp lệ.")
		return
	}
	id := chi.URLParam(r, "approvalID")
	tag, err := s.db.Exec(r.Context(), `UPDATE tool_approvals a SET status=$4,decided_at=NOW() FROM tools t WHERE a.id=$1 AND a.actor_id=$2 AND a.workspace_id=$3 AND a.status='pending' AND a.expires_at>NOW() AND a.lease_until>NOW() AND a.request->>'definition'=$5 AND t.id=a.tool_id AND t.owner_user_id=$2`, id, user.ID, workspace, input.Decision, input.Definition)
	if err != nil {
		writeToolError(w, err)
		return
	}
	if tag.RowsAffected() != 1 {
		writeError(w, 409, "Yêu cầu không còn chờ xác nhận hoặc bạn không có quyền.")
		return
	}
	s.audit(r, auditEvent{Action: "tool.approval.decided", TargetType: "tool_approval", TargetID: id, WorkspaceID: workspace, Metadata: map[string]string{"decision": input.Decision}})
	w.WriteHeader(http.StatusNoContent)
}
