package httpapi

import (
	"context"
	"testing"
	"time"

	"cosmo/backend/internal/runs"
)

func TestChatAdmissionCommitsQuestionAttachmentAndRunTogether(t *testing.T) {
	s, agent, owner, _ := agentAccessFixture(t)
	s.runs = runs.NewRepository(s.db)
	ctx := context.Background()
	conversation, attachment := "con_"+randomID(18), "att_"+randomID(18)
	if _, err := s.db.Exec(ctx, `INSERT INTO conversations(id,user_id,workspace_id,title) VALUES($1,$2,$3,'Cuộc trò chuyện mới')`, conversation, owner.ID, agent.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO conversation_attachments(id,conversation_id,user_id,name) VALUES($1,$2,$3,'policy.txt')`, attachment, conversation, owner.ID); err != nil {
		t.Fatal(err)
	}
	question := Message{ID: "msg_" + randomID(18), ConversationID: conversation, Role: "user", Content: "Question", CreatedAt: time.Now()}
	input := runs.NewRun{WorkspaceID: agent.WorkspaceID, ActorUserID: owner.ID, ResourceType: "conversation", ResourceID: conversation}
	for _, failure := range []string{"run", "attachment"} {
		t.Run(failure, func(t *testing.T) {
			bad := input
			ids := []string{attachment}
			if failure == "run" {
				bad.WorkspaceID = "missing-workspace"
			} else {
				ids = append(ids, "missing-attachment")
			}
			if _, _, _, err := s.acceptChatQuestion(ctx, question, bad, ids); err == nil {
				t.Fatal("expected admission failure")
			}
			var messages, runCount int
			var title, claimed string
			if err := s.db.QueryRow(ctx, `SELECT (SELECT count(*) FROM messages WHERE conversation_id=$1),(SELECT count(*) FROM runs WHERE resource_id=$1),title FROM conversations WHERE id=$1`, conversation).Scan(&messages, &runCount, &title); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRow(ctx, `SELECT COALESCE(message_id,'') FROM conversation_attachments WHERE id=$1`, attachment).Scan(&claimed); err != nil {
				t.Fatal(err)
			}
			if messages != 0 || runCount != 0 || claimed != "" || title != "Cuộc trò chuyện mới" {
				t.Fatalf("partial admission survived rollback: messages=%d runs=%d claim=%s title=%s", messages, runCount, claimed, title)
			}
		})
	}
	history, run, first, err := s.acceptChatQuestion(ctx, question, input, []string{attachment})
	if err != nil {
		t.Fatal(err)
	}
	if !first || len(history) != 1 || history[0].Content != question.Content {
		t.Fatalf("invalid admitted context: %+v %v", history, first)
	}
	var claimed string
	if err := s.db.QueryRow(ctx, `SELECT message_id FROM conversation_attachments WHERE id=$1`, attachment).Scan(&claimed); err != nil || claimed != question.ID {
		t.Fatalf("attachment not claimed: %s %v", claimed, err)
	}
	var events int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM run_events WHERE run_id=$1 AND type='run.queued'`, run.ID).Scan(&events); err != nil || events != 1 {
		t.Fatalf("run event not committed: %d %v", events, err)
	}
}
