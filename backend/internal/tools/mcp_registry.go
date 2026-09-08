package tools

import (
	"context"
	"errors"
	"time"
)

var ErrMCPDiscoveryChanged = errors.New("Tool đã thay đổi trong lúc discovery. Vui lòng tải lại và thử lại.")

// ReplaceMCPActions commits a complete discovered registry or keeps the old one.
// Matching remote names retain action IDs. The parent revision prevents a slow
// discovery from overwriting a concurrent edit or a changed credential/endpoint.
func (repository *Repository) ReplaceMCPActions(ctx context.Context, tool Tool, discovered []Action) ([]Action, int, error) {
	if len(discovered) > MaxActions {
		return nil, 0, ErrMCPDiscoveryIncomplete
	}
	tx, err := repository.lockTool(ctx, tool.ID)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback(ctx)
	var revision time.Time
	var kind string
	if err = tx.QueryRow(ctx, `SELECT updated_at,kind FROM tools WHERE id=$1`, tool.ID).Scan(&revision, &kind); err != nil {
		return nil, 0, err
	}
	if kind != KindMCP || !revision.Equal(tool.UpdatedAt) {
		return nil, 0, ErrMCPDiscoveryChanged
	}
	draft := &Repository{db: tx}
	previous, err := draft.Actions(ctx, tool.ID)
	if err != nil {
		return nil, 0, err
	}
	ids := map[string]string{}
	for _, action := range previous {
		ids[action.Name] = action.ID
	}
	names := make([]string, 0, len(discovered))
	for _, action := range discovered {
		names = append(names, action.Name)
	}
	// Remove only withdrawn actions. Recreating retained rows would cascade
	// delete their policies even if their IDs were restored afterwards.
	if _, err = tx.Exec(ctx, `DELETE FROM tool_actions WHERE tool_id=$1 AND NOT (name=ANY($2::text[]))`, tool.ID, names); err != nil {
		return nil, 0, err
	}
	saved := make([]Action, 0, len(discovered))
	seen := map[string]bool{}
	for _, action := range discovered {
		if len(action.MCPTool) == 0 || seen[action.Name] {
			return nil, 0, ErrMCPContract
		}
		seen[action.Name] = true
		result, saveErr := draft.saveAction(ctx, tool.ID, ids[action.Name], action)
		if saveErr != nil {
			return nil, 0, saveErr
		}
		if _, err = tx.Exec(ctx, `UPDATE tool_actions SET position=$2 WHERE id=$1`, result.ID, len(saved)); err != nil {
			return nil, 0, err
		}
		result.Position = len(saved)
		saved = append(saved, result)
	}
	if _, err = tx.Exec(ctx, `UPDATE tools SET updated_at=clock_timestamp() WHERE id=$1`, tool.ID); err != nil {
		return nil, 0, err
	}
	return saved, len(previous), tx.Commit(ctx)
}
