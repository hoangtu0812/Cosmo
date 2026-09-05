package agents

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Publish freezes the draft into a version and makes it the live one.
//
// Publication serializes on the agent row. Every release freezes its reading
// list and tool versions; PublishKnowledge additionally pins indexed KB copies
// when explicitly requested. Existing callers keep live knowledge semantics.
func (repository *Repository) Publish(ctx context.Context, agentID, publishedBy, changelog string) (Version, error) {
	return repository.PublishKnowledge(ctx, agentID, publishedBy, changelog, "live")
}

func (repository *Repository) PublishKnowledge(ctx context.Context, agentID, publishedBy, changelog, mode string) (Version, error) {
	if mode == "" {
		mode = "live"
	}
	if mode != "live" && mode != "snapshot" {
		return Version{}, ErrKnowledgeMode
	}
	changelog = CapChangelog(changelog)
	transaction, err := repository.db.Begin(ctx)
	if err != nil {
		return Version{}, err
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	// Serialize publication with revision-checked binding changes.
	var lockedID string
	if err := transaction.QueryRow(ctx, `SELECT id FROM agents WHERE id=$1 FOR UPDATE`, agentID).Scan(&lockedID); err != nil {
		return Version{}, err
	}
	toolRows, err := transaction.Query(ctx, `SELECT t.id FROM tools t
		JOIN agent_tools at ON at.tool_id=t.id WHERE at.agent_id=$1
		ORDER BY t.id FOR SHARE OF t`, agentID)
	if err != nil {
		return Version{}, err
	}
	for toolRows.Next() { /* hold dependency pointers stable until commit */
	}
	toolRows.Close()
	if err := toolRows.Err(); err != nil {
		return Version{}, err
	}
	var incomplete bool
	if err := transaction.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM agent_tools at JOIN tools t ON t.id=at.tool_id
		LEFT JOIN tool_versions v ON v.id=t.published_version_id AND v.tool_id=t.id
		WHERE at.agent_id=$1 AND v.id IS NULL
	)`, agentID).Scan(&incomplete); err != nil {
		return Version{}, err
	}
	if incomplete {
		return Version{}, ErrToolReleaseRequired
	}

	// Lock KB publication pointers until this agent release commits.
	kbRows, err := transaction.Query(ctx, `SELECT kb.id FROM knowledge_bases kb JOIN agent_knowledge_bases ak ON ak.kb_id=kb.id WHERE ak.agent_id=$1 ORDER BY kb.id FOR SHARE OF kb`, agentID)
	if err != nil {
		return Version{}, err
	}
	for kbRows.Next() {
	}
	kbRows.Close()
	if err := kbRows.Err(); err != nil {
		return Version{}, err
	}
	if mode == "snapshot" {
		var missing bool
		if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_knowledge_bases ak JOIN knowledge_bases kb ON kb.id=ak.kb_id LEFT JOIN knowledge_snapshots ks ON ks.kb_id=kb.id AND ks.version=kb.version WHERE ak.agent_id=$1 AND ks.id IS NULL)`, agentID).Scan(&missing); err != nil {
			return Version{}, err
		}
		if missing {
			return Version{}, ErrKnowledgeSnapshotRequired
		}
	}
	var version Version
	var presets, knowledge, toolIDs, snapshotIDs []byte
	versionID := newID("agv_")
	err = transaction.QueryRow(ctx, `
		INSERT INTO agent_versions (
			id, agent_id, version_number, model, system_prompt, opening_line,
			preset_questions, has_suggested_questions, is_memory_enabled,
			knowledge_base_ids, tool_ids, tool_versions, changelog, published_by, knowledge_mode, knowledge_snapshots
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
			-- Every attachment has a published version, validated above.
			COALESCE((
				SELECT jsonb_object_agg(at.tool_id, t.published_version_id)
				FROM agent_tools at
				JOIN tools t ON t.id = at.tool_id
				WHERE at.agent_id = a.id AND t.published_version_id IS NOT NULL
			), '{}'::jsonb),
			$3, $4, $5,
            CASE WHEN $5='snapshot' THEN COALESCE((SELECT jsonb_object_agg(ak.kb_id,ks.id) FROM agent_knowledge_bases ak JOIN knowledge_bases kb ON kb.id=ak.kb_id JOIN knowledge_snapshots ks ON ks.kb_id=kb.id AND ks.version=kb.version WHERE ak.agent_id=a.id),'{}'::jsonb) ELSE '{}'::jsonb END
		FROM agents a
		WHERE a.id = $2
		RETURNING id, agent_id, version_number, model, system_prompt, opening_line,
			preset_questions, has_suggested_questions, is_memory_enabled,
			knowledge_base_ids, tool_ids, changelog, COALESCE(published_by, ''), created_at, knowledge_mode, knowledge_snapshots`,
		versionID, agentID, changelog, publishedBy, mode).Scan(
		&version.ID, &version.AgentID, &version.VersionNumber, &version.Model,
		&version.SystemPrompt, &version.OpeningLine, &presets,
		&version.HasSuggestedQuestions, &version.IsMemoryEnabled, &knowledge, &toolIDs,
		&version.Changelog, &version.PublishedBy, &version.CreatedAt, &version.KnowledgeMode, &snapshotIDs)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Version{}, ErrNotFound
		}
		return Version{}, err
	}
	version.PresetQuestions = decodeStringList(presets)
	version.KnowledgeBaseIDs = decodeStringList(knowledge)
	version.KnowledgeSnapshots = decodeStringMap(snapshotIDs)

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
			knowledge_base_ids, changelog, COALESCE(published_by, ''), created_at, knowledge_mode, knowledge_snapshots
		FROM agent_versions WHERE agent_id = $1 ORDER BY version_number DESC`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []Version{}
	for rows.Next() {
		var item Version
		var presets, knowledge, snapshotIDs []byte
		if rows.Scan(&item.ID, &item.AgentID, &item.VersionNumber, &item.Model,
			&item.SystemPrompt, &item.OpeningLine, &presets,
			&item.HasSuggestedQuestions, &item.IsMemoryEnabled, &knowledge,
			&item.Changelog, &item.PublishedBy, &item.CreatedAt, &item.KnowledgeMode, &snapshotIDs) == nil {
			item.PresetQuestions = decodeStringList(presets)
			item.KnowledgeBaseIDs = decodeStringList(knowledge)
			item.KnowledgeSnapshots = decodeStringMap(snapshotIDs)
			items = append(items, item)
		}
	}
	return items, rows.Err()
}

// SaveDraft checks the revision before changing any fields or bindings.
func (repository *Repository) SaveDraft(ctx context.Context, current Agent, changes Changes, revision int64) error {
	return repository.SaveDraftWithBindings(ctx, current, changes, revision, nil)
}

// SaveDraftWithBindings lets the tool domain update its attachments inside the
// same transaction as the draft, without teaching agents the tool schema.
func (repository *Repository) SaveDraftWithBindings(ctx context.Context, current Agent, changes Changes, revision int64, bindings func(pgx.Tx) error) error {
	if revision <= 0 {
		return ErrRevisionRequired
	}
	if revision != current.DraftRevision {
		return ErrStaleDraft
	}
	tx, err := repository.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
		UPDATE agents SET draft_revision = draft_revision + 1
		WHERE id = $1 AND draft_revision = $2`, current.ID, revision)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrStaleDraft
	}
	transactional := *repository
	transactional.db = tx
	if err := transactional.updateDraft(ctx, current, changes); err != nil {
		return err
	}
	if bindings != nil {
		if err := bindings(tx); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// RuntimeForVersion reads an immutable published snapshot. A conversation
// pinned to a version keeps answering from it even after the draft moves on -
// including the versions of the tools it was published against.
func (repository *Repository) RuntimeForVersion(ctx context.Context, agentID, versionID string) (Runtime, error) {
	var item Runtime
	var knowledge, tools, toolVersions, snapshotIDs []byte
	err := repository.db.QueryRow(ctx, `
		SELECT model, system_prompt, is_memory_enabled, has_suggested_questions,
		       knowledge_base_ids, tool_ids, tool_versions, knowledge_mode, knowledge_snapshots
		FROM agent_versions WHERE id = $1 AND agent_id = $2`, versionID, agentID).Scan(
		&item.Model, &item.SystemPrompt, &item.IsMemoryEnabled,
		&item.HasSuggestedQuestions, &knowledge, &tools, &toolVersions, &item.KnowledgeMode, &snapshotIDs)
	if err != nil {
		return Runtime{}, err
	}
	item.KnowledgeBaseIDs = decodeStringList(knowledge)
	item.ToolIDs = decodeStringList(tools)
	item.ToolVersions = decodeStringMap(toolVersions)
	item.KnowledgeSnapshots = decodeStringMap(snapshotIDs)
	if item.KnowledgeMode == "snapshot" {
		for _, kbID := range item.KnowledgeBaseIDs {
			if item.KnowledgeSnapshots[kbID] == "" {
				return Runtime{}, ErrKnowledgeSnapshotRequired
			}
		}
	}
	return item, nil
}

func decodeStringMap(raw []byte) map[string]string {
	pairs := map[string]string{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &pairs)
	}
	return pairs
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
