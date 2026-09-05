package database

var knowledgeIngestionStatements = []string{
	`ALTER TABLE knowledge_bases ADD COLUMN live_index_id TEXT NOT NULL DEFAULT '', ADD COLUMN live_index_settings JSONB NOT NULL DEFAULT '{}'::jsonb`,
	`CREATE TABLE knowledge_ingestion_jobs (
 id TEXT PRIMARY KEY,kb_id TEXT NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
 requested_by TEXT REFERENCES users(id) ON DELETE SET NULL,manifest TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'queued' CHECK(status IN ('uploading','queued','running','succeeded','failed')),
 attempts INTEGER NOT NULL DEFAULT 0,attempt_id TEXT NOT NULL DEFAULT '',lease_owner TEXT NOT NULL DEFAULT '',
 lease_expires_at TIMESTAMPTZ,next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 error_code TEXT NOT NULL DEFAULT '',created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),finished_at TIMESTAMPTZ)`,
	`CREATE UNIQUE INDEX active_ingestion_job ON knowledge_ingestion_jobs(kb_id) WHERE status IN ('uploading','queued','running')`,
	`CREATE INDEX ingestion_job_queue ON knowledge_ingestion_jobs(next_attempt_at,created_at) WHERE status IN ('uploading','queued','running')`,
	`CREATE TRIGGER cleanup_ingestion_job_attempt BEFORE DELETE ON knowledge_ingestion_jobs FOR EACH ROW EXECUTE FUNCTION cleanup_snapshot_job_attempt()`,
	`CREATE FUNCTION cleanup_live_knowledge_index() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF OLD.live_index_id<>'' THEN INSERT INTO knowledge_snapshot_cleanup(snapshot_id,next_attempt_at) VALUES(OLD.live_index_id,NOW()+INTERVAL '1 hour') ON CONFLICT DO NOTHING; END IF;
 RETURN OLD; END $$`,
	`CREATE TRIGGER cleanup_live_knowledge_index BEFORE DELETE ON knowledge_bases FOR EACH ROW EXECUTE FUNCTION cleanup_live_knowledge_index()`,
}
