package tools

import (
	"encoding/json"
	"errors"
	"github.com/google/jsonschema-go/jsonschema"
)

const MaxArgumentBytes = 64 * 1024

var ErrArguments = errors.New("Tham số tool không hợp lệ theo schema hoặc vượt giới hạn kích thước.")

// Validate the original MCP schema, not the editor's compatibility projection.
// Remote $ref loading is disabled: a schema cannot initiate network requests.
func validateMCPArguments(action Action, arguments map[string]any) error {
	raw, err := json.Marshal(arguments)
	if err != nil || len(raw) > MaxArgumentBytes {
		return ErrArguments
	}
	if len(action.MCPTool) == 0 {
		return nil
	} // Legacy hand-authored action.
	schema, ok := mcpInputSchema(action)
	if !ok {
		return ErrMCPContract
	}
	schemaBytes, err := json.Marshal(schema)
	if err != nil || len(schemaBytes) > 1024*1024 {
		return ErrMCPContract
	}
	var parsed jsonschema.Schema
	if json.Unmarshal(schemaBytes, &parsed) != nil {
		return ErrMCPContract
	}
	resolved, err := parsed.Resolve(nil)
	if err != nil {
		return ErrMCPContract
	}
	var instance any
	if arguments == nil {
		instance = map[string]any{}
	} else if json.Unmarshal(raw, &instance) != nil {
		return ErrArguments
	}
	if resolved.Validate(instance) != nil {
		return ErrArguments
	}
	return nil
}
