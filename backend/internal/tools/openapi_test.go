package tools

import (
	"testing"
)

// A specification exercising the parts that decide whether an import is worth
// having: an operationId to prefer, an operation without one, parameters in
// three places, a verb we do not support, and a header parameter that a model
// must not be asked to fill in.
const sampleSpec = `{
  "openapi": "3.0.0",
  "paths": {
    "/customers/{id}": {
      "get": {
        "operationId": "getCustomer",
        "summary": "Read one customer",
        "parameters": [
          {"name": "id", "in": "path", "required": true, "schema": {"type": "string"}},
          {"name": "expand", "in": "query", "description": "Extra fields", "schema": {"type": "string"}},
          {"name": "X-Trace", "in": "header", "schema": {"type": "string"}}
        ]
      },
      "trace": {"operationId": "traceCustomer"}
    },
    "/customers": {
      "post": {
        "summary": "Create a customer",
        "requestBody": {
          "content": {
            "application/json": {
              "schema": {
                "properties": {
                  "name": {"type": "string", "description": "Display name"},
                  "active": {"type": "boolean"}
                },
                "required": ["name"]
              }
            }
          }
        }
      }
    }
  }
}`

func findAction(actions []Action, name string) (Action, bool) {
	for _, action := range actions {
		if action.Name == name {
			return action, true
		}
	}
	return Action{}, false
}

func TestActionsFromOpenAPI(t *testing.T) {
	actions, err := ActionsFromOpenAPI([]byte(sampleSpec))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// TRACE is not a verb an action can carry, so that operation is skipped
	// rather than failing the whole import.
	if len(actions) != 2 {
		t.Fatalf("expected two usable operations, got %#v", actions)
	}

	read, found := findAction(actions, "getcustomer")
	if !found {
		t.Fatalf("the specification's own operationId should name the action, got %#v", actions)
	}
	if read.Method != "GET" || read.Path != "/customers/{id}" {
		t.Fatalf("unexpected verb or path: %#v", read)
	}
	if read.Description != "Read one customer" {
		t.Fatalf("the summary should describe the action, got %q", read.Description)
	}
	if len(read.Parameters) != 2 {
		// The header parameter is deliberately absent: a model should not be
		// asked to supply one, and the tool's credential covers that case.
		t.Fatalf("path and query only, got %#v", read.Parameters)
	}
	for _, parameter := range read.Parameters {
		if parameter.Name == "id" && (parameter.In != "path" || !parameter.IsRequired) {
			t.Fatalf("the path parameter lost its place or its requirement: %#v", parameter)
		}
		if parameter.Name == "X-Trace" {
			t.Fatal("a header parameter should not have been kept")
		}
	}

	// An operation with no operationId is still worth having, named from what
	// it does rather than dropped.
	create, found := findAction(actions, "post_customers")
	if !found {
		t.Fatalf("an operation without an id should be named from its verb and path, got %#v", actions)
	}
	if len(create.Parameters) != 2 {
		t.Fatalf("body properties should become parameters, got %#v", create.Parameters)
	}
	for _, parameter := range create.Parameters {
		if parameter.In != "body" {
			t.Fatalf("%s should be sent in the body, got %q", parameter.Name, parameter.In)
		}
		if parameter.Name == "name" && !parameter.IsRequired {
			t.Fatal("a required body field should stay required")
		}
		if parameter.Name == "active" && parameter.Type != "boolean" {
			t.Fatalf("the declared type should survive, got %q", parameter.Type)
		}
	}
}

func TestActionsFromOpenAPIRefusesRubbish(t *testing.T) {
	if _, err := ActionsFromOpenAPI([]byte("not json")); err == nil {
		t.Fatal("a document that is not JSON should be refused rather than silently empty")
	}
	// Valid JSON with no paths is not an error - it is an API with nothing to
	// offer, and the reader should be told that plainly by an empty list.
	actions, err := ActionsFromOpenAPI([]byte(`{"openapi":"3.0.0"}`))
	if err != nil || len(actions) != 0 {
		t.Fatalf("an empty specification should yield nothing, got %#v %v", actions, err)
	}
}
