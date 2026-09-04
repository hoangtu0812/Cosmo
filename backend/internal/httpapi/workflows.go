package httpapi

import (
	"context"
	"errors"
	"net/http"

	"cosmo/backend/internal/modelgateway"
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
		Input string `json:"input"`
		Model string `json:"model"`
	}
	if r.Body != nil && r.ContentLength != 0 && !decodeJSON(w, r, &input) {
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

	flusher, streaming := w.(http.Flusher)
	if !streaming {
		writeError(w, http.StatusInternalServerError, "Trình duyệt không nhận được dữ liệu streaming.")
		return
	}
	// Recorded before the stream opens rather than after it closes: a workflow
	// calls tools, and the record that one was set running has to survive the
	// reader closing the tab halfway through.
	s.audit(r, auditEvent{
		Action: "workflow.run.started", TargetType: "workflow", TargetID: item.ID, TargetLabel: item.Name,
		WorkspaceID: workspaceID,
		Metadata:    map[string]any{"model": models.ResolveModel(options), "nodes": len(item.Graph.Nodes)},
	})

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	invoker := workflowInvoker{server: s, userID: user.ID, workspaceID: workspaceID}
	runErr := s.workflows.Run(r.Context(), item.Graph, input.Input, models, options, invoker, func(step workflows.Step) {
		writeSSE(w, "step", step)
		flusher.Flush()
	})
	if runErr != nil {
		// The failing step already said what went wrong and where; this closes
		// the stream rather than repeating it.
		writeSSE(w, "error", map[string]string{"message": runErr.Error()})
		flusher.Flush()
		return
	}
	writeSSE(w, "done", map[string]any{"workflow_id": item.ID})
	flusher.Flush()
}
