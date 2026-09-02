package httpapi

import (
	"errors"
	"net/http"
	"strings"

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
		Kind        string   `json:"kind"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.tools.Create(r.Context(), user.ID, workspaceID, input.Name, input.Description, input.Icon, input.Tags, input.BaseURL, input.Kind)
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

// generateToolActions drafts a tool's actions with the model rather than
// asking the reader to type a dozen fields per endpoint. The draft goes
// through the same validation as a hand-typed action, so a confident-sounding
// invention is refused exactly as a typo would be, and nothing is called until
// someone opens an action and runs it.
func (s *Server) generateToolActions(w http.ResponseWriter, r *http.Request) {
	item, _, workspaceID, ok := s.toolForWrite(w, r, chi.URLParam(r, "toolID"))
	if !ok {
		return
	}
	models := s.modelsFor(r.Context(), workspaceID)
	if !models.HasGateway() {
		writeError(w, http.StatusServiceUnavailable, "Workspace chưa cấu hình Model Gateway.")
		return
	}

	var input struct {
		Description string `json:"description"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	description := input.Description
	if strings.TrimSpace(description) == "" {
		description = item.Description
	}

	drafted, err := tools.DraftActions(r.Context(), models, item.BaseURL, description)
	if err != nil {
		s.logger.Error("draft tool actions", "tool_id", item.ID, "error", err)
		writeError(w, http.StatusBadGateway, "Model Gateway hiện không phản hồi. Vui lòng thử lại.")
		return
	}

	saved := []tools.Action{}
	for _, action := range drafted {
		// A name the tool already uses is skipped rather than failing the
		// batch: drafting twice should add what is new, not refuse everything.
		result, saveErr := s.tools.SaveAction(r.Context(), item.ID, "", action)
		if saveErr != nil {
			continue
		}
		saved = append(saved, result)
	}
	writeJSON(w, http.StatusOK, map[string]any{"actions": saved})
}

// discoverMCPTools asks an MCP server what it offers and stores the answer.
// The server is the authority on its own tools, so this replaces what we hold
// rather than merging: a tool the server has dropped should disappear here too.
func (s *Server) discoverMCPTools(w http.ResponseWriter, r *http.Request) {
	item, _, _, ok := s.toolForWrite(w, r, chi.URLParam(r, "toolID"))
	if !ok {
		return
	}
	if item.Kind != tools.KindMCP {
		writeError(w, http.StatusBadRequest, "Chỉ tool MCP mới dò được danh sách action.")
		return
	}

	discovered, err := s.tools.DiscoverMCP(r.Context(), item)
	if err != nil {
		s.logger.Error("discover mcp tools", "tool_id", item.ID, "error", err)
		writeError(w, http.StatusBadGateway, "Không kết nối được MCP server.")
		return
	}

	existing, err := s.tools.Actions(r.Context(), item.ID)
	if err != nil {
		writeToolError(w, err)
		return
	}
	for _, action := range existing {
		_ = s.tools.DeleteAction(r.Context(), item.ID, action.ID)
	}

	saved := []tools.Action{}
	for _, action := range discovered {
		result, saveErr := s.tools.SaveAction(r.Context(), item.ID, "", action)
		if saveErr != nil {
			continue
		}
		saved = append(saved, result)
	}
	writeJSON(w, http.StatusOK, map[string]any{"actions": saved})
}

func (s *Server) listToolCatalog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"entries": tools.Catalog()})
}

// installCatalogTool creates a tool from a catalogue entry with its actions
// already described, so the first call works rather than landing the reader in
// an empty editor.
func (s *Server) installCatalogTool(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := s.agentWorkspace(w, r, r.URL.Query().Get("workspace"))
	if !ok {
		return
	}
	entry, found := tools.CatalogEntryByID(chi.URLParam(r, "entryID"))
	if !found {
		writeError(w, http.StatusNotFound, "Không tìm thấy tool trong danh mục.")
		return
	}

	item, err := s.tools.Create(r.Context(), user.ID, workspaceID,
		entry.Name, entry.Description, entry.Icon, nil, entry.BaseURL, tools.KindHTTP)
	if err != nil {
		writeToolError(w, err)
		return
	}
	for _, action := range entry.Actions {
		if _, saveErr := s.tools.SaveAction(r.Context(), item.ID, "", action); saveErr != nil {
			s.logger.Error("install catalog action", "tool_id", item.ID, "action", action.Name, "error", saveErr)
		}
	}

	installed, err := s.tools.Get(r.Context(), item.ID, user.ID, workspaceID)
	if err != nil {
		writeToolError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"tool": installed})
}

// importOpenAPI reads the API's own description rather than asking a model to
// remember it. Existing actions are left alone: an import adds what the
// specification has, and a name already in use is skipped by SaveAction.
func (s *Server) importOpenAPI(w http.ResponseWriter, r *http.Request) {
	item, _, _, ok := s.toolForWrite(w, r, chi.URLParam(r, "toolID"))
	if !ok {
		return
	}
	var input struct {
		URL  string `json:"url"`
		Spec string `json:"spec"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}

	raw := []byte(input.Spec)
	if strings.TrimSpace(input.Spec) == "" {
		fetched, err := s.tools.FetchOpenAPI(r.Context(), input.URL)
		if err != nil {
			writeToolError(w, err)
			return
		}
		raw = fetched
	}

	parsed, err := tools.ActionsFromOpenAPI(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Không đọc được tài liệu OpenAPI.")
		return
	}

	saved := []tools.Action{}
	for _, action := range parsed {
		result, saveErr := s.tools.SaveAction(r.Context(), item.ID, "", action)
		if saveErr != nil {
			continue
		}
		saved = append(saved, result)
	}
	writeJSON(w, http.StatusOK, map[string]any{"actions": saved})
}
