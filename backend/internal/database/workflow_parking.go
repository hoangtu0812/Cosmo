package database

var workflowParkingStatements = []string{
	`ALTER TABLE workflow_executions DROP CONSTRAINT workflow_executions_status_check`,
	`ALTER TABLE workflow_executions ADD CONSTRAINT workflow_executions_status_check CHECK(status IN ('queued','running','waiting_approval','succeeded','failed','interrupted','cancelled'))`,
	`ALTER TABLE workflow_executions ADD COLUMN deadline TIMESTAMPTZ`,
	`DROP INDEX workflow_execution_active`,
	`CREATE UNIQUE INDEX workflow_execution_active ON workflow_executions(actor_id,workspace_id,workflow_id) WHERE status IN ('queued','running','waiting_approval')`,
	`CREATE INDEX workflow_execution_waiting ON workflow_executions(approval_id) WHERE status='waiting_approval'`,
}
