package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The worker writes the same SSE contract as HTTP, but persists it before a
// subscriber can see it. An unavailable database cancels further execution.
type chatEventWriter struct {
	server    *Server
	ctx       context.Context
	execution *chatExecution
	cancel    context.CancelFunc
	header    http.Header
	status    int
}

func (w *chatEventWriter) Header() http.Header    { return w.header }
func (w *chatEventWriter) WriteHeader(status int) { w.status = status }
func (w *chatEventWriter) Flush()                 {}
func (w *chatEventWriter) Write(frame []byte) (int, error) {
	if w.status >= 400 {
		w.cancel()
		return len(frame), nil
	}
	tag, err := w.server.db.Exec(w.ctx, `INSERT INTO chat_turn_events(conversation_id,client_message_id,frame)
	SELECT conversation_id,client_message_id,$4 FROM chat_turns WHERE conversation_id=$1 AND client_message_id=$2 AND lease_owner=$3 AND lease_expires_at>NOW() AND status IN ('executing','succeeded') FOR SHARE`, w.execution.Conversation, w.execution.Identity.ClientMessageID, w.execution.Owner, string(frame))
	if err != nil || tag.RowsAffected() != 1 {
		w.cancel()
		if err == nil {
			err = fmt.Errorf("chat event lease lost")
		}
		return 0, err
	}
	return len(frame), nil
}

func (s *Server) followChatTurn(w http.ResponseWriter, r *http.Request, conversation, key, hash string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, 500, "Streaming không được hỗ trợ.")
		return
	}
	var cursor int64
	if raw := r.Header.Get("Last-Event-ID"); raw != "" {
		var err error
		cursor, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || cursor < 0 {
			writeError(w, 400, "Vị trí sự kiện không hợp lệ.")
			return
		}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	writeSSE(w, "status", map[string]string{"stage": "queued", "message": "Câu hỏi đã được tiếp nhận. Đang chờ xử lý…"})
	flusher.Flush()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	lastHeartbeat := time.Now()
	for {
		rows, err := s.db.Query(r.Context(), `SELECT id,frame FROM chat_turn_events WHERE conversation_id=$1 AND client_message_id=$2 AND id>$3 ORDER BY id LIMIT 200`, conversation, key, cursor)
		if err != nil {
			return
		}
		terminal := false
		count := 0
		for rows.Next() {
			var id int64
			var frame string
			if err = rows.Scan(&id, &frame); err != nil {
				break
			}
			if _, err = fmt.Fprintf(w, "id: %d\n%s", id, frame); err != nil {
				break
			}
			cursor = id
			count++
			if strings.HasPrefix(frame, "event: done\n") || strings.HasPrefix(frame, "event: error\n") {
				terminal = true
			}
		}
		rows.Close()
		if err != nil || rows.Err() != nil {
			return
		}
		flusher.Flush()
		if terminal {
			return
		}
		if count == 200 {
			continue
		}
		var status string
		if err = s.db.QueryRow(r.Context(), `SELECT status FROM chat_turns WHERE conversation_id=$1 AND client_message_id=$2`, conversation, key).Scan(&status); err != nil {
			return
		}
		if status == "succeeded" {
			existing, err := lookupChatTurn(r.Context(), s.db, conversation, key, hash)
			if err == nil && existing != nil {
				s.writeChatTurnError(w, r, conversation, existing)
				flusher.Flush()
			}
			return
		}
		if status == "interrupted" {
			writeSSE(w, "error", map[string]string{"message": "Lượt chat đã bị hủy hoặc gián đoạn. Tác động của tool cần được kiểm tra trước khi thử lại."})
			flusher.Flush()
			return
		}
		if time.Since(lastHeartbeat) >= 10*time.Second {
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
			lastHeartbeat = time.Now()
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}
