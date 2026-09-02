package agents

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Publish freezes the draft into a version and makes it the live one.
//
// It runs in a transaction and takes the version number from the rows already
// stored rather than from a counter on the agent, so two people publishing at
// once cannot mint the same number: the unique constraint on
// (agent_id, version_number) is what settles it, and the loser retries.
//
// The reading list is snapshotted into the version. A knowledge base later
// detached from the draft therefore stays part of what the published version
// was, which is the whole point of a snapshot.
func (repository *Repository) Publish(ctx context.Context, agentID, publishedBy, changelog string) (Version, error) {
	changelog = CapChangelog(changelog)
	transaction, err := repository.db.Begin(ctx)
	if err != nil {
		return Version{}, err
	}
	defer func() { _ = transaction.Rollback(ctx) }()

	var version Version
	var presets, knowledge, toolIDs []byte
	versionID := newID("agv_")
	err = transaction.QueryRow(ctx, `
		INSERT INTO agent_versions (
			id, agent_id, version_number, model, system_prompt, opening_line,
			preset_questions, has_suggested_questions, is_memory_enabled,
			knowledge_base_ids, tool_ids, changelog, published_by
		)
		SELECT
			$1, a.id,
			COALESCE((SELECT MAX(v.version_number) FROM agent_versions v WHERE v.agent_id = a.id), 0) + 1,
			a.model, a.system_prompt, a.opening_line, a.preset_questions,
			a.has_suggested_questions, a.is_memory_enabled,
			COALESCE((
				SELECT jsonb_agg(k.kb_id ORDER BY k.created_at)
				FROM agent_knowledge_bases k WHERE k.agent_id = a.id
			), '[]'::jsonb),
			COALESCE((
				SELECT jsonb_agg(at.tool_id ORDER BY at.created_at)
				FROM agent_tools at WHERE at.agent_id = a.id
			), '[]'::jsonb),
			$3, $4
		FROM agents a
		WHERE a.id = $2
		RETURNING id, agent_id, version_number, model, system_prompt, opening_line,
			preset_questions, has_suggested_questions, is_memory_enabled,
			knowledge_base_ids, tool_ids, changelog, COALESCE(published_by, ''), created_at`,
		versionID, agentID, changelog, publishedBy).Scan(
		&version.ID, &version.AgentID, &version.VersionNumber, &version.Model,
		&version.SystemPrompt, &version.OpeningLine, &presets,
		&version.HasSuggestedQuestions, &version.IsMemoryEnabled, &knowledge, &toolIDs,
		&version.Changelog, &version.PublishedBy, &version.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Version{}, ErrNotFound
		}
		return Version{}, err
	}
	version.PresetQuestions = decodeStringList(presets)
	version.KnowledgeBaseIDs = decodeStringList(knowledge)

	if _, err := transaction.Exec(ctx,
		`UPDATE agents SET published_version_id = $2, updated_at = NOW() WHERE id = $1`,
		agentID, version.ID); err != nil {
		return Version{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return Version{}, err
	}
	return version, nil
}

// Versions lists what has been published, newest first.
func (repository *Repository) Versions(ctx context.Context, agentID string) ([]Version, error) {
	rows, err := repository.db.Query(ctx, `
		SELECT id, agent_id, version_number, model, system_prompt, opening_line,
			preset_questions, has_suggested_questions, is_memory_enabled,
			knowledge_base_ids, changelog, COALESCE(published_by, ''), created_at
		FROM agent_versions WHERE agent_id = $1 ORDER BY version_number DESC`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []Version{}
	for rows.Next() {
		var item Version
		var presets, knowledge []byte
		if rows.Scan(&item.ID, &item.AgentID, &item.VersionNumber, &item.Model,
			&item.SystemPrompt, &item.OpeningLine, &presets,
			&item.HasSuggestedQuestions, &item.IsMemoryEnabled, &knowledge,
			&item.Changelog, &item.PublishedBy, &item.CreatedAt) == nil {
			item.PresetQuestions = decodeStringList(presets)
			item.KnowledgeBaseIDs = decodeStringList(knowledge)
			items = append(items, item)
		}
	}
	return items, rows.Err()
}

// SaveDraft applies changes only if the caller read the current revision, and
// bumps it. Passing revision 0 means "I am not tracking revisions", which the
// editor uses for the first save after loading; anything else must match.
func (repository *Repository) SaveDraft(ctx context.Context, current Agent, changes Changes, revision int64) error {
	if revision != 0 && revision != current.DraftRevision {
		return ErrStaleDraft
	}
	if err := repository.Update(ctx, current, changes); err != nil {
		return err
	}
	tag, err := repository.db.Exec(ctx, `
		UPDATE agents SET draft_revision = draft_revision + 1
		WHERE id = $1 AND ($2 = 0 OR draft_revision = $2)`, current.ID, revision)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrStaleDraft
	}
	return nil
}

// RuntimeForVersion reads an immutable published snapshot. A conversation
// pinned to a version keeps answering from it even after the draft moves on.
func (repository *Repository) RuntimeForVersion(ctx context.Context, versionID string) (Runtime, error) {
	var item Runtime
	var knowledge, tools []byte
	err := repository.db.QueryRow(ctx, `
		SELECT model, system_prompt, is_memory_enabled, has_suggested_questions, knowledge_base_ids, tool_ids
		FROM agent_versions WHERE id = $1`, versionID).Scan(
		&item.Model, &item.SystemPrompt, &item.IsMemoryEnabled,
		&item.HasSuggestedQuestions, &knowledge, &tools)
	if err != nil {
		return Runtime{}, err
	}
	item.KnowledgeBaseIDs = decodeStringList(knowledge)
	item.ToolIDs = decodeStringList(tools)
	return item, nil
}

// PublishedVersionID is what a new conversation should pin itself to. It is
// empty when the agent has never been published, and the caller then runs the
// draft - which is what the editor's own debug panel wants anyway.
func (repository *Repository) PublishedVersionID(ctx context.Context, agentID string) string {
	var versionID string
	if err := repository.db.QueryRow(ctx,
		`SELECT COALESCE(published_version_id, '') FROM agents WHERE id = $1`, agentID).Scan(&versionID); err != nil {
		return ""
	}
	return versionID
}

// CapChangelog trims a publish note and cuts it to the limit by rune, so a
// Vietnamese note is not cut short by counting bytes. It is separate from
// Publish so the rule can be tested without a database.
func CapChangelog(changelog string) string {
	changelog = strings.TrimSpace(changelog)
	if len([]rune(changelog)) > MaxChangelogRunes {
		changelog = string([]rune(changelog)[:MaxChangelogRunes])
	}
	return changelog
}
