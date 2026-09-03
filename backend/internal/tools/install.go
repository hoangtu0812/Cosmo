package tools

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Installing from the catalogue is one operation, not a create followed by a
// series of action writes from the transport layer: half an installed tool is
// worse than none, and the check for "already installed" only means anything
// if it is made in the same breath as the insert.
//
// It is idempotent. Clicking Install twice used to produce two identical
// tools, and an agent attached to both handed the model two tools with the
// same name - which is worse than either, because the model cannot tell them
// apart and neither can the reader.
func (repository *Repository) InstallCatalogEntry(ctx context.Context, userID, workspaceID string, entry CatalogEntry) (Tool, bool, error) {
	var existingID string
	err := repository.db.QueryRow(ctx,
		`SELECT id FROM tools WHERE owner_workspace_id = $1 AND catalog_id = $2`,
		workspaceID, entry.ID).Scan(&existingID)
	if err == nil {
		installed, getErr := repository.Get(ctx, existingID, userID, workspaceID)
		return installed, true, getErr
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Tool{}, false, err
	}

	name, err := ValidateName(entry.Name)
	if err != nil {
		return Tool{}, false, err
	}
	description, err := ValidateDescription(entry.Description)
	if err != nil {
		return Tool{}, false, err
	}
	kind := entry.Kind
	if kind != KindMCP && kind != KindBuiltin {
		kind = KindHTTP
	}
	baseURL, err := BaseURLForKind(kind, entry.BaseURL)
	if err != nil {
		return Tool{}, false, err
	}
	if baseURL != "" {
		if err := repository.egress.CheckEgress(baseURL); err != nil {
			return Tool{}, false, err
		}
	}

	transaction, err := repository.db.Begin(ctx)
	if err != nil {
		return Tool{}, false, err
	}
	defer transaction.Rollback(ctx)

	id := newID("tol_")
	if _, err := transaction.Exec(ctx, `
		INSERT INTO tools (id, name, description, icon, tags, owner_user_id, owner_workspace_id, base_url, kind, catalog_id)
		VALUES ($1, $2, $3, $4, '[]'::jsonb, $5, $6, $7, $8, $9)`,
		id, name, description, entry.Icon, userID, workspaceID, baseURL, kind, entry.ID); err != nil {
		return Tool{}, false, err
	}

	for position, action := range entry.Actions {
		// The catalogue is ours, so a malformed action in it is a bug here
		// rather than bad input; failing the install surfaces it instead of
		// quietly shipping a tool with a missing action.
		actionName, err := ValidateActionName(action.Name)
		if err != nil {
			return Tool{}, false, err
		}
		method, err := ValidateMethod(action.Method)
		if err != nil {
			return Tool{}, false, err
		}
		path, err := ValidatePath(action.Path)
		if err != nil {
			return Tool{}, false, err
		}
		parameters, err := CleanParameters(action.Parameters)
		if err != nil {
			return Tool{}, false, err
		}
		parameterJSON, _ := json.Marshal(parameters)
		if _, err := transaction.Exec(ctx, `
			INSERT INTO tool_actions (id, tool_id, name, description, method, path, parameters, result_type, result_description, position)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			newID("act_"), id, actionName, action.Description, method, path, parameterJSON,
			ValidateResultType(action.ResultType), action.ResultDescription, position); err != nil {
			return Tool{}, false, err
		}
	}

	if err := transaction.Commit(ctx); err != nil {
		return Tool{}, false, err
	}
	installed, err := repository.Get(ctx, id, userID, workspaceID)
	return installed, false, err
}

// InstalledCatalogIDs is what the market reads to show an entry as installed
// rather than offering it again.
func (repository *Repository) InstalledCatalogIDs(ctx context.Context, workspaceID string) (map[string]string, error) {
	rows, err := repository.db.Query(ctx,
		`SELECT catalog_id, id FROM tools WHERE owner_workspace_id = $1 AND catalog_id <> ''`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	installed := map[string]string{}
	for rows.Next() {
		var catalogID, toolID string
		if err := rows.Scan(&catalogID, &toolID); err != nil {
			return nil, err
		}
		installed[catalogID] = toolID
	}
	return installed, rows.Err()
}
