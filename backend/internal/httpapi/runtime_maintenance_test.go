package httpapi

import (
	"context"
	"cosmo/backend/internal/runs"
	"errors"
	"testing"
	"time"
)

func TestRuntimeCapacityAndRetentionPreserveReceipts(t *testing.T) {
	s, agent, owner, _ := agentAccessFixture(t)
	s.runs = runs.NewRepository(s.db)
	s.cfg.WorkspaceQueueLimit = 1
	ctx := context.Background()
	con := "con_" + randomID(18)
	if _, err := s.db.Exec(ctx, `INSERT INTO conversations(id,user_id,workspace_id,title) VALUES($1,$2,$3,'Queue test')`, con, owner.ID, agent.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	question := Message{ID: "msg_" + randomID(18), ConversationID: con, Role: "user", Content: "saved", CreatedAt: time.Now()}
	input := runs.NewRun{WorkspaceID: agent.WorkspaceID, ActorUserID: owner.ID, ResourceType: "conversation", ResourceID: con}
	identity := chatTurnIdentity{ClientMessageID: "first", RequestHash: "hash", AssistantID: "msg_" + randomID(18), Payload: []byte(`{"content":"saved"}`)}
	if _, _, _, err := s.acceptChatQuestion(ctx, question, input, nil, identity); err != nil {
		t.Fatal(err)
	}
	second := identity
	second.ClientMessageID = "second"
	if _, _, _, err := s.acceptChatQuestion(ctx, question, input, nil, second); !errors.Is(err, errRuntimeCapacity) {
		t.Fatalf("capacity not enforced: %v", err)
	}
	var existing *chatTurn
	if _, _, _, err := s.acceptChatQuestion(ctx, question, input, nil, identity); !errors.As(err, &existing) {
		t.Fatalf("retry rejected at capacity: %v", err)
	}
	if _, err := s.db.Exec(ctx, `UPDATE chat_turns SET status='interrupted',finished_at=NOW()-INTERVAL '40 days' WHERE conversation_id=$1`, con); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO chat_turn_events(conversation_id,client_message_id,frame) VALUES($1,'first','sensitive frame')`, con); err != nil {
		t.Fatal(err)
	}
	wf := "wf_" + randomID(18)
	if _, err := s.db.Exec(ctx, `INSERT INTO workflows(id,name,owner_user_id,owner_workspace_id) VALUES($1,'Retention',$2,$3)`, wf, owner.ID, agent.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"succeeded", "interrupted", "waiting_approval"} {
		if _, err := s.db.Exec(ctx, `INSERT INTO workflow_executions(id,workflow_id,actor_id,workspace_id,input,model,runtime_hash,completed,status,lease_owner,lease_until,finished_at) VALUES($1,$2,$3,$4,'saved','model','hash','{"step":{"status":"complete","output":"saved"}}',$5,'',NOW(),NOW()-INTERVAL '40 days')`, wf+state, wf, owner.ID, agent.WorkspaceID, state); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(ctx, `INSERT INTO workflow_execution_events(execution_id,frame) VALUES($1,'frame')`, wf+state); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.pruneRuntimeHistory(ctx, time.Now().Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var events, receipts, messages int
	if err := s.db.QueryRow(ctx, `SELECT (SELECT count(*) FROM chat_turn_events WHERE conversation_id=$1),(SELECT count(*) FROM chat_turns WHERE conversation_id=$1),(SELECT count(*) FROM messages WHERE conversation_id=$1)`, con).Scan(&events, &receipts, &messages); err != nil {
		t.Fatal(err)
	}
	if events != 0 || receipts != 1 || messages != 1 {
		t.Fatalf("retention damaged receipt/transcript: %d/%d/%d", events, receipts, messages)
	}
	for _, state := range []string{"succeeded", "interrupted", "waiting_approval"} {
		var kept string
		if err := s.db.QueryRow(ctx, `SELECT input FROM workflow_executions WHERE id=$1`, wf+state).Scan(&kept); err != nil {
			t.Fatal(err)
		}
		if (state == "succeeded" && kept != "") || (state != "succeeded" && kept != "saved") {
			t.Fatalf("checkpoint retention for %s: %q", state, kept)
		}
	}
	if _, _, _, err := s.acceptChatQuestion(ctx, question, input, nil, identity); !errors.As(err, &existing) {
		t.Fatalf("receipt lost after pruning: %v", err)
	}
}
