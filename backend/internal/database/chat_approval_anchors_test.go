package database

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestApprovalAnchorBackfillUsesTurnAndEventOrdering(t *testing.T) {
	url := os.Getenv("COSMO_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("requires PostgreSQL")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	// Shadow only these names on this connection; no application rows change.
	_, err = conn.Exec(ctx, `CREATE TEMP TABLE tool_approvals(id TEXT,source_kind TEXT,source_id TEXT,message_id TEXT DEFAULT '',call_id TEXT DEFAULT '');
CREATE TEMP TABLE chat_turn_events(id BIGSERIAL,conversation_id TEXT,client_message_id TEXT,frame TEXT);
CREATE TEMP TABLE chat_turns(conversation_id TEXT,client_message_id TEXT,assistant_message_id TEXT);
INSERT INTO tool_approvals(id,source_kind,source_id) VALUES('approval-old','conversation','con'),('approval-new','conversation','con'),('unmatched','conversation','con');
INSERT INTO chat_turns VALUES('con','turn-old','answer-old'),('con','turn-new','answer-new');
INSERT INTO chat_turn_events(conversation_id,client_message_id,frame) VALUES
('con','turn-old',E'event: tool\ndata: {"id":"reused-call"}\n\n'),
('con','turn-old',E'event: approval\ndata: {"id":"approval-old"}\n\n'),
('con','turn-new',E'event: tool\ndata: {"id":"reused-call"}\n\n'),
('con','turn-new',E'event: approval\ndata: {"id":"approval-new"}\n\n'),
('con','turn-new',E'event: tool\ndata: {"id":"clock"}\n\n');`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, chatApprovalAnchorStatements[1]); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{"approval-old": "answer-old/reused-call", "approval-new": "answer-new/reused-call", "unmatched": "/"} {
		var got string
		if err = conn.QueryRow(ctx, `SELECT message_id||'/'||call_id FROM tool_approvals WHERE id=$1`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s: %q want %q", id, got, want)
		}
	}
}
