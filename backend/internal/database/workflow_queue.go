package database

var workflowQueueStatements = []string{
	`ALTER TABLE workflow_executions DROP CONSTRAINT workflow_executions_status_check`,
	`ALTER TABLE workflow_executions ADD CONSTRAINT workflow_executions_status_check CHECK(status IN ('queued','running','succeeded','failed','interrupted'))`,
	`DROP INDEX workflow_execution_active`,
	`CREATE UNIQUE INDEX workflow_execution_active ON workflow_executions(actor_id,workspace_id,workflow_id) WHERE status IN ('queued','running')`,
	`CREATE TABLE workflow_execution_requests(actor_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,request_key TEXT NOT NULL,execution_id TEXT NOT NULL REFERENCES workflow_executions(id) ON DELETE CASCADE,request_hash TEXT NOT NULL,PRIMARY KEY(actor_id,workspace_id,request_key))`,
	`CREATE INDEX workflow_execution_queue ON workflow_executions(created_at) WHERE status='queued'`,
	`CREATE TABLE workflow_execution_events(id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,execution_id TEXT NOT NULL REFERENCES workflow_executions(id) ON DELETE CASCADE,frame TEXT NOT NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
	`CREATE INDEX workflow_execution_event_replay ON workflow_execution_events(execution_id,id)`,
}
