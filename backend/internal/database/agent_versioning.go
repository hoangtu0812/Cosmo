package database

// Agent versioning turns an agent from a row that is edited in place into an
// artifact with a lifecycle. The agents row keeps being the working copy - the
// draft - because that is what it already was; what it gains is a revision to
// detect concurrent edits and a pointer to the version currently published.
//
// A published version is an immutable snapshot. Editing the draft afterwards
// cannot reach it, which is the whole point: a conversation running on a
// release must not change under its reader because someone opened the editor.
//
// Existing agents are backfilled as published version 1. They are in use, and
// leaving them unpublished would strip working conversations of a definition
// to run.
var agentVersioningStatements = []string{
	`CREATE TABLE IF NOT EXISTS agent_versions (
		id TEXT PRIMARY KEY,
		agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
		version_number INTEGER NOT NULL,
		model TEXT NOT NULL DEFAULT '',
		system_prompt TEXT NOT NULL DEFAULT '',
		opening_line TEXT NOT NULL DEFAULT '',
		preset_questions JSONB NOT NULL DEFAULT '[]'::jsonb,
		has_suggested_questions BOOLEAN NOT NULL DEFAULT FALSE,
		is_memory_enabled BOOLEAN NOT NULL DEFAULT FALSE,
		knowledge_base_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
		changelog TEXT NOT NULL DEFAULT '',
		published_by TEXT REFERENCES users(id) ON DELETE SET NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		UNIQUE (agent_id, version_number)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_agent_versions_agent ON agent_versions(agent_id, version_number DESC)`,

	// The draft's revision guards against two editors overwriting each other:
	// a save carries the revision it read, and a stale one is refused.
	`ALTER TABLE agents ADD COLUMN IF NOT EXISTS draft_revision BIGINT NOT NULL DEFAULT 1`,
	// Which version is live. NULL means the agent has never been published and
	// exists only as a draft.
	`ALTER TABLE agents ADD COLUMN IF NOT EXISTS published_version_id TEXT REFERENCES agent_versions(id) ON DELETE SET NULL`,

	// A conversation records the version it started on, so an answer can be
	// traced to the exact configuration that produced it. Older conversations
	// keep NULL rather than being rewritten to claim a version they never ran.
	`ALTER TABLE conversations ADD COLUMN IF NOT EXISTS agent_version_id TEXT REFERENCES agent_versions(id) ON DELETE SET NULL`,

	// Backfill. The id is derived from the agent id so the statement is
	// idempotent: running it twice cannot create a second version 1.
	`INSERT INTO agent_versions (
		id, agent_id, version_number, model, system_prompt, opening_line,
		preset_questions, has_suggested_questions, is_memory_enabled,
		knowledge_base_ids, changelog, published_by, created_at
	)
	SELECT
		'agv_1_' || a.id, a.id, 1, a.model, a.system_prompt, a.opening_line,
		a.preset_questions, a.has_suggested_questions, a.is_memory_enabled,
		COALESCE((
			SELECT jsonb_agg(k.kb_id ORDER BY k.created_at)
			FROM agent_knowledge_bases k WHERE k.agent_id = a.id
		), '[]'::jsonb),
		'Bản đầu tiên, tạo từ cấu hình sẵn có.', a.owner_user_id, a.created_at
	FROM agents a
	ON CONFLICT (agent_id, version_number) DO NOTHING`,

	`UPDATE agents a SET published_version_id = v.id
	 FROM agent_versions v
	 WHERE v.agent_id = a.id AND v.version_number = 1 AND a.published_version_id IS NULL`,
}
