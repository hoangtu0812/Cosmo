package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const migrationLockID int64 = 0x434F534D4F // COSMO

type Migration struct {
	Version    int64
	Name       string
	Statements []string
}

type MigrationStatus struct {
	Version   int64
	Name      string
	Checksum  string
	AppliedAt *time.Time
}

var migrations = []Migration{
	{Version: 1, Name: "baseline", Statements: baselineStatements},
	{Version: 2, Name: "run_engine_foundation", Statements: runEngineStatements},
	{Version: 3, Name: "agent_versioning", Statements: agentVersioningStatements},
	{Version: 4, Name: "tools", Statements: toolStatements},
	{Version: 5, Name: "agent_version_tools", Statements: agentVersionToolStatements},
	{Version: 6, Name: "tool_kind", Statements: toolKindStatements},
	{Version: 7, Name: "builtin_tool_kind", Statements: builtinKindStatements},
	{Version: 8, Name: "tool_catalog_origin", Statements: toolCatalogStatements},
	{Version: 9, Name: "message_tool_calls", Statements: messageToolCallStatements},
	{Version: 10, Name: "action_result_shape", Statements: actionResultStatements},
	{Version: 11, Name: "workflows", Statements: workflowStatements},
	{Version: 12, Name: "workspace_tools", Statements: workspaceToolStatements},
	{Version: 13, Name: "tool_versions", Statements: toolVersionStatements},
}

var runEngineStatements = []string{
	`CREATE TABLE IF NOT EXISTS runs (
		id TEXT PRIMARY KEY,
		workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
		project_id TEXT NOT NULL DEFAULT '',
		actor_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
		trigger_type TEXT NOT NULL DEFAULT 'manual',
		resource_type TEXT NOT NULL,
		resource_id TEXT NOT NULL,
		resource_version TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'waiting_approval', 'succeeded', 'failed', 'cancelled', 'timed_out')),
		input JSONB NOT NULL DEFAULT '{}'::jsonb,
		output JSONB NOT NULL DEFAULT '{}'::jsonb,
		error_code TEXT NOT NULL DEFAULT '',
		error_message TEXT NOT NULL DEFAULT '',
		idempotency_key TEXT NOT NULL DEFAULT '',
		trace_id TEXT NOT NULL DEFAULT '',
		next_event_sequence BIGINT NOT NULL DEFAULT 1,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		queued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		started_at TIMESTAMPTZ,
		finished_at TIMESTAMPTZ,
		cancelled_at TIMESTAMPTZ
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_runs_workspace_idempotency
	 ON runs(workspace_id, idempotency_key) WHERE idempotency_key <> ''`,
	`CREATE INDEX IF NOT EXISTS idx_runs_workspace_created ON runs(workspace_id, created_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_runs_status_queued ON runs(status, queued_at)`,
	`CREATE TABLE IF NOT EXISTS run_steps (
		id TEXT PRIMARY KEY,
		run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
		node_id TEXT NOT NULL DEFAULT '',
		type TEXT NOT NULL,
		name TEXT NOT NULL DEFAULT '',
		attempt INTEGER NOT NULL DEFAULT 1 CHECK (attempt > 0),
		status TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'waiting_approval', 'succeeded', 'failed', 'cancelled', 'timed_out')),
		input JSONB NOT NULL DEFAULT '{}'::jsonb,
		output JSONB NOT NULL DEFAULT '{}'::jsonb,
		output_ref TEXT NOT NULL DEFAULT '',
		timeout_ms BIGINT NOT NULL DEFAULT 0 CHECK (timeout_ms >= 0),
		error_code TEXT NOT NULL DEFAULT '',
		error_message TEXT NOT NULL DEFAULT '',
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		started_at TIMESTAMPTZ,
		finished_at TIMESTAMPTZ,
		UNIQUE(run_id, node_id, attempt)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_run_steps_run_created ON run_steps(run_id, created_at, id)`,
	`CREATE TABLE IF NOT EXISTS run_events (
		id BIGSERIAL PRIMARY KEY,
		run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
		step_id TEXT REFERENCES run_steps(id) ON DELETE CASCADE,
		sequence BIGINT NOT NULL,
		type TEXT NOT NULL,
		payload JSONB NOT NULL DEFAULT '{}'::jsonb,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		UNIQUE(run_id, sequence)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_run_events_run_sequence ON run_events(run_id, sequence)`,
	`CREATE TABLE IF NOT EXISTS worker_jobs (
		id BIGSERIAL PRIMARY KEY,
		run_id TEXT REFERENCES runs(id) ON DELETE CASCADE,
		type TEXT NOT NULL,
		payload JSONB NOT NULL DEFAULT '{}'::jsonb,
		status TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
		available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		lease_owner TEXT NOT NULL DEFAULT '',
		lease_expires_at TIMESTAMPTZ,
		attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0),
		max_attempts INTEGER NOT NULL DEFAULT 5 CHECK (max_attempts > 0),
		dedupe_key TEXT NOT NULL DEFAULT '',
		error_message TEXT NOT NULL DEFAULT '',
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		finished_at TIMESTAMPTZ
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_worker_jobs_dedupe ON worker_jobs(dedupe_key) WHERE dedupe_key <> ''`,
	`CREATE INDEX IF NOT EXISTS idx_worker_jobs_claim ON worker_jobs(status, available_at, lease_expires_at)`,
}

func migrationChecksum(migration Migration) string {
	sum := sha256.Sum256([]byte(strings.Join(migration.Statements, "\x00")))
	return hex.EncodeToString(sum[:])
}

func validateMigrations(items []Migration) error {
	var previous int64
	for _, migration := range items {
		if migration.Version <= previous || strings.TrimSpace(migration.Name) == "" || len(migration.Statements) == 0 {
			return fmt.Errorf("invalid migration version %d", migration.Version)
		}
		previous = migration.Version
	}
	return nil
}

// Migrate serializes schema changes across replicas and records an immutable
// checksum for every version. An edited migration fails closed instead of
// silently producing different schemas in different environments.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if err := validateMigrations(migrations); err != nil {
		return err
	}
	connection, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockID); err != nil {
		return fmt.Errorf("lock: %w", err)
	}
	defer func() { _, _ = connection.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationLockID) }()

	if _, err := connection.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version BIGINT PRIMARY KEY,
		name TEXT NOT NULL,
		checksum TEXT NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`); err != nil {
		return fmt.Errorf("metadata: %w", err)
	}
	for _, migration := range migrations {
		checksum := migrationChecksum(migration)
		var appliedChecksum string
		err := connection.QueryRow(ctx, `SELECT checksum FROM schema_migrations WHERE version = $1`, migration.Version).Scan(&appliedChecksum)
		if err == nil {
			if appliedChecksum != checksum {
				return fmt.Errorf("migration %d checksum changed", migration.Version)
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read migration %d: %w", migration.Version, err)
		}
		tx, err := connection.Begin(ctx)
		if err != nil {
			return err
		}
		for _, statement := range migration.Statements {
			if _, err = tx.Exec(ctx, statement); err != nil {
				_ = tx.Rollback(ctx)
				return fmt.Errorf("apply migration %d %s: %w", migration.Version, migration.Name, err)
			}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO schema_migrations(version, name, checksum) VALUES($1, $2, $3)`, migration.Version, migration.Name, checksum); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record migration %d: %w", migration.Version, err)
		}
		if err = tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %d: %w", migration.Version, err)
		}
	}
	return nil
}

func Status(ctx context.Context, pool *pgxpool.Pool) ([]MigrationStatus, error) {
	var metadataTable *string
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.schema_migrations')::text`).Scan(&metadataTable); err != nil {
		return nil, err
	}
	statuses := make([]MigrationStatus, 0, len(migrations))
	for _, migration := range migrations {
		status := MigrationStatus{Version: migration.Version, Name: migration.Name, Checksum: migrationChecksum(migration)}
		if metadataTable == nil {
			statuses = append(statuses, status)
			continue
		}
		var applied time.Time
		err := pool.QueryRow(ctx, `SELECT applied_at FROM schema_migrations WHERE version = $1`, migration.Version).Scan(&applied)
		if err == nil {
			status.AppliedAt = &applied
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}
