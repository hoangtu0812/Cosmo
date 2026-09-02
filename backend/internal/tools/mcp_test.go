package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// The MCP client is verified against a server written here rather than against
// somebody else's, for two reasons: the test then passes on a machine with no
// network, and the fixture can be made to answer in the awkward ways a real
// server is allowed to - a session header, an event-stream body - which is
// exactly where a client breaks.
//
// The fixture listens on loopback, which the egress guard exists to refuse, so
// the policy under test names the host explicitly. That is the same mechanism
// an on-premises deployment uses for its internal APIs, so the test exercises
// the allowlist rather than working around it.
type mcpFixture struct {
	server        *httptest.Server
	sawSession    string
	sawArguments  map[string]any
	asEventStream bool
}

func newMCPFixture(t *testing.T, asEventStream bool) *mcpFixture {
	t.Helper()
	fixture := &mcpFixture{asEventStream: asEventStream}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		var result any
		switch request.Method {
		case "initialize":
			// A real server hands back a session and expects to see it again.
			w.Header().Set("Mcp-Session-Id", "session-1")
			result = map[string]any{"protocolVersion": mcpProtocolVersion}
		case "tools/list":
			fixture.sawSession = r.Header.Get("Mcp-Session-Id")
			result = map[string]any{"tools": []any{
				map[string]any{
					"name":        "lookup_customer",
					"description": "Find a customer by id",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"customer_id": map[string]any{"type": "string", "description": "The id"},
							"verbose":     map[string]any{"type": "boolean", "description": "More detail"},
						},
						"required": []string{"customer_id"},
					},
				},
				// Refused later: a model could not call this name back.
				map[string]any{"name": "not a valid name", "description": "skipped"},
			}}
		case "tools/call":
			fixture.sawSession = r.Header.Get("Mcp-Session-Id")
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(request.Params, &params)
			fixture.sawArguments = params.Arguments
			result = map[string]any{"content": []any{
				map[string]any{"type": "text", "text": "Customer 42 is active"},
			}}
		default:
			http.Error(w, "unknown method", http.StatusNotFound)
			return
		}

		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
		if fixture.asEventStream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: message\ndata: " + string(body) + "\n\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(fixture.server.Close)
	return fixture
}

// repositoryFor builds a repository that may reach the fixture. No database is
// touched: discovery and calling never read one, which is what makes them
// testable in isolation.
func repositoryFor(t *testing.T, server *httptest.Server) *Repository {
	t.Helper()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("fixture URL: %v", err)
	}
	return &Repository{egress: EgressPolicy{AllowedHosts: []string{parsed.Hostname()}}}
}

func TestDiscoverMCPReadsWhatTheServerOffers(t *testing.T) {
	fixture := newMCPFixture(t, false)
	repository := repositoryFor(t, fixture.server)
	tool := Tool{ID: "tol_test", BaseURL: fixture.server.URL, Kind: KindMCP, AuthType: AuthNone}

	discovered, err := repository.DiscoverMCP(context.Background(), tool)
	if err != nil {
		t.Fatalf("discovery failed: %v", err)
	}
	if len(discovered) != 1 {
		t.Fatalf("a name a model could not call should have been dropped, got %#v", discovered)
	}

	action := discovered[0]
	if action.Name != "lookup_customer" || action.Method != "POST" {
		t.Fatalf("unexpected action: %#v", action)
	}
	if len(action.Parameters) != 2 {
		t.Fatalf("both schema properties should survive, got %#v", action.Parameters)
	}
	for _, parameter := range action.Parameters {
		// Everything an MCP server takes travels in the arguments object.
		if parameter.In != "body" {
			t.Fatalf("%s should be sent in the body, got %q", parameter.Name, parameter.In)
		}
		if parameter.Name == "customer_id" && !parameter.IsRequired {
			t.Fatal("the required field should have been marked required")
		}
		if parameter.Name == "verbose" && parameter.Type != "boolean" {
			t.Fatalf("the schema type should survive, got %q", parameter.Type)
		}
	}

	// The handshake's session has to come back on the next call, or a server
	// that uses sessions rejects everything after initialize.
	if fixture.sawSession != "session-1" {
		t.Fatalf("session was not carried, server saw %q", fixture.sawSession)
	}
}

func TestInvokeMCPReturnsTextContent(t *testing.T) {
	fixture := newMCPFixture(t, false)
	repository := repositoryFor(t, fixture.server)
	tool := Tool{ID: "tol_test", BaseURL: fixture.server.URL, Kind: KindMCP, AuthType: AuthNone}
	action := Action{Name: "lookup_customer", Parameters: []Parameter{{Name: "customer_id", In: "body"}}}

	result, err := repository.Invoke(context.Background(), tool, action, map[string]any{"customer_id": "42"})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Status != http.StatusOK {
		t.Fatalf("expected a success status, got %d", result.Status)
	}
	if !strings.Contains(result.Body, "Customer 42 is active") {
		t.Fatalf("text content should be what a model reads, got %q", result.Body)
	}
	if fixture.sawArguments["customer_id"] != "42" {
		t.Fatalf("arguments did not arrive, server saw %#v", fixture.sawArguments)
	}
}

func TestInvokeMCPReadsAnEventStreamReply(t *testing.T) {
	// The transport allows a server to answer either way, and which one you get
	// is not something the client chooses.
	fixture := newMCPFixture(t, true)
	repository := repositoryFor(t, fixture.server)
	tool := Tool{ID: "tol_test", BaseURL: fixture.server.URL, Kind: KindMCP, AuthType: AuthNone}
	action := Action{Name: "lookup_customer"}

	result, err := repository.Invoke(context.Background(), tool, action, nil)
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if !strings.Contains(result.Body, "Customer 42 is active") {
		t.Fatalf("an event-stream reply should read the same, got %q", result.Body)
	}
}

func TestMCPRefusesAServerTheEgressPolicyDoesNotAllow(t *testing.T) {
	// Same fixture, but the policy does not name it: an MCP endpoint is still a
	// URL a user chose, so it goes through the same guard as any other tool.
	fixture := newMCPFixture(t, false)
	repository := &Repository{egress: EgressPolicy{}}
	tool := Tool{ID: "tol_test", BaseURL: fixture.server.URL, Kind: KindMCP, AuthType: AuthNone}

	if _, err := repository.DiscoverMCP(context.Background(), tool); err == nil {
		t.Fatal("discovery against a private address should have been refused")
	}
}
