package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *Server) getToolActionPolicy(w http.ResponseWriter, r *http.Request) {
	item, _, _, ok := s.toolForWrite(w, r, chi.URLParam(r, "toolID"))
	if !ok {
		return
	}
	action, err := s.tools.Action(r.Context(), item.ID, chi.URLParam(r, "actionID"))
	if err != nil {
		writeToolError(w, err)
		return
	}
	effect, err := s.tools.ActionEffect(r.Context(), item, action)
	if err != nil {
		writeToolError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"effect": effect, "definition": s.tools.PolicyReview(item, action), "destination": item.BaseURL, "method": action.Method, "path": action.Path, "parameters": action.Parameters, "action": action.Name})
}

func (s *Server) setToolActionPolicy(w http.ResponseWriter, r *http.Request) {
	item, user, workspace, ok := s.toolForWrite(w, r, chi.URLParam(r, "toolID"))
	if !ok {
		return
	}
	action, err := s.tools.Action(r.Context(), item.ID, chi.URLParam(r, "actionID"))
	if err != nil {
		writeToolError(w, err)
		return
	}
	var input struct {
		Effect     string `json:"effect"`
		Definition string `json:"definition"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Definition != s.tools.PolicyReview(item, action) {
		writeError(w, 409, "Cấu hình action đã thay đổi; tải lại trước khi xác nhận chính sách.")
		return
	}
	if err = s.tools.SetActionEffect(r.Context(), item, action, user.ID, input.Effect); err != nil {
		writeToolError(w, err)
		return
	}
	s.audit(r, auditEvent{Action: "tool.action.policy_changed", TargetType: "tool", TargetID: item.ID, WorkspaceID: workspace, Metadata: map[string]string{"action_id": action.ID, "effect": input.Effect}})
	s.getToolActionPolicy(w, r)
}

func (s *Server) listToolWriteOperations(w http.ResponseWriter, r *http.Request) {
	item, user, workspace, ok := s.toolForWrite(w, r, chi.URLParam(r, "toolID"))
	if !ok {
		return
	}
	operations, err := s.tools.WriteOperations(r.Context(), item.ID, user.ID, workspace)
	if err != nil {
		writeToolError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"operations": operations})
}

func (s *Server) reconcileToolWrite(w http.ResponseWriter, r *http.Request) {
	item, user, workspace, ok := s.toolForWrite(w, r, chi.URLParam(r, "toolID"))
	if !ok {
		return
	}
	var input struct {
		Outcome string `json:"outcome"`
		Note    string `json:"note"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	id := chi.URLParam(r, "operationID")
	if err := s.tools.ReconcileWrite(r.Context(), item.ID, user.ID, workspace, id, input.Outcome, input.Note); err != nil {
		writeToolError(w, err)
		return
	}
	s.audit(r, auditEvent{Action: "tool.write.reconciled", TargetType: "tool", TargetID: item.ID, WorkspaceID: workspace, Metadata: map[string]string{"operation_id": id, "outcome": input.Outcome}})
	w.WriteHeader(http.StatusNoContent)
}
