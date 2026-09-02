package httpapi

import (
	"errors"
	"net/http"

	"cosmo/backend/internal/tools"

	"github.com/go-chi/chi/v5"
)

// writeToolError maps a domain error onto a status. The wording lives with the
// rule in the tools package, so it is never restated here.
func writeToolError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, tools.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, tools.ErrDuplicateAction):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, tools.ErrSecretsOff):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, tools.ErrCallFailed):
		// 502: we are reporting someone else's endpoint failing, not our own.
		writeError(w, http.StatusBadGateway, err.Error())
	case errors.Is(err, tools.ErrNameLength), errors.Is(err, tools.ErrDescription),
		errors.Is(err, tools.ErrBaseURL), errors.Is(err, tools.ErrPrivateAddress),
		errors.Is(err, tools.ErrAuthType), errors.Is(err, tools.ErrAuthHeaderName),
		errors.Is(err, tools.ErrActionName), errors.Is(err, tools.ErrActionMethod),
		errors.Is(err, tools.ErrActionPath), errors.Is(err, tools.ErrTooManyActions),
		errors.Is(err, tools.ErrTooManyParams):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "Không thể xử lý tool.")
	}
}

// toolForWrite loads a tool the caller may change. Authorisation is a question
// about the caller, so it stays here; the domain only ever sees the ids the
// transport layer has already vouched for.
func (s *Server) toolForWrite(w http.ResponseWriter, r *http.Request, toolID string) (tools.Tool, User, string, bool) {
	user, workspaceID, ok := s.agentWorkspace(w, r, r.URL.Query().Get("workspace"))
	if !ok {
		return tools.Tool{}, User{}, "", false
	}
	item, err := s.tools.Get(r.Context(), toolID, user.ID, workspaceID)
	if err != nil {
		writeToolError(w, err)
		return tools.Tool{}, User{}, "", false
	}
	if !item.IsEditable {
		// A tool holds a credential. Someone who can see it must not be able to
		// point it elsewhere and read what comes back, so this is a refusal
		// rather than a silent no-op.
		writeError(w, http.StatusForbidden, "Chỉ người tạo tool mới sửa được.")
		return tools.Tool{}, User{}, "", false
	}
	return item, user, workspaceID, true
}

func (s *Server) listTools(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := s.agentWorkspace(w, r, r.URL.Query().Get("workspace"))
	if !ok {
		return
	}
	list, err := s.tools.List(r.Context(), user.ID, workspaceID)
	if err != nil {
		writeToolError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": list})
}

func (s *Server) createTool(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := s.agentWorkspace(w, r, r.URL.Query().Get("workspace"))
	if !ok {
		return
	}
	var input struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Icon        string   `json:"icon"`
		Tags        []string `json:"tags"`
		BaseURL     string   `json:"base_url"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.tools.Create(r.Context(), user.ID, workspaceID, input.Name, input.Description, input.Icon, input.Tags, input.BaseURL)
	if err != nil {
		writeToolError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"tool": item})
}

func (s *Server) getTool(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := s.agentWorkspace(w, r, r.URL.Query().Get("workspace"))
	if !ok {
		return
	}
	toolID := chi.URLParam(r, "toolID")
	item, err := s.tools.Get(r.Context(), toolID, user.ID, workspaceID)
	if err != nil {
		writeToolError(w, err)
		return
	}
	actions, err := s.tools.Actions(r.Context(), toolID)
	if err != nil {
		writeToolError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tool": item, "actions": actions})
}

func (s *Server) updateTool(w http.ResponseWriter, r *http.Request) {
	item, user, workspaceID, ok := s.toolForWrite(w, r, chi.URLParam(r, "toolID"))
	if !ok {
		return
	}
	var changes tools.Changes
	if !decodeJSON(w, r, &changes) {
		return
	}
	updated, err := s.tools.Update(r.Context(), item.ID, user.ID, workspaceID, changes)
	if err != nil {
		writeToolError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tool": updated})
}

func (s *Server) deleteTool(w http.ResponseWriter, r *http.Request) {
	item, user, workspaceID, ok := s.toolForWrite(w, r, chi.URLParam(r, "toolID"))
	if !ok {
		return
	}
	if err := s.tools.Delete(r.Context(), item.ID, user.ID, workspaceID); err != nil {
		writeToolError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) saveToolAction(w http.ResponseWriter, r *http.Request) {
	item, _, _, ok := s.toolForWrite(w, r, chi.URLParam(r, "toolID"))
	if !ok {
		return
	}
	var input tools.Action
	if !decodeJSON(w, r, &input) {
		return
	}
	action, err := s.tools.SaveAction(r.Context(), item.ID, chi.URLParam(r, "actionID"), input)
	if err != nil {
		writeToolError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"action": action})
}

func (s *Server) deleteToolAction(w http.ResponseWriter, r *http.Request) {
	item, _, _, ok := s.toolForWrite(w, r, chi.URLParam(r, "toolID"))
	if !ok {
		return
	}
	if err := s.tools.DeleteAction(r.Context(), item.ID, chi.URLParam(r, "actionID")); err != nil {
		writeToolError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// testToolAction calls the endpoint once with arguments the reader typed and
// hands back what came off the wire. It is restricted to people who may edit
// the tool: the response can contain whatever the credential unlocks.
func (s *Server) testToolAction(w http.ResponseWriter, r *http.Request) {
	item, _, _, ok := s.toolForWrite(w, r, chi.URLParam(r, "toolID"))
	if !ok {
		return
	}
	action, err := s.tools.Action(r.Context(), item.ID, chi.URLParam(r, "actionID"))
	if err != nil {
		writeToolError(w, err)
		return
	}
	var input struct {
		Arguments map[string]any `json:"arguments"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	result, err := s.tools.Invoke(r.Context(), item, action, input.Arguments)
	if err != nil {
		writeToolError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": result})
}

// listAgentTools and setAgentTools are the Capabilities tab. They live with the
// tool handlers rather than the agent ones because the rule they enforce is
// about tools: you may only attach what you can already see.
func (s *Server) listAgentTools(w http.ResponseWriter, r *http.Request) {
	item, _, _, ok := s.agentForWrite(w, r, chi.URLParam(r, "agentID"))
	if !ok {
		return
	}
	ids, err := s.tools.AgentToolIDs(r.Context(), item.ID)
	if err != nil {
		writeToolError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tool_ids": ids})
}

func (s *Server) setAgentTools(w http.ResponseWriter, r *http.Request) {
	item, user, workspaceID, ok := s.agentForWrite(w, r, chi.URLParam(r, "agentID"))
	if !ok {
		return
	}
	var input struct {
		ToolIDs []string `json:"tool_ids"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := s.tools.SetAgentTools(r.Context(), item.ID, user.ID, workspaceID, input.ToolIDs); err != nil {
		writeToolError(w, err)
		return
	}
	ids, err := s.tools.AgentToolIDs(r.Context(), item.ID)
	if err != nil {
		writeToolError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tool_ids": ids})
}
