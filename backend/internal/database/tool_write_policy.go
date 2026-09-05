package database

var toolWritePolicyStatements = []string{
	`CREATE TABLE tool_action_policies(action_id TEXT PRIMARY KEY REFERENCES tool_actions(id) ON DELETE CASCADE,tool_id TEXT NOT NULL REFERENCES tools(id) ON DELETE CASCADE,definition_hash TEXT NOT NULL,effect TEXT NOT NULL CHECK(effect IN ('read','approval','blocked')),updated_by TEXT REFERENCES users(id) ON DELETE SET NULL,updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
	`CREATE TABLE tool_write_operations(id TEXT PRIMARY KEY,tool_id TEXT NOT NULL REFERENCES tools(id) ON DELETE CASCADE,action_id TEXT NOT NULL,actor_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,idempotency_key TEXT NOT NULL,request_hash TEXT NOT NULL,status TEXT NOT NULL CHECK(status IN ('executing','succeeded','uncertain','reconciled_succeeded','reconciled_no_effect')),result JSONB NOT NULL DEFAULT '{}'::jsonb,created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),finished_at TIMESTAMPTZ,reconciliation_note TEXT NOT NULL DEFAULT '',UNIQUE(actor_id,workspace_id,idempotency_key))`,
	`CREATE INDEX tool_write_operation_review ON tool_write_operations(tool_id,actor_id,workspace_id,created_at DESC)`,
}
