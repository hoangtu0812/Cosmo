package tools

import (
	"context"
	"encoding/json"
	"strings"

	"cosmo/backend/internal/modelgateway"
)

// Describing an API by hand is the tedious part of adding a tool: a dozen
// fields per endpoint, most of them obvious from the endpoint's own name. The
// model is asked to draft them, and the draft is then run through the same
// validation a hand-typed action goes through - so a plausible-looking
// hallucination is refused exactly like a typo would be.
//
// What it produces is a starting point, not a finished tool. Nothing is called
// until someone opens the action and runs it.
const draftInstruction = `You describe HTTP APIs as tool definitions.

Reply with JSON only, no prose and no code fence, in this exact shape:
{"actions":[{"name":"","description":"","method":"GET","path":"/","parameters":[{"name":"","description":"","type":"string","in":"query","is_required":false}]}]}

Rules:
- name: lowercase letters, digits and underscores only. It is what a model will call.
- method: one of GET, POST, PUT, PATCH, DELETE.
- path: begins with "/", relative to the base URL. Use {braces} for path parameters.
- type: one of string, number, boolean.
- in: one of query, path, body.
- At most 8 actions. Describe only endpoints you are confident the API has.
- If you do not know the API, reply {"actions":[]} rather than guessing.`

// DraftActions asks the model to describe an API and returns actions that
// survived validation. Anything malformed is dropped rather than failing the
// whole draft: seven good actions and one bad one should leave seven.
func DraftActions(ctx context.Context, models *modelgateway.Client, baseURL, description string) ([]Action, error) {
	prompt := strings.TrimSpace(
		"Base URL: " + baseURL + "\n" +
			"What the API is for: " + description + "\n\n" +
			"List the endpoints worth exposing as actions.")

	reply, err := models.Complete(ctx, []modelgateway.Message{
		{Role: "system", Content: draftInstruction},
		{Role: "user", Content: prompt},
	}, modelgateway.Options{})
	if err != nil {
		return nil, err
	}

	var decoded struct {
		Actions []Action `json:"actions"`
	}
	if err := json.Unmarshal([]byte(extractJSON(reply)), &decoded); err != nil {
		return []Action{}, nil
	}

	drafted := []Action{}
	for _, action := range decoded.Actions {
		name, err := ValidateActionName(action.Name)
		if err != nil {
			continue
		}
		method, err := ValidateMethod(action.Method)
		if err != nil {
			continue
		}
		path, err := ValidatePath(action.Path)
		if err != nil {
			continue
		}
		summary, err := ValidateDescription(action.Description)
		if err != nil {
			continue
		}
		parameters, err := CleanParameters(action.Parameters)
		if err != nil {
			continue
		}
		drafted = append(drafted, Action{
			Name:        name,
			Description: summary,
			Method:      method,
			Path:        path,
			Parameters:  parameters,
		})
		if len(drafted) >= 8 {
			break
		}
	}
	return drafted, nil
}

// extractJSON pulls the object out of a reply that came wrapped in a code
// fence or a sentence, which models do however firmly they are asked not to.
func extractJSON(reply string) string {
	start := strings.Index(reply, "{")
	end := strings.LastIndex(reply, "}")
	if start < 0 || end <= start {
		return "{}"
	}
	return reply[start : end+1]
}
