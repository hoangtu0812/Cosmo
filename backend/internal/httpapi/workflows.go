package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"cosmo/backend/internal/modelgateway"
	"cosmo/backend/internal/tools"
	"cosmo/backend/internal/workflows"

	"github.com/go-chi/chi/v5"
)

// writeWorkflowError maps a domain error to the status a client should see.
// Not-found and not-yours are the same answer on purpose: telling someone a
// workflow exists but is not theirs is telling them something.
func writeWorkflowError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, workflows.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, workflows.ErrNameRequired), errors.Is(err, workflows.ErrNameTooLong),
		errors.Is(err, workflows.ErrTooLong), errors.Is(err, workflows.ErrTooManyNodes),
		errors.Is(err, workflows.ErrTooManyEdges), errors.Is(err, workflows.ErrNoStart),
		errors.Is(err, workflows.ErrManyStarts), errors.Is(err, workflows.ErrCycle),
		errors.Is(err, workflows.ErrUnknownTarget), errors.Is(err, workflows.ErrNotRunnable):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "Không thể xử lý workflow.")
	}
}

func (s *Server) listWorkflows(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := s.agentWorkspace(w, r, r.URL.Query().Get("workspace"))
	if !ok {
		return
	}
	list, err := s.workflows.List(r.Context(), user.ID, workspaceID)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflows": list})
}

func (s *Server) createWorkflow(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := s.agentWorkspace(w, r, r.URL.Query().Get("workspace"))
	if !ok {
		return
	}
	var input struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Icon        string `json:"icon"`
		Visibility  string `json:"visibility"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.workflows.Create(r.Context(), user.ID, workspaceID, input.Name, input.Description, input.Icon, input.Visibility)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	s.audit(r, auditEvent{
		Action: "workflow.created", TargetType: "workflow", TargetID: item.ID, TargetLabel: item.Name,
		WorkspaceID: workspaceID, Metadata: map[string]string{"visibility": item.Visibility},
	})
	writeJSON(w, http.StatusCreated, map[string]any{"workflow": item})
}

func (s *Server) getWorkflow(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := s.agentWorkspace(w, r, r.URL.Query().Get("workspace"))
	if !ok {
		return
	}
	item, err := s.workflows.Get(r.Context(), chi.URLParam(r, "workflowID"), user.ID, workspaceID)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflow": item})
}

func (s *Server) updateWorkflow(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := s.agentWorkspace(w, r, r.URL.Query().Get("workspace"))
	if !ok {
		return
	}
	var input struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		Icon        *string `json:"icon"`
		Visibility  *string `json:"visibility"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.workflows.Update(r.Context(), chi.URLParam(r, "workflowID"), user.ID, workspaceID,
		workflows.Changes{Name: input.Name, Description: input.Description, Icon: input.Icon, Visibility: input.Visibility})
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	s.audit(r, auditEvent{
		Action: "workflow.updated", TargetType: "workflow", TargetID: item.ID, TargetLabel: item.Name,
		WorkspaceID: workspaceID, Metadata: map[string]string{"visibility": item.Visibility},
	})
	writeJSON(w, http.StatusOK, map[string]any{"workflow": item})
}

func (s *Server) saveWorkflowGraph(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := s.agentWorkspace(w, r, r.URL.Query().Get("workspace"))
	if !ok {
		return
	}
	var input struct {
		Graph workflows.Graph `json:"graph"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.workflows.SaveGraph(r.Context(), chi.URLParam(r, "workflowID"), user.ID, workspaceID, input.Graph)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	// The shape of the graph, not the graph: a workflow is edited many times a
	// sitting, and storing each version here would make the audit log a second
	// and worse copy of the workflow table.
	s.audit(r, auditEvent{
		Action: "workflow.graph.saved", TargetType: "workflow", TargetID: item.ID, TargetLabel: item.Name,
		WorkspaceID: workspaceID,
		Metadata:    map[string]int{"nodes": len(input.Graph.Nodes), "edges": len(input.Graph.Edges)},
	})
	writeJSON(w, http.StatusOK, map[string]any{"workflow": item})
}

func (s *Server) deleteWorkflow(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := s.agentWorkspace(w, r, r.URL.Query().Get("workspace"))
	if !ok {
		return
	}
	workflowID := chi.URLParam(r, "workflowID")
	removed, _ := s.workflows.Get(r.Context(), workflowID, user.ID, workspaceID)
	if err := s.workflows.Delete(r.Context(), workflowID, user.ID, workspaceID); err != nil {
		writeWorkflowError(w, err)
		return
	}
	s.audit(r, auditEvent{
		Action: "workflow.deleted", TargetType: "workflow", TargetID: workflowID, TargetLabel: removed.Name,
		WorkspaceID: workspaceID,
	})
	w.WriteHeader(http.StatusNoContent)
}

// workflowInvoker gives the workflow runner a way to reach a tool without the
// workflows package knowing who is asking. The caller is fixed here, at the
// edge, so a node cannot be wired to a tool its author could not see.
type workflowInvoker struct {
	server      *Server
	userID      string
	workspaceID string
}

func (invoker workflowInvoker) InvokeAction(ctx context.Context, toolID, actionID string, arguments map[string]any) (string, error) {
	if execution, _ := ctx.Value(workflowExecutionKey{}).(*workflowExecution); execution != nil && execution.ApprovalID != "" && execution.ActiveNode == execution.resumeNode {
		result, err := invoker.server.resumeWorkflowApproval(ctx, execution, toolID, actionID)
		execution.ApprovalID = ""
		return result.Body, err
	}
	return invoker.server.tools.InvokeAction(ctx, invoker.userID, invoker.workspaceID, toolID, actionID, arguments)
}

// runWorkflow walks the graph and streams a step per node. Streamed rather
// than returned whole because the editor draws the graph lighting up from
// these: a run of several model calls should look like progress, not a hang.
func (s *Server) runWorkflow(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := s.agentWorkspace(w, r, r.URL.Query().Get("workspace"))
	if !ok {
		return
	}
	item, err := s.workflows.Get(r.Context(), chi.URLParam(r, "workflowID"), user.ID, workspaceID)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	var input struct {
		Input      string `json:"input"`
		Model      string `json:"model"`
		Resume     string `json:"execution_id"`
		RequestKey string `json:"request_id"`
	}
	if r.Body != nil && r.ContentLength != 0 && !decodeJSON(w, r, &input) {
		return
	}

	if raw := r.Header.Get("Last-Event-ID"); raw != "" {
		cursor, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || cursor < 0 {
			writeError(w, 400, "Vị trí sự kiện không hợp lệ.")
			return
		}
	}
	if len(input.RequestKey) > 100 || len(input.Input) > 65536 {
		writeError(w, 400, "Yêu cầu chạy workflow vượt giới hạn.")
		return
	}
	models := s.modelsFor(r.Context(), workspaceID)
	if !models.HasGateway() {
		writeError(w, http.StatusServiceUnavailable, "Workspace này chưa cấu hình Model Gateway.")
		return
	}
	options := modelgateway.Options{Model: input.Model}
	if models.ResolveModel(options) == "" {
		writeError(w, http.StatusBadRequest, "Hãy chọn model cho workflow hoặc đặt model mặc định trong Cài đặt workspace.")
		return
	}

	_, streaming := w.(http.Flusher)
	if !streaming {
		writeError(w, http.StatusInternalServerError, "Trình duyệt không nhận được dữ liệu streaming.")
		return
	}
	execution, err := s.admitWorkflowExecution(context.WithValue(r.Context(), workflowQueueKey{}, input.RequestKey), item, user.ID, input.Input, models.ResolveModel(options), input.Resume)
	if err != nil {
		if errors.Is(err, errRuntimeCapacity) {
			w.Header().Set("Retry-After", "5")
			writeError(w, 429, errRuntimeCapacity.Error())
			return
		}
		writeError(w, 409, errWorkflowResume.Error())
		return
	}
	s.audit(r, auditEvent{Action: "workflow.run.queued", TargetType: "workflow", TargetID: item.ID, WorkspaceID: workspaceID, Metadata: map[string]string{"execution_id": execution.ID}})
	s.followWorkflowExecution(w, r, execution.ID)
}

func (s *Server) executeWorkflowJob(parent context.Context, execution *workflowExecution, item workflows.Workflow, user User, workspaceID string) {
	models := s.modelsFor(parent, workspaceID)
	options := modelgateway.Options{Model: execution.Model}
	deadline := time.Now().Add(workflows.RunTimeout)
	if execution.deadline != nil {
		deadline = *execution.deadline
	}
	ctx, cancel := context.WithDeadline(parent, deadline)
	ctx = context.WithValue(ctx, workflowExecutionKey{}, execution)
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			tag, err := s.db.Exec(ctx, `UPDATE workflow_executions SET lease_until=NOW()+INTERVAL '5 seconds' WHERE id=$1 AND lease_owner=$2 AND lease_until>NOW() AND status='running'`, execution.ID, execution.owner)
			if err != nil || tag.RowsAffected() != 1 {
				cancel()
				return
			}
		}
	}()
	finalStatus := "interrupted"
	defer func() {
		cancel()
		<-heartbeatDone
		finish, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_, _ = s.db.Exec(finish, `UPDATE workflow_executions SET status=$3,finished_at=NOW() WHERE id=$1 AND lease_owner=$2 AND status='running'`, execution.ID, execution.owner, finalStatus)
	}()

	w := &workflowEventWriter{server: s, ctx: ctx, execution: execution, cancel: cancel, header: make(http.Header)}
	var flusher http.Flusher = w
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	invoker := workflowInvoker{server: s, userID: user.ID, workspaceID: workspaceID}
	writeSSE(w, "execution", map[string]string{"id": execution.ID})
	flusher.Flush()
	toolCtx := tools.WithCaller(ctx, s.callerFor(ctx, user, workspaceID))
	toolCtx = context.WithValue(toolCtx, workflowParkKey{}, chatParkHandler(func(approval toolApproval) error { return s.parkWorkflowApproval(toolCtx, execution, approval) }))
	toolCtx = tools.WithApprovalHandler(toolCtx, func(wait context.Context, tool tools.Tool, action tools.Action, args map[string]any) (tools.CallResult, error) {
		return s.awaitToolApproval(wait, "workflow", item.ID, tool, action, args, func(toolApproval) {})
	})
	runErr := s.workflows.RunCheckpointed(toolCtx, item.Graph, execution.Input, models, options, invoker, execution.Completed, func(step workflows.Step) error { return s.saveWorkflowCheckpoint(toolCtx, execution, step) }, func(step workflows.Step) {
		if execution.parked {
			return
		}
		writeSSE(w, "step", step)
		flusher.Flush()
	})
	if errors.Is(runErr, errWorkflowSuspended) {
		return
	}
	if runErr != nil {
		// The failing step already said what went wrong and where; this closes
		// the stream rather than repeating it.
		writeSSE(w, "error", map[string]string{"message": runErr.Error()})
		flusher.Flush()
		return
	}
	finalStatus = "succeeded"
	writeSSE(w, "done", map[string]any{"workflow_id": item.ID, "execution_id": execution.ID})
	flusher.Flush()
}
