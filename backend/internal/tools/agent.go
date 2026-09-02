package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"cosmo/backend/internal/modelgateway"
)

// The name a model calls is "tool__action", because a model is given one flat
// list and two tools may each have a "search". Joining them keeps the names
// unique without asking the reader to prefix their own.
const nameSeparator = "__"

// A model sees at most this many actions. Past a point the list stops helping
// the model choose and starts crowding out the conversation.
const MaxDefinitions = 40

func callName(tool Tool, action Action) string {
	return sanitise(tool.Name) + nameSeparator + action.Name
}

// sanitise turns a tool's display name into something a model can call back.
// Names are free text - "Tra cứu khách hàng" is a reasonable tool name - but a
// call name is an identifier.
func sanitise(raw string) string {
	var builder strings.Builder
	for _, r := range raw {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_':
			builder.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r + 32)
		case r == ' ' || r == '-':
			builder.WriteRune('_')
		}
	}
	name := strings.Trim(builder.String(), "_")
	if name == "" {
		return "tool"
	}
	return name
}

// AttachedTools lists the tools an agent may call, with their actions loaded.
//
// A nil `pinned` means the live attachment - what the draft is wired to now.
// A non-nil one is a published version's frozen list, and an empty non-nil
// slice therefore means "this version had no tools", which is not the same
// thing as "read the draft".
//
// Visibility is not re-checked: the attachment was authorised when it was
// made, and a tool later made private should not silently stop an agent that
// was already built on it.
func (repository *Repository) AttachedTools(ctx context.Context, agentID string, pinned []string) ([]Tool, map[string][]Action, error) {
	query := `
		SELECT ` + columns + `
		FROM agent_tools at
		JOIN tools t ON t.id = at.tool_id
		LEFT JOIN users u ON u.id = t.owner_user_id
		WHERE at.agent_id = $1
		ORDER BY at.created_at`
	arguments := []any{agentID}
	if pinned != nil {
		if len(pinned) == 0 {
			return []Tool{}, map[string][]Action{}, nil
		}
		query = `
			SELECT ` + columns + `
			FROM tools t
			LEFT JOIN users u ON u.id = t.owner_user_id
			WHERE t.id = ANY($1)
			ORDER BY t.created_at`
		arguments = []any{pinned}
	}
	rows, err := repository.db.Query(ctx, query, arguments...)
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
		found, err := repository.Actions(ctx, tool.ID)
		if err != nil {
			return nil, nil, err
		}
		actions[tool.ID] = found
	}
	return list, actions, nil
}

// SetAgentTools replaces the set an agent may call. Only tools the caller can
// see may be attached, which is checked here rather than trusted from the
// request: attaching by id is otherwise a way to borrow someone else's
// credential.
func (repository *Repository) SetAgentTools(ctx context.Context, agentID, userID, workspaceID string, toolIDs []string) error {
	transaction, err := repository.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)

	if _, err := transaction.Exec(ctx, `DELETE FROM agent_tools WHERE agent_id = $1`, agentID); err != nil {
		return err
	}
	for _, toolID := range toolIDs {
		var exists bool
		if err := transaction.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM tools t WHERE t.id = $3 AND `+visibleSQL+`)`,
			userID, workspaceID, toolID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		if _, err := transaction.Exec(ctx,
			`INSERT INTO agent_tools (agent_id, tool_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			agentID, toolID); err != nil {
			return err
		}
	}
	return transaction.Commit(ctx)
}

// AgentToolIDs is what the editor reads back to tick the right boxes.
func (repository *Repository) AgentToolIDs(ctx context.Context, agentID string) ([]string, error) {
	rows, err := repository.db.Query(ctx, `SELECT tool_id FROM agent_tools WHERE agent_id = $1 ORDER BY created_at`, agentID)
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

// Definitions describes an agent's actions in the shape a model expects. The
// description is the only thing standing between a model and calling the wrong
// endpoint, so the tool's purpose is folded into each action: on its own,
// "search" says nothing about what is being searched.
func (repository *Repository) Definitions(ctx context.Context, agentID string, pinned []string) ([]modelgateway.ToolDefinition, error) {
	list, actions, err := repository.AttachedTools(ctx, agentID, pinned)
	if err != nil {
		return nil, err
	}

	definitions := []modelgateway.ToolDefinition{}
	for _, tool := range list {
		for _, action := range actions[tool.ID] {
			properties := map[string]any{}
			required := []string{}
			for _, parameter := range action.Parameters {
				properties[parameter.Name] = map[string]any{
					"type":        parameter.Type,
					"description": parameter.Description,
				}
				if parameter.IsRequired {
					required = append(required, parameter.Name)
				}
			}
			schema := map[string]any{"type": "object", "properties": properties}
			if len(required) > 0 {
				schema["required"] = required
			}

			description := action.Description
			if tool.Description != "" {
				description = strings.TrimSpace(tool.Description + ". " + description)
			}
			definitions = append(definitions, modelgateway.ToolDefinition{
				Name:        callName(tool, action),
				Description: description,
				Parameters:  schema,
			})
			if len(definitions) >= MaxDefinitions {
				return definitions, nil
			}
		}
	}
	return definitions, nil
}

// InvokeNamed runs the call a model asked for. The name is resolved against
// what the agent is actually attached to, so a model that invents a name - or
// is talked into naming someone else's tool - reaches nothing.
func (repository *Repository) InvokeNamed(ctx context.Context, agentID, name, rawArguments string, pinned []string) (CallResult, error) {
	list, actions, err := repository.AttachedTools(ctx, agentID, pinned)
	if err != nil {
		return CallResult{}, err
	}

	arguments := map[string]any{}
	if strings.TrimSpace(rawArguments) != "" {
		if err := json.Unmarshal([]byte(rawArguments), &arguments); err != nil {
			// The model produced something that is not JSON. Saying so is more
			// use to it than a generic failure, because it can correct itself.
			return CallResult{}, fmt.Errorf("arguments were not valid JSON")
		}
	}

	for _, tool := range list {
		for _, action := range actions[tool.ID] {
			if callName(tool, action) == name {
				return repository.Invoke(ctx, tool, action, arguments)
			}
		}
	}
	return CallResult{}, ErrNotFound
}
