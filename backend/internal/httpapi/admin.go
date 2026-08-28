package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"cosmo/backend/internal/knowledge"

	"github.com/go-chi/chi/v5"
)

const defaultEmbeddingModel = "BAAI/bge-m3"
const defaultRerankerModel = "BAAI/bge-reranker-v2-m3"

// AdminUser is deliberately limited to provisioned Cosmo accounts. Listing a
// whole Entra tenant would require broad Graph application permissions, while
// this view is sufficient to manage people who have actually used Cosmo.
type AdminUser struct {
	ID             string    `json:"id"`
	Email          string    `json:"email"`
	Name           string    `json:"name"`
	Role           string    `json:"role"`
	Provider       string    `json:"provider"`
	WorkspaceCount int       `json:"workspace_count"`
	CreatedAt      time.Time `json:"created_at"`
	HasAvatar      bool      `json:"has_avatar"`
}

type AuditLog struct {
	ID         int64          `json:"id"`
	ActorName  string         `json:"actor_name"`
	ActorEmail string         `json:"actor_email"`
	Action     string         `json:"action"`
	TargetType string         `json:"target_type"`
	TargetID   string         `json:"target_id"`
	Metadata   map[string]any `json:"metadata"`
	CreatedAt  time.Time      `json:"created_at"`
}

type SystemStatus struct {
	EntraEnabled        bool   `json:"entra_enabled"`
	EntraTenantID       string `json:"entra_tenant_id,omitempty"`
	ModelGatewayEnabled bool   `json:"model_gateway_enabled"`
	KnowledgeEnabled    bool   `json:"knowledge_enabled"`
	CookieSecure        bool   `json:"cookie_secure"`
	SessionTTL          string `json:"session_ttl"`
	AdminEmailCount     int    `json:"admin_email_count"`
	ConfigurationSource string `json:"configuration_source"`
	EmbeddingModel      string `json:"embedding_model"`
	RerankerModel       string `json:"reranker_model"`
}

func (s *Server) requirePlatformAdmin(w http.ResponseWriter, r *http.Request) (User, bool) {
	user := currentUser(r.Context())
	if s.cfg.IsPlatformAdmin(user.Email) && user.Role != "admin" {
		// The environment is the source of truth for an emergency promotion. It
		// takes effect on the next authenticated request even when the account
		// existed before its email was added to ADMIN_EMAILS.
		if _, err := s.db.Exec(r.Context(), `UPDATE users SET role = 'admin', updated_at = NOW() WHERE id = $1`, user.ID); err == nil {
			user.Role = "admin"
		}
	}
	if user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Bạn cần quyền quản trị hệ thống để thực hiện thao tác này.")
		return User{}, false
	}
	return user, true
}

func (s *Server) listAdminUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePlatformAdmin(w, r); !ok {
		return
	}
	rows, err := s.db.Query(r.Context(), `
		SELECT u.id, u.email, u.name, u.role,
		       COALESCE((SELECT i.provider FROM oauth_identities i WHERE i.user_id = u.id ORDER BY i.created_at LIMIT 1), 'local'),
		       (SELECT COUNT(*) FROM workspace_memberships m WHERE m.user_id = u.id),
		       u.created_at, (u.avatar_image IS NOT NULL)
		FROM users u
		ORDER BY u.created_at DESC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải danh sách người dùng.")
		return
	}
	defer rows.Close()
	items := []AdminUser{}
	for rows.Next() {
		var item AdminUser
		if rows.Scan(&item.ID, &item.Email, &item.Name, &item.Role, &item.Provider, &item.WorkspaceCount, &item.CreatedAt, &item.HasAvatar) == nil {
			items = append(items, item)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": items})
}

func (s *Server) updateAdminUser(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	userID := chi.URLParam(r, "userID")
	var input struct {
		Role string `json:"role"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Role = strings.TrimSpace(input.Role)
	if input.Role != "admin" && input.Role != "user" {
		writeError(w, http.StatusBadRequest, "Vai trò chỉ có thể là admin hoặc user.")
		return
	}
	var targetEmail string
	if err := s.db.QueryRow(r.Context(), `SELECT email FROM users WHERE id = $1`, userID).Scan(&targetEmail); err != nil {
		writeError(w, http.StatusNotFound, "Không tìm thấy người dùng.")
		return
	}
	if actor.ID == userID && input.Role != "admin" {
		writeError(w, http.StatusBadRequest, "Không thể tự gỡ quyền quản trị của chính bạn.")
		return
	}
	if s.cfg.IsPlatformAdmin(targetEmail) && input.Role != "admin" {
		writeError(w, http.StatusBadRequest, "Email này được khai báo trong ADMIN_EMAILS và phải giữ quyền quản trị.")
		return
	}
	if _, err := s.db.Exec(r.Context(), `UPDATE users SET role = $2, updated_at = NOW() WHERE id = $1`, userID, input.Role); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể cập nhật quyền người dùng.")
		return
	}
	s.writeAudit(r.Context(), actor.ID, "admin.user.role_updated", "user", userID, map[string]string{"role": input.Role})
	writeJSON(w, http.StatusOK, map[string]any{"id": userID, "role": input.Role})
}

func (s *Server) listAuditLogs(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePlatformAdmin(w, r); !ok {
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	rows, err := s.db.Query(r.Context(), `
		SELECT a.id, COALESCE(u.name, ''), COALESCE(u.email, ''), a.action, a.target_type, a.target_id, a.metadata, a.created_at
		FROM audit_logs a LEFT JOIN users u ON u.id = a.actor_user_id
		ORDER BY a.created_at DESC LIMIT $1`, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải nhật ký audit.")
		return
	}
	defer rows.Close()
	items := []AuditLog{}
	for rows.Next() {
		var item AuditLog
		var metadata []byte
		if rows.Scan(&item.ID, &item.ActorName, &item.ActorEmail, &item.Action, &item.TargetType, &item.TargetID, &metadata, &item.CreatedAt) == nil {
			_ = json.Unmarshal(metadata, &item.Metadata)
			if item.Metadata == nil {
				item.Metadata = map[string]any{}
			}
			items = append(items, item)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": items})
}

func (s *Server) systemStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePlatformAdmin(w, r); !ok {
		return
	}
	models, err := s.knowledgeModelSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải cấu hình mô hình knowledge.")
		return
	}
	writeJSON(w, http.StatusOK, SystemStatus{
		EntraEnabled: s.cfg.EntraEnabled(), EntraTenantID: s.cfg.EntraTenantID,
		ModelGatewayEnabled: s.cfg.LLMEnabled(), KnowledgeEnabled: s.cfg.KnowledgeEnabled(),
		CookieSecure: s.cfg.CookieSecure, SessionTTL: s.cfg.SessionTTL.String(),
		AdminEmailCount:     len(s.cfg.PlatformAdminEmails),
		ConfigurationSource: "Các mô hình knowledge được lưu trong Cosmo. Bí mật triển khai vẫn cấu hình trong .env.",
		EmbeddingModel:      models.EmbeddingModel,
		RerankerModel:       models.RerankerModel,
	})
}

// knowledgeModelSettings returns safe, platform-wide model identifiers. A
// default keeps existing installs working until an administrator first saves
// this section in the console.
func (s *Server) knowledgeModelSettings(ctx context.Context) (knowledge.ModelSettings, error) {
	values := map[string]string{
		"embedding_model": defaultEmbeddingModel,
		"reranker_model":  defaultRerankerModel,
	}
	rows, err := s.db.Query(ctx, `SELECT key, value FROM system_settings WHERE key IN ('embedding_model', 'reranker_model')`)
	if err != nil {
		return knowledge.ModelSettings{EmbeddingModel: values["embedding_model"], RerankerModel: values["reranker_model"]}, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return knowledge.ModelSettings{EmbeddingModel: values["embedding_model"], RerankerModel: values["reranker_model"]}, err
		}
		if strings.TrimSpace(value) != "" {
			values[key] = value
		}
	}
	if err := rows.Err(); err != nil {
		return knowledge.ModelSettings{EmbeddingModel: values["embedding_model"], RerankerModel: values["reranker_model"]}, err
	}
	return knowledge.ModelSettings{EmbeddingModel: values["embedding_model"], RerankerModel: values["reranker_model"]}, nil
}

func (s *Server) updateSystemSettings(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	var input struct {
		EmbeddingModel string `json:"embedding_model"`
		RerankerModel  string `json:"reranker_model"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.EmbeddingModel = strings.TrimSpace(input.EmbeddingModel)
	input.RerankerModel = strings.TrimSpace(input.RerankerModel)
	if input.EmbeddingModel == "" || input.RerankerModel == "" || len(input.EmbeddingModel) > 200 || len(input.RerankerModel) > 200 {
		writeError(w, http.StatusBadRequest, "Tên mô hình embedding và reranker là bắt buộc, tối đa 200 ký tự.")
		return
	}

	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể lưu cấu hình hệ thống.")
		return
	}
	defer tx.Rollback(r.Context())
	for key, value := range map[string]string{"embedding_model": input.EmbeddingModel, "reranker_model": input.RerankerModel} {
		if _, err := tx.Exec(r.Context(), `
			INSERT INTO system_settings(key, value, updated_at, updated_by)
			VALUES($1, $2, NOW(), $3)
			ON CONFLICT(key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW(), updated_by = EXCLUDED.updated_by`, key, value, actor.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "Không thể lưu cấu hình hệ thống.")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể lưu cấu hình hệ thống.")
		return
	}
	s.writeAudit(r.Context(), actor.ID, "admin.system.knowledge_models_updated", "system", "knowledge_models", map[string]string{
		"embedding_model": input.EmbeddingModel,
		"reranker_model":  input.RerankerModel,
	})
	system := SystemStatus{
		EntraEnabled: s.cfg.EntraEnabled(), EntraTenantID: s.cfg.EntraTenantID,
		ModelGatewayEnabled: s.cfg.LLMEnabled(), KnowledgeEnabled: s.cfg.KnowledgeEnabled(),
		CookieSecure: s.cfg.CookieSecure, SessionTTL: s.cfg.SessionTTL.String(),
		AdminEmailCount:     len(s.cfg.PlatformAdminEmails),
		ConfigurationSource: "Các mô hình knowledge được lưu trong Cosmo. Bí mật triển khai vẫn cấu hình trong .env.",
		EmbeddingModel:      input.EmbeddingModel,
		RerankerModel:       input.RerankerModel,
	}
	writeJSON(w, http.StatusOK, system)
}

func (s *Server) writeAudit(ctx context.Context, actorID, action, targetType, targetID string, metadata any) {
	payload, err := json.Marshal(metadata)
	if err != nil {
		payload = []byte(`{}`)
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO audit_logs(actor_user_id, action, target_type, target_id, metadata) VALUES($1, $2, $3, $4, $5::jsonb)`, actorID, action, targetType, targetID, string(payload)); err != nil {
		s.logger.Warn("write audit log", "action", action, "error", err)
	}
}
