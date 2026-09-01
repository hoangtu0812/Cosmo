package httpapi

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"cosmo/backend/internal/agents"

	"github.com/go-chi/chi/v5"
)

// writeAgentError maps a domain error onto a status. The wording lives with
// the rule in the agents package, so it is never restated here.
func writeAgentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, agents.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, agents.ErrStaleDraft):
		// 409: the request was well formed, but the world moved under it.
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, agents.ErrNameLength), errors.Is(err, agents.ErrIntroLength),
		errors.Is(err, agents.ErrKnowledgeNotInstalled), errors.Is(err, agents.ErrKnowledgeSave):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "Không thể xử lý agent.")
	}
}

// agentWorkspace resolves the workspace an agent request applies to and
// refuses when the caller is not a member of it.
func (s *Server) agentWorkspace(w http.ResponseWriter, r *http.Request, requested string) (User, string, bool) {
	user := currentUser(r.Context())
	workspaceID := s.memberWorkspace(r.Context(), user.ID, requested)
	if workspaceID == "" {
		writeError(w, http.StatusForbidden, "Bạn không có quyền truy cập workspace này.")
		return User{}, "", false
	}
	return user, workspaceID, true
}

// agentForWrite loads an agent the caller may change: its author, or an
// administrator of the workspace it lives in. Authorisation stays here because
// it is a question about the caller, which the domain is never told about.
func (s *Server) agentForWrite(w http.ResponseWriter, r *http.Request, agentID string) (agents.Agent, User, string, bool) {
	user, workspaceID, ok := s.agentWorkspace(w, r, r.URL.Query().Get("workspace"))
	if !ok {
		return agents.Agent{}, User{}, "", false
	}
	item, err := s.agents.Get(r.Context(), agentID, user.ID, workspaceID)
	if err != nil {
		writeAgentError(w, err)
		return agents.Agent{}, User{}, "", false
	}
	if item.OwnerUserID != user.ID && !s.isWorkspaceAdmin(r.Context(), user, workspaceID) {
		writeError(w, http.StatusForbidden, "Chỉ người tạo agent hoặc quản trị workspace mới sửa được.")
		return agents.Agent{}, User{}, "", false
	}
	return item, user, workspaceID, true
}

func (s *Server) listAgents(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := s.agentWorkspace(w, r, r.URL.Query().Get("workspace"))
	if !ok {
		return
	}
	items, err := s.agents.List(r.Context(), user.ID, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải danh sách agent.")
		return
	}
	isAdmin := s.isWorkspaceAdmin(r.Context(), user, workspaceID)
	for index := range items {
		items[index].IsEditable = items[index].OwnerUserID == user.ID || isAdmin
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": items})
}

func (s *Server) createAgent(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name         string   `json:"name"`
		Introduction string   `json:"introduction"`
		Avatar       string   `json:"avatar"`
		Tags         []string `json:"tags"`
		Visibility   string   `json:"visibility"`
		WorkspaceID  string   `json:"workspace_id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	user, workspaceID, ok := s.agentWorkspace(w, r, input.WorkspaceID)
	if !ok {
		return
	}
	agentID, err := s.agents.Create(r.Context(), agents.NewAgent{
		Name:         input.Name,
		Introduction: input.Introduction,
		Avatar:       input.Avatar,
		Tags:         input.Tags,
		Visibility:   input.Visibility,
		OwnerUserID:  user.ID,
		WorkspaceID:  workspaceID,
	})
	if err != nil {
		writeAgentError(w, err)
		return
	}
	s.writeAgent(w, r, agentID, user, workspaceID, http.StatusCreated)
}

func (s *Server) getAgent(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := s.agentWorkspace(w, r, r.URL.Query().Get("workspace"))
	if !ok {
		return
	}
	s.writeAgent(w, r, chi.URLParam(r, "agentID"), user, workspaceID, http.StatusOK)
}

func (s *Server) updateAgent(w http.ResponseWriter, r *http.Request) {
	current, user, workspaceID, ok := s.agentForWrite(w, r, chi.URLParam(r, "agentID"))
	if !ok {
		return
	}
	// Every field is optional: the editor saves one tab at a time, and a field
	// left out must keep what is stored rather than blank it.
	var input struct {
		Name                  *string   `json:"name"`
		Introduction          *string   `json:"introduction"`
		Avatar                *string   `json:"avatar"`
		Tags                  *[]string `json:"tags"`
		Visibility            *string   `json:"visibility"`
		Model                 *string   `json:"model"`
		SystemPrompt          *string   `json:"system_prompt"`
		OpeningLine           *string   `json:"opening_line"`
		PresetQuestions       *[]string `json:"preset_questions"`
		HasSuggestedQuestions *bool     `json:"has_suggested_questions"`
		IsMemoryEnabled       *bool     `json:"is_memory_enabled"`
		KnowledgeBaseIDs      *[]string `json:"knowledge_base_ids"`
		DraftRevision         int64     `json:"draft_revision"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	changes := agents.Changes{
		Name: input.Name, Introduction: input.Introduction, Avatar: input.Avatar,
		Tags: input.Tags, Visibility: input.Visibility, Model: input.Model,
		SystemPrompt: input.SystemPrompt, OpeningLine: input.OpeningLine,
		PresetQuestions: input.PresetQuestions, HasSuggestedQuestions: input.HasSuggestedQuestions,
		IsMemoryEnabled: input.IsMemoryEnabled, KnowledgeBaseIDs: input.KnowledgeBaseIDs,
	}
	if err := s.agents.SaveDraft(r.Context(), current, changes, input.DraftRevision); err != nil {
		writeAgentError(w, err)
		return
	}
	s.writeAgent(w, r, current.ID, user, workspaceID, http.StatusOK)
}

func (s *Server) deleteAgent(w http.ResponseWriter, r *http.Request) {
	current, _, _, ok := s.agentForWrite(w, r, chi.URLParam(r, "agentID"))
	if !ok {
		return
	}
	if err := s.agents.Delete(r.Context(), current.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể xoá agent.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) writeAgent(w http.ResponseWriter, r *http.Request, agentID string, user User, workspaceID string, status int) {
	item, err := s.agents.Get(r.Context(), agentID, user.ID, workspaceID)
	if err != nil {
		writeAgentError(w, err)
		return
	}
	item.IsEditable = item.OwnerUserID == user.ID || s.isWorkspaceAdmin(r.Context(), user, workspaceID)
	writeJSON(w, status, map[string]any{"agent": item})
}

func (s *Server) listAgentConversations(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "agentID")
	user, workspaceID, ok := s.agentWorkspace(w, r, r.URL.Query().Get("workspace"))
	if !ok {
		return
	}
	if _, err := s.agents.Get(r.Context(), agentID, user.ID, workspaceID); err != nil {
		writeAgentError(w, err)
		return
	}
	items, err := s.agents.Conversations(r.Context(), agentID, user.ID, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải hội thoại của agent.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversations": items})
}

func (s *Server) startAgentConversation(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "agentID")
	user, workspaceID, ok := s.agentWorkspace(w, r, r.URL.Query().Get("workspace"))
	if !ok {
		return
	}
	// Seeing the agent is what grants the right to talk to it, so a private
	// agent cannot be addressed by anyone but its author.
	agent, err := s.agents.Get(r.Context(), agentID, user.ID, workspaceID)
	if err != nil {
		writeAgentError(w, err)
		return
	}
	conversation, err := s.agents.StartConversation(r.Context(), agentID, user.ID, workspaceID, agent.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tạo hội thoại.")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"conversation": conversation})
}

// agentAvatar serves an uploaded avatar. Anyone who can see the agent can see
// its picture; the visibility check inside Get is what decides that.
func (s *Server) agentAvatar(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "agentID")
	user, workspaceID, ok := s.agentWorkspace(w, r, r.URL.Query().Get("workspace"))
	if !ok {
		return
	}
	if _, err := s.agents.Get(r.Context(), agentID, user.ID, workspaceID); err != nil {
		writeAgentError(w, err)
		return
	}
	image, mime, err := s.agents.Avatar(r.Context(), agentID)
	if err != nil || len(image) == 0 {
		writeError(w, http.StatusNotFound, "Agent chưa có ảnh đại diện.")
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = w.Write(image)
}

func (s *Server) uploadAgentAvatar(w http.ResponseWriter, r *http.Request) {
	current, _, _, ok := s.agentForWrite(w, r, chi.URLParam(r, "agentID"))
	if !ok {
		return
	}
	var input struct {
		MIME string `json:"mime"`
		Data string `json:"data"` // base64, no data: prefix
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if !allowedIconMIME[input.MIME] {
		writeError(w, http.StatusBadRequest, "Chỉ nhận ảnh PNG, JPEG, WebP hoặc GIF.")
		return
	}
	image, err := base64.StdEncoding.DecodeString(input.Data)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Dữ liệu ảnh không hợp lệ.")
		return
	}
	if len(image) == 0 || len(image) > maxIconBytes {
		writeError(w, http.StatusBadRequest, "Ảnh phải nhỏ hơn 256 KB.")
		return
	}
	// Trust the bytes, not the declared type: a mismatch means the upload is
	// not what it claims to be.
	if sniffed := http.DetectContentType(image); sniffed != input.MIME {
		writeError(w, http.StatusBadRequest, "Nội dung ảnh không khớp định dạng khai báo.")
		return
	}
	if err := s.agents.SetAvatar(r.Context(), current.ID, image, input.MIME); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể lưu ảnh đại diện.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteAgentAvatar(w http.ResponseWriter, r *http.Request) {
	current, _, _, ok := s.agentForWrite(w, r, chi.URLParam(r, "agentID"))
	if !ok {
		return
	}
	if err := s.agents.ClearAvatar(r.Context(), current.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể xoá ảnh đại diện.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) publishAgent(w http.ResponseWriter, r *http.Request) {
	current, user, workspaceID, ok := s.agentForWrite(w, r, chi.URLParam(r, "agentID"))
	if !ok {
		return
	}
	var input struct {
		Changelog string `json:"changelog"`
	}
	if r.Body != nil && r.ContentLength != 0 && !decodeJSON(w, r, &input) {
		return
	}
	if _, err := s.agents.Publish(r.Context(), current.ID, user.ID, strings.TrimSpace(input.Changelog)); err != nil {
		writeAgentError(w, err)
		return
	}
	s.writeAgent(w, r, current.ID, user, workspaceID, http.StatusOK)
}

func (s *Server) listAgentVersions(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "agentID")
	user, workspaceID, ok := s.agentWorkspace(w, r, r.URL.Query().Get("workspace"))
	if !ok {
		return
	}
	if _, err := s.agents.Get(r.Context(), agentID, user.ID, workspaceID); err != nil {
		writeAgentError(w, err)
		return
	}
	items, err := s.agents.Versions(r.Context(), agentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải danh sách phiên bản.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": items})
}
