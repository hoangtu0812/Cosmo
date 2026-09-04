package httpapi

import (
	"context"
	"fmt"

	"cosmo/backend/internal/modelgateway"
)

const chatHistoryMessages = 40

// recordChatQuestion reads prior messages and inserts this question in one
// statement. PostgreSQL gives both operations the same snapshot: another
// request arriving while this turn plans or retrieves cannot enter its history.
// The inserted question is not visible to the SELECT in that snapshot, so it
// is appended explicitly, always last and exactly once.
func (s *Server) recordChatQuestion(ctx context.Context, question Message) ([]modelgateway.Message, error) {
	rows, err := s.db.Query(ctx, `
		WITH inserted_question AS (
			INSERT INTO messages(id, conversation_id, role, content, created_at)
			VALUES ($1, $2, 'user', $3, $4)
			RETURNING id
		)
		SELECT role, content FROM (
			SELECT id, role, content, created_at FROM messages
			WHERE conversation_id = $2
			ORDER BY created_at DESC, id DESC LIMIT $5
		) recent ORDER BY created_at ASC, id ASC`,
		question.ID, question.ConversationID, question.Content, question.CreatedAt, chatHistoryMessages-1)
	if err != nil {
		return nil, fmt.Errorf("record chat question: %w", err)
	}
	defer rows.Close()
	history := make([]modelgateway.Message, 0, chatHistoryMessages)
	for rows.Next() {
		var message modelgateway.Message
		if err := rows.Scan(&message.Role, &message.Content); err != nil {
			return nil, fmt.Errorf("read chat history: %w", err)
		}
		history = append(history, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read chat history: %w", err)
	}
	return append(history, modelgateway.Message{Role: "user", Content: question.Content}), nil
}
