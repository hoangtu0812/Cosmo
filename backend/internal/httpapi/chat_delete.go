package httpapi

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Server) deleteChatMessages(ctx context.Context, userID, conversationID, messageID string) ([]string, string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var id string
	if err := tx.QueryRow(ctx, `SELECT id FROM conversations WHERE id=$1 AND user_id=$2 FOR UPDATE`, conversationID, userID).Scan(&id); err != nil {
		return nil, "", err
	}
	var busy bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM chat_turns WHERE conversation_id=$1 AND status='executing')`, conversationID).Scan(&busy); err != nil {
		return nil, "", err
	}
	if busy {
		return nil, "", errChatTurnBusy
	}
	var role string
	var created time.Time
	if err := tx.QueryRow(ctx, `SELECT role,created_at FROM messages WHERE conversation_id=$1 AND id=$2`, conversationID, messageID).Scan(&role, &created); err != nil {
		return nil, "", err
	}
	var question, answer string
	err = tx.QueryRow(ctx, `SELECT user_message_id,assistant_message_id FROM chat_turns WHERE conversation_id=$1 AND (user_message_id=$2 OR assistant_message_id=$2)`, conversationID, messageID).Scan(&question, &answer)
	if errors.Is(err, pgx.ErrNoRows) {
		// Legacy transcripts have no recorded pairing. Keep the previous time
		// heuristic only among legacy messages so it cannot cross a known turn.
		partner := `SELECT m.id FROM messages m WHERE m.conversation_id=$1 AND m.role='assistant' AND m.created_at >= $2 AND m.id <> $3
		AND NOT EXISTS(SELECT 1 FROM chat_turns t WHERE t.conversation_id=$1 AND (t.user_message_id=m.id OR t.assistant_message_id=m.id)) ORDER BY m.created_at ASC,m.id ASC LIMIT 1`
		if role == "assistant" {
			partner = `SELECT m.id FROM messages m WHERE m.conversation_id=$1 AND m.role='user' AND m.created_at <= $2 AND m.id <> $3
			AND NOT EXISTS(SELECT 1 FROM chat_turns t WHERE t.conversation_id=$1 AND (t.user_message_id=m.id OR t.assistant_message_id=m.id)) ORDER BY m.created_at DESC,m.id DESC LIMIT 1`
		}
		question = messageID
		err = tx.QueryRow(ctx, partner, conversationID, created, messageID).Scan(&answer)
		if errors.Is(err, pgx.ErrNoRows) {
			err = nil
		}
	}
	if err != nil {
		return nil, "", err
	}
	rows, err := tx.Query(ctx, `DELETE FROM messages WHERE conversation_id=$1 AND (id=$2 OR id=$3) RETURNING id`, conversationID, question, answer)
	if err != nil {
		return nil, "", err
	}
	deleted := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, "", err
		}
		deleted = append(deleted, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, "", err
	}
	return deleted, role, nil
}
