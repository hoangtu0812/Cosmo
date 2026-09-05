package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"cosmo/backend/internal/agents"
	"cosmo/backend/internal/config"
	"cosmo/backend/internal/knowledge"
	"cosmo/backend/internal/modelgateway"
	"cosmo/backend/internal/runs"
	"cosmo/backend/internal/secrets"
	"cosmo/backend/internal/tools"
	"cosmo/backend/internal/workflows"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"
)

const sessionCookie = "cosmo_session"
const oauthStateCookie = "cosmo_oauth_state"

type Server struct {
	cfg          config.Config
	db           *pgxpool.Pool
	models       *modelgateway.Client
	knowledge    *knowledge.Client
	runs         *runs.Repository
	agents       *agents.Repository
	tools        *tools.Repository
	workflows    *workflows.Repository
	secrets      *secrets.Box
	logger       *slog.Logger
	oauthConfig  *oauth2.Config
	oidcVerifier *oidc.IDTokenVerifier
}

type User struct {
	ID              string `json:"id"`
	Email           string `json:"email"`
	Name            string `json:"name"`
	Role            string `json:"role"`
	LastWorkspaceID string `json:"last_workspace_id,omitempty"`
	HasAvatar       bool   `json:"has_avatar"`
}

type Workspace struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Icon        string `json:"icon,omitempty"`
	// True when an uploaded image exists; the client fetches it from
	// /api/workspaces/{id}/icon rather than receiving it inline.
	HasIconImage bool `json:"has_icon_image"`
	// What this workspace wants said in every answer: what it does, who its
	// members are, how it wants to be addressed. Empty until somebody writes
	// it, and then prepended to every turn.
	Context string `json:"context"`
	Role    string `json:"role"`
	// Model status is per workspace now, so the chat surface can tell the user
	// which workspace still needs a gateway without a second request.
	ModelConfigured bool   `json:"model_configured"`
	ModelAlias      string `json:"model_alias,omitempty"`
}

type Conversation struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	AgentID     string `json:"agent_id,omitempty"`
	// Who answers here, for a list that has to tell two identically titled
	// conversations apart: the agent's name, or the model that wrote the last
	// answer. Both empty on a conversation nobody has answered yet.
	AgentName string    `json:"agent_name,omitempty"`
	Model     string    `json:"model,omitempty"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Message struct {
	ID             string     `json:"id"`
	ConversationID string     `json:"conversation_id"`
	Role           string     `json:"role"`
	Content        string     `json:"content"`
	Model          string     `json:"model,omitempty"`
	Citations      []Citation `json:"citations,omitempty"`
	ToolCalls      []ToolCall `json:"tool_calls,omitempty"`
	// The files this question arrived with. Names and sizes only - the text
	// went to the model, and repeating it in the transcript would send the
	// whole document to the browser on every reload.
	Attachments []Attachment `json:"attachments,omitempty"`
	// What this answer cost and where the window went. Only on an assistant
	// message, and only where the gateway reported it.
	Usage     *modelgateway.Usage `json:"usage,omitempty"`
	CreatedAt time.Time           `json:"created_at"`
}

// Citation points to a source the answer actually retrieved. The frontend
// opens it through a protected document route, never directly in MinIO.
type Citation struct {
	Index      int    `json:"index"`
	KBID       string `json:"kb_id"`
	DocumentID string `json:"document_id"`
	Title      string `json:"title"`
	Source     string `json:"source"`
	Section    string `json:"section,omitempty"`
	Page       string `json:"page,omitempty"`
}

type contextKey string

const userContextKey contextKey = "user"

func New(ctx context.Context, cfg config.Config, db *pgxpool.Pool, models *modelgateway.Client, logger *slog.Logger) (*Server, error) {
	box, err := secrets.New(cfg.SessionSecret)
	if err != nil {
		return nil, fmt.Errorf("initialize secret box: %w", err)
	}
	// A nil client means the knowledge plane is switched off; chat then answers
	// without retrieval rather than failing.
	s := &Server{
		cfg:       cfg,
		db:        db,
		models:    models,
		knowledge: knowledge.New(cfg.RAGServiceURL, cfg.RAGTimeout),
		runs:      runs.NewRepository(db),
		agents:    agents.NewRepository(db, logger),
		tools:     tools.NewRepository(db, logger, box, tools.EgressPolicy{AllowedHosts: cfg.ToolEgressAllowedHosts}, tools.SearchBackend{BaseURL: cfg.SearchURL}),
		workflows: workflows.NewRepository(db, logger),
		secrets:   box,
		logger:    logger,
	}
	if cfg.EntraEnabled() {
		provider, err := oidc.NewProvider(ctx, fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", cfg.EntraTenantID))
		if err != nil {
			return nil, fmt.Errorf("initialize Microsoft Entra OIDC: %w", err)
		}
		s.oauthConfig = &oauth2.Config{
			ClientID:     cfg.EntraClientID,
			ClientSecret: cfg.EntraClientSecret,
			RedirectURL:  cfg.EntraRedirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       []string{oidc.ScopeOpenID, "profile", "email", "User.Read"},
		}
		s.oidcVerifier = provider.Verifier(&oidc.Config{ClientID: cfg.EntraClientID})
	}
	// Ingestion runs inside this process, so a document still marked as being
	// processed when it starts was cut short by the last shutdown and will
	// never finish on its own. Left that way it also blocks re-indexing, which
	// refuses to start while documents are in flight.
	if err := s.recoverInterruptedIngestions(ctx); err != nil {
		logger.Warn("could not recover interrupted ingestions", "error", err)
	}
	return s, nil
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(s.cors)
	r.Get("/api/health", s.health)
	r.Get("/api/auth/config", s.authConfig)
	r.Post("/api/auth/signup", s.signup)
	r.Post("/api/auth/signin", s.signin)
	r.Post("/api/auth/signout", s.signout)
	r.Get("/api/auth/entra/start", s.entraStart)
	r.Get("/api/auth/entra/callback", s.entraCallback)

	r.Group(func(protected chi.Router) {
		protected.Use(s.requireUser)
		protected.Get("/api/auth/me", s.me)
		protected.Get("/api/auth/me/avatar", s.userAvatar)
		protected.Get("/api/admin/users", s.listAdminUsers)
		protected.Patch("/api/admin/users/{userID}", s.updateAdminUser)
		protected.Get("/api/admin/audit-logs", s.listAuditLogs)
		protected.Get("/api/admin/audit-logs/filters", s.auditLogFilters)
		protected.Get("/api/admin/audit-logs/export", s.exportAuditLogs)
		protected.Get("/api/admin/analytics", s.platformAnalytics)
		protected.Get("/api/admin/system", s.systemStatus)
		protected.Post("/api/admin/system/knowledge/reindex", s.reindexKnowledgeDocuments)
		protected.Get("/api/admin/system/knowledge/reindex", s.knowledgeIndexStatus)
		protected.Get("/api/workspaces", s.workspaces)
		protected.Post("/api/workspaces/{workspaceID}/select", s.selectWorkspace)
		protected.Get("/api/conversations", s.listConversations)
		protected.Post("/api/conversations", s.createConversation)
		protected.Patch("/api/conversations/{conversationID}", s.renameConversation)
		protected.Delete("/api/conversations/{conversationID}", s.deleteConversation)
		protected.Get("/api/conversations/{conversationID}/messages", s.listMessages)
		protected.Post("/api/conversations/{conversationID}/messages", s.chat)
		protected.Delete("/api/conversations/{conversationID}/messages/{messageID}", s.deleteMessage)
		protected.Get("/api/conversations/{conversationID}/attachments", s.listAttachments)
		protected.Get("/api/conversations/{conversationID}/attachments/{attachmentID}", s.readAttachment)
		protected.Post("/api/conversations/{conversationID}/attachments", s.uploadAttachment)
		protected.Delete("/api/conversations/{conversationID}/attachments/{attachmentID}", s.deleteAttachment)

		protected.Post("/api/workspaces", s.createWorkspace)
		protected.Patch("/api/workspaces/{workspaceID}", s.updateWorkspace)
		protected.Delete("/api/workspaces/{workspaceID}", s.deleteWorkspace)
		protected.Get("/api/workspaces/{workspaceID}/icon", s.workspaceIcon)
		protected.Put("/api/workspaces/{workspaceID}/icon", s.uploadWorkspaceIcon)
		protected.Delete("/api/workspaces/{workspaceID}/icon", s.deleteWorkspaceIcon)
		protected.Get("/api/workspaces/{workspaceID}/members", s.listMembers)
		protected.Get("/api/workspaces/{workspaceID}/models", s.listWorkspaceModels)
		protected.Get("/api/workspaces/{workspaceID}/knowledge/models", s.listWorkspaceKnowledgeModels)
		protected.Get("/api/workspaces/{workspaceID}/settings/llm", s.getLLMSettings)
		protected.Put("/api/workspaces/{workspaceID}/settings/llm", s.putLLMSettings)
		protected.Post("/api/workspaces/{workspaceID}/settings/llm/models", s.listGatewayModels)
		protected.Get("/api/workspaces/{workspaceID}/invitations", s.listInvitations)
		protected.Post("/api/workspaces/{workspaceID}/invitations", s.createInvitation)
		protected.Delete("/api/workspaces/{workspaceID}/invitations/{invitationID}", s.revokeInvitation)
		protected.Post("/api/invitations/accept", s.acceptInvitation)
		protected.Get("/api/runs", s.listRuns)
		protected.Get("/api/runs/{runID}", s.getRun)
		protected.Get("/api/runs/{runID}/steps", s.listRunSteps)
		protected.Get("/api/runs/{runID}/events", s.listRunEvents)
		protected.Get("/api/runs/{runID}/stream", s.streamRunEvents)
		protected.Post("/api/runs/{runID}/cancel", s.cancelRun)

		protected.Get("/api/agents", s.listAgents)
		protected.Post("/api/agents", s.createAgent)
		protected.Get("/api/agents/{agentID}", s.getAgent)
		protected.Patch("/api/agents/{agentID}", s.updateAgent)
		protected.Delete("/api/agents/{agentID}", s.deleteAgent)
		protected.Post("/api/tools/{toolID}/publish", s.publishTool)
		protected.Get("/api/tools/{toolID}/versions", s.listToolVersions)
		protected.Post("/api/agents/{agentID}/publish", s.publishAgent)
		protected.Get("/api/agents/{agentID}/versions", s.listAgentVersions)
		protected.Get("/api/agents/{agentID}/conversations", s.listAgentConversations)
		protected.Post("/api/agents/{agentID}/conversations", s.startAgentConversation)
		protected.Get("/api/agents/{agentID}/avatar", s.agentAvatar)
		protected.Put("/api/agents/{agentID}/avatar", s.uploadAgentAvatar)
		protected.Delete("/api/agents/{agentID}/avatar", s.deleteAgentAvatar)
		protected.Get("/api/agents/{agentID}/tools", s.listAgentTools)
		protected.Put("/api/agents/{agentID}/tools", s.setAgentTools)
		protected.Get("/api/tools/catalog", s.listToolCatalog)
		protected.Post("/api/tools/catalog/{entryID}", s.installCatalogTool)
		protected.Get("/api/tools", s.listTools)
		protected.Post("/api/tools", s.createTool)
		protected.Get("/api/tools/{toolID}", s.getTool)
		protected.Patch("/api/tools/{toolID}", s.updateTool)
		protected.Delete("/api/tools/{toolID}", s.deleteTool)
		protected.Post("/api/tools/{toolID}/actions", s.saveToolAction)
		protected.Post("/api/tools/{toolID}/draft", s.generateToolActions)
		protected.Get("/api/tools/oauth/callback", s.completeToolOAuth)
		protected.Get("/api/tools/{toolID}/oauth", s.getToolOAuth)
		protected.Post("/api/tools/{toolID}/oauth/start", s.startToolOAuth)
		protected.Delete("/api/tools/{toolID}/oauth", s.disconnectToolOAuth)
		protected.Post("/api/tools/{toolID}/discover", s.discoverMCPTools)
		protected.Post("/api/tools/{toolID}/openapi", s.importOpenAPI)
		protected.Put("/api/tools/{toolID}/actions/{actionID}", s.saveToolAction)
		protected.Delete("/api/tools/{toolID}/actions/{actionID}", s.deleteToolAction)
		protected.Post("/api/tools/{toolID}/actions/{actionID}/test", s.testToolAction)
		protected.Get("/api/tools/{toolID}/shares", s.listToolShares)
		protected.Put("/api/tools/{toolID}/shares", s.setToolShares)

		protected.Get("/api/workspaces/{workspaceID}/tools", s.listWorkspaceTools)
		protected.Put("/api/workspaces/{workspaceID}/tools/{toolID}", s.installWorkspaceTool)
		protected.Delete("/api/workspaces/{workspaceID}/tools/{toolID}", s.uninstallWorkspaceTool)
		protected.Put("/api/workspaces/{workspaceID}/tools/{toolID}/auto-call", s.setWorkspaceToolAutoCall)

		protected.Get("/api/workflows", s.listWorkflows)
		protected.Post("/api/workflows", s.createWorkflow)
		protected.Get("/api/workflows/{workflowID}", s.getWorkflow)
		protected.Patch("/api/workflows/{workflowID}", s.updateWorkflow)
		protected.Delete("/api/workflows/{workflowID}", s.deleteWorkflow)
		protected.Put("/api/workflows/{workflowID}/graph", s.saveWorkflowGraph)
		protected.Post("/api/workflows/{workflowID}/run", s.runWorkflow)
		protected.Get("/api/knowledge", s.listKnowledgeBases)
		protected.Post("/api/knowledge", s.createKnowledgeBase)
		protected.Patch("/api/knowledge/{kbID}", s.updateKnowledgeBase)
		protected.Delete("/api/knowledge/{kbID}", s.deleteKnowledgeBase)
		protected.Get("/api/knowledge/{kbID}/documents", s.listKnowledgeDocuments)
		protected.Post("/api/knowledge/{kbID}/documents", s.uploadKnowledgeDocument)
		protected.Delete("/api/knowledge/{kbID}/documents/{documentID}", s.deleteKnowledgeDocument)
		protected.Get("/api/knowledge/{kbID}/documents/{documentID}/detail", s.getKnowledgeDocumentDetail)
		protected.Get("/api/knowledge/{kbID}/documents/{documentID}/original", s.openKnowledgeDocumentOriginal)
		protected.Get("/api/knowledge/{kbID}/documents/{documentID}/events", s.listKnowledgeDocumentEvents)
		protected.Get("/api/knowledge/{kbID}/documents/{documentID}/stream", s.streamKnowledgeDocumentEvents)
		protected.Post("/api/knowledge/{kbID}/publish", s.publishKnowledgeBase)
		protected.Get("/api/knowledge/{kbID}/shares", s.listKnowledgeShares)
		protected.Get("/api/workspaces/directory", s.workspaceDirectory)
		protected.Get("/api/workspaces/{workspaceID}/knowledge", s.listWorkspaceKnowledge)
		protected.Post("/api/workspaces/{workspaceID}/knowledge/retrieve", s.testWorkspaceRetrieval)
		protected.Put("/api/workspaces/{workspaceID}/knowledge/{kbID}", s.mountKnowledge)
		protected.Delete("/api/workspaces/{workspaceID}/knowledge/{kbID}", s.unmountKnowledge)
	})
	return r
}

func (s *Server) BootstrapAdmin(ctx context.Context) error {
	if s.cfg.AdminEmail == "" {
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(s.cfg.AdminPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash admin password: %w", err)
	}
	userID := "usr_" + randomID(18)
	workspaceID := "ws_" + randomID(18)
	personalID := "ws_" + randomID(18)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	err = tx.QueryRow(ctx, `
		INSERT INTO users(id, email, name, password_hash, role)
		VALUES($1, $2, $3, $4, 'admin')
		ON CONFLICT(email) DO UPDATE SET name = EXCLUDED.name, password_hash = EXCLUDED.password_hash, role = 'admin', updated_at = NOW()
		RETURNING id`, userID, s.cfg.AdminEmail, s.cfg.AdminName, string(hash)).Scan(&userID)
	if err != nil {
		return fmt.Errorf("bootstrap admin: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO workspaces(id, name, slug, type) VALUES($1, 'Cosmo Enterprise', 'cosmo-enterprise', 'team') ON CONFLICT(slug) DO NOTHING`, workspaceID)
	if err != nil {
		return err
	}
	err = tx.QueryRow(ctx, `SELECT id FROM workspaces WHERE slug = 'cosmo-enterprise'`).Scan(&workspaceID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO workspace_memberships(user_id, workspace_id, role) VALUES($1, $2, 'owner') ON CONFLICT(user_id, workspace_id) DO UPDATE SET role = 'owner'`, userID, workspaceID)
	if err != nil {
		return err
	}
	personalSlug := "personal-" + userID
	_, err = tx.Exec(ctx, `INSERT INTO workspaces(id, name, slug, type) VALUES($1, 'Không gian cá nhân', $2, 'personal') ON CONFLICT(slug) DO NOTHING`, personalID, personalSlug)
	if err != nil {
		return err
	}
	err = tx.QueryRow(ctx, `SELECT id FROM workspaces WHERE slug = $1`, personalSlug).Scan(&personalID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO workspace_memberships(user_id, workspace_id, role) VALUES($1, $2, 'owner') ON CONFLICT DO NOTHING`, userID, personalID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "model_configured": s.models.Configured()})
}

func (s *Server) authConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"local_signup_enabled": !s.cfg.EntraEnabled(),
		"local_auth_enabled":   !s.cfg.EntraEnabled(),
		"entra_enabled":        s.cfg.EntraEnabled(),
		"model_configured":     s.models.Configured(),
		"model_alias":          s.models.Model(),
	})
}

func (s *Server) signup(w http.ResponseWriter, r *http.Request) {
	if s.cfg.EntraEnabled() {
		writeError(w, http.StatusForbidden, "Đăng ký tài khoản cục bộ đã tắt khi Microsoft Entra ID được cấu hình.")
		return
	}
	var input struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	if len(input.Name) < 2 || !validEmail(input.Email) || !validPassword(input.Password) {
		writeError(w, http.StatusBadRequest, "Tên, email hoặc mật khẩu không hợp lệ. Mật khẩu cần tối thiểu 10 ký tự, gồm chữ và số.")
		return
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	userID := "usr_" + randomID(18)
	workspaceID := "ws_" + randomID(18)
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tạo tài khoản.")
		return
	}
	defer tx.Rollback(r.Context())
	_, err = tx.Exec(r.Context(), `INSERT INTO users(id, email, name, password_hash, role) VALUES($1, $2, $3, $4, 'user')`, userID, input.Email, input.Name, string(hash))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			writeError(w, http.StatusConflict, "Email này đã được sử dụng.")
		} else {
			writeError(w, http.StatusInternalServerError, "Không thể tạo tài khoản.")
		}
		return
	}
	slug := "personal-" + userID
	_, err = tx.Exec(r.Context(), `INSERT INTO workspaces(id, name, slug, type) VALUES($1, $2, $3, 'personal')`, workspaceID, input.Name+"'s workspace", slug)
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO workspace_memberships(user_id, workspace_id, role) VALUES($1, $2, 'owner')`, userID, workspaceID)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE users SET last_workspace_id = $2, updated_at = NOW() WHERE id = $1`, userID, workspaceID)
	}
	if err != nil || tx.Commit(r.Context()) != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tạo workspace cá nhân.")
		return
	}
	user := User{ID: userID, Email: input.Email, Name: input.Name, Role: "user"}
	s.auditAs(r, user, auditEvent{
		Action: "auth.account.signed_up", TargetType: "user", TargetID: user.ID, TargetLabel: user.Email,
		WorkspaceID: workspaceID, Metadata: map[string]string{"provider": "local"},
	})
	s.setSession(w, user, true)
	writeJSON(w, http.StatusCreated, map[string]any{"user": user})
}

func (s *Server) signin(w http.ResponseWriter, r *http.Request) {
	if s.cfg.EntraEnabled() {
		writeError(w, http.StatusForbidden, "Đăng nhập bằng tài khoản cục bộ đã tắt. Vui lòng dùng Microsoft Entra ID.")
		return
	}
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Remember bool   `json:"remember"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	var user User
	var hash *string
	err := s.db.QueryRow(r.Context(), `SELECT id, email, name, role, password_hash, (avatar_image IS NOT NULL) FROM users WHERE email = $1`, input.Email).Scan(&user.ID, &user.Email, &user.Name, &user.Role, &hash, &user.HasAvatar)
	if err != nil || hash == nil || bcrypt.CompareHashAndPassword([]byte(*hash), []byte(input.Password)) != nil {
		// Recorded against the email that was typed, which is all a failed
		// attempt leaves behind and the only way repeated guessing at one
		// account is visible at all.
		reason := "bad_password"
		if err != nil {
			reason = "unknown_account"
		} else if hash == nil {
			reason = "no_local_password"
		}
		s.auditAs(r, User{ID: user.ID, Email: input.Email, Name: user.Name}, auditEvent{
			Action: "auth.session.sign_in_failed", TargetType: "user", TargetID: user.ID, TargetLabel: input.Email,
			Outcome: auditFailure, Metadata: map[string]string{"provider": "local", "reason": reason},
		})
		writeError(w, http.StatusUnauthorized, "Email hoặc mật khẩu không đúng.")
		return
	}
	s.auditAs(r, user, auditEvent{
		Action: "auth.session.signed_in", TargetType: "user", TargetID: user.ID, TargetLabel: user.Email,
		Metadata: map[string]string{"provider": "local", "remember": strconv.FormatBool(input.Remember)},
	})
	s.setSession(w, user, input.Remember)
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) signout(w http.ResponseWriter, r *http.Request) {
	if user := s.sessionActor(r); user.ID != "" {
		s.auditAs(r, user, auditEvent{
			Action: "auth.session.signed_out", TargetType: "user", TargetID: user.ID, TargetLabel: user.Email,
		})
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) entraStart(w http.ResponseWriter, r *http.Request) {
	if s.oauthConfig == nil {
		http.Redirect(w, r, s.cfg.FrontendURL+"/?auth_error=entra_not_configured", http.StatusFound)
		return
	}
	state := randomID(32)
	http.SetCookie(w, &http.Cookie{Name: oauthStateCookie, Value: state, Path: "/api/auth/entra", HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: 600})
	http.Redirect(w, r, s.oauthConfig.AuthCodeURL(state), http.StatusFound)
}

func (s *Server) entraCallback(w http.ResponseWriter, r *http.Request) {
	if s.oauthConfig == nil || s.oidcVerifier == nil {
		http.Redirect(w, r, s.cfg.FrontendURL+"/?auth_error=entra_not_configured", http.StatusFound)
		return
	}
	// Every way this can fail is a sign-in that did not happen, and each is
	// recorded under the same action with the reason attached: a burst of
	// invalid_oauth_state is a different problem from a burst of bad tokens,
	// and neither is visible if the redirect is the only trace.
	refuse := func(reason string, actor User) {
		s.auditAs(r, actor, auditEvent{
			Action: "auth.session.sign_in_failed", TargetType: "user", TargetID: actor.ID, TargetLabel: actor.Email,
			Outcome: auditFailure, Metadata: map[string]string{"provider": "entra", "reason": reason},
		})
		http.Redirect(w, r, s.cfg.FrontendURL+"/?auth_error="+reason, http.StatusFound)
	}
	stateCookie, err := r.Cookie(oauthStateCookie)
	if err != nil || stateCookie.Value == "" || stateCookie.Value != r.URL.Query().Get("state") {
		refuse("invalid_oauth_state", User{})
		return
	}
	token, err := s.oauthConfig.Exchange(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		refuse("token_exchange_failed", User{})
		return
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		refuse("missing_id_token", User{})
		return
	}
	idToken, err := s.oidcVerifier.Verify(r.Context(), rawIDToken)
	if err != nil {
		refuse("invalid_id_token", User{})
		return
	}
	var claims struct {
		Subject           string `json:"sub"`
		Name              string `json:"name"`
		Email             string `json:"email"`
		PreferredUsername string `json:"preferred_username"`
	}
	if idToken.Claims(&claims) != nil {
		refuse("invalid_claims", User{})
		return
	}
	email := strings.ToLower(strings.TrimSpace(claims.Email))
	if email == "" {
		email = strings.ToLower(strings.TrimSpace(claims.PreferredUsername))
	}
	if !validEmail(email) || claims.Subject == "" {
		refuse("email_required", User{Email: email})
		return
	}
	user, err := s.upsertEntraUser(r.Context(), claims.Subject, email, claims.Name)
	if err != nil {
		s.logger.Error("upsert Entra user", "error", err)
		refuse("account_provision_failed", User{Email: email, Name: claims.Name})
		return
	}
	if image, mime, err := fetchEntraAvatar(r.Context(), token.AccessToken); err == nil && len(image) > 0 {
		if _, err := s.db.Exec(r.Context(), `UPDATE users SET avatar_image = $2, avatar_mime = $3, updated_at = NOW() WHERE id = $1`, user.ID, image, mime); err != nil {
			s.logger.Warn("store Entra profile photo", "user_id", user.ID, "error", err)
		} else {
			user.HasAvatar = true
		}
	}
	s.auditAs(r, user, auditEvent{
		Action: "auth.session.signed_in", TargetType: "user", TargetID: user.ID, TargetLabel: user.Email,
		Metadata: map[string]string{"provider": "entra"},
	})
	s.setSession(w, user, true)
	http.Redirect(w, r, s.cfg.FrontendURL+"/chat", http.StatusFound)
}

// fetchEntraAvatar reads the signed-in user's profile photo from Microsoft
// Graph. A missing photo or a tenant without User.Read consent is non-fatal:
// the interface falls back to initials and the login still completes.
func fetchEntraAvatar(ctx context.Context, accessToken string) ([]byte, string, error) {
	if strings.TrimSpace(accessToken) == "" {
		return nil, "", errors.New("missing Microsoft Graph access token")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://graph.microsoft.com/v1.0/me/photo/$value", nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("Microsoft Graph returned %s", resp.Status)
	}
	mime := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	if mime != "image/jpeg" && mime != "image/png" && mime != "image/gif" && mime != "image/webp" {
		return nil, "", fmt.Errorf("unsupported Microsoft Graph avatar type %q", mime)
	}
	const maxAvatarBytes = 1024 * 1024
	image, err := io.ReadAll(io.LimitReader(resp.Body, maxAvatarBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(image) == 0 || len(image) > maxAvatarBytes {
		return nil, "", errors.New("invalid Microsoft Graph avatar size")
	}
	return image, mime, nil
}

func (s *Server) upsertEntraUser(ctx context.Context, subject, email, name string) (User, error) {
	if strings.TrimSpace(name) == "" {
		name = strings.Split(email, "@")[0]
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(ctx)
	var user User
	err = tx.QueryRow(ctx, `SELECT u.id, u.email, u.name, u.role FROM oauth_identities i JOIN users u ON u.id = i.user_id WHERE i.provider = 'entra' AND i.subject = $1`, subject).Scan(&user.ID, &user.Email, &user.Name, &user.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT id, email, name, role FROM users WHERE email = $1`, email).Scan(&user.ID, &user.Email, &user.Name, &user.Role)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		user = User{ID: "usr_" + randomID(18), Email: email, Name: name, Role: "user"}
		if s.cfg.IsPlatformAdmin(email) {
			user.Role = "admin"
		}
		_, err = tx.Exec(ctx, `INSERT INTO users(id, email, name, role) VALUES($1, $2, $3, $4)`, user.ID, user.Email, user.Name, user.Role)
	}
	if err != nil {
		return User{}, err
	}
	if s.cfg.IsPlatformAdmin(user.Email) && user.Role != "admin" {
		if _, err := tx.Exec(ctx, `UPDATE users SET role = 'admin', updated_at = NOW() WHERE id = $1`, user.ID); err != nil {
			return User{}, err
		}
		user.Role = "admin"
	}
	_, err = tx.Exec(ctx, `INSERT INTO oauth_identities(id, user_id, provider, subject) VALUES($1, $2, 'entra', $3) ON CONFLICT(provider, subject) DO UPDATE SET user_id = EXCLUDED.user_id`, "oid_"+randomID(18), user.ID, subject)
	if err != nil {
		return User{}, err
	}
	personalSlug := "personal-" + user.ID
	workspaceID := "ws_" + randomID(18)
	_, err = tx.Exec(ctx, `INSERT INTO workspaces(id, name, slug, type) VALUES($1, $2, $3, 'personal') ON CONFLICT(slug) DO NOTHING`, workspaceID, user.Name+"'s workspace", personalSlug)
	if err != nil {
		return User{}, err
	}
	err = tx.QueryRow(ctx, `SELECT id FROM workspaces WHERE slug = $1`, personalSlug).Scan(&workspaceID)
	if err != nil {
		return User{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO workspace_memberships(user_id, workspace_id, role) VALUES($1, $2, 'owner') ON CONFLICT DO NOTHING`, user.ID, workspaceID)
	if err != nil {
		return User{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE users SET last_workspace_id = COALESCE(last_workspace_id, $2), updated_at = NOW() WHERE id = $1`, user.ID, workspaceID)
	if err != nil {
		return User{}, err
	}
	return user, tx.Commit(ctx)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"user": currentUser(r.Context())})
}

// userAvatar serves the cached Entra profile photo to its own account only.
// Keeping it behind the session avoids exposing employee images as public URLs.
func (s *Server) userAvatar(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	var image []byte
	var mime string
	if err := s.db.QueryRow(r.Context(), `SELECT avatar_image, COALESCE(avatar_mime, '') FROM users WHERE id = $1`, user.ID).Scan(&image, &mime); err != nil || len(image) == 0 {
		writeError(w, http.StatusNotFound, "Tài khoản chưa có ảnh đại diện.")
		return
	}
	if mime != "image/jpeg" && mime != "image/png" && mime != "image/gif" && mime != "image/webp" {
		mime = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(image)
}

func (s *Server) workspaces(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	rows, err := s.db.Query(r.Context(), `
		SELECT w.id, w.name, w.slug, w.type, COALESCE(w.description, ''), COALESCE(w.icon, ''), COALESCE(w.context, ''),
			-- A personal workspace shows the account's own picture, so it has
			-- an icon whenever the reader does.
			(w.icon_image IS NOT NULL OR (w.type = 'personal' AND EXISTS (
				SELECT 1 FROM users u WHERE u.id = m.user_id AND u.avatar_image IS NOT NULL
			))), m.role,
		       COALESCE(c.base_url, ''), COALESCE(c.model, '')
		FROM workspace_memberships m
		JOIN workspaces w ON w.id = m.workspace_id
		LEFT JOIN workspace_llm_configs c ON c.workspace_id = w.id
		WHERE m.user_id = $1
		ORDER BY CASE w.type WHEN 'team' THEN 0 ELSE 1 END, w.name`, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải workspace.")
		return
	}
	defer rows.Close()
	items := []Workspace{}
	for rows.Next() {
		var item Workspace
		var baseURL, model string
		if rows.Scan(&item.ID, &item.Name, &item.Slug, &item.Type, &item.Description, &item.Icon, &item.Context, &item.HasIconImage, &item.Role, &baseURL, &model) == nil {
			if baseURL != "" {
				item.ModelConfigured = true
				item.ModelAlias = model
			} else if s.models.Configured() {
				// Fall back to the process-wide gateway from .env.
				item.ModelConfigured = true
				item.ModelAlias = s.models.Model()
			}
			items = append(items, item)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspaces": items})
}

func (s *Server) selectWorkspace(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	workspaceID := chi.URLParam(r, "workspaceID")
	if !s.hasWorkspace(r.Context(), user.ID, workspaceID) {
		writeError(w, http.StatusForbidden, "Bạn không có quyền truy cập workspace này.")
		return
	}
	if _, err := s.db.Exec(r.Context(), `UPDATE users SET last_workspace_id = $2, updated_at = NOW() WHERE id = $1`, user.ID, workspaceID); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể lưu workspace gần nhất.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listConversations(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	workspaceID := r.URL.Query().Get("workspace_id")
	if !s.hasWorkspace(r.Context(), user.ID, workspaceID) {
		writeError(w, http.StatusForbidden, "Bạn không có quyền truy cập workspace này.")
		return
	}
	// The shared chat surface can target either a workspace model or an Agent.
	// agent_id lets the composer restore the correct target after reload while
	// the message endpoint continues enforcing the Agent's own configuration.
	//
	// Who answered comes along with it: the agent by name, or - for a plain
	// chat, which records no model of its own - the model that wrote the last
	// answer, which is the same fact the composer restores its picker from.
	rows, err := s.db.Query(r.Context(), `
		SELECT c.id, c.workspace_id, COALESCE(c.agent_id, ''), COALESCE(a.name, ''),
		       COALESCE((
		           SELECT m.model FROM messages m
		           WHERE m.conversation_id = c.id AND m.role = 'assistant' AND COALESCE(m.model, '') <> ''
		           ORDER BY m.created_at DESC LIMIT 1
		       ), ''),
		       c.title, c.created_at, c.updated_at
		FROM conversations c
		LEFT JOIN agents a ON a.id = c.agent_id
		WHERE c.user_id = $1 AND c.workspace_id = $2
		ORDER BY c.updated_at DESC LIMIT 100`, user.ID, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải hội thoại.")
		return
	}
	defer rows.Close()
	items := []Conversation{}
	for rows.Next() {
		var item Conversation
		if rows.Scan(&item.ID, &item.WorkspaceID, &item.AgentID, &item.AgentName, &item.Model,
			&item.Title, &item.CreatedAt, &item.UpdatedAt) == nil {
			items = append(items, item)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversations": items})
}

func (s *Server) createConversation(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	var input struct {
		WorkspaceID string `json:"workspace_id"`
		Title       string `json:"title"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if !s.hasWorkspace(r.Context(), user.ID, input.WorkspaceID) {
		writeError(w, http.StatusForbidden, "Bạn không có quyền truy cập workspace này.")
		return
	}
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" {
		input.Title = "Cuộc trò chuyện mới"
	}
	if len([]rune(input.Title)) > 100 {
		input.Title = string([]rune(input.Title)[:100])
	}
	item := Conversation{ID: "cnv_" + randomID(18), WorkspaceID: input.WorkspaceID, Title: input.Title, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	_, err := s.db.Exec(r.Context(), `INSERT INTO conversations(id, user_id, workspace_id, title, created_at, updated_at) VALUES($1, $2, $3, $4, $5, $5)`, item.ID, user.ID, item.WorkspaceID, item.Title, item.CreatedAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tạo hội thoại.")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"conversation": item})
}

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	conversationID := chi.URLParam(r, "conversationID")
	if !s.ownsConversation(r.Context(), user.ID, conversationID) {
		writeError(w, http.StatusNotFound, "Không tìm thấy hội thoại.")
		return
	}
	rows, err := s.db.Query(r.Context(), `
		SELECT m.id, m.conversation_id, m.role, m.content, COALESCE(m.model, ''), m.citations, m.tool_calls, m.usage, m.created_at,
		       COALESCE((
		           SELECT jsonb_agg(jsonb_build_object(
		               'id', a.id, 'name', a.name, 'mime', a.mime,
		               'byte_size', a.byte_size, 'chars', LENGTH(a.text), 'is_truncated', a.is_truncated
		           ) ORDER BY a.created_at)
		           FROM conversation_attachments a WHERE a.message_id = m.id
		       ), '[]'::jsonb)
		FROM messages m LEFT JOIN chat_turns t ON t.conversation_id=m.conversation_id AND (t.user_message_id=m.id OR t.assistant_message_id=m.id)
		WHERE m.conversation_id = $1 ORDER BY COALESCE(t.sequence,0),
		CASE WHEN t.sequence IS NOT NULL AND m.role='assistant' THEN 1 ELSE 0 END,m.created_at,m.id`, conversationID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải nội dung hội thoại.")
		return
	}
	defer rows.Close()
	items := []Message{}
	for rows.Next() {
		var item Message
		var citationsJSON []byte
		var toolCallsJSON []byte
		var attachmentsJSON []byte
		var usageJSON []byte
		if rows.Scan(&item.ID, &item.ConversationID, &item.Role, &item.Content, &item.Model,
			&citationsJSON, &toolCallsJSON, &usageJSON, &item.CreatedAt, &attachmentsJSON) == nil {
			if len(usageJSON) > 0 {
				var counted modelgateway.Usage
				if json.Unmarshal(usageJSON, &counted) == nil && counted.PromptTokens > 0 {
					item.Usage = &counted
				}
			}
			_ = json.Unmarshal(citationsJSON, &item.Citations)
			_ = json.Unmarshal(toolCallsJSON, &item.ToolCalls)
			_ = json.Unmarshal(attachmentsJSON, &item.Attachments)
			items = append(items, item)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": items})
}

func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	execution := currentChatExecution(r.Context())
	user := currentUser(r.Context())
	conversationID := chi.URLParam(r, "conversationID")
	if !s.ownsConversation(r.Context(), user.ID, conversationID) {
		writeError(w, http.StatusNotFound, "Không tìm thấy hội thoại.")
		return
	}
	var conversationWorkspaceID, conversationAgentID, conversationVersionID string
	if err := s.db.QueryRow(r.Context(), `
		SELECT workspace_id, COALESCE(agent_id, ''), COALESCE(agent_version_id, '')
		FROM conversations WHERE id = $1`, conversationID).Scan(
		&conversationWorkspaceID, &conversationAgentID, &conversationVersionID); err != nil {
		writeError(w, http.StatusNotFound, "Không tìm thấy hội thoại.")
		return
	}
	// One pipeline serves both doors. A plain conversation runs on the
	// workspace defaults; one started from an agent runs on that agent's
	// instructions, model and reading list. `agentKnowledge` stays nil for the
	// former, which is what keeps its retrieval workspace-wide.
	models := s.modelsFor(r.Context(), conversationWorkspaceID)
	var agentKnowledge []string
	var knowledgePins map[string]string
	knowledgeMode := "live"
	// Nil while the conversation runs the draft, which means "whatever is
	// attached now"; a published version fills it with what it froze.
	var agentTools []string
	// Which version of each of those tools the published agent was built on.
	var agentToolVersions map[string]string
	var agentRemembers, agentSuggests bool
	var agentPrompt string
	if conversationAgentID != "" {
		// A conversation pinned to a version keeps answering from that frozen
		// snapshot; one with no pin is a draft conversation, which is what the
		// editor's own debug panel wants so a change can be tried before it is
		// published.
		agent, err := s.agentRuntime(r.Context(), user, conversationWorkspaceID, conversationAgentID, conversationVersionID)
		if err != nil {
			s.logger.Error("load agent for conversation", "conversation_id", conversationID, "agent_id", conversationAgentID, "error", err)
			if errors.Is(err, agents.ErrDraftForbidden) || errors.Is(err, agents.ErrNotFound) || errors.Is(err, agents.ErrKnowledgeSnapshotRequired) {
				writeAgentError(w, err)
			} else {
				writeError(w, http.StatusConflict, "Agent không còn khả dụng trong workspace này.")
			}
			return
		}
		if strings.TrimSpace(agent.Model) == "" {
			writeError(w, http.StatusBadRequest, "Agent chưa cấu hình model.")
			return
		}
		models = s.modelsWith(r.Context(), conversationWorkspaceID, agent.SystemPrompt, agent.Model)
		// Kept for the context breakdown: an agent's instructions are part of
		// every prompt it answers, and a reader looking at where the window
		// went should see them named.
		agentPrompt = agent.SystemPrompt
		agentKnowledge = agent.KnowledgeBaseIDs
		if agent.KnowledgeMode == "snapshot" {
			knowledgeMode = "snapshot"
			knowledgePins = agent.KnowledgeSnapshots
		}
		agentTools = agent.ToolIDs
		agentToolVersions = agent.ToolVersions
		agentRemembers = agent.IsMemoryEnabled
		agentSuggests = agent.HasSuggestedQuestions
	}
	if conversationAgentID == "" {
		var pinErr error
		knowledgePins, pinErr = s.workspaceKnowledgePins(r.Context(), conversationWorkspaceID)
		if pinErr != nil {
			writeError(w, 503, "Không thể đọc cấu hình Knowledge Base của workspace.")
			return
		}
		knowledgeMode = "workspace"
	}
	var input struct {
		Content         string `json:"content"`
		Model           string `json:"model"`
		ReasoningEffort string `json:"reasoning_effort"`
		ClientMessageID string `json:"client_message_id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Content = strings.TrimSpace(input.Content)
	if len(input.ClientMessageID) > 100 || strings.TrimSpace(input.ClientMessageID) != input.ClientMessageID {
		writeError(w, http.StatusBadRequest, "ID câu hỏi không hợp lệ.")
		return
	}
	// Legacy callers still work, but must provide a stable key to gain retry
	// protection. The bundled clients send one for every submission.
	if input.ClientMessageID == "" {
		input.ClientMessageID = "legacy_" + randomID(18)
	}
	if input.Content == "" || len([]rune(input.Content)) > 12000 {
		writeError(w, http.StatusBadRequest, "Nội dung câu hỏi không hợp lệ.")
		return
	}
	input.Model = strings.TrimSpace(input.Model)
	if conversationAgentID != "" {
		// Agent conversations are pinned to the model selected in Agent setup;
		// clients cannot override it from the composer or a crafted request.
		input.Model = ""
	}
	if len(input.Model) > 200 {
		writeError(w, http.StatusBadRequest, "Tên model không hợp lệ.")
		return
	}
	// Only the levels the OpenAI-compatible API defines; anything else is
	// dropped rather than forwarded to the gateway.
	switch input.ReasoningEffort {
	case "", "none", "minimal", "low", "medium", "high", "xhigh", "max":
	default:
		writeError(w, http.StatusBadRequest, "Mức suy luận không hợp lệ.")
		return
	}
	options := modelgateway.Options{Model: input.Model, ReasoningEffort: input.ReasoningEffort}
	identity := chatTurnIdentity{ClientMessageID: input.ClientMessageID, RequestHash: chatRequestHash(input.Content, input.Model, input.ReasoningEffort), AssistantID: "msg_" + randomID(18)}
	if execution != nil {
		identity = execution.Identity
	} else {
		if existing, err := lookupChatTurn(r.Context(), s.db, conversationID, identity.ClientMessageID, identity.RequestHash); err != nil {
			s.writeChatTurnError(w, r, conversationID, err)
			return
		} else if existing != nil {
			s.writeChatTurnError(w, r, conversationID, existing)
			return
		}
	}
	if !models.HasGateway() {
		writeError(w, http.StatusServiceUnavailable, "Workspace này chưa cấu hình Model Gateway. Vào Cài đặt để thêm Base URL và API key.")
		return
	}
	if models.ResolveModel(options) == "" {
		writeError(w, http.StatusBadRequest, "Hãy chọn model cho hội thoại hoặc đặt model mặc định trong Cài đặt workspace.")
		return
	}
	// Missing pinned capabilities must fail before the question is accepted.
	caller := s.callerFor(r.Context(), user, conversationWorkspaceID)
	toolCtx := tools.WithCaller(r.Context(), caller)
	set, setErr := s.toolSetFor(toolCtx, conversationAgentID, conversationWorkspaceID, agentTools, agentToolVersions)
	if setErr != nil {
		s.logger.Error("tool set failed", "conversation_id", conversationID, "error", setErr)
		if errors.Is(setErr, tools.ErrPinnedVersionMissing) {
			writeError(w, http.StatusConflict, setErr.Error())
		} else {
			writeError(w, http.StatusServiceUnavailable, "Không thể chuẩn bị tool cho hội thoại.")
		}
		return
	}
	// Two lists, because they answer different questions. The pending ones are
	// what this question claims; every attachment in the conversation is what
	// the model is allowed to read - a file does not stop existing because the
	// question that carried it has been answered.
	attached, attachErr := s.pendingAttachments(r.Context(), conversationID)
	if attachErr != nil {
		s.logger.Error("read attachments", "conversation_id", conversationID, "error", attachErr)
		writeError(w, http.StatusServiceUnavailable, "Không thể đọc tệp đính kèm.")
		return
	}
	var readableIDs []string
	if execution != nil {
		readableIDs = execution.Identity.ReadableIDs
	}
	readable, readableErr := s.conversationAttachmentsFor(r.Context(), conversationID, readableIDs)
	if readableErr != nil {
		s.logger.Error("read conversation attachments", "conversation_id", conversationID, "error", readableErr)
		writeError(w, http.StatusServiceUnavailable, "Không thể đọc tệp của hội thoại.")
		return
	}

	userMessage := Message{ID: "msg_" + randomID(18), ConversationID: conversationID, Role: "user", Content: input.Content, CreatedAt: time.Now()}
	if execution != nil {
		userMessage = execution.Question
	}
	runtimeHash := chatRuntimeHash(models.ExecutionFingerprint(options), agentKnowledge, knowledgeMode, knowledgePins, agentToolVersions, set.definitions, set.actions, set.tools, agentRemembers, agentSuggests)
	if execution != nil && execution.Identity.RuntimeHash != runtimeHash {
		writeError(w, http.StatusConflict, "Cấu hình đã thay đổi trong lúc chờ. Vui lòng gửi một lượt mới.")
		return
	}

	// Chat is the first production path recorded through the common run model.
	// Only identifiers and execution metadata are stored here; the user's text
	// remains in messages and is not duplicated into operational events.
	// The resource of a run is what was executed. A plain conversation runs on
	// the workspace defaults, so the conversation is the resource; one backed
	// by an agent runs that agent, so the agent is. Either way the other id is
	// kept in the input, together with the exact published dependencies.
	runResourceType, runResourceID := "conversation", conversationID
	attachmentIDs := make([]string, 0, len(attached))
	for _, file := range attached {
		attachmentIDs = append(attachmentIDs, file.ID)
	}

	runInput := map[string]any{"message_id": userMessage.ID, "model": models.ResolveModel(options), "conversation_id": conversationID, "knowledge_mode": knowledgeMode, "knowledge_snapshots": knowledgePins}
	if conversationAgentID != "" {
		runResourceType, runResourceID = "agent", conversationAgentID
		runInput["agent_id"] = conversationAgentID
		runInput["tool_versions"] = agentToolVersions
	}
	var history []modelgateway.Message
	var chatRun runs.Run
	var isFirstTurn bool
	var err error
	if execution == nil {
		identity.RuntimeHash = runtimeHash
		for _, file := range readable {
			identity.ReadableIDs = append(identity.ReadableIDs, file.ID)
		}
		if conversationAgentID == "" {
			input.Model = models.ResolveModel(options)
		}
		identity.Payload, _ = json.Marshal(input)
		history, chatRun, isFirstTurn, err = s.acceptChatQuestion(r.Context(), userMessage, runs.NewRun{
			WorkspaceID:     conversationWorkspaceID,
			ActorUserID:     user.ID,
			TriggerType:     "manual",
			ResourceType:    runResourceType,
			ResourceID:      runResourceID,
			ResourceVersion: conversationVersionID,
			Input:           runInput,
			TraceID:         middleware.GetReqID(r.Context()),
		}, attachmentIDs, identity)
		if err != nil {
			s.logger.Error("accept chat question", "conversation_id", conversationID, "error", err)
			s.writeChatTurnError(w, r, conversationID, err)
			return
		}
		s.followChatTurn(w, r, conversationID, identity.ClientMessageID, identity.RequestHash)
		return
	}
	chatRun = execution.Run
	isFirstTurn = execution.First
	history, err = s.executionHistory(r.Context(), execution)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Không thể đọc lịch sử lượt chat.")
		return
	}
	chatRun, runErr := s.runs.Transition(r.Context(), chatRun.ID, runs.Running, nil, "", "")
	if runErr != nil {
		s.logger.Error("start chat run", "conversation_id", conversationID, "error", runErr)
		writeError(w, http.StatusServiceUnavailable, "Chưa thể bắt đầu trả lời câu hỏi.")
		return
	}

	metadataCtx, metadataCancel := context.WithTimeout(r.Context(), 2*time.Second)
	options.ContextWindow = s.contextWindowFor(metadataCtx, conversationWorkspaceID, models.ResolveModel(options))
	metadataCancel()
	r = r.WithContext(modelgateway.WithObserver(r.Context(), s.observeChatModel(chatRun.ID)))
	toolCtx = tools.WithCaller(r.Context(), caller)
	history = withResponsePresentation(history)

	// What the agent remembers about this person joins the conversation before
	// grounding does, so retrieved passages end up closest to the exchange
	// they explain.
	if agentRemembers {
		if memory := s.agents.Memory(r.Context(), conversationAgentID, user.ID); memory != "" {
			history = append([]modelgateway.Message{{Role: "system", Content: agents.MemoryHeader + memory}}, history...)
		}
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "Streaming không được hỗ trợ.")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	assistantID := identity.AssistantID

	// What the turn needs, decided before it is done. Searching the documents
	// for a question that has nothing to do with them produced an answer with
	// a citation to a procedure it never read, which is worse than no citation.
	writeSSE(w, "status", map[string]string{"stage": "planning", "message": "Đang đọc câu hỏi…"})
	flusher.Flush()
	planCtx, cancelPlan := context.WithTimeout(r.Context(), 15*time.Second)
	attachedNames := make([]string, 0, len(readable))
	for _, file := range readable {
		attachedNames = append(attachedNames, file.Name)
	}
	topics, topicsErr := s.knowledgeTopicsFor(planCtx, conversationWorkspaceID, agentKnowledge)
	plan := fallbackTurnPlan(input.Content, "chưa đọc được danh sách Knowledge Base; thử tra cứu lại")
	if topicsErr == nil {
		plan = s.planTurn(planCtx, models, options, input.Content, history, topics, attachedNames)
	}
	cancelPlan()
	writeSSE(w, "status", map[string]string{
		"stage":   "planned",
		"message": "Đã đọc câu hỏi",
		"detail":  plan.Reason,
	})
	flusher.Flush()
	if runErr == nil {
		var planStep runs.Step
		planStep, runErr = s.runs.CreateStep(r.Context(), runs.NewStep{RunID: chatRun.ID, NodeID: "plan", Type: "plan", Name: "Turn plan", TimeoutMS: 15000})
		if runErr == nil {
			planStep, runErr = s.runs.TransitionStep(r.Context(), planStep.ID, runs.Running, nil, "", "", "")
		}
		if runErr == nil {
			_, runErr = s.runs.TransitionStep(r.Context(), planStep.ID, runs.Succeeded,
				map[string]any{"needs_knowledge": plan.NeedsKnowledge, "query_rewritten": plan.QueryRewritten, "reason": plan.Reason}, "", "", "")
		}
	}

	var citations []Citation
	// What the turn called, for the transcript to keep beside the answer.
	var toolCalls []ToolCall
	// What each part of the prompt came to, in characters. The gateway counts
	// the tokens; this is what says which of them were the knowledge base and
	// which were the file somebody attached.
	contextParts := map[string]int{}
	var passages []knowledgePassage
	var evidenceAnswer string
	var partialKnowledge bool

	// Retrieval happens before model generation, but it must not look like the
	// reply is stuck. Sources deliberately remain server-side until generation
	// finishes: showing evidence before the answer makes the evidence look like
	// the answer and creates a large, shifting block above the streamed text.
	if plan.NeedsKnowledge {
		writeSSE(w, "status", map[string]string{"stage": "retrieving", "message": "Đang tìm trong Knowledge Base…"})
		flusher.Flush()
		var retrievalStep runs.Step
		if runErr == nil {
			retrievalStep, runErr = s.runs.CreateStep(r.Context(), runs.NewStep{RunID: chatRun.ID, NodeID: "retrieval", Type: "retrieval", Name: "Knowledge retrieval", TimeoutMS: s.knowledgeRetrievalPolicy().timeout.Milliseconds()})
			if runErr == nil {
				retrievalStep, runErr = s.runs.TransitionStep(r.Context(), retrievalStep.ID, runs.Running, nil, "", "", "")
			}
		}

		// The log records which answer this search fed, so a relevance floor
		// can later be chosen from what the answers actually used.
		retrieval, retrievalErr := s.retrieveKnowledgeSelection(withRetrievalTurn(r.Context(), assistantID), conversationWorkspaceID, plan.SearchQuery, agentKnowledge, knowledgePins, knowledgeMode != "workspace")
		passages = retrieval.Passages
		incomplete := retrievalErr != nil || retrieval.incomplete()
		partialKnowledge = incomplete && len(passages) > 0
		evidenceAnswer = missingKnowledgeAnswer(true, len(passages), incomplete)
		if retrievalErr != nil {
			s.logger.Error("knowledge retrieval failed", "conversation_id", conversationID, "error", retrievalErr)
		}
		if incomplete && !partialKnowledge {
			writeSSE(w, "status", map[string]string{"stage": "retrieval_failed", "message": "Không thể truy xuất Knowledge Base."})
			flusher.Flush()
		}
		if partialKnowledge {
			writeSSE(w, "status", map[string]string{"stage": "retrieval_partial", "message": "Chỉ truy cập được một phần nguồn Knowledge Base.", "detail": describePassages(passages)})
			flusher.Flush()
		}
		if !incomplete {
			writeSSE(w, "status", map[string]string{
				"stage":   "retrieved",
				"message": "Đã tra Knowledge Base",
				"detail":  describePassages(passages),
			})
			flusher.Flush()
		}
		if runErr == nil {
			output := map[string]any{"passage_count": len(passages), "sources": retrieval.Sources, "partial": partialKnowledge}
			if incomplete && !partialKnowledge {
				_, runErr = s.runs.TransitionStep(r.Context(), retrievalStep.ID, runs.Failed, output, "", "retrieval_failed", "Knowledge retrieval incomplete")
			} else {
				_, runErr = s.runs.TransitionStep(r.Context(), retrievalStep.ID, runs.Succeeded, output, "", "", "")
			}
		}
	}
	if len(passages) > 0 {
		// Grounding goes in front of the conversation so the passages frame
		// the whole exchange rather than arriving as the latest turn.
		grounding := buildGroundingPrompt(passages)
		if partialKnowledge {
			grounding = "Some knowledge sources could not be searched. Answer only the parts supported by the available passages; identify any unverified parts. Do not claim a complete cross-source comparison or treat an unavailable source as having no matching information.\n" + grounding
		}
		contextParts["knowledge"] = len([]rune(grounding))
		history = append([]modelgateway.Message{{Role: "system", Content: grounding}}, history...)
		for index, passage := range passages {
			citations = append(citations, Citation{
				Index:      index + 1,
				KBID:       passage.KBID,
				DocumentID: passage.DocumentID,
				Title:      passage.Title,
				Source:     passage.Source,
				Section:    passage.Section,
				Page:       passage.Page,
			})
		}
	}
	// The attached files go in front of the exchange like grounding does, and
	// are labelled as the reader's own so an answer does not present them as
	// indexed workspace knowledge.
	if block := attachmentPrompt(readable); block != "" {
		contextParts["files"] = len([]rune(block))
		history = append([]modelgateway.Message{{Role: "system", Content: block}}, history...)
	}

	// Who is asking and where. The prompt gets it as a block below; the tools
	// get it on the context, where the Profile built-in reads it - a model
	// that could name whose profile it wanted would be reading other people's.
	if block := conversationContext(caller, s.workspaceContext(r.Context(), conversationWorkspaceID)); block != "" {
		contextParts["context"] = len([]rune(block))
		history = append([]modelgateway.Message{{Role: "system", Content: block}}, history...)
	}

	// The answer is accumulated across both phases: a tool round can narrate
	// before it calls, and that narration is part of the same answer.
	var assistant strings.Builder
	var decidedAnswer string
	if partialKnowledge {
		assistant.WriteString(partialKnowledgeNotice)
		writeSSE(w, "delta", map[string]string{"content": partialKnowledgeNotice})
		flusher.Flush()
	}
	// What this turn may call. An agent brings what it was wired to; a plain
	// chat brings what the workspace installed and switched on - nothing at
	// all until somebody does both, so the ordinary chat pays for none of this.
	if !set.isEmpty() && evidenceAnswer == "" {
		writeSSE(w, "status", map[string]string{
			"stage":   "tools_ready",
			"message": "Đang tìm tool",
			"detail":  describeToolSet(set),
		})
		flusher.Flush()
		if described, marshalErr := json.Marshal(set.definitions); marshalErr == nil {
			contextParts["tools"] = len([]rune(string(described)))
		}
		history, toolCalls, decidedAnswer, err = s.runToolRounds(toolCtx, w, flusher, set, history, options, models, chatRun.ID, &assistant)
		if err != nil {
			writeSSE(w, "error", map[string]string{"message": err.Error()})
			flusher.Flush()
			_, _ = s.runs.Transition(context.Background(), chatRun.ID, runs.Failed, nil, "context_budget", err.Error())
			return
		}
	}

	writeSSE(w, "status", map[string]string{"stage": "writing", "message": "Đang soạn câu trả lời…"})
	flusher.Flush()
	writeSSE(w, "meta", map[string]any{"user_message": userMessage, "assistant_message_id": assistantID, "model": models.ResolveModel(options), "run_id": chatRun.ID})
	flusher.Flush()
	var generationStep runs.Step
	if runErr == nil {
		stepType, stepName := "model", "Answer generation"
		if decidedAnswer != "" {
			stepType, stepName = "output", "Answer from tool decision"
		}
		if evidenceAnswer != "" {
			stepType, stepName = "policy", "Insufficient knowledge evidence"
		}
		generationStep, runErr = s.runs.CreateStep(r.Context(), runs.NewStep{RunID: chatRun.ID, NodeID: "generation", Type: stepType, Name: stepName, TimeoutMS: s.cfg.LLMRequestTimeout.Milliseconds()})
		if runErr == nil {
			generationStep, runErr = s.runs.TransitionStep(r.Context(), generationStep.ID, runs.Running, nil, "", "", "")
		}
	}

	// The counts come back with the stream, so the reader can be told what the
	// turn cost rather than shown an estimate of it.
	onDelta := func(delta string) error {
		if err := r.Context().Err(); err != nil {
			return err
		}
		assistant.WriteString(delta)
		writeSSE(w, "delta", map[string]string{"content": delta})
		flusher.Flush()
		return nil
	}
	var usage modelgateway.Usage
	if evidenceAnswer != "" {
		err = onDelta(evidenceAnswer)
	} else if decidedAnswer != "" {
		if assistant.Len() > 0 {
			decidedAnswer = "\n\n" + decidedAnswer
		}
		err = onDelta(decidedAnswer)
	} else {
		usage, err = models.StreamWithUsage(modelgateway.WithPhase(r.Context(), "generation"), history, options, onDelta)
	}
	if err != nil {
		s.logger.Error("model stream failed", "conversation_id", conversationID, "error", err)
		message := "Model Gateway hiện không phản hồi. Vui lòng thử lại."
		if errors.Is(err, modelgateway.ErrContextBudget) || errors.Is(err, modelgateway.ErrToolHistory) {
			message = err.Error()
		}
		writeSSE(w, "error", map[string]string{"message": message})
		flusher.Flush()
		if runErr == nil {
			_, _ = s.runs.TransitionStep(context.Background(), generationStep.ID, runs.Failed, nil, "", "model_gateway", err.Error())
			_, _ = s.runs.Transition(context.Background(), chatRun.ID, runs.Failed, nil, "model_gateway", err.Error())
		}
		return
	}
	// What the turn cost, and what it had to fit in. Sent before the answer is
	// stored so a reader watching the composer sees it settle with the answer
	// rather than a beat later.
	if usage.PromptTokens > 0 {
		usage.ContextWindow = options.ContextWindow
		// The instructions the model runs under, whether an agent's or the
		// workspace's, and then whatever the blocks above did not account for:
		// the exchange itself.
		instructions := s.cfg.LLMSystemPrompt
		if conversationAgentID != "" && agentPrompt != "" {
			instructions = agentPrompt
		}
		contextParts["instructions"] = len([]rune(instructions))
		total := 0
		for _, message := range history {
			total += len([]rune(message.Content))
		}
		for name, size := range contextParts {
			if name != "instructions" && name != "tools" {
				total -= size
			}
		}
		if total > 0 {
			contextParts["messages"] = total
		}
		usage.Parts = contextParts
		writeSSE(w, "usage", usage)
		flusher.Flush()
	}

	citations = citationsUsedByAnswer(assistant.String(), citations)
	assistantMessage := Message{ID: assistantID, ConversationID: conversationID, Role: "assistant", Content: assistant.String(), CreatedAt: time.Now(), Model: models.ResolveModel(options), Citations: citations, ToolCalls: toolCalls}
	citationsJSON, _ := json.Marshal(citations)
	toolCallsJSON, _ := json.Marshal(toolCalls)
	var usageJSON []byte
	if usage.PromptTokens > 0 {
		usageJSON, _ = json.Marshal(usage)
		assistantMessage.Usage = &usage
	}
	if err = s.checkChatExecution(r.Context()); err != nil {
		return
	}
	tag, saveErr := s.db.Exec(r.Context(), `WITH active AS (
		SELECT t.conversation_id FROM chat_turns t JOIN runs r ON r.id=t.run_id WHERE t.conversation_id=$2 AND t.client_message_id=$10 AND t.lease_owner=$11 AND t.status='executing' AND t.lease_expires_at>NOW() AND r.status='running' FOR UPDATE OF t,r
	), saved AS (
		INSERT INTO messages(id, conversation_id, role, content, model, citations, tool_calls, usage, created_at) SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9 FROM active RETURNING id
	) UPDATE chat_turns SET status='succeeded',finished_at=NOW()
	WHERE conversation_id=$2 AND client_message_id=$10 AND assistant_message_id=(SELECT id FROM saved)`, assistantMessage.ID, conversationID, assistantMessage.Role, assistantMessage.Content, assistantMessage.Model, citationsJSON, toolCallsJSON, usageJSON, assistantMessage.CreatedAt, identity.ClientMessageID, execution.Owner)
	err = saveErr
	if err == nil && tag.RowsAffected() != 1 {
		err = fmt.Errorf("chat execution no longer owns completion")
	}
	if err != nil {
		if runErr == nil {
			_, _ = s.runs.TransitionStep(context.Background(), generationStep.ID, runs.Failed, nil, "", "history_write", err.Error())
			_, _ = s.runs.Transition(context.Background(), chatRun.ID, runs.Failed, nil, "history_write", err.Error())
		}
		writeSSE(w, "error", map[string]string{"message": "Câu trả lời đã hoàn tất nhưng chưa thể lưu lịch sử."})
		flusher.Flush()
		return
	}
	if runErr == nil {
		_, runErr = s.runs.TransitionStep(r.Context(), generationStep.ID, runs.Succeeded, map[string]any{"message_id": assistantMessage.ID, "citation_count": len(citations), "model": models.ResolveModel(options), "answer_chars": len([]rune(assistantMessage.Content))}, "", "", "")
		if runErr == nil {
			_, runErr = s.runs.Transition(r.Context(), chatRun.ID, runs.Succeeded, map[string]any{"message_id": assistantMessage.ID}, "", "")
		}
		if runErr != nil {
			s.logger.Warn("chat run finalization failed", "run_id", chatRun.ID, "error", runErr)
		}
	}
	// A conversation is named once, from its first exchange, for the same
	// reason and with the same tolerance for failure as the suggestions below:
	// it happens after the answer is saved, so losing it costs nothing.
	if isFirstTurn && evidenceAnswer == "" {
		if title := s.agents.SuggestTitle(modelgateway.WithPhase(r.Context(), "title"), input.Content, assistantMessage.Content, models, options); title != "" {
			if _, titleErr := s.db.Exec(r.Context(), `UPDATE conversations SET title = $2 WHERE id = $1`, conversationID, title); titleErr == nil {
				writeSSE(w, "title", map[string]string{"title": title})
				flusher.Flush()
			}
		}
	}

	// Suggestions come after the answer is saved, so a failure here can never
	// cost the reader the reply itself.
	// An agent decides for itself whether it offers follow-ups; a plain chat
	// always does. Without them the model writes its offers into the prose -
	// "tôi có thể vẽ tiếp: biểu đồ theo ban, biểu đồ theo tư vấn viên" - and a
	// reader has to retype the one they want.
	if evidenceAnswer == "" && (agentSuggests || conversationAgentID == "") {
		if followUps := s.agents.SuggestFollowUps(modelgateway.WithPhase(r.Context(), "suggestions"), input.Content, assistantMessage.Content, models, options); len(followUps) > 0 {
			writeSSE(w, "suggestions", map[string]any{"questions": followUps})
			flusher.Flush()
		}
	}
	writeSSE(w, "done", map[string]any{"message": assistantMessage})
	flusher.Flush()

	if agentRemembers && evidenceAnswer == "" {
		go s.agents.RememberExchange(conversationAgentID, user.ID, input.Content, assistantMessage.Content, models, options)
	}
}

var inlineCitationPattern = regexp.MustCompile(`\[([\d\s,]+)]`)

// citationsUsedByAnswer keeps the evidence list aligned with the answer the
// reader actually received. Retrieval may collect many candidates, but only
// passages cited inline are evidence for the generated claims.
//
// An answer that cites nothing gets no sources. There used to be a fallback of
// three candidates here, resting on the premise that retrieval runs only when
// the question is about the documents - so anything retrieved must be
// relevant. The premise does not hold. The planner sends unrelated questions to
// the knowledge base often enough that readers noticed documents listed under
// answers which plainly ignored them, and the fallback is what turned a
// planner's mistake into a visible false claim about where an answer came from.
//
// The model already reports what it used, by marking it. Nothing is a report
// too, and it is the honest one to pass on.
func citationsUsedByAnswer(answer string, candidates []Citation) []Citation {
	if len(candidates) == 0 {
		return []Citation{}
	}
	byIndex := make(map[int]Citation, len(candidates))
	for _, citation := range candidates {
		byIndex[citation.Index] = citation
	}
	used := make([]Citation, 0, len(candidates))
	seen := map[int]bool{}
	for _, match := range inlineCitationPattern.FindAllStringSubmatch(answer, -1) {
		// One marker may name several passages: [2] and [1, 2] are both things
		// a model writes, and the second used to match nothing at all.
		for _, field := range strings.Split(match[1], ",") {
			index, err := strconv.Atoi(strings.TrimSpace(field))
			if err != nil || seen[index] {
				continue
			}
			if citation, ok := byIndex[index]; ok {
				used = append(used, citation)
				seen[index] = true
			}
		}
	}
	return used
}

func (s *Server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "Bạn cần đăng nhập.")
			return
		}
		token, err := jwt.Parse(cookie.Value, func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, fmt.Errorf("unexpected signing method")
			}
			return []byte(s.cfg.SessionSecret), nil
		}, jwt.WithIssuer("cosmo"), jwt.WithExpirationRequired())
		if err != nil || !token.Valid {
			writeError(w, http.StatusUnauthorized, "Phiên đăng nhập đã hết hạn.")
			return
		}
		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			writeError(w, http.StatusUnauthorized, "Phiên đăng nhập không hợp lệ.")
			return
		}
		userID, _ := claims.GetSubject()
		var user User
		if s.db.QueryRow(r.Context(), `SELECT id, email, name, role, COALESCE(last_workspace_id, ''), (avatar_image IS NOT NULL) FROM users WHERE id = $1`, userID).Scan(&user.ID, &user.Email, &user.Name, &user.Role, &user.LastWorkspaceID, &user.HasAvatar) != nil {
			writeError(w, http.StatusUnauthorized, "Tài khoản không còn hoạt động.")
			return
		}
		if s.cfg.IsPlatformAdmin(user.Email) && user.Role != "admin" {
			if _, err := s.db.Exec(r.Context(), `UPDATE users SET role = 'admin', updated_at = NOW() WHERE id = $1`, user.ID); err == nil {
				user.Role = "admin"
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey, user)))
	})
}

// sessionActor reads who a request claims to be, without refusing it when the
// answer is nobody. Signing out is not behind requireUser - a cookie that has
// already expired still has to be cleared - so this is how the sign-out record
// gets a name rather than being written against an empty actor.
func (s *Server) sessionActor(r *http.Request) User {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		return User{}
	}
	token, err := jwt.Parse(cookie.Value, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(s.cfg.SessionSecret), nil
	}, jwt.WithIssuer("cosmo"))
	if err != nil || !token.Valid {
		return User{}
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return User{}
	}
	userID, _ := claims.GetSubject()
	email, _ := claims["email"].(string)
	return User{ID: userID, Email: email}
}

func (s *Server) setSession(w http.ResponseWriter, user User, persistent bool) {
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": user.ID, "email": user.Email, "role": user.Role,
		"iss": "cosmo", "iat": now.Unix(), "exp": now.Add(s.cfg.SessionTTL).Unix(),
	})
	signed, _ := token.SignedString([]byte(s.cfg.SessionSecret))
	maxAge := 0
	if persistent {
		maxAge = int(s.cfg.SessionTTL.Seconds())
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: signed, Path: "/", HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && origin == s.cfg.FrontendURL {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Last-Event-ID")
			w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		}
		if r.Method == http.MethodOptions {
			if origin != s.cfg.FrontendURL {
				writeError(w, http.StatusForbidden, "Origin không được phép.")
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) hasWorkspace(ctx context.Context, userID, workspaceID string) bool {
	var exists bool
	return s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workspace_memberships WHERE user_id = $1 AND workspace_id = $2)`, userID, workspaceID).Scan(&exists) == nil && exists
}

func (s *Server) ownsConversation(ctx context.Context, userID, conversationID string) bool {
	var exists bool
	return s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM conversations c JOIN workspace_memberships m ON m.workspace_id = c.workspace_id AND m.user_id = $1 WHERE c.id = $2 AND c.user_id = $1)`, userID, conversationID).Scan(&exists) == nil && exists
}

func currentUser(ctx context.Context) User {
	user, _ := ctx.Value(userContextKey).(User)
	return user
}

func validEmail(value string) bool {
	address, err := mail.ParseAddress(value)
	return err == nil && strings.EqualFold(address.Address, value) && len(value) <= 254
}

func validPassword(value string) bool {
	if len([]rune(value)) < 10 || len([]rune(value)) > 128 {
		return false
	}
	var letter, digit bool
	for _, r := range value {
		letter = letter || unicode.IsLetter(r)
		digit = digit || unicode.IsDigit(r)
	}
	return letter && digit
}

func randomID(bytesLength int) string {
	data := make([]byte, bytesLength)
	if _, err := rand.Read(data); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "Dữ liệu gửi lên không hợp lệ.")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"message": message}})
}

func writeSSE(w http.ResponseWriter, event string, value any) {
	data, _ := json.Marshal(value)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
}

// Recheck access on every turn, including conversations created before a
// visibility change or an editor's workspace role being revoked.
func (s *Server) agentRuntime(ctx context.Context, user User, workspaceID, agentID, versionID string) (agents.Runtime, error) {
	if !s.hasWorkspace(ctx, user.ID, workspaceID) {
		return agents.Runtime{}, agents.ErrNotFound
	}
	agent, err := s.agents.Get(ctx, agentID, user.ID, workspaceID)
	if err != nil {
		return agents.Runtime{}, err
	}
	if versionID != "" {
		runtime, err := s.agents.RuntimeForVersion(ctx, agentID, versionID)
		if err != nil {
			return runtime, err
		}
		if runtime.KnowledgeMode == "snapshot" {
			for _, kbID := range runtime.KnowledgeBaseIDs {
				var allowed bool
				err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM knowledge_bases kb
					JOIN knowledge_mounts m ON m.kb_id=kb.id AND m.target_type='workspace' AND m.target_id=$1
					JOIN knowledge_snapshots ks ON ks.kb_id=kb.id AND ks.id=$3
					WHERE kb.id=$2 AND (`+workspaceRetrievableKnowledgeSQL+`))`, workspaceID, kbID, runtime.KnowledgeSnapshots[kbID]).Scan(&allowed)
				if err != nil || !allowed {
					return agents.Runtime{}, agents.ErrKnowledgeSnapshotRequired
				}
			}
		}
		return runtime, nil
	}
	if agent.OwnerUserID != user.ID && !s.isWorkspaceAdmin(ctx, user, workspaceID) {
		return agents.Runtime{}, agents.ErrDraftForbidden
	}
	return s.agents.Runtime(ctx, agentID)
}
