package httpapi

import (
	"context"
	"fmt"

	"cosmo/backend/internal/modelgateway"
	"cosmo/backend/internal/runs"
)

// acceptChatQuestion commits the question, attachment claims and run together.
// No model/tool call runs inside this short transaction. The conversation row
// orders admission and protects pending attachment claims between writers.
func (s *Server) acceptChatQuestion(ctx context.Context, question Message, runInput runs.NewRun, attachments []string, identity chatTurnIdentity) ([]modelgateway.Message, runs.Run, bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, runs.Run{}, false, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var id string
	if err := tx.QueryRow(ctx, `SELECT id FROM conversations WHERE id=$1 AND user_id=$2 FOR UPDATE`, question.ConversationID, runInput.ActorUserID).Scan(&id); err != nil {
		return nil, runs.Run{}, false, err
	}
	if existing, err := lookupChatTurn(ctx, tx, id, identity.ClientMessageID, identity.RequestHash); err != nil {
		return nil, runs.Run{}, false, err
	} else if existing != nil {
		return nil, runs.Run{}, false, existing
	}
	var first bool
	if err := tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM messages WHERE conversation_id=$1)`, id).Scan(&first); err != nil {
		return nil, runs.Run{}, false, err
	}
	history, err := recordChatQuestion(ctx, tx, question)
	if err != nil {
		return nil, runs.Run{}, false, err
	}
	if len(attachments) > 0 {
		tag, err := tx.Exec(ctx, `UPDATE conversation_attachments SET message_id=$2 WHERE id=ANY($1) AND conversation_id=$3 AND message_id IS NULL`, attachments, question.ID, id)
		if err != nil {
			return nil, runs.Run{}, false, err
		}
		if tag.RowsAffected() != int64(len(attachments)) {
			return nil, runs.Run{}, false, fmt.Errorf("attachment claim conflict")
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE conversations SET title=CASE WHEN title='Cuộc trò chuyện mới' THEN LEFT($2,100) ELSE title END, updated_at=NOW() WHERE id=$1`, id, question.Content); err != nil {
		return nil, runs.Run{}, false, err
	}
	run, created, err := s.runs.CreateTx(ctx, tx, runInput)
	if err != nil {
		return nil, runs.Run{}, false, err
	}
	if !created {
		return nil, runs.Run{}, false, fmt.Errorf("run identity already exists")
	}
	if len(identity.Payload) == 0 {
		identity.Payload = []byte(`{}`)
	}
	if identity.ReadableIDs == nil {
		identity.ReadableIDs = []string{}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO chat_turns(conversation_id,client_message_id,request_hash,user_message_id,assistant_message_id,run_id,status,request_payload,runtime_hash,readable_ids,is_first_turn) VALUES($1,$2,$3,$4,$5,$6,'queued',$7,$8,$9,$10)`, id, identity.ClientMessageID, identity.RequestHash, question.ID, identity.AssistantID, run.ID, identity.Payload, identity.RuntimeHash, identity.ReadableIDs, first); err != nil {
		return nil, runs.Run{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, runs.Run{}, false, err
	}
	return history, run, first, nil
}
