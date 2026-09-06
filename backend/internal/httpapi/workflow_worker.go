package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// Admission persists queued work. Only these workers execute it; subscribers
// can disconnect or replay events without owning an execution context.
func (s *Server) RunWorkflowWorker(ctx context.Context) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for ctx.Err() == nil {
		_, err := s.db.Exec(ctx, `UPDATE workflow_executions SET status='interrupted',finished_at=NOW() WHERE status='running' AND lease_until<=NOW()`)
		if err == nil {
			execution, claimErr := s.claimWorkflowJob(ctx)
			if claimErr != nil {
				err = claimErr
			} else if execution != nil {
				s.runWorkflowJob(ctx, execution)
			}
		}
		if err != nil && ctx.Err() == nil {
			s.logger.Error("workflow queue", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) claimWorkflowJob(ctx context.Context) (*workflowExecution, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	execution := &workflowExecution{owner: "wown_" + randomID(18)}
	var completed []byte
	err = tx.QueryRow(ctx, `SELECT id,workflow_id,actor_id,workspace_id,input,model,runtime_hash,completed FROM workflow_executions WHERE status='queued' ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&execution.ID, &execution.workflowID, &execution.actorID, &execution.workspaceID, &execution.Input, &execution.Model, &execution.runtimeHash, &completed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(completed, &execution.Completed); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE workflow_executions SET status='running',lease_owner=$2,lease_until=NOW()+INTERVAL '5 seconds' WHERE id=$1`, execution.ID, execution.owner); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	execution.Status = "running"
	return execution, nil
}

func (s *Server) runWorkflowJob(ctx context.Context, execution *workflowExecution) {
	// Includes setup failures and panics. Never requeue an admitted node whose
	// outcome is unknown; existing manual checkpoint reconciliation applies.
	defer func() {
		if p := recover(); p != nil {
			s.logger.Error("workflow worker panic", "execution_id", execution.ID)
		}
		finish, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = s.db.Exec(finish, `UPDATE workflow_executions SET status='interrupted',finished_at=NOW() WHERE id=$1 AND lease_owner=$2 AND status='running'`, execution.ID, execution.owner)
	}()
	var user User
	err := s.db.QueryRow(ctx, `SELECT u.id,u.email,u.name,u.role FROM users u JOIN workspace_memberships m ON m.user_id=u.id WHERE u.id=$1 AND m.workspace_id=$2`, execution.actorID, execution.workspaceID).Scan(&user.ID, &user.Email, &user.Name, &user.Role)
	if err != nil {
		return
	}
	item, err := s.workflows.Get(ctx, execution.workflowID, user.ID, execution.workspaceID)
	if err != nil {
		return
	}
	hash, err := s.workflowRuntimeHash(ctx, item, user.ID, execution.Model)
	if err != nil || hash != execution.runtimeHash {
		return
	}
	s.executeWorkflowJob(ctx, execution, item, user, execution.workspaceID)
}

type workflowEventWriter struct {
	server    *Server
	ctx       context.Context
	execution *workflowExecution
	cancel    context.CancelFunc
	header    http.Header
}

func (w *workflowEventWriter) Header() http.Header { return w.header }
func (w *workflowEventWriter) WriteHeader(int)     {}
func (w *workflowEventWriter) Flush()              {}
func (w *workflowEventWriter) Write(frame []byte) (int, error) {
	tag, err := w.server.db.Exec(w.ctx, `INSERT INTO workflow_execution_events(execution_id,frame) SELECT id,$3 FROM workflow_executions WHERE id=$1 AND lease_owner=$2 AND lease_until>NOW() AND status='running' FOR SHARE`, w.execution.ID, w.execution.owner, string(frame))
	if err != nil || tag.RowsAffected() != 1 {
		w.cancel()
		if err == nil {
			err = errWorkflowResume
		}
		return 0, err
	}
	return len(frame), nil
}

func (s *Server) workflowEvents(w http.ResponseWriter, r *http.Request) {
	user, workspace, ok := s.agentWorkspace(w, r, r.URL.Query().Get("workspace"))
	if !ok {
		return
	}
	workflowID := chi.URLParam(r, "workflowID")
	if _, err := s.workflows.Get(r.Context(), workflowID, user.ID, workspace); err != nil {
		writeWorkflowError(w, err)
		return
	}
	id := chi.URLParam(r, "executionID")
	var allowed bool
	err := s.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM workflow_executions WHERE id=$1 AND actor_id=$2 AND workspace_id=$3 AND workflow_id=$4)`, id, user.ID, workspace, workflowID).Scan(&allowed)
	if err != nil || !allowed {
		writeError(w, 404, "Không tìm thấy phiên workflow.")
		return
	}
	s.followWorkflowExecution(w, r, id)
}

func (s *Server) followWorkflowExecution(w http.ResponseWriter, r *http.Request, id string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, 500, "Streaming không được hỗ trợ.")
		return
	}
	var cursor int64
	if raw := r.Header.Get("Last-Event-ID"); raw != "" {
		var err error
		cursor, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || cursor < 0 {
			writeError(w, 400, "Vị trí sự kiện không hợp lệ.")
			return
		}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	writeSSE(w, "execution", map[string]string{"id": id})
	flusher.Flush()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	lastHeartbeat := time.Now()
	for {
		var workflowID, workspaceID string
		err := s.db.QueryRow(r.Context(), `SELECT e.workflow_id,e.workspace_id FROM workflow_executions e JOIN workspace_memberships m ON m.workspace_id=e.workspace_id AND m.user_id=e.actor_id WHERE e.id=$1 AND e.actor_id=$2`, id, currentUser(r.Context()).ID).Scan(&workflowID, &workspaceID)
		if err != nil {
			return
		}
		if _, err = s.workflows.Get(r.Context(), workflowID, currentUser(r.Context()).ID, workspaceID); err != nil {
			return
		}
		// Read state before events so a concurrent completion cannot hide its last frames.
		var status string
		if err = s.db.QueryRow(r.Context(), `SELECT status FROM workflow_executions WHERE id=$1`, id).Scan(&status); err != nil {
			return
		}
		rows, err := s.db.Query(r.Context(), `SELECT id,frame FROM workflow_execution_events WHERE execution_id=$1 AND id>$2 ORDER BY id LIMIT 200`, id, cursor)
		if err != nil {
			return
		}
		count := 0
		terminal := false
		for rows.Next() {
			var eventID int64
			var frame string
			if err = rows.Scan(&eventID, &frame); err != nil {
				break
			}
			if _, err = fmt.Fprintf(w, "id: %d\n%s", eventID, frame); err != nil {
				break
			}
			cursor = eventID
			count++
			if strings.HasPrefix(frame, "event: done\n") || strings.HasPrefix(frame, "event: error\n") {
				terminal = true
			}
		}
		rows.Close()
		if err != nil || rows.Err() != nil {
			return
		}
		flusher.Flush()
		if terminal {
			return
		}
		if count == 200 {
			continue
		}
		if status == "succeeded" {
			writeSSE(w, "done", map[string]string{"execution_id": id})
			flusher.Flush()
			return
		}
		if status == "interrupted" || status == "failed" {
			writeSSE(w, "error", map[string]string{"message": "Phiên workflow bị gián đoạn. Kiểm tra kết quả các bước trước khi tiếp tục."})
			flusher.Flush()
			return
		}
		if time.Since(lastHeartbeat) >= 10*time.Second {
			if _, err = fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
			lastHeartbeat = time.Now()
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) cancelWorkflowExecution(w http.ResponseWriter, r *http.Request) {
	user, workspace, ok := s.agentWorkspace(w, r, r.URL.Query().Get("workspace"))
	if !ok {
		return
	}
	workflowID := chi.URLParam(r, "workflowID")
	if _, err := s.workflows.Get(r.Context(), workflowID, user.ID, workspace); err != nil {
		writeWorkflowError(w, err)
		return
	}
	var stopped int
	err := s.db.QueryRow(r.Context(), `WITH stopped AS (UPDATE workflow_executions SET status='failed',finished_at=NOW() WHERE id=$1 AND actor_id=$2 AND workspace_id=$3 AND workflow_id=$4 AND status IN ('queued','running') RETURNING approval_id), expired AS (UPDATE tool_approvals SET status='expired',lease_until=NOW() WHERE id IN (SELECT approval_id FROM stopped) AND status IN ('pending','approved')) SELECT count(*) FROM stopped`, chi.URLParam(r, "executionID"), user.ID, workspace, workflowID).Scan(&stopped)
	if err != nil {
		writeError(w, 500, "Không dừng được phiên workflow.")
		return
	}
	if stopped != 1 {
		writeError(w, 409, "Phiên không còn chạy hoặc bạn không có quyền.")
		return
	}

	s.audit(r, auditEvent{Action: "workflow.run.cancelled", TargetType: "workflow_execution", TargetID: chi.URLParam(r, "executionID"), WorkspaceID: workspace})
	w.WriteHeader(204)
}
