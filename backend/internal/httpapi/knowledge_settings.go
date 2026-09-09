package httpapi

import (
	"context"
	"fmt"
	"github.com/go-chi/chi/v5"
	"net/http"
	"time"
)

func (s *Server) requireKnowledgeLayout(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	available, err := s.knowledge.LayoutAvailable(ctx)
	if err != nil {
		return fmt.Errorf("Không thể kiểm tra dịch vụ phân tích layout.")
	}
	if !available {
		return fmt.Errorf("Chưa cấu hình dịch vụ phân tích layout. Hãy chọn Off.")
	}
	return nil
}

func (s *Server) knowledgeCapabilities(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeAccess(r.Context(), currentUser(r.Context()).ID, chi.URLParam(r, "kbID")) == "" {
		writeError(w, 404, "Không tìm thấy Knowledge Base.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	available, err := s.knowledge.LayoutAvailable(ctx)
	if err != nil {
		writeError(w, 503, "Không thể kiểm tra dịch vụ phân tích layout.")
		return
	}
	writeJSON(w, 200, map[string]bool{"layout_available": available})
}

func (s *Server) reindexKnowledgeBase(w http.ResponseWriter, r *http.Request) {
	kb := chi.URLParam(r, "kbID")
	user := currentUser(r.Context())
	if s.knowledgeAccess(r.Context(), user.ID, kb) != "owner" {
		writeError(w, 403, "Bạn không có quyền re-index Knowledge Base này.")
		return
	}
	if s.knowledge == nil {
		writeError(w, 503, "Dịch vụ tri thức chưa được cấu hình.")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "Không thể tạo tác vụ.")
		return
	}
	defer tx.Rollback(r.Context())
	var layout string
	if err = tx.QueryRow(r.Context(), `SELECT layout_mode FROM knowledge_bases WHERE id=$1 FOR UPDATE`, kb).Scan(&layout); err != nil {
		writeError(w, 404, "Không tìm thấy Knowledge Base.")
		return
	}
	if layout != layoutOff {
		if err = s.requireKnowledgeLayout(r.Context()); err != nil {
			writeError(w, 400, err.Error())
			return
		}
	}
	job, err := enqueueIngestion(r.Context(), tx, kb, user.ID, "queued")
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "Không thể lưu tác vụ.")
		return
	}
	s.audit(r, auditEvent{Action: "knowledge.base.reindex_started", TargetType: "knowledge_base", TargetID: kb})
	writeJSON(w, 202, map[string]string{"job_id": job})
}
