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

	// Knowledge Plane control tables. A knowledge base is owned by the user who
	// created it and lives outside any workspace, so it can be shared and
	// mounted independently — grants say who may see it, mounts say where it is
	// actually used for retrieval. Keeping those separate is what lets one KB
	// serve several workspaces without widening who can read it.
	`CREATE TABLE IF NOT EXISTS knowledge_bases (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
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
	`CREATE INDEX IF NOT EXISTS idx_knowledge_owner ON knowledge_bases(owner_user_id)`,
	`CREATE INDEX IF NOT EXISTS idx_knowledge_mounts_target ON knowledge_mounts(target_type, target_id)`,
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
