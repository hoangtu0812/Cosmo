package httpapi

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestDeleteChatTurnDoesNotConsumeAnotherTurnsAnswer(t *testing.T) {
	s, agent, owner, member := agentAccessFixture(t)
	ctx := context.Background()
	conversation := "con_" + randomID(18)
	if _, err := s.db.Exec(ctx, `INSERT INTO conversations(id,user_id,workspace_id,title) VALUES($1,$2,$3,'Test')`, conversation, owner.ID, agent.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	q1, q2, a2, legacy := "msg_"+randomID(18), "msg_"+randomID(18), "msg_"+randomID(18), "msg_"+randomID(18)
	if _, err := s.db.Exec(ctx, `INSERT INTO messages(id,conversation_id,role,content,created_at) VALUES($1,$5,'user','Interrupted',NOW()-interval '3 seconds'),($2,$5,'user','Second',NOW()-interval '2 seconds'),($3,$5,'assistant','Second answer',NOW()-interval '1 second'),($4,$5,'user','Legacy',NOW()-interval '4 seconds')`, q1, q2, a2, legacy, conversation); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO chat_turns(conversation_id,client_message_id,request_hash,user_message_id,assistant_message_id,run_id,status) VALUES($1,'first','hash',$2,'missing-answer','run-first','interrupted'),($1,'second','hash',$3,$4,'run-second','executing')`, conversation, q1, q2, a2); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.deleteChatMessages(ctx, owner.ID, conversation, q1); !errors.Is(err, errChatTurnBusy) {
		t.Fatalf("deleted during active execution: %v", err)
	}
	if _, _, err := s.deleteChatMessages(ctx, member.ID, conversation, q1); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("another user deleted messages: %v", err)
	}
	if _, err := s.db.Exec(ctx, `UPDATE chat_turns SET status='succeeded' WHERE conversation_id=$1 AND client_message_id='second'`, conversation); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{q1, legacy} {
		deleted, _, err := s.deleteChatMessages(ctx, owner.ID, conversation, id)
		if err != nil || len(deleted) != 1 || deleted[0] != id {
			t.Fatalf("deleted another turn via missing/legacy answer: %v %v", deleted, err)
		}
	}
	deleted, _, err := s.deleteChatMessages(ctx, owner.ID, conversation, a2)
	if err != nil || len(deleted) != 2 {
		t.Fatalf("did not delete exact pair: %v %v", deleted, err)
	}
	var remaining, identities int
	if err := s.db.QueryRow(ctx, `SELECT (SELECT count(*) FROM messages WHERE conversation_id=$1),(SELECT count(*) FROM chat_turns WHERE conversation_id=$1)`, conversation).Scan(&remaining, &identities); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 || identities != 2 {
		t.Fatalf("deletion lost idempotency tombstones: messages=%d turns=%d", remaining, identities)
	}
}
