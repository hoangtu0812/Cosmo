package httpapi

import (
	"github.com/go-chi/chi/v5"
	"net/http"
	"strings"
	"time"
)

// This endpoint calls precisely the retrieval orchestration used by chat.
// Supplied KB IDs only narrow workspace access; they never bypass mounts.
func (s *Server) testWorkspaceRetrieval(w http.ResponseWriter, r *http.Request) {
	workspace := chi.URLParam(r, "workspaceID")
	if !s.hasWorkspace(r.Context(), currentUser(r.Context()).ID, workspace) {
		writeError(w, http.StatusForbidden, "Bạn không có quyền truy cập workspace này.")
		return
	}
	var input struct {
		Query string   `json:"query"`
		KBIDs []string `json:"kb_ids"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Query = strings.TrimSpace(input.Query)
	if input.Query == "" || len([]rune(input.Query)) > 2000 || len(input.KBIDs) > 100 {
		writeError(w, 400, "Câu hỏi hoặc danh sách Knowledge Base không hợp lệ.")
		return
	}
	started := time.Now()
	report, err := s.retrieveKnowledge(r.Context(), workspace, input.Query, input.KBIDs)
	if err != nil {
		writeError(w, 503, "Không thể thực hiện truy vấn Knowledge Base.")
		return
	}
	// Recheck membership after a potentially slow fan-out before returning data.
	if !s.hasWorkspace(r.Context(), currentUser(r.Context()).ID, workspace) {
		writeError(w, 403, "Quyền truy cập workspace đã thay đổi.")
		return
	}
	writeJSON(w, 200, map[string]any{"passages": report.Passages, "sources": report.Sources, "incomplete": report.incomplete(), "duration_ms": time.Since(started).Milliseconds(), "retrieval_contract": "chat-go-v1", "knowledge_mode": "live"})
}
