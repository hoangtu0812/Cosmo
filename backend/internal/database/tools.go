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
