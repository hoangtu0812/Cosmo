package database

var chatTurnStatements = []string{
	`CREATE TABLE chat_turns (
		conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
		client_message_id TEXT NOT NULL,
		request_hash TEXT NOT NULL,
		sequence BIGINT GENERATED ALWAYS AS IDENTITY,
		user_message_id TEXT NOT NULL,
		assistant_message_id TEXT NOT NULL,
		run_id TEXT NOT NULL,
		status TEXT NOT NULL CHECK(status IN ('executing','succeeded','interrupted')),
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		finished_at TIMESTAMPTZ,
		PRIMARY KEY(conversation_id,client_message_id),
		UNIQUE(conversation_id,sequence)
	)`,
	// Keep identity tombstones when messages/runs are deleted: deleting an
	// answer must not allow a retried request to execute external actions again.
	`CREATE UNIQUE INDEX chat_turns_one_executing ON chat_turns(conversation_id) WHERE status='executing'`,
}
