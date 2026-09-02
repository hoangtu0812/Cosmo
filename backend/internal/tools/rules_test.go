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
		if err := policy.CheckEgress(raw); !errors.Is(err, ErrPrivateAddress) {
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
	// A tool carries a credential, so a typo must never widen who can use it.
	for _, raw := range []string{"", "public", "everyone", "WORKSPACE"} {
		if got := NormalizeVisibility(raw); got != Private {
			t.Fatalf("NormalizeVisibility(%q) = %q, want %q", raw, got, Private)
		}
	}
	if got := NormalizeVisibility(Shared); got != Shared {
		t.Fatalf("NormalizeVisibility(%q) = %q", Shared, got)
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
