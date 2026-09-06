package database

var chatApprovalCheckpointStatements = []string{
	`ALTER TABLE chat_turns DROP CONSTRAINT chat_turns_status_check`,
	`ALTER TABLE chat_turns ADD CONSTRAINT chat_turns_status_check CHECK(status IN ('queued','executing','waiting_approval','succeeded','interrupted'))`,
	`CREATE TABLE chat_approval_checkpoints(run_id TEXT PRIMARY KEY REFERENCES runs(id) ON DELETE CASCADE,conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,approval_id TEXT REFERENCES tool_approvals(id) ON DELETE SET NULL,state JSONB NOT NULL CHECK(octet_length(state::text)<=4194304),created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
	`CREATE UNIQUE INDEX chat_checkpoint_approval ON chat_approval_checkpoints(approval_id) WHERE approval_id IS NOT NULL`,
	`CREATE INDEX chat_turn_waiting ON chat_turns(sequence) WHERE status='waiting_approval'`,
}
