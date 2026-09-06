package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"cosmo/backend/internal/tools"
	"cosmo/backend/internal/workflows"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

var errWorkflowResume = errors.New("Phiên chạy đang hoạt động, cấu hình đã đổi, hoặc bước bị ngắt chưa có kết quả chắc chắn để tiếp tục.")

type workflowExecutionKey struct{}
type workflowQueueKey struct{}
type workflowExecution struct {
	ID          string                    `json:"id"`
	Status      string                    `json:"status"`
	Input       string                    `json:"input"`
	Model       string                    `json:"model"`
	Completed   map[string]workflows.Step `json:"completed"`
	ActiveNode  string                    `json:"active_node"`
	ApprovalID  string                    `json:"approval_id"`
	CreatedAt   time.Time                 `json:"created_at"`
	workflowID  string
	actorID     string
	workspaceID string
	runtimeHash string
	owner       string
}

// Runtime changes require a new reviewed run. Hash gateway data rather than
// copying credentials into the checkpoint; tool contracts are revision bound.
func (s *Server) workflowRuntimeHash(ctx context.Context, item workflows.Workflow, userID, model string) (string, error) {
	parts := []any{item.Graph, model}
	var gateway []byte
	if err := s.db.QueryRow(ctx, `SELECT to_jsonb(c) FROM workspace_llm_configs c WHERE workspace_id=$1`, item.WorkspaceID).Scan(&gateway); err != nil {
		return "", err
	}
	parts = append(parts, string(gateway))
	for _, node := range workflows.Order(item.Graph) {
		if node.Kind != workflows.KindTool {
			continue
		}
		toolID, _ := node.Config["tool_id"].(string)
		actionID, _ := node.Config["action_id"].(string)
		tool, err := s.tools.Get(ctx, toolID, userID, item.WorkspaceID)
		if err != nil {
			return "", err
		}
		action, err := s.tools.Action(ctx, toolID, actionID)
		if err != nil {
			return "", err
		}
		parts = append(parts, s.tools.PolicyReview(tool, action))
	}
	raw, err := json.Marshal(parts)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), nil
}

func (s *Server) admitWorkflowExecution(ctx context.Context, item workflows.Workflow, userID, input, model, resume string) (*workflowExecution, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	// Serialize admission across tabs and retries, including expired leases.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, item.ID+":"+userID); err != nil {
		return nil, err
	}
	requestKey, queued := ctx.Value(workflowQueueKey{}).(string)
	requestHash := chatRuntimeHash(item.ID, input, model, resume)
	if queued && requestKey != "" {
		var existing workflowExecution
		var previousHash string
		err := tx.QueryRow(ctx, `SELECT e.id,e.status,r.request_hash FROM workflow_execution_requests r JOIN workflow_executions e ON e.id=r.execution_id WHERE r.actor_id=$1 AND r.workspace_id=$2 AND r.request_key=$3`, userID, item.WorkspaceID, requestKey).Scan(&existing.ID, &existing.Status, &previousHash)
		if err == nil {
			if previousHash != requestHash {
				return nil, errWorkflowResume
			}
			return &existing, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}

	if _, err = tx.Exec(ctx, `UPDATE workflow_executions SET status='interrupted' WHERE workflow_id=$1 AND actor_id=$2 AND workspace_id=$3 AND status='running' AND lease_until<=NOW()`, item.ID, userID, item.WorkspaceID); err != nil {
		return nil, err
	}
	exec := &workflowExecution{ID: "wex_" + randomID(18), Status: "running", Input: input, Model: model, Completed: map[string]workflows.Step{}, owner: "wown_" + randomID(18)}
	hash, err := s.workflowRuntimeHash(ctx, item, userID, model)
	if err != nil {
		return nil, err
	}
	if resume != "" {
		var raw []byte
		var storedHash string
		err = tx.QueryRow(ctx, `SELECT id,status,input,model,runtime_hash,completed,active_node,approval_id FROM workflow_executions WHERE id=$1 AND actor_id=$2 AND workspace_id=$3 AND workflow_id=$4 FOR UPDATE`, resume, userID, item.WorkspaceID, item.ID).Scan(&exec.ID, &exec.Status, &exec.Input, &exec.Model, &storedHash, &raw, &exec.ActiveNode, &exec.ApprovalID)
		if err != nil {
			return nil, errWorkflowResume
		}
		// Resume always uses the saved input/model, not a new editor value.
		hash, err = s.workflowRuntimeHash(ctx, item, userID, exec.Model)
		if err != nil {
			return nil, err
		}
		if storedHash != hash || exec.Status != "interrupted" {
			return nil, errWorkflowResume
		}
		if err = json.Unmarshal(raw, &exec.Completed); err != nil {
			return nil, err
		}
		if exec.ActiveNode != "" {
			// Do not rerun an admitted node without proof of its outcome. A
			// pending approval can be invalidated atomically before dispatch;
			// a successful write can be reconstructed from its existing ledger.
			if exec.ApprovalID == "" {
				return nil, errWorkflowResume
			}
			var status string
			if err = tx.QueryRow(ctx, `SELECT status FROM tool_approvals WHERE id=$1 AND actor_id=$2 AND workspace_id=$3 FOR UPDATE`, exec.ApprovalID, userID, item.WorkspaceID).Scan(&status); err != nil {
				return nil, errWorkflowResume
			}
			var operationStatus string
			var resultRaw []byte
			err = tx.QueryRow(ctx, `SELECT status,result FROM tool_write_operations WHERE actor_id=$1 AND workspace_id=$2 AND idempotency_key=$3`, userID, item.WorkspaceID, exec.ApprovalID).Scan(&operationStatus, &resultRaw)
			if err == nil && operationStatus == "succeeded" {
				var result tools.CallResult
				if err = json.Unmarshal(resultRaw, &result); err != nil {
					return nil, err
				}
				for _, node := range item.Graph.Nodes {
					if node.ID == exec.ActiveNode {
						if node.Kind != workflows.KindTool {
							return nil, errWorkflowResume
						}
						exec.Completed[node.ID] = workflows.RestoreToolStep(node, result.Body, result.DurationMS)
					}
				}
			} else if errors.Is(err, pgx.ErrNoRows) && (status == "pending" || status == "expired") {
				if _, err = tx.Exec(ctx, `UPDATE tool_approvals SET status='expired',lease_until=NOW() WHERE id=$1`, exec.ApprovalID); err != nil {
					return nil, err
				}
			} else {
				return nil, errWorkflowResume
			}
		}
		raw, err = json.Marshal(exec.Completed)
		if err != nil {
			return nil, err
		}
		_, err = tx.Exec(ctx, `UPDATE workflow_executions SET status='running',completed=$2,active_node='',approval_id='',lease_owner=$3,lease_until=NOW()+INTERVAL '5 seconds',finished_at=NULL WHERE id=$1`, exec.ID, string(raw), exec.owner)
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO workflow_executions(id,workflow_id,actor_id,workspace_id,input,model,runtime_hash,status,lease_owner,lease_until) VALUES($1,$2,$3,$4,$5,$6,$7,'running',$8,NOW()+INTERVAL '5 seconds')`, exec.ID, item.ID, userID, item.WorkspaceID, input, model, hash, exec.owner)
	}
	if err != nil {
		return nil, errWorkflowResume
	}
	if queued {
		if _, err = tx.Exec(ctx, `UPDATE workflow_executions SET status='queued',lease_owner='',lease_until=NOW() WHERE id=$1`, exec.ID); err != nil {
			return nil, err
		}
		if requestKey != "" {
			if _, err = tx.Exec(ctx, `INSERT INTO workflow_execution_requests(actor_id,workspace_id,request_key,execution_id,request_hash) VALUES($1,$2,$3,$4,$5)`, userID, item.WorkspaceID, requestKey, exec.ID, requestHash); err != nil {
				return nil, err
			}
		}
		// A manual resume begins a new stream over the same durable checkpoints.
		if _, err = tx.Exec(ctx, `DELETE FROM workflow_execution_events WHERE execution_id=$1`, exec.ID); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	exec.Status = "running"
	if queued {
		exec.Status = "queued"
	}
	exec.ActiveNode = ""
	exec.ApprovalID = ""
	return exec, nil
}

func (s *Server) saveWorkflowCheckpoint(ctx context.Context, exec *workflowExecution, step workflows.Step) error {
	var err error
	if step.Status == workflows.StatusRunning {
		if exec.actorID != "" {
			var member bool
			if e := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workspace_memberships WHERE user_id=$1 AND workspace_id=$2)`, exec.actorID, exec.workspaceID).Scan(&member); e != nil {
				return e
			} else if !member {
				return errWorkflowResume
			}
			if _, e := s.workflows.Get(ctx, exec.workflowID, exec.actorID, exec.workspaceID); e != nil {
				return e
			}
		}

		tag, e := s.db.Exec(ctx, `UPDATE workflow_executions SET active_node=$3,approval_id='' WHERE id=$1 AND lease_owner=$2 AND lease_until>NOW() AND status='running'`, exec.ID, exec.owner, step.NodeID)
		err = e
		if e == nil && tag.RowsAffected() != 1 {
			err = errWorkflowResume
		}
	} else {
		raw, e := json.Marshal(step)
		if e != nil {
			return e
		}
		tag, e := s.db.Exec(ctx, `UPDATE workflow_executions SET completed=completed||jsonb_build_object($3::text,$4::jsonb),active_node='',approval_id='' WHERE id=$1 AND lease_owner=$2 AND lease_until>NOW() AND status='running'`, exec.ID, exec.owner, step.NodeID, string(raw))
		err = e
		if e == nil && tag.RowsAffected() != 1 {
			err = errWorkflowResume
		}
	}
	return err
}

func (s *Server) checkWorkflowExecution(ctx context.Context) error {
	exec, _ := ctx.Value(workflowExecutionKey{}).(*workflowExecution)
	if exec == nil {
		return ctx.Err()
	}
	var valid bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workflow_executions WHERE id=$1 AND lease_owner=$2 AND lease_until>NOW() AND status='running')`, exec.ID, exec.owner).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return errWorkflowResume
	}
	return ctx.Err()
}

func (s *Server) listWorkflowExecutions(w http.ResponseWriter, r *http.Request) {
	user, workspace, ok := s.agentWorkspace(w, r, r.URL.Query().Get("workspace"))
	if !ok {
		return
	}
	id := chi.URLParam(r, "workflowID")
	if _, err := s.workflows.Get(r.Context(), id, user.ID, workspace); err != nil {
		writeWorkflowError(w, err)
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT id,CASE WHEN status='running' AND lease_until<=NOW() THEN 'interrupted' ELSE status END,input,model,completed,active_node,approval_id,created_at FROM workflow_executions WHERE workflow_id=$1 AND actor_id=$2 AND workspace_id=$3 ORDER BY created_at DESC LIMIT 20`, id, user.ID, workspace)
	if err != nil {
		writeError(w, 500, "Không tải được tiến độ workflow.")
		return
	}
	defer rows.Close()
	items := []workflowExecution{}
	for rows.Next() {
		var item workflowExecution
		var raw []byte
		if err = rows.Scan(&item.ID, &item.Status, &item.Input, &item.Model, &raw, &item.ActiveNode, &item.ApprovalID, &item.CreatedAt); err != nil {
			writeError(w, 500, "Không đọc được tiến độ.")
			return
		}
		if err = json.Unmarshal(raw, &item.Completed); err != nil {
			writeError(w, 500, "Tiến độ không hợp lệ.")
			return
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		writeError(w, 500, "Không đọc được tiến độ.")
		return
	}
	writeJSON(w, 200, map[string]any{"executions": items})
}
