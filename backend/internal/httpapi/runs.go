package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"cosmo/backend/internal/runs"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	workspaceID := s.memberWorkspace(r.Context(), user.ID, r.URL.Query().Get("workspace"))
	if workspaceID == "" {
		writeError(w, http.StatusForbidden, "Bạn không có quyền truy cập workspace này.")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := s.runs.List(r.Context(), workspaceID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải danh sách run.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": items})
}

func (s *Server) authorizedRun(w http.ResponseWriter, r *http.Request) (runs.Run, bool) {
	item, err := s.runs.Get(r.Context(), chi.URLParam(r, "runID"))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "Run không tồn tại.")
		return runs.Run{}, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể đọc run.")
		return runs.Run{}, false
	}
	if !s.hasWorkspace(r.Context(), currentUser(r.Context()).ID, item.WorkspaceID) {
		writeError(w, http.StatusForbidden, "Bạn không có quyền truy cập run này.")
		return runs.Run{}, false
	}
	return item, true
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	item, ok := s.authorizedRun(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": item})
}

func (s *Server) listRunEvents(w http.ResponseWriter, r *http.Request) {
	item, ok := s.authorizedRun(w, r)
	if !ok {
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	events, err := s.runs.Events(r.Context(), item.ID, after, 200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải run events.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) listRunSteps(w http.ResponseWriter, r *http.Request) {
	item, ok := s.authorizedRun(w, r)
	if !ok {
		return
	}
	steps, err := s.runs.Steps(r.Context(), item.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải run steps.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"steps": steps})
}

func (s *Server) streamRunEvents(w http.ResponseWriter, r *http.Request) {
	item, ok := s.authorizedRun(w, r)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "Streaming không được hỗ trợ.")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		events, err := s.runs.Events(r.Context(), item.ID, after, 200)
		if err != nil {
			writeSSE(w, "error", map[string]string{"message": "Không thể đọc run events."})
			flusher.Flush()
			return
		}
		for _, event := range events {
			writeSSE(w, "event", event)
			after = event.Sequence
		}
		if len(events) > 0 {
			flusher.Flush()
		}
		current, err := s.runs.Get(r.Context(), item.ID)
		if err != nil {
			return
		}
		if current.Status.Terminal() {
			writeSSE(w, "done", map[string]any{"run": current, "last_sequence": after})
			flusher.Flush()
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) cancelRun(w http.ResponseWriter, r *http.Request) {
	item, ok := s.authorizedRun(w, r)
	if !ok {
		return
	}
	user := currentUser(r.Context())
	if item.ActorUserID != user.ID && !s.isWorkspaceAdmin(r.Context(), user, item.WorkspaceID) {
		writeError(w, http.StatusForbidden, "Bạn không có quyền hủy run này.")
		return
	}
	cancelled, err := s.runs.Cancel(r.Context(), item.ID)
	if errors.Is(err, runs.ErrInvalidTransition) {
		writeError(w, http.StatusConflict, "Run đã kết thúc và không thể hủy.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể hủy run.")
		return
	}
	s.audit(r, auditEvent{
		Action: "run.cancelled", TargetType: "run", TargetID: item.ID, WorkspaceID: item.WorkspaceID,
		Metadata: map[string]string{"resource_type": item.ResourceType, "resource_id": item.ResourceID},
	})
	writeJSON(w, http.StatusOK, map[string]any{"run": cancelled})
}
