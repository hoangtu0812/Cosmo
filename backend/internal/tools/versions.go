package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Publishing a tool.
//
// Until now editing a tool changed it under every agent already built on it,
// at once: a renamed action or a moved path broke a published agent without
// anyone touching that agent. A version freezes the callable surface, and an
// agent published afterwards calls the version it was built against.
//
// Deliberately the same shape as an agent's versions - numbered from one,
// carrying a changelog, with the newest published one live - because the two
// answer the same question about different things.

const MaxChangelogRunes = 2000

// versionColumns is exactly what a version freezes: where the tool lives, how
// it authenticates, and what it offers. Named once so the insert and the test
// that guards it read the same list.
const versionColumns = `base_url, kind, auth_type, auth_header_name, actions`

// Version is a tool as it read at the moment it was published.
//
// The credential is absent by design: a key is current state, not part of the
// description, and a snapshot carrying one would put a revoked key back into
// service on a rollback.
type Version struct {
	ID             string    `json:"id"`
	ToolID         string    `json:"tool_id"`
	VersionNumber  int       `json:"version_number"`
	BaseURL        string    `json:"base_url"`
	Kind           string    `json:"kind"`
	AuthType       string    `json:"auth_type"`
	AuthHeaderName string    `json:"auth_header_name"`
	Actions        []Action  `json:"actions"`
	Changelog      string    `json:"changelog"`
	PublishedBy    string    `json:"published_by"`
	PublishedName  string    `json:"published_name"`
	CreatedAt      time.Time `json:"created_at"`
	// Whether this is the version agents published from now on will pin.
	IsLive bool `json:"is_live"`
}

var ErrNoActions = errors.New("Tool chưa có action nào để phát hành.")

func CapChangelog(changelog string) string {
	changelog = strings.TrimSpace(changelog)
	if len([]rune(changelog)) > MaxChangelogRunes {
		changelog = string([]rune(changelog)[:MaxChangelogRunes])
	}
	return changelog
}

// Publish freezes the draft into a version and makes it the live one.
//
// The number comes from the rows already stored rather than a counter on the
// tool, so two people publishing at once cannot mint the same number: the
// unique constraint settles it and the loser retries.
func (repository *Repository) Publish(ctx context.Context, toolID, publishedBy, changelog string) (Version, error) {
	transaction, err := repository.lockTool(ctx, toolID)
	if err != nil {
		return Version{}, err
	}
	defer transaction.Rollback(ctx)
	actions, err := (&Repository{db: transaction}).Actions(ctx, toolID)
	if err != nil {
		return Version{}, err
	}
	// A tool with nothing to call is not a release. Saying so beats minting a
	// version that no agent could ever use.
	if len(actions) == 0 {
		return Version{}, ErrNoActions
	}
	frozen, err := json.Marshal(actions)
	if err != nil {
		return Version{}, err
	}

	version := Version{Actions: actions, IsLive: true}
	versionID := newID("tvr_")
	err = transaction.QueryRow(ctx, `
		INSERT INTO tool_versions (
			id, tool_id, version_number, `+versionColumns+`, changelog, published_by
		)
		SELECT
			$1, t.id,
			COALESCE((SELECT MAX(v.version_number) FROM tool_versions v WHERE v.tool_id = t.id), 0) + 1,
			t.base_url, t.kind, t.auth_type, t.auth_header_name, $3, $4, $5
		FROM tools t
		WHERE t.id = $2
		RETURNING id, tool_id, version_number, base_url, kind, auth_type,
			auth_header_name, changelog, COALESCE(published_by, ''), created_at`,
		versionID, toolID, frozen, CapChangelog(changelog), publishedBy).Scan(
		&version.ID, &version.ToolID, &version.VersionNumber, &version.BaseURL,
		&version.Kind, &version.AuthType, &version.AuthHeaderName,
		&version.Changelog, &version.PublishedBy, &version.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Version{}, ErrNotFound
		}
		return Version{}, err
	}

	if _, err := transaction.Exec(ctx,
		`UPDATE tools SET published_version_id = $2, updated_at = NOW() WHERE id = $1`,
		toolID, version.ID); err != nil {
		return Version{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return Version{}, err
	}
	return version, nil
}

// Versions lists what has been published, newest first.
func (repository *Repository) Versions(ctx context.Context, toolID string) ([]Version, error) {
	rows, err := repository.db.Query(ctx, `
		SELECT v.id, v.tool_id, v.version_number, v.base_url, v.kind, v.auth_type,
		       v.auth_header_name, v.actions, v.changelog, COALESCE(v.published_by, ''),
		       COALESCE(u.name, ''), v.created_at,
		       (t.published_version_id = v.id)
		FROM tool_versions v
		JOIN tools t ON t.id = v.tool_id
		LEFT JOIN users u ON u.id = v.published_by
		WHERE v.tool_id = $1
		ORDER BY v.version_number DESC`, toolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Version{}
	for rows.Next() {
		var version Version
		var actionsRaw []byte
		var isLive *bool
		if err := rows.Scan(&version.ID, &version.ToolID, &version.VersionNumber,
			&version.BaseURL, &version.Kind, &version.AuthType, &version.AuthHeaderName,
			&actionsRaw, &version.Changelog, &version.PublishedBy, &version.PublishedName,
			&version.CreatedAt, &isLive); err != nil {
			return nil, err
		}
		version.Actions = decodeActions(actionsRaw)
		version.IsLive = isLive != nil && *isLive
		list = append(list, version)
	}
	return list, rows.Err()
}

// PinnedTools is what a published agent may call: its frozen list of tools,
// each answered from the version it was published against.
//
// Anything the version did not freeze stays live, and deliberately so - the
// credential above all, which is current state rather than a description, and
// the name and icon, which are how a person recognises the tool rather than
// how a model calls it.
//
// Published agents fail closed when any tool or version is missing. Legacy
// releases without pins must be reviewed and republished rather than silently
// executing whatever happens to be in today's draft.
func (repository *Repository) PinnedTools(ctx context.Context, agentID string, pinned []string, versions map[string]string) ([]Tool, map[string][]Action, error) {
	list, actions, err := repository.AttachedTools(ctx, agentID, pinned)
	if err != nil {
		return nil, nil, err
	}
	if pinned == nil {
		return list, actions, nil
	}
	present := make(map[string]bool, len(list))
	for _, tool := range list {
		present[tool.ID] = true
	}
	for _, id := range pinned {
		if !present[id] || versions[id] == "" {
			return nil, nil, ErrPinnedVersionMissing
		}
	}

	ids := make([]string, 0, len(versions))
	for _, versionID := range versions {
		ids = append(ids, versionID)
	}
	frozen, err := repository.versionsByTool(ctx, ids)
	if err != nil {
		return nil, nil, err
	}

	for index, tool := range list {
		version, ok := frozen[tool.ID]
		if !ok || versions[tool.ID] != version.ID {
			return nil, nil, ErrPinnedVersionMissing
		}
		tool.BaseURL = version.BaseURL
		tool.Kind = version.Kind
		tool.AuthType = version.AuthType
		tool.AuthHeaderName = version.AuthHeaderName
		list[index] = tool
		actions[tool.ID] = version.Actions
	}
	return list, actions, nil
}

// versionsByTool reads a set of versions, keyed by the tool each belongs to.
func (repository *Repository) versionsByTool(ctx context.Context, versionIDs []string) (map[string]Version, error) {
	frozen := map[string]Version{}
	if len(versionIDs) == 0 {
		return frozen, nil
	}
	rows, err := repository.db.Query(ctx,
		`SELECT id, tool_id, version_number, base_url, kind, auth_type, auth_header_name, actions
		 FROM tool_versions WHERE id = ANY($1)`, versionIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var version Version
		var actionsRaw []byte
		if err := rows.Scan(&version.ID, &version.ToolID, &version.VersionNumber,
			&version.BaseURL, &version.Kind, &version.AuthType, &version.AuthHeaderName,
			&actionsRaw); err != nil {
			return nil, err
		}
		version.Actions = decodeActions(actionsRaw)
		frozen[version.ToolID] = version
	}
	return frozen, rows.Err()
}

func decodeActions(raw []byte) []Action {
	list := []Action{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &list)
	}
	return list
}
