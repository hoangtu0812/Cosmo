package database

var inlineToolApprovalStatements = []string{
	`CREATE TABLE tool_approvals(id TEXT PRIMARY KEY,actor_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,tool_id TEXT NOT NULL REFERENCES tools(id) ON DELETE CASCADE,source_kind TEXT NOT NULL CHECK(source_kind IN ('conversation','workflow')),source_id TEXT NOT NULL,request JSONB NOT NULL,status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','approved','rejected','expired','completed','uncertain','failed')),operation_id TEXT NOT NULL DEFAULT '',created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),expires_at TIMESTAMPTZ NOT NULL,lease_until TIMESTAMPTZ NOT NULL DEFAULT NOW()+INTERVAL '5 seconds',decided_at TIMESTAMPTZ)`,
	`CREATE INDEX tool_approval_scope ON tool_approvals(actor_id,workspace_id,source_kind,source_id,created_at DESC)`,
}
