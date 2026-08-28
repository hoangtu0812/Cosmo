package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS users (
		id TEXT PRIMARY KEY,
		email TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL,
		password_hash TEXT,
		role TEXT NOT NULL DEFAULT 'user',
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE TABLE IF NOT EXISTS oauth_identities (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		provider TEXT NOT NULL,
		subject TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		UNIQUE(provider, subject)
	)`,
	`CREATE TABLE IF NOT EXISTS workspaces (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		slug TEXT NOT NULL UNIQUE,
		type TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE TABLE IF NOT EXISTS workspace_memberships (
		user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
		role TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY(user_id, workspace_id)
	)`,
	`CREATE TABLE IF NOT EXISTS conversations (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
		title TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE TABLE IF NOT EXISTS messages (
		id TEXT PRIMARY KEY,
		conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
		role TEXT NOT NULL,
		content TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	// Kept as a standalone, idempotent migration so existing installations gain
	// the preference without requiring a destructive schema reset.
	`ALTER TABLE users ADD COLUMN IF NOT EXISTS last_workspace_id TEXT`,
	// Entra profile photos are cached after sign-in and served only to that
	// signed-in user. The access token used to fetch them is never persisted.
	`ALTER TABLE users ADD COLUMN IF NOT EXISTS avatar_image BYTEA`,
	`ALTER TABLE users ADD COLUMN IF NOT EXISTS avatar_mime TEXT`,
	// Records which model produced each answer, so history stays accurate when
	// the composer picker is used mid-conversation.
	`ALTER TABLE messages ADD COLUMN IF NOT EXISTS model TEXT`,
	// Emoji mark shown next to the workspace name; stored as text so no file
	// storage is needed for what is really a one-glyph label.
	`ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS icon TEXT`,
	// Uploaded icons live as bytes with their content type, served from a
	// dedicated endpoint so the workspace list payload stays small.
	`ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS icon_image BYTEA`,
	`ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS icon_mime TEXT`,
	`ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT ''`,

	// Knowledge Plane control tables. A knowledge base belongs to the workspace
	// where it was created. The creator is audit metadata only; visibility says
	// which workspaces may discover it and mounts say where it is used for
	// retrieval. Keeping reach and mounts separate lets one KB serve several
	// workspaces without enabling it automatically.
	`CREATE TABLE IF NOT EXISTS knowledge_bases (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		owner_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
		visibility TEXT NOT NULL DEFAULT 'private',
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE TABLE IF NOT EXISTS knowledge_grants (
		kb_id TEXT NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
		subject_type TEXT NOT NULL,
		subject_id TEXT NOT NULL,
		role TEXT NOT NULL DEFAULT 'viewer',
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY (kb_id, subject_type, subject_id)
	)`,
	`CREATE TABLE IF NOT EXISTS knowledge_mounts (
		kb_id TEXT NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
		target_type TEXT NOT NULL,
		target_id TEXT NOT NULL,
		mounted_by TEXT REFERENCES users(id) ON DELETE SET NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY (kb_id, target_type, target_id)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_knowledge_creator ON knowledge_bases(owner_user_id)`,
	`CREATE INDEX IF NOT EXISTS idx_knowledge_mounts_target ON knowledge_mounts(target_type, target_id)`,
	// Documents keep only metadata and the object reference here; the bytes
	// live in MinIO and the chunks in Qdrant.
	`CREATE TABLE IF NOT EXISTS knowledge_documents (
		id TEXT PRIMARY KEY,
		kb_id TEXT NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
		title TEXT NOT NULL,
		filename TEXT NOT NULL,
		content_type TEXT NOT NULL DEFAULT '',
		size_bytes BIGINT NOT NULL DEFAULT 0,
		storage_key TEXT NOT NULL DEFAULT '',
		version INTEGER NOT NULL DEFAULT 1,
		status TEXT NOT NULL DEFAULT 'pending',
		chunk_count INTEGER NOT NULL DEFAULT 0,
		error TEXT NOT NULL DEFAULT '',
		uploaded_by TEXT REFERENCES users(id) ON DELETE SET NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE INDEX IF NOT EXISTS idx_knowledge_documents_kb ON knowledge_documents(kb_id, created_at DESC)`,
	// One row per ingestion stage. Kept in the database rather than only
	// streamed, so the log of what happened to a document survives a reload
	// and can be read by someone who was not watching at the time.
	`CREATE TABLE IF NOT EXISTS knowledge_document_events (
		id BIGSERIAL PRIMARY KEY,
		document_id TEXT NOT NULL REFERENCES knowledge_documents(id) ON DELETE CASCADE,
		stage TEXT NOT NULL,
		message TEXT NOT NULL DEFAULT '',
		done INTEGER NOT NULL DEFAULT 0,
		total INTEGER NOT NULL DEFAULT 0,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE INDEX IF NOT EXISTS idx_knowledge_document_events ON knowledge_document_events(document_id, id)`,
	// A knowledge base belongs to the workspace it was created in, which is
	// what its narrowest visibility means.
	`ALTER TABLE knowledge_bases ADD COLUMN IF NOT EXISTS owner_workspace_id TEXT REFERENCES workspaces(id) ON DELETE SET NULL`,
	`CREATE INDEX IF NOT EXISTS idx_knowledge_owner_workspace ON knowledge_bases(owner_workspace_id)`,
	// Earlier builds treated owner_user_id as the owner. Preserve its value as
	// creator metadata but remove the cascade: deleting that account must not
	// delete a workspace-owned KB.
	`ALTER TABLE knowledge_bases DROP CONSTRAINT IF EXISTS knowledge_bases_owner_user_id_fkey`,
	`ALTER TABLE knowledge_bases ALTER COLUMN owner_user_id DROP NOT NULL`,
	`ALTER TABLE knowledge_bases ADD CONSTRAINT knowledge_bases_owner_user_id_fkey FOREIGN KEY (owner_user_id) REFERENCES users(id) ON DELETE SET NULL`,
	// Version zero is a draft only its owning workspace can see. Publishing
	// releases it to the configured sharing scope.
	`ALTER TABLE knowledge_bases ADD COLUMN IF NOT EXISTS version INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE knowledge_bases ADD COLUMN IF NOT EXISTS published_at TIMESTAMPTZ`,
	// Sharing is workspace to workspace: everyone signs in to a workspace
	// already, so naming a person as well would only restate that.
	`CREATE TABLE IF NOT EXISTS knowledge_shares (
		kb_id TEXT NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
		workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY (kb_id, workspace_id)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_knowledge_shares_workspace ON knowledge_shares(workspace_id)`,
	// Which version a workspace signed up for, so a later publish can be
	// announced rather than applied behind its back.
	`ALTER TABLE knowledge_mounts ADD COLUMN IF NOT EXISTS installed_version INTEGER NOT NULL DEFAULT 0`,
	// Carry the earlier vocabulary over: private now means the owning workspace,
	// and organization means everyone.
	`UPDATE knowledge_bases SET visibility = 'workspace' WHERE visibility = 'private'`,
	`UPDATE knowledge_bases SET visibility = 'everyone' WHERE visibility = 'organization'`,
	// Bases made before workspaces owned them adopt their creator's own space.
	`UPDATE knowledge_bases kb SET owner_workspace_id = (
		SELECT m.workspace_id FROM workspace_memberships m
		WHERE m.user_id = kb.owner_user_id ORDER BY m.created_at LIMIT 1
	) WHERE kb.owner_workspace_id IS NULL`,
	// Anything that already had documents was in use before publishing
	// existed, so treat it as released rather than silently hiding it.
	`UPDATE knowledge_bases kb SET version = 1, published_at = NOW()
	 WHERE kb.version = 0 AND EXISTS (
		SELECT 1 FROM knowledge_documents d WHERE d.kb_id = kb.id AND d.status = 'ready'
	 )`,
	`DROP TABLE IF EXISTS knowledge_grants`,
	// Model gateway credentials live per workspace so each team can point at its
	// own LiteLLM key; the API key is stored sealed, never in plaintext.
	`CREATE TABLE IF NOT EXISTS workspace_llm_configs (
		workspace_id TEXT PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
		base_url TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		api_key_sealed BYTEA,
		api_key_hint TEXT NOT NULL DEFAULT '',
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_by TEXT REFERENCES users(id) ON DELETE SET NULL
	)`,
	// Invitations carry only a hash of the token, so a database reader cannot
	// mint a working invite link.
	`CREATE TABLE IF NOT EXISTS workspace_invitations (
		id TEXT PRIMARY KEY,
		workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
		email TEXT NOT NULL,
		role TEXT NOT NULL DEFAULT 'member',
		token_hash TEXT NOT NULL UNIQUE,
		invited_by TEXT REFERENCES users(id) ON DELETE SET NULL,
		expires_at TIMESTAMPTZ NOT NULL,
		accepted_at TIMESTAMPTZ,
		accepted_by TEXT REFERENCES users(id) ON DELETE SET NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE INDEX IF NOT EXISTS idx_invitations_workspace ON workspace_invitations(workspace_id, accepted_at)`,
	`CREATE INDEX IF NOT EXISTS idx_memberships_user ON workspace_memberships(user_id)`,
	`CREATE INDEX IF NOT EXISTS idx_conversations_user_workspace_updated ON conversations(user_id, workspace_id, updated_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_messages_conversation_created ON messages(conversation_id, created_at ASC)`,
	// Platform-level, append-only audit records. Metadata is deliberately JSON
	// rather than a free-form message so the admin console can render it safely
	// and no credentials need ever be stored in a log row.
	`CREATE TABLE IF NOT EXISTS audit_logs (
		id BIGSERIAL PRIMARY KEY,
		actor_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
		action TEXT NOT NULL,
		target_type TEXT NOT NULL DEFAULT '',
		target_id TEXT NOT NULL DEFAULT '',
		metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_logs_created ON audit_logs(created_at DESC)`,
	// Non-secret platform settings that an administrator may update in the
	// console. Keys are intentionally explicit at their call sites; this table
	// is not a replacement for deployment secrets in .env.
	`CREATE TABLE IF NOT EXISTS system_settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_by TEXT REFERENCES users(id) ON DELETE SET NULL
	)`,
	// The platform gateway is distinct from workspace gateways. It serves
	// platform jobs such as choosing the embedding and reranker models, and its
	// API key remains sealed just like a workspace credential.
	`CREATE TABLE IF NOT EXISTS system_model_gateway_config (
		id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
		base_url TEXT NOT NULL DEFAULT '',
		api_key_sealed BYTEA,
		api_key_hint TEXT NOT NULL DEFAULT '',
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_by TEXT REFERENCES users(id) ON DELETE SET NULL
	)`,
}

func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect database: %w", err)
	}
	for _, statement := range migrations {
		if _, err := pool.Exec(ctx, statement); err != nil {
			pool.Close()
			return nil, fmt.Errorf("database migration: %w", err)
		}
	}
	return pool, nil
}
