package tools

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"cosmo/backend/internal/secrets"
)

type toolDatabase interface {
	Begin(context.Context) (pgx.Tx, error)
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type Repository struct {
	db      toolDatabase
	logger  *slog.Logger
	secrets *secrets.Box
	egress  EgressPolicy
	search  SearchBackend
	// Access tokens fetched for tools that authenticate with OAuth. Not
	// constructed here: a repository built by hand in a test has no need of it
	// until something asks for a token.
	tokens tokenCache
}

func NewRepository(db *pgxpool.Pool, logger *slog.Logger, box *secrets.Box, egress EgressPolicy, search SearchBackend) *Repository {
	return &Repository{db: db, logger: logger, secrets: box, egress: egress, search: search}
}

func newID(prefix string) string {
	data := make([]byte, 18)
	if _, err := rand.Read(data); err != nil {
		panic(err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(data)
}

const columns = `
	t.id, t.name, t.description, t.icon, t.tags, COALESCE(t.owner_user_id, ''),
	COALESCE(u.name, ''), t.owner_workspace_id, t.visibility, t.base_url,
	t.kind, t.auth_type, t.auth_header_name, t.auth_hint, (t.auth_secret IS NOT NULL),
	COALESCE((SELECT COUNT(*) FROM tool_actions a WHERE a.tool_id = t.id), 0),
	COALESCE((SELECT w.name FROM workspaces w WHERE w.id = t.owner_workspace_id), ''),
	COALESCE((SELECT v.version_number FROM tool_versions v WHERE v.id = t.published_version_id), 0),
	COALESCE(t.published_version_id, ''),
	-- Whether the draft has moved since it was published. A tool never
	-- published counts as changed: there is something to publish.
	(t.published_version_id IS NULL OR EXISTS (
		SELECT 1 FROM tool_versions v WHERE v.id = t.published_version_id
		AND (t.updated_at > v.created_at
		     OR EXISTS (SELECT 1 FROM tool_actions a
		                WHERE a.tool_id = t.id AND a.updated_at > v.created_at))
	)),
	t.created_at, t.updated_at`

// workspaceColumns carry the two things that are only true of a tool relative
// to one workspace: how much of that workspace leans on it, and what it has
// decided about it. NULL auto_call means no install row at all, which is a
// different thing from installed and not allowed to answer on its own.
//
// param names the placeholder holding the workspace id, because the queries
// that need these put the workspace in different positions. Append them, and
// scan with scanInWorkspace.
func workspaceColumns(param string) string {
	return `,
	COALESCE((SELECT COUNT(*) FROM agent_tools at
	          JOIN agents ag ON ag.id = at.agent_id
	          WHERE at.tool_id = t.id AND ag.owner_workspace_id = ` + param + `), 0),
	(SELECT wt.auto_call FROM workspace_tools wt
	 WHERE wt.tool_id = t.id AND wt.workspace_id = ` + param + `)`
}

// visibleSQL is the one place that decides who may see a tool.
//
// Inside the owning workspace: everyone sees the shared ones, only the author
// sees their private ones. Beyond it: what has been offered, by name or to
// everyone - an offer nobody can see is an offer nobody can accept, which is
// what this said before tools could be shared at all.
//
// Every read goes through it, so a listing and a fetch cannot disagree.
const visibleSQL = `(
	(t.owner_workspace_id = $2 AND (t.visibility = 'workspace' OR t.owner_user_id = $1))
	OR t.visibility = 'everyone'
	OR (t.visibility = 'selected' AND EXISTS (
		SELECT 1 FROM tool_shares sh WHERE sh.tool_id = t.id AND sh.workspace_id = $2
	))
)`

func scan(row pgx.Row, userID string) (Tool, error) {
	var tool Tool
	var tagsRaw []byte
	if err := row.Scan(
		&tool.ID, &tool.Name, &tool.Description, &tool.Icon, &tagsRaw, &tool.OwnerUserID,
		&tool.OwnerName, &tool.WorkspaceID, &tool.Visibility, &tool.BaseURL,
		&tool.Kind, &tool.AuthType, &tool.AuthHeaderName, &tool.AuthHint, &tool.HasSecret,
		&tool.ActionCount, &tool.WorkspaceName, &tool.PublishedVersion,
		&tool.PublishedVersionID, &tool.HasUnpublishedChanges,
		&tool.CreatedAt, &tool.UpdatedAt,
	); err != nil {
		return Tool{}, err
	}
	tool.Tags = decodeStrings(tagsRaw)
	tool.IsEditable = tool.OwnerUserID == userID
	return tool, nil
}

// scanInWorkspace reads a row selected with columns + workspaceColumns, which
// append after the timestamps rather than in the middle of the shared list.
func scanInWorkspace(row pgx.Row, userID string) (Tool, error) {
	var tool Tool
	var tagsRaw []byte
	var autoCall *bool
	if err := row.Scan(
		&tool.ID, &tool.Name, &tool.Description, &tool.Icon, &tagsRaw, &tool.OwnerUserID,
		&tool.OwnerName, &tool.WorkspaceID, &tool.Visibility, &tool.BaseURL,
		&tool.Kind, &tool.AuthType, &tool.AuthHeaderName, &tool.AuthHint, &tool.HasSecret,
		&tool.ActionCount, &tool.WorkspaceName, &tool.PublishedVersion,
		&tool.PublishedVersionID, &tool.HasUnpublishedChanges,
		&tool.CreatedAt, &tool.UpdatedAt,
		&tool.ReferenceCount, &autoCall,
	); err != nil {
		return Tool{}, err
	}
	tool.IsInstalled = autoCall != nil
	tool.AutoCall = autoCall != nil && *autoCall
	tool.Tags = decodeStrings(tagsRaw)
	tool.IsEditable = tool.OwnerUserID == userID
	return tool, nil
}

func decodeStrings(raw []byte) []string {
	list := []string{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &list)
	}
	if list == nil {
		list = []string{}
	}
	return list
}

func (repository *Repository) List(ctx context.Context, userID, workspaceID string) ([]Tool, error) {
	rows, err := repository.db.Query(ctx, `
		SELECT `+columns+workspaceColumns("$2")+`
		FROM tools t LEFT JOIN users u ON u.id = t.owner_user_id
		WHERE `+visibleSQL+`
		ORDER BY t.updated_at DESC`, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Tool{}
	for rows.Next() {
		tool, err := scanInWorkspace(rows, userID)
		if err != nil {
			return nil, err
		}
		list = append(list, tool)
	}
	return list, rows.Err()
}

func (repository *Repository) Get(ctx context.Context, id, userID, workspaceID string) (Tool, error) {
	row := repository.db.QueryRow(ctx, `
		SELECT `+columns+workspaceColumns("$2")+`
		FROM tools t LEFT JOIN users u ON u.id = t.owner_user_id
		WHERE t.id = $3 AND `+visibleSQL, userID, workspaceID, id)
	tool, err := scanInWorkspace(row, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Tool{}, ErrNotFound
	}
	return tool, err
}

func (repository *Repository) Create(ctx context.Context, userID, workspaceID, rawName, rawDescription, icon string, tags []string, rawBaseURL, kind string) (Tool, error) {
	name, err := ValidateName(rawName)
	if err != nil {
		return Tool{}, err
	}
	description, err := ValidateDescription(rawDescription)
	if err != nil {
		return Tool{}, err
	}
	// Anything unrecognised becomes a plain HTTP tool rather than being
	// refused: a wrong kind is a mistake in the request, not in the tool.
	if kind != KindMCP && kind != KindBuiltin {
		kind = KindHTTP
	}

	baseURL, err := BaseURLForKind(kind, rawBaseURL)
	if err != nil {
		return Tool{}, err
	}
	if baseURL != "" {
		if err := repository.egress.CheckEgress(baseURL); err != nil {
			return Tool{}, err
		}
	}
	id := newID("tol_")
	tagJSON, _ := json.Marshal(CleanStringList(tags, 10, 40))
	if _, err := repository.db.Exec(ctx, `
		INSERT INTO tools (id, name, description, icon, tags, owner_user_id, owner_workspace_id, base_url, kind)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		id, name, description, icon, tagJSON, userID, workspaceID, baseURL, kind); err != nil {
		return Tool{}, err
	}
	return repository.Get(ctx, id, userID, workspaceID)
}

// Update applies only the fields the caller sent. Ownership is the gate: a
// tool carries a credential, so someone who can merely see it must not be able
// to point it somewhere else and read what comes back.
func (repository *Repository) Update(ctx context.Context, id, userID, workspaceID string, changes Changes) (Tool, error) {
	existing, err := repository.Get(ctx, id, userID, workspaceID)
	if err != nil {
		return Tool{}, err
	}
	if !existing.IsEditable {
		return Tool{}, ErrNotFound
	}

	if changes.Name != nil {
		name, err := ValidateName(*changes.Name)
		if err != nil {
			return Tool{}, err
		}
		if _, err := repository.db.Exec(ctx, `UPDATE tools SET name = $2, updated_at = NOW() WHERE id = $1`, id, name); err != nil {
			return Tool{}, err
		}
	}
	if changes.Description != nil {
		description, err := ValidateDescription(*changes.Description)
		if err != nil {
			return Tool{}, err
		}
		if _, err := repository.db.Exec(ctx, `UPDATE tools SET description = $2, updated_at = NOW() WHERE id = $1`, id, description); err != nil {
			return Tool{}, err
		}
	}
	if changes.Icon != nil {
		if _, err := repository.db.Exec(ctx, `UPDATE tools SET icon = $2, updated_at = NOW() WHERE id = $1`, id, *changes.Icon); err != nil {
			return Tool{}, err
		}
	}
	if changes.Tags != nil {
		tagJSON, _ := json.Marshal(CleanStringList(*changes.Tags, 10, 40))
		if _, err := repository.db.Exec(ctx, `UPDATE tools SET tags = $2, updated_at = NOW() WHERE id = $1`, id, tagJSON); err != nil {
			return Tool{}, err
		}
	}
	if changes.Visibility != nil {
		if _, err := repository.db.Exec(ctx, `UPDATE tools SET visibility = $2, updated_at = NOW() WHERE id = $1`, id, NormalizeVisibility(*changes.Visibility)); err != nil {
			return Tool{}, err
		}
	}
	if changes.BaseURL != nil {
		baseURL, err := BaseURLForKind(existing.Kind, *changes.BaseURL)
		if err != nil {
			return Tool{}, err
		}
		// A built-in yields an empty destination, so there is nothing to guard
		// and nothing to write. Create and Install already knew that; this is
		// where the rule had been missing, and a built-in could not be renamed
		// for the want of a URL it can never have.
		if baseURL != "" {
			if err := repository.egress.CheckEgress(baseURL); err != nil {
				return Tool{}, err
			}
			if _, err := repository.db.Exec(ctx, `UPDATE tools SET base_url = $2, updated_at = NOW() WHERE id = $1`, id, baseURL); err != nil {
				return Tool{}, err
			}
		}
	}
	if changes.AuthType != nil || changes.AuthHeaderName != nil {
		authType := existing.AuthType
		if changes.AuthType != nil {
			authType = *changes.AuthType
		}
		headerName := existing.AuthHeaderName
		if changes.AuthHeaderName != nil {
			headerName = *changes.AuthHeaderName
		}
		kind, name, err := ValidateAuth(authType, headerName)
		if err != nil {
			return Tool{}, err
		}
		if _, err := repository.db.Exec(ctx, `UPDATE tools SET auth_type = $2, auth_header_name = $3, updated_at = NOW() WHERE id = $1`, id, kind, name); err != nil {
			return Tool{}, err
		}
	}
	if changes.AuthSecret != nil {
		// The kind the credential is being stored *as*, which is the one
		// arriving in this request when it carries one - a registration and the
		// switch to OAuth are saved together.
		storedAs := existing.AuthType
		if changes.AuthType != nil {
			storedAs = strings.TrimSpace(*changes.AuthType)
		}
		if err := repository.setSecret(ctx, id, storedAs, *changes.AuthSecret); err != nil {
			return Tool{}, err
		}
	}
	return repository.Get(ctx, id, userID, workspaceID)
}

// setSecret seals the credential before it touches the database. An empty
// value clears it, which is how a reader removes a key they no longer want
// stored.
func (repository *Repository) setSecret(ctx context.Context, id, authType, secret string) error {
	if secret == "" {
		_, err := repository.db.Exec(ctx, `UPDATE tools SET auth_secret = NULL, auth_hint = '', updated_at = NOW() WHERE id = $1`, id)
		return err
	}
	if !repository.secrets.Configured() {
		return ErrSecretsOff
	}
	sealed, err := repository.secrets.Seal(secret)
	if err != nil {
		return ErrSecretsOff
	}
	_, err = repository.db.Exec(ctx,
		`UPDATE tools SET auth_secret = $2, auth_hint = $3, updated_at = NOW() WHERE id = $1`,
		id, sealed, hintFor(authType, secret))
	return err
}

func (repository *Repository) Delete(ctx context.Context, id, userID, workspaceID string) error {
	tag, err := repository.db.Exec(ctx,
		`DELETE FROM tools WHERE id = $1 AND owner_user_id = $2 AND owner_workspace_id = $3`,
		id, userID, workspaceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (repository *Repository) Actions(ctx context.Context, toolID string) ([]Action, error) {
	rows, err := repository.db.Query(ctx, `
		SELECT id, tool_id, name, description, method, path, parameters, mcp_tool,
		       result_type, result_description, position, created_at, updated_at
		FROM tool_actions WHERE tool_id = $1 ORDER BY position, created_at`, toolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Action{}
	for rows.Next() {
		var action Action
		var parameterRaw, mcpToolRaw []byte
		if err := rows.Scan(&action.ID, &action.ToolID, &action.Name, &action.Description,
			&action.Method, &action.Path, &parameterRaw, &mcpToolRaw,
			&action.ResultType, &action.ResultDescription, &action.Position,
			&action.CreatedAt, &action.UpdatedAt); err != nil {
			return nil, err
		}
		action.Parameters = []Parameter{}
		if len(parameterRaw) > 0 {
			_ = json.Unmarshal(parameterRaw, &action.Parameters)
		}
		if action.Parameters == nil {
			action.Parameters = []Parameter{}
		}
		action.MCPTool = decodeMCPTool(mcpToolRaw)
		list = append(list, action)
	}
	return list, rows.Err()
}

func (repository *Repository) Action(ctx context.Context, toolID, actionID string) (Action, error) {
	list, err := repository.Actions(ctx, toolID)
	if err != nil {
		return Action{}, err
	}
	for _, action := range list {
		if action.ID == actionID {
			return action, nil
		}
	}
	return Action{}, ErrNotFound
}

// SaveAction creates or replaces one action. Both paths validate the same way,
// so an action cannot be edited into a shape that creation would have refused.
func (repository *Repository) SaveAction(ctx context.Context, toolID, actionID string, input Action) (Action, error) {
	tx, err := repository.lockTool(ctx, toolID)
	if err != nil {
		return Action{}, err
	}
	defer tx.Rollback(ctx)
	saved, err := (&Repository{db: tx}).saveAction(ctx, toolID, actionID, input)
	if err != nil {
		return Action{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE tools SET updated_at=clock_timestamp() WHERE id=$1`, toolID); err != nil {
		return Action{}, err
	}
	return saved, tx.Commit(ctx)
}

func (repository *Repository) saveAction(ctx context.Context, toolID, actionID string, input Action) (Action, error) {
	// Older UI clients do not know about mcp_tool. Preserve the server-owned
	// contract on update instead of silently replacing it with an empty object.
	if actionID != "" && len(input.MCPTool) == 0 {
		current, err := repository.Action(ctx, toolID, actionID)
		if err != nil {
			return Action{}, err
		}
		input.MCPTool = current.MCPTool
	}
	mcpTool, remoteName, err := cleanMCPTool(input.MCPTool)
	if err != nil {
		return Action{}, err
	}
	var name string
	if len(mcpTool) > 0 {
		name, err = ValidateMCPToolName(input.Name)
		if err == nil && name != remoteName {
			err = ErrMCPContract
		}
	} else {
		name, err = ValidateActionName(input.Name)
	}
	if err != nil {
		return Action{}, err
	}
	description, err := ValidateDescription(input.Description)
	if err != nil {
		return Action{}, err
	}
	method, err := ValidateMethod(input.Method)
	if err != nil {
		return Action{}, err
	}
	path, err := ValidatePath(input.Path)
	if err != nil {
		return Action{}, err
	}
	parameters, err := CleanParameters(input.Parameters)
	if err != nil {
		return Action{}, err
	}
	parameterJSON, _ := json.Marshal(parameters)
	mcpToolJSON := []byte("{}")
	if len(mcpTool) > 0 {
		mcpToolJSON = mcpTool
	}
	resultType := ValidateResultType(input.ResultType)
	resultDescription, err := ValidateDescription(input.ResultDescription)
	if err != nil {
		return Action{}, err
	}

	if actionID == "" {
		var count int
		if err := repository.db.QueryRow(ctx, `SELECT COUNT(*) FROM tool_actions WHERE tool_id = $1`, toolID).Scan(&count); err != nil {
			return Action{}, err
		}
		if count >= MaxActions {
			return Action{}, ErrTooManyActions
		}
		actionID = newID("act_")
		if _, err := repository.db.Exec(ctx, `
			INSERT INTO tool_actions (id, tool_id, name, description, method, path, parameters, mcp_tool, result_type, result_description, position)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			actionID, toolID, name, description, method, path, parameterJSON, mcpToolJSON, resultType, resultDescription, count); err != nil {
			return Action{}, duplicateOr(err)
		}
	} else if _, err := repository.db.Exec(ctx, `
		UPDATE tool_actions
		SET name = $3, description = $4, method = $5, path = $6, parameters = $7,
		    mcp_tool = $8, result_type = $9, result_description = $10, updated_at = NOW()
		WHERE id = $1 AND tool_id = $2`,
		actionID, toolID, name, description, method, path, parameterJSON, mcpToolJSON, resultType, resultDescription); err != nil {
		return Action{}, duplicateOr(err)
	}
	return repository.Action(ctx, toolID, actionID)
}

// duplicateOr turns the unique-name violation into the message a reader can
// act on, and leaves anything else alone.
func duplicateOr(err error) error {
	if err != nil && contains(err.Error(), "tool_actions_name_idx") {
		return ErrDuplicateAction
	}
	return err
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func (repository *Repository) DeleteAction(ctx context.Context, toolID, actionID string) error {
	tx, err := repository.lockTool(ctx, toolID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `DELETE FROM tool_actions WHERE id = $1 AND tool_id = $2`, actionID, toolID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err = tx.Exec(ctx, `UPDATE tools SET updated_at=clock_timestamp() WHERE id=$1`, toolID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Every action writer and publisher shares this parent lock. Network discovery
// happens before taking it; the transaction contains database work only.
func (repository *Repository) lockTool(ctx context.Context, toolID string) (pgx.Tx, error) {
	tx, err := repository.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	var id string
	if err = tx.QueryRow(ctx, `SELECT id FROM tools WHERE id=$1 FOR UPDATE`, toolID).Scan(&id); err != nil {
		tx.Rollback(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrNotFound
		}
		return nil, err
	}
	return tx, nil
}

// secretFor opens the sealed credential for one call. It is deliberately not
// part of Tool: the plaintext exists only for the moment a request is built.
func (repository *Repository) secretFor(ctx context.Context, toolID string) (string, error) {
	var sealed []byte
	if err := repository.db.QueryRow(ctx, `SELECT auth_secret FROM tools WHERE id = $1`, toolID).Scan(&sealed); err != nil {
		return "", err
	}
	if len(sealed) == 0 {
		return "", nil
	}
	if !repository.secrets.Configured() {
		return "", ErrSecretsOff
	}
	return repository.secrets.Open(sealed)
}
