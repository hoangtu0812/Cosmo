package httpapi

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"

	"cosmo/backend/internal/tools"

	"github.com/go-chi/chi/v5"
)

// writeToolError maps a domain error onto a status. The wording lives with the
// rule in the tools package, so it is never restated here.
func writeToolError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, tools.ErrNotOffered), errors.Is(err, tools.ErrNotInstalled),
		errors.Is(err, tools.ErrKeyedAutoCall), errors.Is(err, tools.ErrNoActions):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, tools.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, tools.ErrDuplicateAction):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, tools.ErrSecretsOff):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, tools.ErrToolUnauthorized):
		writeError(w, http.StatusBadGateway, err.Error())
	case errors.Is(err, tools.ErrOAuthConfig), errors.Is(err, tools.ErrOAuthToken),
		errors.Is(err, tools.ErrOAuthDiscovery), errors.Is(err, tools.ErrOAuthConnection),
		errors.Is(err, tools.ErrOAuthState), errors.Is(err, tools.ErrOAuthProvider), errors.Is(err, tools.ErrOAuthRegistration):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, tools.ErrCallFailed):
		// 502: we are reporting someone else's endpoint failing, not our own.
		writeError(w, http.StatusBadGateway, err.Error())
	case errors.Is(err, tools.ErrNameLength), errors.Is(err, tools.ErrDescription),
		errors.Is(err, tools.ErrBaseURL), errors.Is(err, tools.ErrPrivateAddress),
		errors.Is(err, tools.ErrLoopbackAddress),
		errors.Is(err, tools.ErrBuiltinHasNoBaseURL),
		errors.Is(err, tools.ErrAuthType), errors.Is(err, tools.ErrAuthHeaderName),
		errors.Is(err, tools.ErrActionName), errors.Is(err, tools.ErrMCPToolName),
		errors.Is(err, tools.ErrMCPContract), errors.Is(err, tools.ErrActionMethod),
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
	// A tool is an address Cosmo will call with a credential attached, so where
	// it points is recorded every time it is set or changed.
	s.audit(r, auditEvent{
		Action: "tool.created", TargetType: "tool", TargetID: item.ID, TargetLabel: item.Name,
		WorkspaceID: workspaceID, Metadata: map[string]string{"kind": item.Kind, "base_url": item.BaseURL},
	})
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
	changed := []string{}
	for field, sent := range map[string]bool{
		"name": changes.Name != nil, "description": changes.Description != nil, "icon": changes.Icon != nil,
		"tags": changes.Tags != nil, "visibility": changes.Visibility != nil, "base_url": changes.BaseURL != nil,
		"auth_type": changes.AuthType != nil, "auth_header_name": changes.AuthHeaderName != nil,
		"credential": changes.AuthSecret != nil,
	} {
		if sent {
			changed = append(changed, field)
		}
	}
	sort.Strings(changed)
	if len(changed) > 0 {
		metadata := map[string]any{"fields": changed, "base_url": updated.BaseURL, "visibility": updated.Visibility}
		if changes.AuthSecret != nil {
			// The key never appears. Whether one was set or cleared does: a tool
			// that gains a credential also loses the right to be called
			// automatically, and that follows from this row.
			metadata["credential"] = "cleared"
			if strings.TrimSpace(*changes.AuthSecret) != "" {
				metadata["credential"] = "replaced"
			}
		}
		s.audit(r, auditEvent{
			Action: "tool.updated", TargetType: "tool", TargetID: item.ID, TargetLabel: updated.Name,
			WorkspaceID: workspaceID, Metadata: metadata,
		})
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
	s.audit(r, auditEvent{
		Action: "tool.deleted", TargetType: "tool", TargetID: item.ID, TargetLabel: item.Name,
		WorkspaceID: workspaceID, Metadata: map[string]string{"kind": item.Kind, "base_url": item.BaseURL},
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) saveToolAction(w http.ResponseWriter, r *http.Request) {
	item, _, workspaceID, ok := s.toolForWrite(w, r, chi.URLParam(r, "toolID"))
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
	s.audit(r, auditEvent{
		Action: "tool.action.saved", TargetType: "tool", TargetID: item.ID, TargetLabel: item.Name,
		WorkspaceID: workspaceID,
		Metadata:    map[string]string{"action": action.Name, "method": action.Method, "path": action.Path},
	})
	writeJSON(w, http.StatusOK, map[string]any{"action": action})
}

func (s *Server) deleteToolAction(w http.ResponseWriter, r *http.Request) {
	item, _, workspaceID, ok := s.toolForWrite(w, r, chi.URLParam(r, "toolID"))
	if !ok {
		return
	}
	actionID := chi.URLParam(r, "actionID")
	removed, _ := s.tools.Action(r.Context(), item.ID, actionID)
	if err := s.tools.DeleteAction(r.Context(), item.ID, actionID); err != nil {
		writeToolError(w, err)
		return
	}
	s.audit(r, auditEvent{
		Action: "tool.action.deleted", TargetType: "tool", TargetID: item.ID, TargetLabel: item.Name,
		WorkspaceID: workspaceID, Metadata: map[string]string{"action": removed.Name},
	})
	w.WriteHeader(http.StatusNoContent)
}

// testToolAction calls the endpoint once with arguments the reader typed and
// hands back what came off the wire. It is restricted to people who may edit
// the tool: the response can contain whatever the credential unlocks.
func (s *Server) testToolAction(w http.ResponseWriter, r *http.Request) {
	item, user, workspaceID, ok := s.toolForWrite(w, r, chi.URLParam(r, "toolID"))
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
	// The test runs as the reader, so a built-in that describes them has the
	// same answer here as it would mid-conversation.
	ctx := tools.WithCaller(r.Context(), s.callerFor(r.Context(), user, workspaceID))
	result, err := s.tools.Invoke(ctx, item, action, input.Arguments)
	// Recorded either way: this is Cosmo reaching a third-party endpoint with
	// the workspace's credential, on someone's say-so, outside any run. The
	// arguments and the response body are not stored - the point of the row is
	// that the call happened, not what came back.
	outcome, status := auditSuccess, result.Status
	if err != nil {
		outcome = auditFailure
	}
	s.audit(r, auditEvent{
		Action: "tool.action.tested", TargetType: "tool", TargetID: item.ID, TargetLabel: item.Name,
		WorkspaceID: workspaceID, Outcome: outcome,
		Metadata: map[string]any{"action": action.Name, "method": action.Method, "path": action.Path, "status": status},
	})
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
	s.audit(r, auditEvent{
		Action: "agent.tools.updated", TargetType: "agent", TargetID: item.ID, TargetLabel: item.Name,
		WorkspaceID: workspaceID, Metadata: map[string]any{"tool_ids": ids},
	})
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
	s.audit(r, auditEvent{
		Action: "tool.actions.drafted", TargetType: "tool", TargetID: item.ID, TargetLabel: item.Name,
		WorkspaceID: workspaceID, Metadata: map[string]int{"actions": len(saved), "offered": len(drafted)},
	})
	writeJSON(w, http.StatusOK, map[string]any{"actions": saved})
}

// discoverMCPTools asks an MCP server what it offers and stores the answer.
// The server is the authority on its own tools, so this replaces what we hold
// rather than merging: a tool the server has dropped should disappear here too.
func (s *Server) discoverMCPTools(w http.ResponseWriter, r *http.Request) {
	item, user, workspaceID, ok := s.toolForWrite(w, r, chi.URLParam(r, "toolID"))
	if !ok {
		return
	}
	if item.Kind != tools.KindMCP {
		writeError(w, http.StatusBadRequest, "Chỉ tool MCP mới dò được danh sách action.")
		return
	}

	ctx := tools.WithCaller(r.Context(), s.callerFor(r.Context(), user, workspaceID))
	discovered, err := s.tools.DiscoverMCP(ctx, item)
	if err != nil {
		s.logger.Error("discover mcp tools", "tool_id", item.ID, "error", err)
		if errors.Is(err, tools.ErrOAuthConnection) || errors.Is(err, tools.ErrOAuthRegistration) ||
			errors.Is(err, tools.ErrOAuthToken) || errors.Is(err, tools.ErrOAuthDiscovery) {
			writeToolError(w, err)
			return
		}
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
	// This replaces the whole callable surface of the tool, so what it replaced
	// is recorded alongside what it became.
	s.audit(r, auditEvent{
		Action: "tool.actions.discovered", TargetType: "tool", TargetID: item.ID, TargetLabel: item.Name,
		WorkspaceID: workspaceID,
		Metadata:    map[string]int{"actions": len(saved), "replaced": len(existing)},
	})
	writeJSON(w, http.StatusOK, map[string]any{"actions": saved})
}

// The order of the categories is decided beside the entries rather than in the
// interface, so a new category cannot appear without someone choosing where it
// belongs.
func (s *Server) listToolCatalog(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := s.agentWorkspace(w, r, r.URL.Query().Get("workspace"))
	if !ok {
		return
	}
	// Which entries this workspace already has, so the market can say so
	// instead of offering the same toolkit a second time.
	installed, err := s.tools.InstalledCatalogIDs(r.Context(), workspaceID)
	if err != nil {
		writeToolError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries":    tools.Catalog(),
		"categories": tools.CatalogCategories(),
		"installed":  installed,
	})
}

// installCatalogTool creates a tool from a catalogue entry with its actions
// already described, so the first call works rather than landing the reader in
// an empty editor. Installing one already installed returns the tool that is
// there: two copies of the same toolkit are indistinguishable to a reader and
// ambiguous to a model.
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

	installed, existed, err := s.tools.InstallCatalogEntry(r.Context(), user.ID, workspaceID, entry)
	if err != nil {
		writeToolError(w, err)
		return
	}
	status := http.StatusCreated
	if existed {
		status = http.StatusOK
	}
	if !existed {
		s.audit(r, auditEvent{
			Action: "tool.installed_from_catalog", TargetType: "tool", TargetID: installed.ID,
			TargetLabel: installed.Name, WorkspaceID: workspaceID,
			Metadata: map[string]string{"catalog_entry": entry.ID, "base_url": installed.BaseURL},
		})
	}
	writeJSON(w, status, map[string]any{"tool": installed, "already_installed": existed})
}

// importOpenAPI reads the API's own description rather than asking a model to
// remember it. Existing actions are left alone: an import adds what the
// specification has, and a name already in use is skipped by SaveAction.
func (s *Server) importOpenAPI(w http.ResponseWriter, r *http.Request) {
	item, _, workspaceID, ok := s.toolForWrite(w, r, chi.URLParam(r, "toolID"))
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
	s.audit(r, auditEvent{
		Action: "tool.actions.imported", TargetType: "tool", TargetID: item.ID, TargetLabel: item.Name,
		WorkspaceID: workspaceID,
		Metadata:    map[string]any{"actions": len(saved), "described": len(parsed), "source": input.URL},
	})
	writeJSON(w, http.StatusOK, map[string]any{"actions": saved})
}

// listWorkspaceTools is what the workspace has installed, with the switch that
// decides whether a plain chat may reach for each one.
func (s *Server) listWorkspaceTools(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := s.agentWorkspace(w, r, chi.URLParam(r, "workspaceID"))
	if !ok {
		return
	}
	installs, err := s.tools.InstalledInWorkspace(r.Context(), workspaceID, user.ID)
	if err != nil {
		writeToolError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"installs": installs})
}

// installWorkspaceTool makes a tool available here. Available is not callable:
// the switch below is a second, separate act.
func (s *Server) installWorkspaceTool(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := s.agentWorkspace(w, r, chi.URLParam(r, "workspaceID"))
	if !ok {
		return
	}
	if !s.isWorkspaceAdmin(r.Context(), user, workspaceID) {
		writeError(w, http.StatusForbidden, "Chỉ quản trị viên workspace mới được cài tool.")
		return
	}
	toolID := chi.URLParam(r, "toolID")
	if err := s.tools.InstallToWorkspace(r.Context(), workspaceID, toolID, user.ID); err != nil {
		writeToolError(w, err)
		return
	}
	s.audit(r, auditEvent{
		Action: "workspace.tool.installed", TargetType: "tool", TargetID: toolID,
		TargetLabel: s.toolName(r.Context(), toolID), WorkspaceID: workspaceID,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) uninstallWorkspaceTool(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := s.agentWorkspace(w, r, chi.URLParam(r, "workspaceID"))
	if !ok {
		return
	}
	if !s.isWorkspaceAdmin(r.Context(), user, workspaceID) {
		writeError(w, http.StatusForbidden, "Chỉ quản trị viên workspace mới được gỡ tool.")
		return
	}
	toolID := chi.URLParam(r, "toolID")
	name := s.toolName(r.Context(), toolID)
	if err := s.tools.UninstallFromWorkspace(r.Context(), workspaceID, toolID); err != nil {
		writeToolError(w, err)
		return
	}
	s.audit(r, auditEvent{
		Action: "workspace.tool.uninstalled", TargetType: "tool", TargetID: toolID,
		TargetLabel: name, WorkspaceID: workspaceID,
	})
	w.WriteHeader(http.StatusNoContent)
}

// toolName is what a tool is called, for the audit rows raised by handlers that
// only ever see its id.
func (s *Server) toolName(ctx context.Context, toolID string) string {
	var name string
	_ = s.db.QueryRow(ctx, `SELECT name FROM tools WHERE id = $1`, toolID).Scan(&name)
	return name
}

// setWorkspaceToolAutoCall is the flag that lets the model reach for a tool on
// its own. Separate from installing, and refused for a tool holding a key.
func (s *Server) setWorkspaceToolAutoCall(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := s.agentWorkspace(w, r, chi.URLParam(r, "workspaceID"))
	if !ok {
		return
	}
	if !s.isWorkspaceAdmin(r.Context(), user, workspaceID) {
		writeError(w, http.StatusForbidden, "Chỉ quản trị viên workspace mới đổi được thiết lập này.")
		return
	}
	var input struct {
		AutoCall bool `json:"auto_call"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	toolID := chi.URLParam(r, "toolID")
	if err := s.tools.SetAutoCall(r.Context(), workspaceID, toolID, input.AutoCall); err != nil {
		writeToolError(w, err)
		return
	}
	// Switching this on is what lets the model call a third-party endpoint
	// without anyone asking it to, which makes it the single most consequential
	// toggle a workspace admin has.
	s.audit(r, auditEvent{
		Action: "workspace.tool.auto_call_updated", TargetType: "tool", TargetID: toolID,
		TargetLabel: s.toolName(r.Context(), toolID), WorkspaceID: workspaceID,
		Metadata: map[string]bool{"auto_call": input.AutoCall},
	})
	w.WriteHeader(http.StatusNoContent)
}

// listToolShares says which workspaces a tool is offered to. Only its owner
// may look: the list is a statement about other workspaces.
func (s *Server) listToolShares(w http.ResponseWriter, r *http.Request) {
	item, _, _, ok := s.toolForWrite(w, r, chi.URLParam(r, "toolID"))
	if !ok {
		return
	}
	shares, err := s.tools.Shares(r.Context(), item.ID)
	if err != nil {
		writeToolError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspaces": shares})
}

func (s *Server) setToolShares(w http.ResponseWriter, r *http.Request) {
	item, _, workspaceID, ok := s.toolForWrite(w, r, chi.URLParam(r, "toolID"))
	if !ok {
		return
	}
	var input struct {
		Workspaces []string `json:"workspaces"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := s.tools.SetShares(r.Context(), item.ID, input.Workspaces); err != nil {
		writeToolError(w, err)
		return
	}
	s.audit(r, auditEvent{
		Action: "tool.sharing_updated", TargetType: "tool", TargetID: item.ID, TargetLabel: item.Name,
		WorkspaceID: workspaceID, Metadata: map[string]any{"workspaces": input.Workspaces},
	})
	w.WriteHeader(http.StatusNoContent)
}

// Publishing a tool freezes what it offers, so an agent published afterwards
// keeps calling that and not whatever the tool becomes later. Only someone who
// may edit the tool may publish it, which toolForWrite already decides.
func (s *Server) publishTool(w http.ResponseWriter, r *http.Request) {
	item, user, workspaceID, ok := s.toolForWrite(w, r, chi.URLParam(r, "toolID"))
	if !ok {
		return
	}
	var input struct {
		Changelog string `json:"changelog"`
	}
	if r.Body != nil && r.ContentLength != 0 && !decodeJSON(w, r, &input) {
		return
	}
	version, err := s.tools.Publish(r.Context(), item.ID, user.ID, strings.TrimSpace(input.Changelog))
	if err != nil {
		writeToolError(w, err)
		return
	}
	s.audit(r, auditEvent{
		Action: "tool.published", TargetType: "tool", TargetID: item.ID, TargetLabel: item.Name,
		WorkspaceID: workspaceID,
		Metadata:    map[string]any{"version": version.VersionNumber, "changelog": strings.TrimSpace(input.Changelog)},
	})
	writeJSON(w, http.StatusCreated, map[string]any{"version": version})
}

func (s *Server) listToolVersions(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := s.agentWorkspace(w, r, r.URL.Query().Get("workspace"))
	if !ok {
		return
	}
	toolID := chi.URLParam(r, "toolID")
	// Read through the same visibility rule as the tool itself: a version list
	// is a description of a tool, and must not reach further than the tool.
	if _, err := s.tools.Get(r.Context(), toolID, user.ID, workspaceID); err != nil {
		writeToolError(w, err)
		return
	}
	items, err := s.tools.Versions(r.Context(), toolID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải danh sách phiên bản.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": items})
}
