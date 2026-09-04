package tools

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateBaseURL(t *testing.T) {
	for _, raw := range []string{"", "ftp://example.com", "example.com", "https://"} {
		if _, err := ValidateBaseURL(raw); !errors.Is(err, ErrBaseURL) {
			t.Fatalf("ValidateBaseURL(%q) should have been refused, got %v", raw, err)
		}
	}
	// A trailing slash is dropped so joining a path cannot produce "//".
	if got, err := ValidateBaseURL("https://api.example.com/"); err != nil || got != "https://api.example.com" {
		t.Fatalf("trailing slash should be trimmed, got %q %v", got, err)
	}
}

func TestCheckEgressRefusesPrivateAddresses(t *testing.T) {
	policy := EgressPolicy{}
	// The whole point of the guard: a user-supplied URL must not be able to
	// reach the machine this server runs on, its network, or cloud metadata.
	blocked := []string{
		"http://127.0.0.1/",
		"http://localhost/",
		"http://10.0.0.5/",
		"http://192.168.1.1/",
		"http://172.16.0.1/",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]/",
		"http://100.64.0.1/",
		"http://0.0.0.0/",
	}
	for _, raw := range blocked {
		// Refused is what matters here. Loopback is refused in its own words,
		// because it is the commonest mistake and the reader needs telling what
		// to type instead; the test below is the one that holds that apart.
		err := policy.CheckEgress(raw)
		if !errors.Is(err, ErrPrivateAddress) && !errors.Is(err, ErrLoopbackAddress) {
			t.Fatalf("CheckEgress(%q) should have been refused, got %v", raw, err)
		}
	}
	if err := policy.CheckEgress("https://93.184.216.34/"); err != nil {
		t.Fatalf("a public address should pass, got %v", err)
	}
}

func TestValidateAuthPairing(t *testing.T) {
	// Header auth without a header name would send the credential nowhere and
	// look like the endpoint rejected us, so it is refused up front.
	if _, _, err := ValidateAuth(AuthHeader, "  "); !errors.Is(err, ErrAuthHeaderName) {
		t.Fatalf("header auth needs a header name, got %v", err)
	}
	if _, _, err := ValidateAuth("basic", ""); !errors.Is(err, ErrAuthType) {
		t.Fatal("an unknown auth type should be refused rather than downgraded")
	}
	// A header name left over from a previous choice is cleared, so it cannot
	// reappear if the type is switched back.
	kind, name, err := ValidateAuth(AuthBearer, "X-Api-Key")
	if err != nil || kind != AuthBearer || name != "" {
		t.Fatalf("bearer should drop the header name, got %q %q %v", kind, name, err)
	}
}

func TestValidateActionName(t *testing.T) {
	// The name travels to the model as an identifier, so anything it cannot
	// call back reliably is refused.
	for _, raw := range []string{"", "look up", "look.up", "look-up", "tra cứu"} {
		if _, err := ValidateActionName(raw); !errors.Is(err, ErrActionName) {
			t.Fatalf("ValidateActionName(%q) should have been refused, got %v", raw, err)
		}
	}
	if got, err := ValidateActionName("  lookup_customer  "); err != nil || got != "lookup_customer" {
		t.Fatalf("a valid name should be trimmed and kept, got %q %v", got, err)
	}
}

func TestValidatePathStaysRelative(t *testing.T) {
	// An absolute URL here would reach a host the workspace never approved.
	for _, raw := range []string{"customers", "https://evil.example.com/", "//evil.example.com"} {
		if _, err := ValidatePath(raw); !errors.Is(err, ErrActionPath) {
			t.Fatalf("ValidatePath(%q) should have been refused, got %v", raw, err)
		}
	}
	if got, err := ValidatePath(""); err != nil || got != "/" {
		t.Fatalf("an empty path should become the root, got %q %v", got, err)
	}
}

func TestCleanParameters(t *testing.T) {
	got, err := CleanParameters([]Parameter{
		{Name: " id ", Type: "integer", In: "header"},
		{Name: "id", Type: "string", In: "query"},
		{Name: "  ", Type: "string"},
		{Name: "note", Type: "boolean", In: "body"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("blanks and duplicates should be dropped, got %#v", got)
	}
	// An unknown type is described as a string rather than refused: a wrong
	// type is better than no description of the action at all.
	if got[0].Name != "id" || got[0].Type != "string" || got[0].In != "query" {
		t.Fatalf("unknown type and location should fall back, got %#v", got[0])
	}
	if got[1].In != "body" || got[1].Type != "boolean" {
		t.Fatalf("known values should survive, got %#v", got[1])
	}

	many := make([]Parameter, MaxParameters+1)
	for i := range many {
		many[i] = Parameter{Name: string(rune('a'+i%26)) + strings.Repeat("x", i)}
	}
	if _, err := CleanParameters(many); !errors.Is(err, ErrTooManyParams) {
		t.Fatalf("an overlong parameter list should be refused, got %v", err)
	}
}

func TestNormalizeVisibilityNarrows(t *testing.T) {
	// A tool can carry a credential and can now be offered to every workspace,
	// so the fallback matters more than it did: anything unrecognised lands on
	// private, never on a wider rung.
	for _, raw := range []string{"", "public", "all", "WORKSPACE", "Everyone", "shared"} {
		if got := NormalizeVisibility(raw); got != Private {
			t.Errorf("NormalizeVisibility(%q) = %q, want %q", raw, got, Private)
		}
	}
	// The four rungs that exist are kept exactly.
	for _, raw := range []string{Private, Shared, Selected, Everyone} {
		if got := NormalizeVisibility(raw); got != raw {
			t.Errorf("NormalizeVisibility(%q) = %q", raw, got)
		}
	}
}

func TestBuildRequestPlacesArguments(t *testing.T) {
	tool := Tool{BaseURL: "https://api.example.com"}
	action := Action{
		Method: "POST",
		Path:   "/customers/{id}/notes",
		Parameters: []Parameter{
			{Name: "id", In: "path"},
			{Name: "locale", In: "query"},
			{Name: "note", In: "body"},
		},
	}
	target, body, err := buildRequest(EgressPolicy{}, tool, action, map[string]any{"id": "42", "locale": "vi", "note": "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target != "https://api.example.com/customers/42/notes?locale=vi" {
		t.Fatalf("arguments landed in the wrong place: %s", target)
	}
	if !body.isJSON {
		t.Fatal("a body parameter on POST should be sent as JSON")
	}

	// A GET carries no body even if a parameter says otherwise, and a path
	// value cannot smuggle in extra segments.
	action.Method = "GET"
	target, body, err = buildRequest(EgressPolicy{}, tool, action, map[string]any{"id": "../admin", "note": "x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body.reader != nil {
		t.Fatal("GET should not carry a body")
	}
	if strings.Contains(target, "../") {
		t.Fatalf("a path argument must be escaped, got %s", target)
	}
}

// A fixed parameter is the tool's own value: the model never sees it, and a
// call that names it anyway does not get to override it.
func TestCleanParametersKeepsFixedValues(t *testing.T) {
	cleaned, err := CleanParameters([]Parameter{
		{Name: "q", Description: "Search terms", Type: "string", In: "query", IsRequired: true},
		{Name: "format", Description: "Response format", Type: "string", In: "query", Source: SourceFixed, Value: "json", IsRequired: true},
	})
	if err != nil {
		t.Fatalf("clean: %v", err)
	}
	if cleaned[0].Source != SourceModel || cleaned[0].Value != "" {
		t.Errorf("model parameter carried a value: %+v", cleaned[0])
	}
	if !cleaned[1].IsFixed() || cleaned[1].Value != "json" {
		t.Errorf("fixed parameter lost its value: %+v", cleaned[1])
	}
	// "Required" asks the model for something that is already supplied.
	if cleaned[1].IsRequired {
		t.Error("a fixed parameter is still marked required of the model")
	}
}

// A fixed parameter with nothing to send is unfinished, not empty: dropping it
// would send a request missing something the author thought they had set.
func TestCleanParametersRefusesFixedWithoutValue(t *testing.T) {
	if _, err := CleanParameters([]Parameter{
		{Name: "format", Description: "Response format", Type: "string", In: "query", Source: SourceFixed},
	}); !errors.Is(err, ErrFixedNeedsValue) {
		t.Fatalf("got %v, want ErrFixedNeedsValue", err)
	}
}

// The whole point: the constant reaches the request without the model naming
// it, and a model that names it anyway is ignored.
func TestBuildRequestSendsFixedValuesAndIgnoresOverrides(t *testing.T) {
	action := Action{
		Method: "GET",
		Path:   "/v1/forecast",
		Parameters: []Parameter{
			{Name: "latitude", Type: "number", In: "query"},
			{Name: "current", Type: "string", In: "query", Source: SourceFixed, Value: "temperature_2m"},
		},
	}
	target, _, err := buildRequest(EgressPolicy{}, Tool{BaseURL: "https://api.example.com"}, action,
		map[string]any{"latitude": 10.78, "current": "something_else"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(target, "current=temperature_2m") {
		t.Errorf("fixed value missing from %q", target)
	}
	if strings.Contains(target, "something_else") {
		t.Errorf("model overrode a fixed value: %q", target)
	}
	if !strings.Contains(target, "latitude=10.78") {
		t.Errorf("model value missing from %q", target)
	}
}

// The description the model reads is the only place the result shape can
// change a decision, so it has to reach it - and say nothing when the author
// has said nothing, because "Returns: " with nothing after it is worse.
func TestDescribeResult(t *testing.T) {
	cases := []struct {
		action Action
		want   string
	}{
		{Action{ResultType: "object", ResultDescription: "current.temperature_2m in Celsius."},
			"Returns object: current.temperature_2m in Celsius."},
		{Action{ResultDescription: "The value of the expression."},
			"Returns: The value of the expression."},
		{Action{ResultType: "array"}, "Returns array."},
		{Action{}, ""},
	}
	for _, item := range cases {
		if got := describeResult(item.action); got != item.want {
			t.Errorf("describeResult(%+v) = %q, want %q", item.action, got, item.want)
		}
	}
}

// A type outside the shapes JSON has is dropped rather than passed through to
// the model as a fact about the answer.
func TestValidateResultType(t *testing.T) {
	for raw, want := range map[string]string{
		"object": "object", " array ": "array", "number": "number",
		"": "", "int64": "", "Object": "",
	} {
		if got := ValidateResultType(raw); got != want {
			t.Errorf("ValidateResultType(%q) = %q, want %q", raw, got, want)
		}
	}
}

// Every catalogue action ships describing its answer: an entry installed to
// work on the first call should not leave the model guessing on the second.
func TestCatalogActionsDescribeTheirResult(t *testing.T) {
	for _, entry := range Catalog() {
		for _, action := range entry.Actions {
			if describeResult(action) == "" {
				t.Errorf("%s/%s says nothing about what it returns", entry.ID, action.Name)
			}
		}
	}
}

// A built-in runs in this process, so its only valid destination is none.
//
// The rule was written into Create and Install but not Update, which is a
// difference no reader could see and a built-in could feel: it installed, then
// refused to be renamed because the editor sent the empty address field every
// tool editor sends.
func TestBaseURLForKindLetsABuiltinHaveNoAddress(t *testing.T) {
	for _, raw := range []string{"", "   "} {
		got, err := BaseURLForKind(KindBuiltin, raw)
		if err != nil {
			t.Fatalf("a built-in was refused for having no address: %v", err)
		}
		if got != "" {
			t.Fatalf("a built-in was given the address %q", got)
		}
	}
}

// Refused rather than quietly dropped: an address arriving for a built-in is
// not a typo, it is a misunderstanding of what the tool is, and silently
// ignoring it would leave the sender believing it had been saved.
func TestBaseURLForKindRefusesAnAddressForABuiltin(t *testing.T) {
	if _, err := BaseURLForKind(KindBuiltin, "https://api.example.com"); !errors.Is(err, ErrBuiltinHasNoBaseURL) {
		t.Fatalf("a built-in accepted an endpoint: %v", err)
	}
}

// Everything else still has to have one, and still has to be reachable.
func TestBaseURLForKindStillDemandsOneOfEverythingElse(t *testing.T) {
	for _, kind := range []string{KindHTTP, KindMCP} {
		if _, err := BaseURLForKind(kind, ""); !errors.Is(err, ErrBaseURL) {
			t.Fatalf("%s was allowed to have no endpoint: %v", kind, err)
		}
		got, err := BaseURLForKind(kind, "https://api.example.com/")
		if err != nil || got != "https://api.example.com" {
			t.Fatalf("%s lost its endpoint: %q %v", kind, got, err)
		}
	}
}

// localhost is what anyone running a server on their own machine types, and it
// is the one address certainly wrong here: the backend is in a container, so
// localhost there is the container. Being told "internal addresses are refused"
// is true and leaves the reader with nothing to do.
func TestCheckEgressTellsLoopbackApartFromThePrivateNetwork(t *testing.T) {
	policy := EgressPolicy{}
	for _, raw := range []string{"http://localhost:8000/mcp", "http://127.0.0.1:8000/mcp", "http://[::1]:8000/mcp"} {
		err := policy.CheckEgress(raw)
		if !errors.Is(err, ErrLoopbackAddress) {
			t.Fatalf("%s should name the fix, got %v", raw, err)
		}
	}
	// A LAN address is a different mistake and keeps the general wording.
	if err := policy.CheckEgress("http://10.0.0.5/api"); !errors.Is(err, ErrPrivateAddress) {
		t.Fatalf("a private address should not be reported as loopback: %v", err)
	}
	if !strings.Contains(ErrLoopbackAddress.Error(), "host.docker.internal") {
		t.Fatalf("the message should name what to type instead: %v", ErrLoopbackAddress)
	}
}
