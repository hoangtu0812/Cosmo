package database

// Migration 17: an audit row that still answers the question a year later.
//
// The table held who-by-id, what, and when. Three things were missing, and each
// of them is the first thing an investigation asks for:
//
//   - Where. Nothing said which workspace an action happened in, so "what has
//     been done in this workspace" was unanswerable and the console could not
//     group by the unit people actually work in.
//   - Who, after the fact. actor_user_id is a foreign key that nulls on delete,
//     so removing an account erased its author from every record it left
//     behind. The email and name are snapshotted at write time instead: an
//     audit row describes a moment, and a moment does not change when the
//     directory does.
//   - From where, and did it work. No address, no user agent, and no outcome -
//     so a failed sign-in was not recorded at all, which is the single event a
//     security review looks for first.
//
// workspace_id is deliberately a plain column with no foreign key, for the same
// reason as the actor snapshot: deleting a workspace must not quietly rewrite
// the record of it being deleted.
var auditContextStatements = []string{
	`ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS actor_email TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS actor_name TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS workspace_id TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS workspace_name TEXT NOT NULL DEFAULT ''`,
	// What the target was called when it was touched. An id alone reads as
	// noise once the row it points at has been deleted - which is exactly the
	// case a deletion record has to survive.
	`ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS target_label TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS outcome TEXT NOT NULL DEFAULT 'success'`,
	`ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS ip_address TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS user_agent TEXT NOT NULL DEFAULT ''`,
	// The request id the rest of the logs carry, so an audit row can be lined
	// up against what the server was doing at the time.
	`ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS request_id TEXT NOT NULL DEFAULT ''`,

	// Rows written before the snapshot existed still have their actor, so take
	// the identity from the directory once rather than joining for it forever.
	`UPDATE audit_logs a
	 SET actor_email = u.email, actor_name = u.name
	 FROM users u
	 WHERE u.id = a.actor_user_id AND a.actor_email = ''`,

	// The console filters by action, by actor and by workspace, and always in
	// time order. Without these each filter is a sequential scan of the whole
	// table, which is the one table that only ever grows.
	`CREATE INDEX IF NOT EXISTS idx_audit_logs_action ON audit_logs(action, created_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_logs_actor ON audit_logs(actor_user_id, created_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_logs_workspace ON audit_logs(workspace_id, created_at DESC)`,

	// The activity dashboard reads runs, steps and messages by time across
	// every workspace at once. The existing indexes are all workspace-first,
	// which answers "this workspace lately" and not "everyone, lately".
	`CREATE INDEX IF NOT EXISTS idx_runs_created ON runs(created_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_run_steps_type_created ON run_steps(type, created_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_messages_created ON messages(created_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_conversations_created ON conversations(created_at DESC)`,
}
