package httpapi

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"cosmo/backend/internal/knowledge"

	"github.com/go-chi/chi/v5"
)

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

type SystemStatus struct {
	EntraEnabled        bool   `json:"entra_enabled"`
	EntraTenantID       string `json:"entra_tenant_id,omitempty"`
	ModelGatewayEnabled bool   `json:"model_gateway_enabled"`
	KnowledgeEnabled    bool   `json:"knowledge_enabled"`
	CookieSecure        bool   `json:"cookie_secure"`
	SessionTTL          string `json:"session_ttl"`
	AdminEmailCount     int    `json:"admin_email_count"`
	ConfigurationSource string `json:"configuration_source"`
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
	var previousRole string
	_ = s.db.QueryRow(r.Context(), `SELECT role FROM users WHERE id = $1`, userID).Scan(&previousRole)
	if _, err := s.db.Exec(r.Context(), `UPDATE users SET role = $2, updated_at = NOW() WHERE id = $1`, userID, input.Role); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể cập nhật quyền người dùng.")
		return
	}
	s.audit(r, auditEvent{
		Action: "admin.user.role_updated", TargetType: "user", TargetID: userID, TargetLabel: targetEmail,
		Metadata: map[string]string{"role": input.Role, "previous_role": previousRole},
	})
	writeJSON(w, http.StatusOK, map[string]any{"id": userID, "role": input.Role})
}

func (s *Server) systemStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePlatformAdmin(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, SystemStatus{
		EntraEnabled: s.cfg.EntraEnabled(), EntraTenantID: s.cfg.EntraTenantID,
		ModelGatewayEnabled: s.cfg.LLMEnabled(), KnowledgeEnabled: s.cfg.KnowledgeEnabled(),
		CookieSecure: s.cfg.CookieSecure, SessionTTL: s.cfg.SessionTTL.String(),
		AdminEmailCount:     len(s.cfg.PlatformAdminEmails),
		ConfigurationSource: "Model gateway theo từng workspace; mô hình embedding và reranker theo từng knowledge base.",
	})
}

// reindexDocument is one document to rebuild, read before the work starts so
// the transaction does not stay open for the length of the rebuild.
type reindexDocument struct {
	ID          string
	KBID        string
	Title       string
	Filename    string
	ContentType string
	Version     int
	StorageKey  string
}

func (s *Server) reindexKnowledgeDocuments(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePlatformAdmin(w, r); !ok {
		return
	}
	if s.knowledge == nil {
		writeError(w, http.StatusServiceUnavailable, "Dịch vụ tri thức chưa được cấu hình.")
		return
	}
	// Serialize admission of global rebuilds, then commit the complete queue
	// together. A failed preparation must not strand half the corpus processing.
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể chuẩn bị re-index.")
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(), `SELECT pg_advisory_xact_lock(716042901)`); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể khóa hàng đợi re-index.")
		return
	}
	var activeCount int
	if err := tx.QueryRow(r.Context(), `SELECT COUNT(*) FROM knowledge_documents WHERE status IN ('pending', 'processing')`).Scan(&activeCount); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể kiểm tra trạng thái tài liệu.")
		return
	}
	if activeCount > 0 {
		writeError(w, http.StatusConflict, "Đang có tài liệu được xử lý. Hãy đợi hoàn tất trước khi re-index.")
		return
	}

	rows, err := tx.Query(r.Context(), `
		SELECT id, kb_id, title, filename, content_type, version, storage_key
		FROM knowledge_documents
		WHERE storage_key <> ''
		ORDER BY created_at ASC FOR UPDATE`)
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

	for _, document := range documents {
		if _, err := tx.Exec(r.Context(), `
			UPDATE knowledge_documents
			SET status = 'processing', error = '', updated_at = NOW()
			WHERE id = $1`, document.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "Không thể chuẩn bị tài liệu để re-index.")
			return
		}
		if _, err := tx.Exec(r.Context(), `
			INSERT INTO knowledge_document_events(document_id, stage, message, done, total)
			VALUES($1, 'reindex', 'Queued for re-index', 0, 0)`, document.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "Không thể ghi hàng đợi re-index.")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể lưu hàng đợi re-index.")
		return
	}
	s.audit(r, auditEvent{
		Action: "admin.system.knowledge_reindex_started", TargetType: "system", TargetID: "knowledge_index",
		Metadata: map[string]int{"documents": len(documents)},
	})
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
	workers := max(1, s.cfg.ReindexWorkers)
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
