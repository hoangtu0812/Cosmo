package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"cosmo/backend/internal/knowledge"
	"cosmo/backend/internal/secrets"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
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
	EntraEnabled        bool                  `json:"entra_enabled"`
	EntraTenantID       string                `json:"entra_tenant_id,omitempty"`
	ModelGatewayEnabled bool                  `json:"model_gateway_enabled"`
	KnowledgeEnabled    bool                  `json:"knowledge_enabled"`
	CookieSecure        bool                  `json:"cookie_secure"`
	SessionTTL          string                `json:"session_ttl"`
	AdminEmailCount     int                   `json:"admin_email_count"`
	ConfigurationSource string                `json:"configuration_source"`
	EmbeddingModel      string                `json:"embedding_model"`
	RerankerModel       string                `json:"reranker_model"`
	SystemGateway       SystemGatewaySettings `json:"system_gateway"`
}

// SystemGatewaySettings is the safe, administrator-facing view of the gateway
// used by platform tasks. Its API key is never included in an API response.
type SystemGatewaySettings struct {
	BaseURL    string `json:"base_url"`
	HasAPIKey  bool   `json:"has_api_key"`
	APIKeyHint string `json:"api_key_hint,omitempty"`
	Configured bool   `json:"configured"`
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
	gateway, err := s.systemGatewaySettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải cấu hình system model gateway.")
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
		SystemGateway:       gateway,
	})
}

func (s *Server) systemGateway(ctx context.Context) (baseURL, apiKey, hint string, err error) {
	var sealed []byte
	err = s.db.QueryRow(ctx, `SELECT base_url, api_key_sealed, api_key_hint FROM system_model_gateway_config WHERE id = TRUE`).Scan(&baseURL, &sealed, &hint)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", nil
	}
	if err != nil {
		return "", "", "", err
	}
	if len(sealed) == 0 {
		return baseURL, "", hint, nil
	}
	apiKey, err = s.secrets.Open(sealed)
	if err != nil {
		s.logger.Warn("system gateway api key could not be decrypted", "error", err)
		return baseURL, "", hint, nil
	}
	return baseURL, apiKey, hint, nil
}

func (s *Server) systemGatewaySettings(ctx context.Context) (SystemGatewaySettings, error) {
	baseURL, apiKey, hint, err := s.systemGateway(ctx)
	if err != nil {
		return SystemGatewaySettings{}, err
	}
	return SystemGatewaySettings{
		BaseURL: baseURL, HasAPIKey: apiKey != "" || hint != "", APIKeyHint: hint, Configured: baseURL != "",
	}, nil
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
	baseURL, apiKey, _, err := s.systemGateway(ctx)
	if err != nil {
		return knowledge.ModelSettings{EmbeddingModel: values["embedding_model"], RerankerModel: values["reranker_model"]}, err
	}
	if baseURL == "" {
		return knowledge.ModelSettings{EmbeddingModel: values["embedding_model"], RerankerModel: values["reranker_model"]}, errors.New("system model gateway is not configured")
	}
	return knowledge.ModelSettings{
		EmbeddingModel: values["embedding_model"],
		RerankerModel:  values["reranker_model"],
		GatewayBaseURL: baseURL,
		GatewayAPIKey:  apiKey,
	}, nil
}

func (s *Server) updateSystemSettings(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	var input struct {
		EmbeddingModel string  `json:"embedding_model"`
		RerankerModel  string  `json:"reranker_model"`
		GatewayBaseURL string  `json:"gateway_base_url"`
		GatewayAPIKey  *string `json:"gateway_api_key"`
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
	input.GatewayBaseURL = strings.TrimRight(strings.TrimSpace(input.GatewayBaseURL), "/")
	if input.GatewayBaseURL != "" {
		parsed, err := url.Parse(input.GatewayBaseURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			writeError(w, http.StatusBadRequest, "Base URL của system model gateway phải là một địa chỉ http hoặc https hợp lệ.")
			return
		}
	}
	if len(input.GatewayBaseURL) > 500 {
		writeError(w, http.StatusBadRequest, "Base URL của system model gateway quá dài.")
		return
	}
	var sealed []byte
	hint := ""
	if input.GatewayAPIKey != nil {
		key := strings.TrimSpace(*input.GatewayAPIKey)
		if key != "" {
			if !s.secrets.Configured() {
				writeError(w, http.StatusServiceUnavailable, "Máy chủ chưa có SESSION_SECRET nên không thể mã hoá API key.")
				return
			}
			var err error
			sealed, err = s.secrets.Seal(key)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "Không thể mã hoá API key.")
				return
			}
			hint = secrets.Hint(key)
		}
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
	if input.GatewayAPIKey == nil {
		if _, err := tx.Exec(r.Context(), `
			INSERT INTO system_model_gateway_config(id, base_url, updated_at, updated_by)
			VALUES(TRUE, $1, NOW(), $2)
			ON CONFLICT(id) DO UPDATE SET base_url = EXCLUDED.base_url, updated_at = NOW(), updated_by = EXCLUDED.updated_by`, input.GatewayBaseURL, actor.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "Không thể lưu system model gateway.")
			return
		}
	} else if _, err := tx.Exec(r.Context(), `
		INSERT INTO system_model_gateway_config(id, base_url, api_key_sealed, api_key_hint, updated_at, updated_by)
		VALUES(TRUE, $1, $2, $3, NOW(), $4)
		ON CONFLICT(id) DO UPDATE SET base_url = EXCLUDED.base_url, api_key_sealed = EXCLUDED.api_key_sealed,
		api_key_hint = EXCLUDED.api_key_hint, updated_at = NOW(), updated_by = EXCLUDED.updated_by`, input.GatewayBaseURL, sealed, hint, actor.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể lưu system model gateway.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể lưu cấu hình hệ thống.")
		return
	}
	s.writeAudit(r.Context(), actor.ID, "admin.system.knowledge_models_updated", "system", "knowledge_models", map[string]string{
		"embedding_model": input.EmbeddingModel,
		"reranker_model":  input.RerankerModel,
	})
	gateway, err := s.systemGatewaySettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải cấu hình system model gateway.")
		return
	}
	system := SystemStatus{
		EntraEnabled: s.cfg.EntraEnabled(), EntraTenantID: s.cfg.EntraTenantID,
		ModelGatewayEnabled: s.cfg.LLMEnabled(), KnowledgeEnabled: s.cfg.KnowledgeEnabled(),
		CookieSecure: s.cfg.CookieSecure, SessionTTL: s.cfg.SessionTTL.String(),
		AdminEmailCount:     len(s.cfg.PlatformAdminEmails),
		ConfigurationSource: "Các mô hình knowledge được lưu trong Cosmo. Bí mật triển khai vẫn cấu hình trong .env.",
		EmbeddingModel:      input.EmbeddingModel,
		RerankerModel:       input.RerankerModel,
		SystemGateway:       gateway,
	}
	writeJSON(w, http.StatusOK, system)
}

func (s *Server) listSystemGatewayModels(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePlatformAdmin(w, r); !ok {
		return
	}
	var input struct {
		BaseURL string `json:"base_url"`
		APIKey  string `json:"api_key"`
	}
	if r.Body != nil && r.ContentLength != 0 && !decodeJSON(w, r, &input) {
		return
	}
	baseURL, apiKey, _, err := s.systemGateway(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể đọc system model gateway.")
		return
	}
	if candidate := strings.TrimRight(strings.TrimSpace(input.BaseURL), "/"); candidate != "" {
		baseURL = candidate
	}
	if candidate := strings.TrimSpace(input.APIKey); candidate != "" {
		apiKey = candidate
	}
	if baseURL == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": "Chưa có Base URL.", "models": []string{}})
		return
	}
	models, probeErr := fetchGatewayModels(r.Context(), baseURL, apiKey)
	if probeErr != nil {
		s.logger.Warn("list system gateway models", "error", probeErr)
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": "Không kết nối được tới gateway.", "models": []string{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "models": models})
}

type reindexDocument struct {
	ID          string
	KBID        string
	Title       string
	Filename    string
	ContentType string
	Version     int
	StorageKey  string
}

// reindexKnowledgeDocuments rebuilds Qdrant from the original files. It is a
// platform-admin operation because the collection is shared across every
// workspace. The original document objects and database metadata are kept.
func (s *Server) reindexKnowledgeDocuments(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	if s.knowledge == nil {
		writeError(w, http.StatusServiceUnavailable, "Dịch vụ tri thức chưa được cấu hình.")
		return
	}
	if _, err := s.knowledgeModelSettings(r.Context()); err != nil {
		writeError(w, http.StatusBadRequest, "Cần cấu hình system model gateway trước khi re-index.")
		return
	}

	var activeCount int
	if err := s.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM knowledge_documents WHERE status IN ('pending', 'processing')`).Scan(&activeCount); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể kiểm tra trạng thái tài liệu.")
		return
	}
	if activeCount > 0 {
		writeError(w, http.StatusConflict, "Đang có tài liệu được xử lý. Hãy đợi hoàn tất trước khi re-index.")
		return
	}

	rows, err := s.db.Query(r.Context(), `
		SELECT id, kb_id, title, filename, content_type, version, storage_key
		FROM knowledge_documents
		WHERE storage_key <> ''
		ORDER BY created_at ASC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải danh sách tài liệu để re-index.")
		return
	}
	defer rows.Close()
	documents := make([]reindexDocument, 0)
	for rows.Next() {
		var document reindexDocument
		if err := rows.Scan(&document.ID, &document.KBID, &document.Title, &document.Filename, &document.ContentType, &document.Version, &document.StorageKey); err != nil {
			writeError(w, http.StatusInternalServerError, "Không thể đọc danh sách tài liệu để re-index.")
			return
		}
		documents = append(documents, document)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể đọc danh sách tài liệu để re-index.")
		return
	}
	if len(documents) == 0 {
		writeJSON(w, http.StatusOK, map[string]int{"queued": 0})
		return
	}

	if err := s.knowledge.ResetIndex(r.Context()); err != nil {
		s.logger.Error("could not reset knowledge index", "error", err)
		writeError(w, http.StatusBadGateway, "Không thể làm mới knowledge index.")
		return
	}
	for _, document := range documents {
		if _, err := s.db.Exec(r.Context(), `
			UPDATE knowledge_documents
			SET status = 'processing', chunk_count = 0, error = '', updated_at = NOW()
			WHERE id = $1`, document.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "Không thể chuẩn bị tài liệu để re-index.")
			return
		}
		if _, err := s.db.Exec(r.Context(), `
			INSERT INTO knowledge_document_events(document_id, stage, message, done, total)
			VALUES($1, 'reindex', 'Queued for re-index', 0, 0)`, document.ID); err != nil {
			s.logger.Warn("could not record re-index event", "document", document.ID, "error", err)
		}
	}
	s.writeAudit(r.Context(), actor.ID, "admin.system.knowledge_reindex_started", "system", "knowledge_index", map[string]int{"documents": len(documents)})
	go s.runKnowledgeReindex(documents)
	writeJSON(w, http.StatusAccepted, map[string]int{"queued": len(documents)})
}

// runKnowledgeReindex rebuilds the index, several documents at a time.
//
// Re-indexing is the slow path an administrator waits on after changing the
// embedding model, and one document at a time makes it cost the sum of every
// parse and every gateway round trip. The pool is bounded rather than
// unbounded: each worker holds a document and keeps the gateway busy, so the
// point is to overlap the waiting, not to flood the service with it.
//
// Nothing is read here any more. Each job names the original by its object
// key and the knowledge service reads it directly, which keeps a full copy of
// every document from travelling to the control plane and back.
func (s *Server) runKnowledgeReindex(documents []reindexDocument) {
	started := time.Now()
	workers := s.cfg.ReindexWorkers
	if workers > len(documents) {
		workers = len(documents)
	}

	jobs := make(chan reindexDocument)
	var wait sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for document := range jobs {
				s.ingestDocument(knowledge.IngestJob{
					KBID:        document.KBID,
					DocumentID:  document.ID,
					Filename:    document.Filename,
					ContentType: document.ContentType,
					Title:       document.Title,
					Version:     document.Version,
					StorageKey:  document.StorageKey,
				})
			}
		}()
	}
	for _, document := range documents {
		jobs <- document
	}
	close(jobs)
	wait.Wait()

	s.logger.Info("knowledge re-index finished",
		"documents", len(documents), "workers", workers, "seconds", int(time.Since(started).Seconds()))
}

// KnowledgeIndexStatus is how far the index has got, counted from the document
// rows themselves rather than from a job record. A re-index that dies with the
// process therefore stops reporting progress instead of claiming to still run.
type KnowledgeIndexStatus struct {
	Total   int  `json:"total"`
	Ready   int  `json:"ready"`
	Failed  int  `json:"failed"`
	Pending int  `json:"pending"`
	Running bool `json:"running"`
}

func (s *Server) knowledgeIndexStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePlatformAdmin(w, r); !ok {
		return
	}
	var status KnowledgeIndexStatus
	if err := s.db.QueryRow(r.Context(), `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE status = 'ready'),
		       COUNT(*) FILTER (WHERE status = 'failed'),
		       COUNT(*) FILTER (WHERE status IN ('pending', 'processing'))
		FROM knowledge_documents`).Scan(&status.Total, &status.Ready, &status.Failed, &status.Pending); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể đọc tiến độ re-index.")
		return
	}
	status.Running = status.Pending > 0
	writeJSON(w, http.StatusOK, status)
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
