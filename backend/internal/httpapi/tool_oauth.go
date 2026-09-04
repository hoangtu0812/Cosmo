package httpapi

import (
	"net/http"
	"net/url"
	"strings"

	"cosmo/backend/internal/tools"

	"github.com/go-chi/chi/v5"
)

func (s *Server) toolOAuthForRead(w http.ResponseWriter, r *http.Request) (tools.Tool, User, string, bool) {
	user, workspaceID, ok := s.agentWorkspace(w, r, r.URL.Query().Get("workspace"))
	if !ok {
		return tools.Tool{}, User{}, "", false
	}
	item, err := s.tools.Get(r.Context(), chi.URLParam(r, "toolID"), user.ID, workspaceID)
	if err != nil {
		writeToolError(w, err)
		return tools.Tool{}, User{}, "", false
	}
	if item.Kind != tools.KindMCP {
		writeError(w, http.StatusBadRequest, "Chỉ MCP tool mới hỗ trợ OAuth discovery.")
		return tools.Tool{}, User{}, "", false
	}
	return item, user, workspaceID, true
}

func (s *Server) toolOAuthCallbackURL() string {
	return strings.TrimRight(s.cfg.PublicURL, "/") + "/api/tools/oauth/callback"
}

// getToolOAuth is deliberately read-only: it discovers public metadata and
// reports only whether this user has a grant, never any registration secret or
// token. A shared MCP tool therefore remains one integration with one private
// grant per person.
func (s *Server) getToolOAuth(w http.ResponseWriter, r *http.Request) {
	item, user, _, ok := s.toolOAuthForRead(w, r)
	if !ok {
		return
	}
	info, err := s.tools.OAuthConnection(r.Context(), item, user.ID, s.toolOAuthCallbackURL())
	if err != nil {
		writeToolError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"oauth": info})
}

func (s *Server) startToolOAuth(w http.ResponseWriter, r *http.Request) {
	item, user, workspaceID, ok := s.toolOAuthForRead(w, r)
	if !ok {
		return
	}
	if item.AuthType != tools.AuthOAuthUser {
		writeError(w, http.StatusBadRequest, "Hãy lưu kiểu OAuth 2.1 (Authorization Code + PKCE) trước.")
		return
	}
	started, err := s.tools.BeginOAuthAuthorization(r.Context(), item, user.ID, workspaceID, s.toolOAuthCallbackURL())
	if err != nil {
		writeToolError(w, err)
		return
	}
	s.audit(r, auditEvent{
		Action: "tool.oauth.started", TargetType: "tool", TargetID: item.ID, TargetLabel: item.Name,
		WorkspaceID: workspaceID,
	})
	writeJSON(w, http.StatusOK, map[string]any{"oauth": started})
}

func (s *Server) completeToolOAuth(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	state := r.URL.Query().Get("state")
	providerError := r.URL.Query().Get("error")
	result, err := s.tools.CompleteOAuthAuthorization(
		r.Context(), user.ID, state, r.URL.Query().Get("code"), r.URL.Query().Get("iss"),
		providerError, s.toolOAuthCallbackURL(),
	)
	if result.ToolID == "" {
		http.Redirect(w, r, s.cfg.FrontendURL+"/tools?oauth_error=invalid_state", http.StatusFound)
		return
	}
	target := s.cfg.FrontendURL + "/tools/" + url.PathEscape(result.ToolID) + "?workspace=" + url.QueryEscape(result.WorkspaceID)
	if err != nil {
		s.logger.Warn("complete MCP OAuth", "tool_id", result.ToolID, "user_id", user.ID, "error", err, "provider_error", providerError)
		target += "&oauth_error=authorization_failed"
	} else {
		target += "&oauth=connected"
		s.audit(r, auditEvent{
			Action: "tool.oauth.connected", TargetType: "tool", TargetID: result.ToolID,
			WorkspaceID: result.WorkspaceID,
		})
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) disconnectToolOAuth(w http.ResponseWriter, r *http.Request) {
	item, user, workspaceID, ok := s.toolOAuthForRead(w, r)
	if !ok {
		return
	}
	if err := s.tools.DisconnectOAuthUser(r.Context(), item.ID, user.ID); err != nil {
		writeToolError(w, err)
		return
	}
	s.audit(r, auditEvent{
		Action: "tool.oauth.disconnected", TargetType: "tool", TargetID: item.ID, TargetLabel: item.Name,
		WorkspaceID: workspaceID,
	})
	w.WriteHeader(http.StatusNoContent)
}
