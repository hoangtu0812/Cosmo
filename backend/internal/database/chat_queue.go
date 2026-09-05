package database

var chatQueueStatements = []string{
	`ALTER TABLE chat_turns DROP CONSTRAINT chat_turns_status_check`,
	`ALTER TABLE chat_turns ADD CONSTRAINT chat_turns_status_check CHECK(status IN ('queued','executing','succeeded','interrupted'))`,
	`ALTER TABLE chat_turns ADD COLUMN request_payload JSONB NOT NULL DEFAULT '{}', ADD COLUMN runtime_hash TEXT NOT NULL DEFAULT '', ADD COLUMN readable_ids TEXT[] NOT NULL DEFAULT '{}', ADD COLUMN is_first_turn BOOLEAN NOT NULL DEFAULT false, ADD COLUMN lease_owner TEXT NOT NULL DEFAULT '', ADD COLUMN lease_expires_at TIMESTAMPTZ`,
	// Existing executing turns have no live lease from this runtime. Drain the
	// old server before migration; reconciliation never reruns their tools.
	`UPDATE chat_turns SET lease_expires_at=NOW() WHERE status='executing'`,
	`CREATE INDEX chat_turns_queue ON chat_turns(status,sequence) WHERE status IN ('queued','executing')`,
	`CREATE TABLE chat_turn_events (
		id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
		conversation_id TEXT NOT NULL,
		client_message_id TEXT NOT NULL,
		frame TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		FOREIGN KEY(conversation_id,client_message_id) REFERENCES chat_turns(conversation_id,client_message_id) ON DELETE CASCADE
	)`,
	`CREATE INDEX chat_turn_events_replay ON chat_turn_events(conversation_id,client_message_id,id)`,
}
