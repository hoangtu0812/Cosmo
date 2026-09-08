package tools

import (
	"context"
	"errors"
)

// Installing a tool into a workspace, so a plain chat can call it. The shape
// follows knowledge bases, which already work this way and are the proof that
// the shape holds: the workspace decides what is available, and an agent
// narrows that to what it needs.

var (
	ErrNotOffered   = errors.New("Tool này chưa được chia sẻ cho workspace của bạn.")
	ErrNotInstalled = errors.New("Tool chưa được cài vào workspace này.")
)

// offeredSQL is the one definition of what a workspace may install: its own
// tools, plus what another workspace has offered it by name or to everyone.
//
// Deliberately parallel to workspaceRetrievableKnowledgeSQL, because the two
// answer the same question about different things and drifting apart would
// mean a reader has to learn the rules twice. $1 is the workspace asking.
const offeredSQL = `
	t.owner_workspace_id = $1
	OR t.visibility = 'everyone'
	OR (t.visibility = 'selected' AND EXISTS (
		SELECT 1 FROM tool_shares sh WHERE sh.tool_id = t.id AND sh.workspace_id = $1
	))`

// InstallToWorkspace makes a tool available to a workspace. Available, not
// callable: auto_call is a separate decision, made after and revocable
// separately, because installing something and letting it answer questions on
// its own are different sizes of permission.
func (repository *Repository) InstallToWorkspace(ctx context.Context, workspaceID, toolID, userID string) error {
	var offered bool
	if err := repository.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM tools t WHERE t.id = $2 AND (`+offeredSQL+`))`,
		workspaceID, toolID).Scan(&offered); err != nil {
		return err
	}
	if !offered {
		return ErrNotOffered
	}
	_, err := repository.db.Exec(ctx, `
		INSERT INTO workspace_tools (workspace_id, tool_id, installed_by)
		VALUES ($1, $2, $3) ON CONFLICT (workspace_id, tool_id) DO NOTHING`,
		workspaceID, toolID, userID)
	return err
}

func (repository *Repository) UninstallFromWorkspace(ctx context.Context, workspaceID, toolID string) error {
	_, err := repository.db.Exec(ctx,
		`DELETE FROM workspace_tools WHERE workspace_id = $1 AND tool_id = $2`, workspaceID, toolID)
	return err
}

// SetAutoCall lets a workspace admin enable installed tools for member chats,
// including tools using workspace-shared credentials.
func (repository *Repository) SetAutoCall(ctx context.Context, workspaceID, toolID string, autoCall bool) error {
	result, err := repository.db.Exec(ctx,
		`UPDATE workspace_tools SET auto_call = $3 WHERE workspace_id = $1 AND tool_id = $2`,
		workspaceID, toolID, autoCall)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotInstalled
	}
	return nil
}

// WorkspaceInstall is a tool as the workspace sees it: the tool itself, plus
// whether this workspace lets the model reach for it.
type WorkspaceInstall struct {
	Tool     Tool `json:"tool"`
	AutoCall bool `json:"auto_call"`
	// Retained for API compatibility; shared credentials no longer block chat.
	IsBlockedByKey bool `json:"is_blocked_by_key"`
}

// InstalledInWorkspace lists what a workspace has installed and still may keep
// - an offer withdrawn takes the tool with it, exactly as a knowledge base
// unshared disappears from a workspace that had installed it.
func (repository *Repository) InstalledInWorkspace(ctx context.Context, workspaceID, userID string) ([]WorkspaceInstall, error) {
	rows, err := repository.db.Query(ctx, `
		SELECT `+columns+workspaceColumns("$1")+`
		FROM workspace_tools wt
		JOIN tools t ON t.id = wt.tool_id
		LEFT JOIN users u ON u.id = t.owner_user_id
		WHERE wt.workspace_id = $1 AND (`+offeredSQL+`)
		ORDER BY t.name`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []WorkspaceInstall{}
	for rows.Next() {
		tool, err := scanInWorkspace(rows, userID)
		if err != nil {
			return nil, err
		}
		list = append(list, WorkspaceInstall{
			Tool:     tool,
			AutoCall: tool.AutoCall,
		})
	}
	return list, rows.Err()
}

// AutoCallable returns installed tools enabled for chat and still offered to
// this workspace. Shared credentials are used by the normal invocation path.
func autoCallableSQL() string {
	return `
		SELECT ` + columns + `
		FROM workspace_tools wt
		JOIN tools t ON t.id = wt.tool_id
		LEFT JOIN users u ON u.id = t.owner_user_id
		WHERE wt.workspace_id = $1 AND wt.auto_call
		  AND (` + offeredSQL + `)
		ORDER BY t.name`
}

func (repository *Repository) AutoCallable(ctx context.Context, workspaceID string) ([]Tool, map[string][]Action, error) {
	rows, err := repository.db.Query(ctx, autoCallableSQL(), workspaceID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	list := []Tool{}
	for rows.Next() {
		tool, err := scan(rows, "")
		if err != nil {
			return nil, nil, err
		}
		list = append(list, tool)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	actions := map[string][]Action{}
	for _, tool := range list {
		loaded, err := repository.Actions(ctx, tool.ID)
		if err != nil {
			return nil, nil, err
		}
		actions[tool.ID] = loaded
	}
	return list, actions, nil
}

// SetShares replaces the list of workspaces a tool is offered to. Only
// meaningful while the tool's visibility is 'selected'; the rows are kept
// either way so switching to 'everyone' and back does not lose the list.
func (repository *Repository) SetShares(ctx context.Context, toolID string, workspaceIDs []string) error {
	transaction, err := repository.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)

	if _, err := transaction.Exec(ctx, `DELETE FROM tool_shares WHERE tool_id = $1`, toolID); err != nil {
		return err
	}
	for _, workspaceID := range workspaceIDs {
		if _, err := transaction.Exec(ctx,
			`INSERT INTO tool_shares (tool_id, workspace_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			toolID, workspaceID); err != nil {
			return err
		}
	}
	return transaction.Commit(ctx)
}

func (repository *Repository) Shares(ctx context.Context, toolID string) ([]string, error) {
	rows, err := repository.db.Query(ctx,
		`SELECT workspace_id FROM tool_shares WHERE tool_id = $1`, toolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		list = append(list, id)
	}
	return list, rows.Err()
}
