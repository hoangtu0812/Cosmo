package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// An OpenAPI document says exactly what an API offers. Reading it beats asking
// a model to remember: the paths, the verbs and the parameters come from the
// API's own description rather than from a guess that sounds right.
//
// Only what an action can express is kept - a name, a verb, a path, and flat
// parameters. Request bodies with nested schemas are reduced to the top-level
// fields, because that is the shape a model is asked to fill in.
type openAPIParameter struct {
	Name        string `json:"name"`
	In          string `json:"in"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
	Schema      struct {
		Type string `json:"type"`
	} `json:"schema"`
}

type openAPIOperation struct {
	OperationID string             `json:"operationId"`
	Summary     string             `json:"summary"`
	Description string             `json:"description"`
	Parameters  []openAPIParameter `json:"parameters"`
	RequestBody struct {
		Content map[string]struct {
			Schema struct {
				Properties map[string]struct {
					Type        string `json:"type"`
					Description string `json:"description"`
				} `json:"properties"`
				Required []string `json:"required"`
			} `json:"schema"`
		} `json:"content"`
	} `json:"requestBody"`
}

type openAPIDocument struct {
	Paths map[string]map[string]json.RawMessage `json:"paths"`
}

// FetchOpenAPI reads a specification through the same guard a tool call uses:
// the URL comes from a person, so it is a destination like any other.
func (repository *Repository) FetchOpenAPI(ctx context.Context, specURL string) ([]byte, error) {
	if err := repository.egress.CheckEgress(specURL); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, specURL, nil)
	if err != nil {
		return nil, ErrCallFailed
	}
	request.Header.Set("Accept", "application/json")

	response, err := repository.client().Do(request)
	if err != nil {
		return nil, ErrCallFailed
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, ErrCallFailed
	}
	// A specification is bigger than a response but not unbounded; a megabyte
	// covers a large API and stops a hostile one from filling memory.
	return io.ReadAll(io.LimitReader(response.Body, 1024*1024))
}

// ActionsFromOpenAPI turns a specification into actions. Anything that cannot
// be expressed - a verb we do not support, a name a model could not call - is
// skipped rather than failing the import: most of a large API is still worth
// having.
func ActionsFromOpenAPI(spec []byte) ([]Action, error) {
	var document openAPIDocument
	if err := json.Unmarshal(spec, &document); err != nil {
		return nil, ErrCallFailed
	}

	actions := []Action{}
	for path, operations := range document.Paths {
		cleanPath, err := ValidatePath(path)
		if err != nil {
			continue
		}
		for verb, raw := range operations {
			method, err := ValidateMethod(verb)
			if err != nil {
				continue
			}
			var operation openAPIOperation
			if err := json.Unmarshal(raw, &operation); err != nil {
				continue
			}

			name, err := ValidateActionName(nameFor(operation, method, cleanPath))
			if err != nil {
				continue
			}
			description, err := ValidateDescription(firstNonEmpty(operation.Summary, operation.Description))
			if err != nil {
				description = ""
			}

			parameters := []Parameter{}
			for _, parameter := range operation.Parameters {
				where := parameter.In
				if where != "path" && where != "query" {
					// A header or cookie parameter is not something a model
					// should be asked to supply; the tool's credential covers
					// the header case.
					continue
				}
				parameters = append(parameters, Parameter{
					Name:        parameter.Name,
					Description: parameter.Description,
					Type:        parameter.Schema.Type,
					In:          where,
					IsRequired:  parameter.Required,
				})
			}
			for contentType, body := range operation.RequestBody.Content {
				if !strings.Contains(contentType, "json") {
					continue
				}
				required := map[string]bool{}
				for _, field := range body.Schema.Required {
					required[field] = true
				}
				for field, schema := range body.Schema.Properties {
					parameters = append(parameters, Parameter{
						Name:        field,
						Description: schema.Description,
						Type:        schema.Type,
						In:          "body",
						IsRequired:  required[field],
					})
				}
				break
			}

			cleaned, err := CleanParameters(parameters)
			if err != nil {
				continue
			}
			actions = append(actions, Action{
				Name:        name,
				Description: description,
				Method:      method,
				Path:        cleanPath,
				Parameters:  cleaned,
			})
			if len(actions) >= MaxActions {
				return actions, nil
			}
		}
	}
	return actions, nil
}

// nameFor prefers the specification's own operationId, which is what its
// authors chose to call the operation. Falling back to the verb and path keeps
// an operation that has none rather than dropping it.
func nameFor(operation openAPIOperation, method, path string) string {
	if candidate := sanitise(operation.OperationID); candidate != "tool" && candidate != "" {
		return candidate
	}
	parts := []string{strings.ToLower(method)}
	for _, segment := range strings.Split(path, "/") {
		segment = strings.Trim(segment, "{}")
		if segment == "" {
			continue
		}
		parts = append(parts, sanitise(segment))
	}
	return strings.Trim(strings.Join(parts, "_"), "_")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
