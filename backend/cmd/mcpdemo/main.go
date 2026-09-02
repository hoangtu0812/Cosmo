// Command mcpdemo is a minimal MCP server over Streamable HTTP, for proving
// that Cosmo's MCP client works end to end against a real server rather than
// only against a test fixture in the same process.
//
// It runs beside the app and reaches nothing: its two tools do arithmetic on
// their arguments. That is deliberate - verifying our client should not mean
// sending anyone's data to a third party, and it should work on a machine with
// no network at all.
//
// It answers tools/call as an event stream and everything else as JSON, so a
// single run exercises both reply shapes a real server is allowed to use.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"unicode"
)

const protocolVersion = "2025-06-18"

// A session id a real server would generate. Fixed here because there is
// nothing per-session to remember, and a client that fails to echo it back is
// the thing worth catching.
const sessionID = "mcpdemo-session"

type rpcRequest struct {
	ID     any             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func main() {
	address := os.Getenv("MCPDEMO_ADDRESS")
	if address == "" {
		address = ":8090"
	}

	http.HandleFunc("/mcp", handle)
	log.Printf("mcpdemo listening on %s", address)
	if err := http.ListenAndServe(address, nil); err != nil {
		log.Fatal(err)
	}
}

func handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var request rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// A notification carries no id and expects no reply.
	if strings.HasPrefix(request.Method, "notifications/") {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	result, err := dispatch(request)
	if err != nil {
		reply(w, request.ID, nil, err, false)
		return
	}
	// tools/call answers as an event stream, tools/list as JSON, so one run
	// covers both shapes a client has to handle.
	reply(w, request.ID, result, nil, request.Method == "tools/call")
}

func dispatch(request rpcRequest) (any, error) {
	switch request.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "cosmo-mcpdemo", "version": "1.0.0"},
		}, nil

	case "tools/list":
		return map[string]any{"tools": []any{
			tool("count_words", "Count the words in a piece of text",
				property("text", "string", "The text to count")),
			tool("celsius_to_fahrenheit", "Convert a temperature from Celsius to Fahrenheit",
				property("celsius", "number", "Temperature in degrees Celsius")),
		}}, nil

	case "tools/call":
		var call struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(request.Params, &call); err != nil {
			return nil, fmt.Errorf("bad params")
		}
		return run(call.Name, call.Arguments)
	}
	return nil, fmt.Errorf("unknown method %q", request.Method)
}

func run(name string, arguments map[string]any) (any, error) {
	switch name {
	case "count_words":
		text, _ := arguments["text"].(string)
		return text0(fmt.Sprintf("%d", len(strings.FieldsFunc(text, func(r rune) bool {
			return unicode.IsSpace(r)
		})))), nil
	case "celsius_to_fahrenheit":
		celsius, ok := arguments["celsius"].(float64)
		if !ok {
			// Told what went wrong, a model can correct itself; a generic
			// failure leaves it guessing.
			return errorContent("celsius must be a number"), nil
		}
		return text0(fmt.Sprintf("%.1f", celsius*9/5+32)), nil
	}
	return errorContent("unknown tool " + name), nil
}

func tool(name, description string, properties ...map[string]any) map[string]any {
	merged := map[string]any{}
	required := []string{}
	for _, property := range properties {
		for key, value := range property {
			merged[key] = value
			required = append(required, key)
		}
	}
	return map[string]any{
		"name":        name,
		"description": description,
		"inputSchema": map[string]any{"type": "object", "properties": merged, "required": required},
	}
}

func property(name, kind, description string) map[string]any {
	return map[string]any{name: map[string]any{"type": kind, "description": description}}
}

func text0(value string) map[string]any {
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": value}}}
}

func errorContent(message string) map[string]any {
	return map[string]any{
		"content": []any{map[string]any{"type": "text", "text": message}},
		"isError": true,
	}
}

func reply(w http.ResponseWriter, id any, result any, failure error, asEventStream bool) {
	body := map[string]any{"jsonrpc": "2.0", "id": id}
	if failure != nil {
		body["error"] = map[string]any{"code": -32603, "message": failure.Error()}
	} else {
		body["result"] = result
	}
	encoded, _ := json.Marshal(body)

	w.Header().Set("Mcp-Session-Id", sessionID)
	if asEventStream {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", encoded)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(encoded)
}
