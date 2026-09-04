package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

const legacyMCPProtocolVersion = "2025-06-18"

// The MCP client is verified against a server written here rather than against
// somebody else's, for two reasons: the test then passes on a machine with no
// network, and the fixture can be made to answer in the awkward ways a real
// server is allowed to - a session header, an event-stream body - which is
// exactly where a client breaks.
//
// The fixture enforces the lifecycle rather than merely tolerating it: it
// refuses tools/list and tools/call until the initialized notification has
// arrived, the way a server built on the official SDKs does. The previous
// fixture did not, which is how this client shipped for months unable to talk
// to a real server without anyone noticing.
//
// It listens on loopback, which the egress guard exists to refuse, so the
// policy under test names the host explicitly. That is the same mechanism an
// on-premises deployment uses for its internal APIs, so the test exercises the
// allowlist rather than working around it.
type mcpFixture struct {
	server        *httptest.Server
	asEventStream bool
	modern        bool
	structured    bool

	mutex        sync.Mutex
	isInitalized bool
	sawSession   string
	sawVersions  []string
	sawMethods   []string
	sawNames     []string
	sawArguments map[string]any
	sawDelete    bool
	// Set to answer tools/list in two pages, so a client that reads only the
	// first one loses the tool on the second.
	paginates bool
	// The version the server claims to speak. Empty means "echo the client".
	speaks string
}

func newMCPFixture(t *testing.T, asEventStream bool) *mcpFixture {
	t.Helper()
	fixture := &mcpFixture{asEventStream: asEventStream}
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.handle))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func (fixture *mcpFixture) handle(w http.ResponseWriter, r *http.Request) {
	fixture.mutex.Lock()
	defer fixture.mutex.Unlock()

	if version := r.Header.Get("MCP-Protocol-Version"); version != "" {
		fixture.sawVersions = append(fixture.sawVersions, version)
	}
	if method := r.Header.Get("Mcp-Method"); method != "" {
		fixture.sawMethods = append(fixture.sawMethods, method)
	}
	if name := r.Header.Get("Mcp-Name"); name != "" {
		fixture.sawNames = append(fixture.sawNames, name)
	}
	if r.Method == http.MethodDelete {
		fixture.sawDelete = true
		w.WriteHeader(http.StatusOK)
		return
	}

	var request struct {
		ID     *int            `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// A notification carries no id and must not be answered with a result.
	if request.ID == nil {
		if request.Method == "notifications/initialized" {
			fixture.isInatializedSet()
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}

	var result any
	switch request.Method {
	case "server/discover":
		if !fixture.modern {
			fixture.writeRPCErrorCode(w, request.ID, -32601, "Method not found")
			return
		}
		result = map[string]any{
			"resultType":        "complete",
			"supportedVersions": []string{"2026-07-28"},
			"capabilities":      map[string]any{"tools": map[string]any{}},
			"cacheScope":        "private",
			"ttlMs":             0,
			"_meta": map[string]any{
				"io.modelcontextprotocol/serverInfo": map[string]any{
					"name": "fixture", "version": "1",
				},
			},
		}
	case "initialize":
		// A real server hands back a session and expects to see it again.
		w.Header().Set("Mcp-Session-Id", "session-1")
		version := fixture.speaks
		if version == "" {
			version = legacyMCPProtocolVersion
		}
		result = map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "fixture", "version": "1"},
		}
	case "tools/list":
		if !fixture.modern && !fixture.isInitalized {
			fixture.writeRPCError(w, request.ID, "Received request before initialization was complete")
			return
		}
		fixture.sawSession = r.Header.Get("Mcp-Session-Id")
		var params struct {
			Cursor string `json:"cursor"`
		}
		_ = json.Unmarshal(request.Params, &params)
		result = fixture.page(params.Cursor)
	case "tools/call":
		if !fixture.modern && !fixture.isInitalized {
			fixture.writeRPCError(w, request.ID, "Received request before initialization was complete")
			return
		}
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
		if fixture.structured {
			result = map[string]any{
				"content": []any{
					map[string]any{"type": "text", "text": "Customer 42 is active"},
					map[string]any{"type": "resource_link", "uri": "sap://customer/42", "name": "customer-42"},
				},
				"structuredContent": map[string]any{
					"customer": map[string]any{"id": "42", "active": true},
				},
				"_meta": map[string]any{"source": "fixture"},
			}
		}
	default:
		http.Error(w, "unknown method", http.StatusNotFound)
		return
	}

	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	if fixture.asEventStream {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message\ndata: " + string(body) + "\n\n"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func (fixture *mcpFixture) isInatializedSet() { fixture.isInitalized = true }

func (fixture *mcpFixture) writeRPCError(w http.ResponseWriter, id *int, message string) {
	fixture.writeRPCErrorCode(w, id, -32602, message)
}

func (fixture *mcpFixture) writeRPCErrorCode(w http.ResponseWriter, id *int, code int, message string) {
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id,
		"error": map[string]any{"code": code, "message": message},
	})
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func TestMCPUsesCurrentStatelessProtocol(t *testing.T) {
	fixture := newMCPFixture(t, false)
	fixture.modern = true
	repository := repositoryFor(t, fixture.server)

	discovered, err := repository.DiscoverMCP(context.Background(), mcpTool(fixture))
	if err != nil {
		t.Fatalf("current-protocol discovery failed: %v", err)
	}
	if len(discovered) != 1 || discovered[0].Name != "lookup_customer" {
		t.Fatalf("unexpected tools: %#v", discovered)
	}
	if fixture.isInitalized {
		t.Fatal("2026-07-28 must not use the legacy initialized notification")
	}
	if len(fixture.sawVersions) < 2 {
		t.Fatalf("expected version headers on current requests, got %v", fixture.sawVersions)
	}
	for _, version := range fixture.sawVersions {
		if version != "2026-07-28" {
			t.Fatalf("current request used protocol %q: %v", version, fixture.sawVersions)
		}
	}
	if len(fixture.sawMethods) < 2 || fixture.sawMethods[0] != "server/discover" || fixture.sawMethods[1] != "tools/list" {
		t.Fatalf("missing current MCP method headers: %v", fixture.sawMethods)
	}

	_, err = repository.Invoke(context.Background(), mcpTool(fixture), Action{Name: "lookup_customer"}, nil)
	if err != nil {
		t.Fatalf("current-protocol tool call failed: %v", err)
	}
	if len(fixture.sawNames) == 0 || fixture.sawNames[len(fixture.sawNames)-1] != "lookup_customer" {
		t.Fatalf("tools/call did not carry the MCP tool name header: %v", fixture.sawNames)
	}
}

// page answers tools/list. With paginates set it hands back one tool and a
// cursor, then the second tool on the next request.
func (fixture *mcpFixture) page(cursor string) map[string]any {
	lookup := map[string]any{
		"name":        "lookup_customer",
		"title":       "Customer lookup",
		"description": "Find a customer by id",
		"annotations": map[string]any{"readOnlyHint": true},
		"_meta":       map[string]any{"owner": "crm"},
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"customer_id": map[string]any{
					"type": "string", "description": "The id",
					"pattern": "^[0-9]+$", "enum": []string{"42", "84"},
				},
				"verbose": map[string]any{"type": "boolean", "description": "More detail"},
				"filters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"status": map[string]any{"type": "string", "enum": []string{"active", "blocked"}},
					},
				},
			},
			"required": []string{"customer_id"},
		},
		"outputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"customer": map[string]any{
					"type":       "object",
					"properties": map[string]any{"active": map[string]any{"type": "boolean"}},
				},
			},
		},
	}
	// Refused later: a model could not call this name back.
	unusable := map[string]any{"name": "not a valid name", "description": "skipped"}
	onSecondPage := map[string]any{
		"name":        "close_ticket",
		"description": "Only reachable by following the cursor",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
	}

	if !fixture.paginates {
		return map[string]any{"tools": []any{lookup, unusable}}
	}
	if cursor == "" {
		return map[string]any{"tools": []any{lookup, unusable}, "nextCursor": "page-2"}
	}
	return map[string]any{"tools": []any{onSecondPage}}
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

func mcpTool(fixture *mcpFixture) Tool {
	return Tool{ID: "tol_test", BaseURL: fixture.server.URL, Kind: KindMCP, AuthType: AuthNone}
}

func TestDiscoverMCPReadsWhatTheServerOffers(t *testing.T) {
	fixture := newMCPFixture(t, false)
	repository := repositoryFor(t, fixture.server)

	discovered, err := repository.DiscoverMCP(context.Background(), mcpTool(fixture))
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
	if len(action.Parameters) != 3 {
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

func TestDiscoverMCPKeepsTheCompleteToolContract(t *testing.T) {
	fixture := newMCPFixture(t, false)
	fixture.modern = true
	repository := repositoryFor(t, fixture.server)

	discovered, err := repository.DiscoverMCP(context.Background(), mcpTool(fixture))
	if err != nil {
		t.Fatalf("discovery failed: %v", err)
	}
	action := discovered[0]
	if action.ResultType != "object" {
		t.Fatalf("output schema type was lost: %#v", action)
	}
	var contract map[string]any
	if err := json.Unmarshal(action.MCPTool, &contract); err != nil {
		t.Fatalf("stored MCP contract is not JSON: %v", err)
	}
	input := contract["inputSchema"].(map[string]any)
	properties := input["properties"].(map[string]any)
	customerID := properties["customer_id"].(map[string]any)
	if customerID["pattern"] != "^[0-9]+$" || len(customerID["enum"].([]any)) != 2 {
		t.Fatalf("JSON Schema constraints were lost: %#v", customerID)
	}
	filters := properties["filters"].(map[string]any)
	if filters["properties"] == nil || contract["outputSchema"] == nil || contract["annotations"] == nil || contract["_meta"] == nil {
		t.Fatalf("nested schema or MCP metadata was lost: %#v", contract)
	}
}

// The lifecycle bug, stated as a test: the spec requires the client to say it
// is initialized before it asks for anything, and the fixture refuses to answer
// until it does. Without the notification this fails with the server's own
// words rather than with something vague.
func TestMCPSaysItIsInitializedBeforeAsking(t *testing.T) {
	fixture := newMCPFixture(t, false)
	repository := repositoryFor(t, fixture.server)

	if _, err := repository.DiscoverMCP(context.Background(), mcpTool(fixture)); err != nil {
		t.Fatalf("discovery failed: %v", err)
	}
	if !fixture.isInitalized {
		t.Fatal("the server never received notifications/initialized")
	}
}

// A session the client opens is a session the server has to hold. Ending it is
// the client's job, and nothing else tells the server we are done.
func TestMCPEndsTheSessionItOpened(t *testing.T) {
	fixture := newMCPFixture(t, false)
	repository := repositoryFor(t, fixture.server)

	if _, err := repository.DiscoverMCP(context.Background(), mcpTool(fixture)); err != nil {
		t.Fatalf("discovery failed: %v", err)
	}
	if !fixture.sawDelete {
		t.Fatal("the session was left open on the server")
	}
}

func TestDiscoverMCPFollowsTheCursor(t *testing.T) {
	fixture := newMCPFixture(t, false)
	fixture.paginates = true
	repository := repositoryFor(t, fixture.server)

	discovered, err := repository.DiscoverMCP(context.Background(), mcpTool(fixture))
	if err != nil {
		t.Fatalf("discovery failed: %v", err)
	}
	names := []string{}
	for _, action := range discovered {
		names = append(names, action.Name)
	}
	// Reading one page and stopping loses whatever the server put on the next,
	// and says nothing about having done so.
	if len(discovered) != 2 || names[1] != "close_ticket" {
		t.Fatalf("the second page was never asked for, got %v", names)
	}
}

// The version to speak is the server's answer, not the client's opening bid.
func TestMCPSpeaksTheVersionTheServerAgreedTo(t *testing.T) {
	fixture := newMCPFixture(t, false)
	fixture.speaks = "2025-03-26"
	repository := repositoryFor(t, fixture.server)

	if _, err := repository.DiscoverMCP(context.Background(), mcpTool(fixture)); err != nil {
		t.Fatalf("an older server should still be usable: %v", err)
	}
	// The opening request carries our version; everything after it carries the
	// one that came back.
	if len(fixture.sawVersions) < 2 {
		t.Fatalf("too few requests to judge: %v", fixture.sawVersions)
	}
	for _, seen := range fixture.sawVersions[1:] {
		if seen != "2025-03-26" {
			t.Fatalf("a later request still claimed %q: %v", seen, fixture.sawVersions)
		}
	}
}

func TestMCPRefusesAVersionItCannotSpeak(t *testing.T) {
	fixture := newMCPFixture(t, false)
	fixture.speaks = "2099-01-01"
	repository := repositoryFor(t, fixture.server)

	_, err := repository.DiscoverMCP(context.Background(), mcpTool(fixture))
	if err == nil {
		t.Fatal("a protocol this client cannot speak was attempted anyway")
	}
	if !strings.Contains(err.Error(), "2099-01-01") {
		t.Fatalf("the refusal should name the version: %v", err)
	}
}

func TestInvokeMCPReturnsTextContent(t *testing.T) {
	fixture := newMCPFixture(t, false)
	repository := repositoryFor(t, fixture.server)
	action := Action{Name: "lookup_customer", Parameters: []Parameter{{Name: "customer_id", In: "body"}}}

	result, err := repository.Invoke(context.Background(), mcpTool(fixture), action, map[string]any{"customer_id": "42"})
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
	action := Action{Name: "lookup_customer"}

	result, err := repository.Invoke(context.Background(), mcpTool(fixture), action, nil)
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if !strings.Contains(result.Body, "Customer 42 is active") {
		t.Fatalf("an event-stream reply should read the same, got %q", result.Body)
	}
}

func TestInvokeMCPKeepsStructuredAndNonTextContent(t *testing.T) {
	fixture := newMCPFixture(t, false)
	fixture.modern = true
	fixture.structured = true
	repository := repositoryFor(t, fixture.server)

	result, err := repository.Invoke(context.Background(), mcpTool(fixture), Action{Name: "lookup_customer"}, nil)
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(result.Body), &envelope); err != nil {
		t.Fatalf("mixed MCP output should be returned as JSON: %v: %s", err, result.Body)
	}
	if envelope["structuredContent"] == nil || envelope["_meta"] == nil {
		t.Fatalf("structured output or metadata was lost: %#v", envelope)
	}
	content := envelope["content"].([]any)
	if len(content) != 2 || content[1].(map[string]any)["type"] != "resource_link" {
		t.Fatalf("non-text content was lost: %#v", content)
	}
}

func TestBoundedMCPJSONStaysValid(t *testing.T) {
	body := `{"content":"` + strings.Repeat("ừ", MaxResponseBytes) + `"}`
	bounded := boundedMCPBody(body, true)
	if !json.Valid([]byte(bounded)) {
		t.Fatalf("truncated MCP result is broken JSON")
	}
	if len(bounded) > MaxResponseBytes {
		t.Fatalf("truncated MCP result still exceeds the limit: %d", len(bounded))
	}
	var envelope map[string]any
	_ = json.Unmarshal([]byte(bounded), &envelope)
	if envelope["isTruncated"] != true || envelope["originalBytes"] == nil {
		t.Fatalf("truncation was not disclosed: %#v", envelope)
	}
}

func TestMCPRefusesAServerTheEgressPolicyDoesNotAllow(t *testing.T) {
	// Same fixture, but the policy does not name it: an MCP endpoint is still a
	// URL a user chose, so it goes through the same guard as any other tool.
	fixture := newMCPFixture(t, false)
	repository := &Repository{egress: EgressPolicy{}}

	if _, err := repository.DiscoverMCP(context.Background(), mcpTool(fixture)); err == nil {
		t.Fatal("discovery against a private address should have been refused")
	}
}
