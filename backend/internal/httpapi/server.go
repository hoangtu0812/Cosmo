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

	"cosmo/backend/internal/config"
	"cosmo/backend/internal/knowledge"
	"cosmo/backend/internal/modelgateway"
	"cosmo/backend/internal/runs"
	"cosmo/backend/internal/secrets"

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
	HasIconImage bool   `json:"has_icon_image"`
	Role         string `json:"role"`
	// Model status is per workspace now, so the chat surface can tell the user
	// which workspace still needs a gateway without a second request.
	ModelConfigured bool   `json:"model_configured"`
	ModelAlias      string `json:"model_alias,omitempty"`
}

type Conversation struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Title       string    `json:"title"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Message struct {
	ID             string     `json:"id"`
	ConversationID string     `json:"conversation_id"`
	Role           string     `json:"role"`
	Content        string     `json:"content"`
	Model          string     `json:"model,omitempty"`
	Citations      []Citation `json:"citations,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
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
		protected.Get("/api/admin/system", s.systemStatus)
		protected.Put("/api/admin/system", s.updateSystemSettings)
		protected.Post("/api/admin/system/models", s.listSystemGatewayModels)
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

		protected.Post("/api/workspaces", s.createWorkspace)
		protected.Patch("/api/workspaces/{workspaceID}", s.updateWorkspace)
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
		protected.Get("/api/agents/{agentID}/conversations", s.listAgentConversations)
		protected.Post("/api/agents/{agentID}/conversations", s.startAgentConversation)
		protected.Get("/api/agents/{agentID}/avatar", s.agentAvatar)
		protected.Put("/api/agents/{agentID}/avatar", s.uploadAgentAvatar)
		protected.Delete("/api/agents/{agentID}/avatar", s.deleteAgentAvatar)
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
	s.writeAudit(r.Context(), user.ID, "auth.local.signed_up", "user", user.ID, map[string]string{"provider": "local"})
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
		writeError(w, http.StatusUnauthorized, "Email hoặc mật khẩu không đúng.")
		return
	}
	s.writeAudit(r.Context(), user.ID, "auth.local.signed_in", "user", user.ID, map[string]string{"provider": "local"})
	s.setSession(w, user, input.Remember)
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) signout(w http.ResponseWriter, _ *http.Request) {
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
	stateCookie, err := r.Cookie(oauthStateCookie)
	if err != nil || stateCookie.Value == "" || stateCookie.Value != r.URL.Query().Get("state") {
		http.Redirect(w, r, s.cfg.FrontendURL+"/?auth_error=invalid_oauth_state", http.StatusFound)
		return
	}
	token, err := s.oauthConfig.Exchange(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		http.Redirect(w, r, s.cfg.FrontendURL+"/?auth_error=token_exchange_failed", http.StatusFound)
		return
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		http.Redirect(w, r, s.cfg.FrontendURL+"/?auth_error=missing_id_token", http.StatusFound)
		return
	}
	idToken, err := s.oidcVerifier.Verify(r.Context(), rawIDToken)
	if err != nil {
		http.Redirect(w, r, s.cfg.FrontendURL+"/?auth_error=invalid_id_token", http.StatusFound)
		return
	}
	var claims struct {
		Subject           string `json:"sub"`
		Name              string `json:"name"`
		Email             string `json:"email"`
		PreferredUsername string `json:"preferred_username"`
	}
	if idToken.Claims(&claims) != nil {
		http.Redirect(w, r, s.cfg.FrontendURL+"/?auth_error=invalid_claims", http.StatusFound)
		return
	}
	email := strings.ToLower(strings.TrimSpace(claims.Email))
	if email == "" {
		email = strings.ToLower(strings.TrimSpace(claims.PreferredUsername))
	}
	if !validEmail(email) || claims.Subject == "" {
		http.Redirect(w, r, s.cfg.FrontendURL+"/?auth_error=email_required", http.StatusFound)
		return
	}
	user, err := s.upsertEntraUser(r.Context(), claims.Subject, email, claims.Name)
	if err != nil {
		s.logger.Error("upsert Entra user", "error", err)
		http.Redirect(w, r, s.cfg.FrontendURL+"/?auth_error=account_provision_failed", http.StatusFound)
		return
	}
	if image, mime, err := fetchEntraAvatar(r.Context(), token.AccessToken); err == nil && len(image) > 0 {
		if _, err := s.db.Exec(r.Context(), `UPDATE users SET avatar_image = $2, avatar_mime = $3, updated_at = NOW() WHERE id = $1`, user.ID, image, mime); err != nil {
			s.logger.Warn("store Entra profile photo", "user_id", user.ID, "error", err)
		} else {
			user.HasAvatar = true
		}
	}
	s.writeAudit(r.Context(), user.ID, "auth.entra.signed_in", "user", user.ID, map[string]string{"provider": "entra"})
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
		SELECT w.id, w.name, w.slug, w.type, COALESCE(w.description, ''), COALESCE(w.icon, ''), (w.icon_image IS NOT NULL), m.role,
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
		if rows.Scan(&item.ID, &item.Name, &item.Slug, &item.Type, &item.Description, &item.Icon, &item.HasIconImage, &item.Role, &baseURL, &model) == nil {
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
	// Chat is the general surface: its own conversations and knowledge ones.
	// An agent keeps its conversations in its own place, so they are excluded
	// here rather than filtered in the sidebar - the two lists never mix.
	rows, err := s.db.Query(r.Context(), `SELECT id, workspace_id, title, created_at, updated_at FROM conversations WHERE user_id = $1 AND workspace_id = $2 AND agent_id IS NULL ORDER BY updated_at DESC LIMIT 100`, user.ID, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải hội thoại.")
		return
	}
	defer rows.Close()
	items := []Conversation{}
	for rows.Next() {
		var item Conversation
		if rows.Scan(&item.ID, &item.WorkspaceID, &item.Title, &item.CreatedAt, &item.UpdatedAt) == nil {
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
	rows, err := s.db.Query(r.Context(), `SELECT id, conversation_id, role, content, COALESCE(model, ''), citations, created_at FROM messages WHERE conversation_id = $1 ORDER BY created_at ASC`, conversationID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải nội dung hội thoại.")
		return
	}
	defer rows.Close()
	items := []Message{}
	for rows.Next() {
		var item Message
		var citationsJSON []byte
		if rows.Scan(&item.ID, &item.ConversationID, &item.Role, &item.Content, &item.Model, &citationsJSON, &item.CreatedAt) == nil {
			_ = json.Unmarshal(citationsJSON, &item.Citations)
			items = append(items, item)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": items})
}

func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	conversationID := chi.URLParam(r, "conversationID")
	if !s.ownsConversation(r.Context(), user.ID, conversationID) {
		writeError(w, http.StatusNotFound, "Không tìm thấy hội thoại.")
		return
	}
	var conversationWorkspaceID, conversationAgentID string
	if err := s.db.QueryRow(r.Context(), `SELECT workspace_id, COALESCE(agent_id, '') FROM conversations WHERE id = $1`, conversationID).Scan(&conversationWorkspaceID, &conversationAgentID); err != nil {
		writeError(w, http.StatusNotFound, "Không tìm thấy hội thoại.")
		return
	}
	// One pipeline serves both doors. A plain conversation runs on the
	// workspace defaults; one started from an agent runs on that agent's
	// instructions, model and reading list. `agentKnowledge` stays nil for the
	// former, which is what keeps its retrieval workspace-wide.
	models := s.modelsFor(r.Context(), conversationWorkspaceID)
	var agentKnowledge []string
	var agentRemembers, agentSuggests bool
	if conversationAgentID != "" {
		if agent, err := s.loadAgentForRun(r.Context(), conversationAgentID); err == nil {
			models = s.modelsWith(r.Context(), conversationWorkspaceID, agent.SystemPrompt, agent.Model)
			agentKnowledge = agent.KnowledgeBaseIDs
			agentRemembers = agent.IsMemoryEnabled
			agentSuggests = agent.HasSuggestedQuestions
		} else {
			s.logger.Error("load agent for conversation", "conversation_id", conversationID, "agent_id", conversationAgentID, "error", err)
		}
	}
	var input struct {
		Content         string `json:"content"`
		Model           string `json:"model"`
		ReasoningEffort string `json:"reasoning_effort"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Content = strings.TrimSpace(input.Content)
	if input.Content == "" || len([]rune(input.Content)) > 12000 {
		writeError(w, http.StatusBadRequest, "Nội dung câu hỏi không hợp lệ.")
		return
	}
	input.Model = strings.TrimSpace(input.Model)
	if len(input.Model) > 200 {
		writeError(w, http.StatusBadRequest, "Tên model không hợp lệ.")
		return
	}
	// Only the levels the OpenAI-compatible API defines; anything else is
	// dropped rather than forwarded to the gateway.
	switch input.ReasoningEffort {
	case "", "minimal", "low", "medium", "high":
	default:
		writeError(w, http.StatusBadRequest, "Mức suy luận không hợp lệ.")
		return
	}
	options := modelgateway.Options{Model: input.Model, ReasoningEffort: input.ReasoningEffort}
	if !models.HasGateway() {
		writeError(w, http.StatusServiceUnavailable, "Workspace này chưa cấu hình Model Gateway. Vào Cài đặt để thêm Base URL và API key.")
		return
	}
	if models.ResolveModel(options) == "" {
		writeError(w, http.StatusBadRequest, "Hãy chọn model cho hội thoại hoặc đặt model mặc định trong Cài đặt workspace.")
		return
	}
	userMessage := Message{ID: "msg_" + randomID(18), ConversationID: conversationID, Role: "user", Content: input.Content, CreatedAt: time.Now()}
	_, err := s.db.Exec(r.Context(), `INSERT INTO messages(id, conversation_id, role, content, created_at) VALUES($1, $2, $3, $4, $5)`, userMessage.ID, conversationID, userMessage.Role, userMessage.Content, userMessage.CreatedAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể lưu câu hỏi.")
		return
	}
	_, _ = s.db.Exec(r.Context(), `UPDATE conversations SET title = CASE WHEN title = 'Cuộc trò chuyện mới' THEN LEFT($2, 100) ELSE title END, updated_at = NOW() WHERE id = $1`, conversationID, input.Content)

	// Chat is the first production path recorded through the common run model.
	// Only identifiers and execution metadata are stored here; the user's text
	// remains in messages and is not duplicated into operational events.
	chatRun, _, runErr := s.runs.Create(r.Context(), runs.NewRun{
		WorkspaceID:  conversationWorkspaceID,
		ActorUserID:  user.ID,
		TriggerType:  "manual",
		ResourceType: "conversation",
		ResourceID:   conversationID,
		Input:        map[string]any{"message_id": userMessage.ID, "model": models.ResolveModel(options)},
		TraceID:      middleware.GetReqID(r.Context()),
	})
	if runErr == nil {
		chatRun, runErr = s.runs.Transition(r.Context(), chatRun.ID, runs.Running, nil, "", "")
	}
	if runErr != nil {
		s.logger.Warn("chat run telemetry unavailable", "conversation_id", conversationID, "error", runErr)
	}

	historyRows, err := s.db.Query(r.Context(), `SELECT role, content FROM messages WHERE conversation_id = $1 ORDER BY created_at ASC LIMIT 40`, conversationID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể chuẩn bị ngữ cảnh trò chuyện.")
		return
	}
	history := []modelgateway.Message{}
	for historyRows.Next() {
		var item modelgateway.Message
		if historyRows.Scan(&item.Role, &item.Content) == nil {
			history = append(history, item)
		}
	}
	historyRows.Close()
	history = withResponsePresentation(history)

	// What the agent remembers about this person joins the conversation before
	// grounding does, so retrieved passages end up closest to the exchange
	// they explain.
	if agentRemembers {
		if memory := s.agentMemory(r.Context(), conversationAgentID, user.ID); memory != "" {
			history = append([]modelgateway.Message{{Role: "system", Content: agentMemoryHeader + memory}}, history...)
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
	assistantID := "msg_" + randomID(18)

	// Retrieval happens before model generation, but it must not look like the
	// reply is stuck. Sources deliberately remain server-side until generation
	// finishes: showing evidence before the answer makes the evidence look like
	// the answer and creates a large, shifting block above the streamed text.
	writeSSE(w, "status", map[string]string{"stage": "retrieving", "message": "Đang tìm trong Knowledge Base…"})
	flusher.Flush()
	var retrievalStep runs.Step
	if runErr == nil {
		retrievalStep, runErr = s.runs.CreateStep(r.Context(), runs.NewStep{RunID: chatRun.ID, NodeID: "retrieval", Type: "retrieval", Name: "Knowledge retrieval", TimeoutMS: 30000})
		if runErr == nil {
			retrievalStep, runErr = s.runs.TransitionStep(r.Context(), retrievalStep.ID, runs.Running, nil, "", "", "")
		}
	}

	var citations []Citation
	retrievalCtx, cancelRetrieval := context.WithTimeout(r.Context(), 30*time.Second)
	passages, retrievalErr := s.retrievalContextFor(retrievalCtx, conversationWorkspaceID, input.Content, agentKnowledge)
	cancelRetrieval()
	if retrievalErr != nil {
		s.logger.Error("knowledge retrieval failed", "conversation_id", conversationID, "error", retrievalErr)
		writeSSE(w, "status", map[string]string{"stage": "retrieval_failed", "message": "Không thể truy xuất Knowledge Base."})
		flusher.Flush()
	}
	if runErr == nil {
		if retrievalErr != nil {
			_, runErr = s.runs.TransitionStep(r.Context(), retrievalStep.ID, runs.Failed, nil, "", "retrieval_failed", retrievalErr.Error())
		} else {
			_, runErr = s.runs.TransitionStep(r.Context(), retrievalStep.ID, runs.Succeeded, map[string]any{"passage_count": len(passages)}, "", "", "")
		}
	}
	if len(passages) > 0 {
		// Grounding goes in front of the conversation so the passages frame
		// the whole exchange rather than arriving as the latest turn.
		history = append([]modelgateway.Message{{Role: "system", Content: buildGroundingPrompt(passages)}}, history...)
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
	writeSSE(w, "status", map[string]string{"stage": "writing", "message": "Đang soạn câu trả lời…"})
	flusher.Flush()
	writeSSE(w, "meta", map[string]any{"user_message": userMessage, "assistant_message_id": assistantID, "model": models.ResolveModel(options)})
	flusher.Flush()
	var generationStep runs.Step
	if runErr == nil {
		generationStep, runErr = s.runs.CreateStep(r.Context(), runs.NewStep{RunID: chatRun.ID, NodeID: "generation", Type: "model", Name: "Answer generation", TimeoutMS: s.cfg.LLMRequestTimeout.Milliseconds()})
		if runErr == nil {
			generationStep, runErr = s.runs.TransitionStep(r.Context(), generationStep.ID, runs.Running, nil, "", "", "")
		}
	}

	var assistant strings.Builder
	err = models.Stream(r.Context(), history, options, func(delta string) error {
		assistant.WriteString(delta)
		writeSSE(w, "delta", map[string]string{"content": delta})
		flusher.Flush()
		return nil
	})
	if err != nil {
		s.logger.Error("model stream failed", "conversation_id", conversationID, "error", err)
		writeSSE(w, "error", map[string]string{"message": "Model Gateway hiện không phản hồi. Vui lòng thử lại."})
		flusher.Flush()
		if runErr == nil {
			_, _ = s.runs.TransitionStep(context.Background(), generationStep.ID, runs.Failed, nil, "", "model_gateway", err.Error())
			_, _ = s.runs.Transition(context.Background(), chatRun.ID, runs.Failed, nil, "model_gateway", err.Error())
		}
		return
	}
	citations = citationsUsedByAnswer(assistant.String(), citations)
	assistantMessage := Message{ID: assistantID, ConversationID: conversationID, Role: "assistant", Content: assistant.String(), CreatedAt: time.Now(), Model: models.ResolveModel(options), Citations: citations}
	citationsJSON, _ := json.Marshal(citations)
	_, err = s.db.Exec(r.Context(), `INSERT INTO messages(id, conversation_id, role, content, model, citations, created_at) VALUES($1, $2, $3, $4, $5, $6, $7)`, assistantMessage.ID, conversationID, assistantMessage.Role, assistantMessage.Content, assistantMessage.Model, citationsJSON, assistantMessage.CreatedAt)
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
		_, runErr = s.runs.TransitionStep(r.Context(), generationStep.ID, runs.Succeeded, map[string]any{"message_id": assistantMessage.ID, "citation_count": len(citations)}, "", "", "")
		if runErr == nil {
			_, runErr = s.runs.Transition(r.Context(), chatRun.ID, runs.Succeeded, map[string]any{"message_id": assistantMessage.ID}, "", "")
		}
		if runErr != nil {
			s.logger.Warn("chat run finalization failed", "run_id", chatRun.ID, "error", runErr)
		}
	}
	// Suggestions come after the answer is saved, so a failure here can never
	// cost the reader the reply itself.
	if agentSuggests {
		if followUps := s.suggestFollowUps(r.Context(), input.Content, assistantMessage.Content, models, options); len(followUps) > 0 {
			writeSSE(w, "suggestions", map[string]any{"questions": followUps})
			flusher.Flush()
		}
	}
	writeSSE(w, "done", map[string]any{"message": assistantMessage})
	flusher.Flush()

	if agentRemembers {
		go s.rememberExchange(conversationAgentID, user.ID, input.Content, assistantMessage.Content, models, options)
	}
}

var inlineCitationPattern = regexp.MustCompile(`\[(\d+)]`)

// citationsUsedByAnswer keeps the evidence list aligned with the answer the
// reader actually received. Retrieval may collect many candidates, but only
// passages cited inline are evidence for the generated claims. A model that
// omits citations falls back to at most three candidates rather than flooding
// the answer with the whole retrieval set.
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
		index, err := strconv.Atoi(match[1])
		if err != nil || seen[index] {
			continue
		}
		if citation, ok := byIndex[index]; ok {
			used = append(used, citation)
			seen[index] = true
		}
	}
	if len(used) > 0 {
		return used
	}
	limit := 3
	if len(candidates) < limit {
		limit = len(candidates)
	}
	return append([]Citation(nil), candidates[:limit]...)
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
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
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
