package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

var errChatTurnBusy = errors.New("conversation already has an executing turn")
var errChatTurnMismatch = errors.New("client message identity has different content")

type chatTurnIdentity struct {
	ClientMessageID string
	RequestHash     string
	AssistantID     string
}

type chatTurn struct {
	ClientMessageID string `json:"client_message_id"`
	UserMessageID   string `json:"user_message_id"`
	AssistantID     string `json:"assistant_message_id"`
	RunID           string `json:"run_id"`
	Sequence        int64  `json:"sequence"`
	Status          string `json:"status"`
	RequestHash     string `json:"-"`
}

func (t *chatTurn) Error() string { return "chat turn already accepted" }

func chatRequestHash(content, model, effort string) string {
	encoded, _ := json.Marshal([]string{content, model, effort})
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:])
}

func lookupChatTurn(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, conversation, key, hash string) (*chatTurn, error) {
	var turn chatTurn
	err := db.QueryRow(ctx, `SELECT client_message_id,user_message_id,assistant_message_id,run_id,sequence,status,request_hash FROM chat_turns WHERE conversation_id=$1 AND client_message_id=$2`, conversation, key).Scan(&turn.ClientMessageID, &turn.UserMessageID, &turn.AssistantID, &turn.RunID, &turn.Sequence, &turn.Status, &turn.RequestHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if turn.RequestHash != hash {
		return nil, errChatTurnMismatch
	}
	return &turn, nil
}

func (s *Server) writeChatTurnError(w http.ResponseWriter, r *http.Request, conversation string, err error) {
	var existing *chatTurn
	if errors.As(err, &existing) {
		if existing.Status == "succeeded" {
			var message Message
			var citations, calls, usage []byte
			err := s.db.QueryRow(r.Context(), `SELECT id,conversation_id,role,content,model,citations,tool_calls,usage,created_at FROM messages WHERE conversation_id=$1 AND id=$2 AND role='assistant'`, conversation, existing.AssistantID).Scan(&message.ID, &message.ConversationID, &message.Role, &message.Content, &message.Model, &citations, &calls, &usage, &message.CreatedAt)
			if err == nil {
				_ = json.Unmarshal(citations, &message.Citations)
				_ = json.Unmarshal(calls, &message.ToolCalls)
				if len(usage) > 0 {
					_ = json.Unmarshal(usage, &message.Usage)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache, no-transform")
				writeSSE(w, "meta", map[string]any{"assistant_message_id": message.ID, "user_message_id": existing.UserMessageID, "model": message.Model, "run_id": existing.RunID, "replayed": true})
				writeSSE(w, "done", map[string]any{"message": message})
				return
			}
			// Deleted answer or transient read error: retain the execution identity.
		}
		message := "Lượt này đã được tiếp nhận. Vui lòng tải lại hội thoại để xem trạng thái."
		if existing.Status == "executing" {
			message = "Câu hỏi này đang được xử lý. Vui lòng chờ."
		}
		if existing.Status == "interrupted" {
			message = "Lượt này đã bị gián đoạn và không được chạy lại tự động. Vui lòng kiểm tra lịch sử."
		}
		writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]string{"code": "chat_turn_exists", "message": message}, "turn": existing})
		return
	}
	if errors.Is(err, errChatTurnBusy) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]string{"code": "chat_turn_busy", "message": "Hội thoại đang trả lời câu hỏi trước. Vui lòng chờ rồi gửi lại."}})
		return
	}
	if errors.Is(err, errChatTurnMismatch) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]string{"code": "chat_turn_mismatch", "message": "ID câu hỏi đã được dùng cho nội dung khác."}})
		return
	}
	writeError(w, http.StatusServiceUnavailable, "Không thể tiếp nhận câu hỏi. Vui lòng thử lại.")
}

// An exited HTTP handler cannot be resumed safely: a tool may already have
// changed external state. Preserve the identity and mark it interrupted.
// A process crash leaves executing untouched pending recovery/reconciliation.
func (s *Server) interruptChatTurn(conversation, key string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.db.Exec(ctx, `UPDATE chat_turns SET status='interrupted',finished_at=NOW() WHERE conversation_id=$1 AND client_message_id=$2 AND status='executing'`, conversation, key); err != nil {
		s.logger.Error("finalize interrupted chat turn", "conversation_id", conversation, "error", err)
	}
}
