package database

var workflowCheckpointStatements = []string{
	`CREATE TABLE workflow_executions(id TEXT PRIMARY KEY,workflow_id TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,actor_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,input TEXT NOT NULL,model TEXT NOT NULL,runtime_hash TEXT NOT NULL,completed JSONB NOT NULL DEFAULT '{}'::jsonb,active_node TEXT NOT NULL DEFAULT '',approval_id TEXT NOT NULL DEFAULT '',status TEXT NOT NULL CHECK(status IN ('running','succeeded','failed','interrupted')),lease_owner TEXT NOT NULL,lease_until TIMESTAMPTZ NOT NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),finished_at TIMESTAMPTZ)`,
	`CREATE INDEX workflow_execution_scope ON workflow_executions(actor_id,workspace_id,workflow_id,created_at DESC)`,
	`CREATE UNIQUE INDEX workflow_execution_active ON workflow_executions(actor_id,workspace_id,workflow_id) WHERE status='running'`,
}
