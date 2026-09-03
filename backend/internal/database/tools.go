package database

// A tool is an HTTP integration the workspace can call: one base URL, one set
// of credentials, and the actions available underneath it. The reference
// models it the same way - a plugin holds a base URL and the endpoints beneath
// it - which keeps a credential in one place rather than repeated per action.
//
// The credential is stored sealed. The column holds ciphertext produced by
// internal/secrets, so a database dump does not hand over the workspace's API
// keys, and a hint is kept alongside so the interface can show which key is
// configured without opening it.
var toolStatements = []string{
	`CREATE TABLE IF NOT EXISTS tools (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		icon TEXT NOT NULL DEFAULT '',
		tags JSONB NOT NULL DEFAULT '[]'::jsonb,
		owner_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
		owner_workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
		visibility TEXT NOT NULL DEFAULT 'private' CHECK (visibility IN ('private', 'workspace')),
		base_url TEXT NOT NULL DEFAULT '',
		auth_type TEXT NOT NULL DEFAULT 'none' CHECK (auth_type IN ('none', 'bearer', 'header')),
		auth_header_name TEXT NOT NULL DEFAULT '',
		auth_secret BYTEA,
		auth_hint TEXT NOT NULL DEFAULT '',
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE INDEX IF NOT EXISTS tools_workspace_idx ON tools (owner_workspace_id, updated_at DESC)`,

	// An action is what a model actually calls. The parameter list is stored as
	// JSON because it is described, not queried: it becomes the JSON Schema in
	// the tool definition handed to the model.
	`CREATE TABLE IF NOT EXISTS tool_actions (
		id TEXT PRIMARY KEY,
		tool_id TEXT NOT NULL REFERENCES tools(id) ON DELETE CASCADE,
		name TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		method TEXT NOT NULL DEFAULT 'GET' CHECK (method IN ('GET', 'POST', 'PUT', 'PATCH', 'DELETE')),
		path TEXT NOT NULL DEFAULT '/',
		parameters JSONB NOT NULL DEFAULT '[]'::jsonb,
		position INTEGER NOT NULL DEFAULT 0,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE INDEX IF NOT EXISTS tool_actions_tool_idx ON tool_actions (tool_id, position, created_at)`,
	// A name is what the model calls, so it has to be unique within its tool.
	`CREATE UNIQUE INDEX IF NOT EXISTS tool_actions_name_idx ON tool_actions (tool_id, lower(name))`,

	// Which tools an agent may call. Mirrors agent_knowledge_bases, so both
	// kinds of attachment are read and written the same way.
	`CREATE TABLE IF NOT EXISTS agent_tools (
		agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
		tool_id TEXT NOT NULL REFERENCES tools(id) ON DELETE CASCADE,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY (agent_id, tool_id)
	)`,
	`CREATE INDEX IF NOT EXISTS agent_tools_tool_idx ON agent_tools (tool_id)`,
}

// A published version freezes which tools it may call, exactly as it already
// freezes which knowledge bases it may read: a conversation pinned to an old
// version must not pick up a tool attached since. Its own migration because
// the one above has already been applied, and an applied migration is never
// edited - the checksum exists to catch exactly that.
var agentVersionToolStatements = []string{
	`ALTER TABLE agent_versions ADD COLUMN IF NOT EXISTS tool_ids JSONB NOT NULL DEFAULT '[]'::jsonb`,
}

// A tool is either an HTTP API described by hand or an MCP server that
// describes itself. They differ only in how a call is made, so they share a
// table and are told apart by a column.
var toolKindStatements = []string{
	`ALTER TABLE tools ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'http'`,
	`ALTER TABLE tools DROP CONSTRAINT IF EXISTS tools_kind_check`,
	`ALTER TABLE tools ADD CONSTRAINT tools_kind_check CHECK (kind IN ('http', 'mcp'))`,
}

// A third kind: one that reaches nothing at all and does its work in this
// process. Arithmetic and the clock are the two a model most reliably gets
// wrong on its own. Its own migration for the usual reason - the one above is
// already applied.
var builtinKindStatements = []string{
	`ALTER TABLE tools DROP CONSTRAINT IF EXISTS tools_kind_check`,
	`ALTER TABLE tools ADD CONSTRAINT tools_kind_check CHECK (kind IN ('http', 'mcp', 'builtin'))`,
}

// Remembering which catalogue entry a tool came from is what stops the same
// one being installed twice. Without it, clicking Install again produced a
// second identical tool - and an agent attached to both handed the model two
// tools with the same name, which is worse than either.
//
// Unique per workspace rather than globally: two workspaces installing the
// same entry are two separate tools, each with its own credential.
var toolCatalogStatements = []string{
	`ALTER TABLE tools ADD COLUMN IF NOT EXISTS catalog_id TEXT NOT NULL DEFAULT ''`,
	`CREATE UNIQUE INDEX IF NOT EXISTS tools_catalog_idx
		ON tools (owner_workspace_id, catalog_id) WHERE catalog_id <> ''`,
}

// What a turn called is evidence for its answer, the same as a citation, and
// belongs on the message rather than only in the run log the inspector reads.
var messageToolCallStatements = []string{
	`ALTER TABLE messages ADD COLUMN IF NOT EXISTS tool_calls JSONB NOT NULL DEFAULT '[]'::jsonb`,
}

// An action says what it takes but not what it gives back, so a model calls,
// receives a wall of JSON and guesses. These two carry the answer into the
// description it reads before deciding.
var actionResultStatements = []string{
	`ALTER TABLE tool_actions ADD COLUMN IF NOT EXISTS result_type TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE tool_actions ADD COLUMN IF NOT EXISTS result_description TEXT NOT NULL DEFAULT ''`,
}

// A workflow is an ordered graph of steps, stored whole rather than as rows
// per node: it is edited as one document, saved as one document, and nothing
// queries inside it. Splitting it into tables would buy joins nobody makes.
var workflowStatements = []string{
	`CREATE TABLE IF NOT EXISTS workflows (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		icon TEXT NOT NULL DEFAULT '',
		owner_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
		owner_workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
		visibility TEXT NOT NULL DEFAULT 'private' CHECK (visibility IN ('private', 'workspace')),
		graph JSONB NOT NULL DEFAULT '{"nodes":[],"edges":[]}'::jsonb,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE INDEX IF NOT EXISTS workflows_workspace_idx ON workflows(owner_workspace_id, updated_at DESC)`,
}

// Installing a tool into a workspace, so a plain chat can call it - the same
// two-level shape knowledge bases already use, and for the same reason: the
// workspace decides what is available, an agent narrows that to what it needs.
//
// Three rules the user settled, each with a home here:
//   - Installing is not permission to call. `auto_call` is the separate flag,
//     and it is per install: workspace A may want a tool answering questions
//     while workspace B keeps it for its agents only.
//   - Only what an owner has offered can be installed, which is the same
//     visibility ladder a knowledge base climbs - hence 'selected' and
//     'everyone' joining the two values tools already had.
//   - A tool holding a credential may not be called automatically. Enforced on
//     write here and again on read, because a tool can be given a key after it
//     was installed and must stop answering questions when it is.
var workspaceToolStatements = []string{
	`ALTER TABLE tools DROP CONSTRAINT IF EXISTS tools_visibility_check`,
	`ALTER TABLE tools ADD CONSTRAINT tools_visibility_check
		CHECK (visibility IN ('private', 'workspace', 'selected', 'everyone'))`,

	`CREATE TABLE IF NOT EXISTS tool_shares (
		tool_id TEXT NOT NULL REFERENCES tools(id) ON DELETE CASCADE,
		workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY (tool_id, workspace_id)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_tool_shares_workspace ON tool_shares(workspace_id)`,

	`CREATE TABLE IF NOT EXISTS workspace_tools (
		workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
		tool_id TEXT NOT NULL REFERENCES tools(id) ON DELETE CASCADE,
		auto_call BOOLEAN NOT NULL DEFAULT FALSE,
		installed_by TEXT REFERENCES users(id) ON DELETE SET NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY (workspace_id, tool_id)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_workspace_tools_workspace ON workspace_tools(workspace_id)`,
}

// Migration 13: a tool can be published, and what was published stays put.
//
// Editing a tool changed it for every agent already built on it, at once and
// without warning - a renamed action or a changed path broke a published agent
// mid-conversation. A version freezes the callable surface: where the tool
// lives, how it authenticates, and the actions as they read at that moment.
//
// The credential is deliberately not part of it. A key is current state, not
// a description of the tool, and a snapshot that carried one would resurrect a
// revoked key on rollback.
var toolVersionStatements = []string{
	`CREATE TABLE IF NOT EXISTS tool_versions (
		id TEXT PRIMARY KEY,
		tool_id TEXT NOT NULL REFERENCES tools(id) ON DELETE CASCADE,
		version_number INTEGER NOT NULL,
		base_url TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL DEFAULT 'http',
		auth_type TEXT NOT NULL DEFAULT 'none',
		auth_header_name TEXT NOT NULL DEFAULT '',
		actions JSONB NOT NULL DEFAULT '[]'::jsonb,
		changelog TEXT NOT NULL DEFAULT '',
		published_by TEXT REFERENCES users(id) ON DELETE SET NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		UNIQUE (tool_id, version_number)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_tool_versions_tool ON tool_versions(tool_id, version_number DESC)`,

	// Which version an agent is calling. NULL means the tool has never been
	// published, and callers fall back to the draft - which is how every tool
	// behaved until now, so nothing that already works stops working.
	`ALTER TABLE tools ADD COLUMN IF NOT EXISTS published_version_id TEXT REFERENCES tool_versions(id) ON DELETE SET NULL`,

	// What each published agent pinned: tool id to tool version id, decided
	// when the agent was published. An agent published before this column
	// existed pins nothing and keeps reading the live tool.
	`ALTER TABLE agent_versions ADD COLUMN IF NOT EXISTS tool_versions JSONB NOT NULL DEFAULT '{}'::jsonb`,
}

// Migration 14: what a workspace wants said in every answer.
//
// One workspace is a refinery's IT desk and the next is a finance team; the
// same question deserves different footing in each. This is where a workspace
// writes that footing down once, rather than every member repeating it in
// every conversation.
var workspaceContextStatements = []string{
	`ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS context TEXT NOT NULL DEFAULT ''`,
}

// Migration 15: files attached to a question.
//
// The text is stored rather than the file: it is what the model reads and what
// a reader wants to see again. The original is not kept, because keeping it
// would make this a document store, and Cosmo already has one - a file worth
// keeping belongs in a knowledge base.
var conversationAttachmentStatements = []string{
	`CREATE TABLE IF NOT EXISTS conversation_attachments (
		id TEXT PRIMARY KEY,
		conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
		message_id TEXT,
		user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
		name TEXT NOT NULL,
		mime TEXT NOT NULL DEFAULT '',
		byte_size BIGINT NOT NULL DEFAULT 0,
		text TEXT NOT NULL DEFAULT '',
		is_truncated BOOLEAN NOT NULL DEFAULT FALSE,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE INDEX IF NOT EXISTS idx_conversation_attachments_conversation
		ON conversation_attachments(conversation_id, created_at)`,
	`CREATE INDEX IF NOT EXISTS idx_conversation_attachments_message
		ON conversation_attachments(message_id)`,
}
