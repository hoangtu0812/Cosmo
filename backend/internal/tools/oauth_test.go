package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// issuer stands in for Entra: it answers client_credentials with a token and
// counts how many times it was asked, which is the only way to see a cache
// working.
type issuer struct {
	server   *httptest.Server
	requests int
	sawForm  url.Values
	lifetime int
	token    string
	status   int
}

func newIssuer(t *testing.T) *issuer {
	t.Helper()
	stub := &issuer{lifetime: 3600, token: "issued-token-1", status: http.StatusOK}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stub.requests++
		_ = r.ParseForm()
		stub.sawForm = r.PostForm
		if stub.status != http.StatusOK {
			w.WriteHeader(stub.status)
			_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
			return
		}
		body, _ := json.Marshal(map[string]any{
			"access_token": stub.token,
			"token_type":   "Bearer",
			"expires_in":   stub.lifetime,
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

func (stub *issuer) credential(scope string) string {
	body, _ := json.Marshal(oauthCredential{
		TokenURL:     stub.server.URL,
		ClientID:     "cosmo-client",
		ClientSecret: "cosmo-secret",
		Scope:        scope,
	})
	return string(body)
}

func repositoryForIssuer(t *testing.T, stub *issuer) *Repository {
	t.Helper()
	parsed, err := url.Parse(stub.server.URL)
	if err != nil {
		t.Fatalf("issuer URL: %v", err)
	}
	return &Repository{egress: EgressPolicy{AllowedHosts: []string{parsed.Hostname()}}}
}

func TestAccessTokenAsksForClientCredentials(t *testing.T) {
	stub := newIssuer(t)
	repository := repositoryForIssuer(t, stub)

	token, err := repository.accessToken(context.Background(), "tol_a", stub.credential("api://sap/.default"))
	if err != nil {
		t.Fatalf("token request failed: %v", err)
	}
	if token != "issued-token-1" {
		t.Fatalf("wrong token returned: %q", token)
	}
	if got := stub.sawForm.Get("grant_type"); got != "client_credentials" {
		t.Fatalf("grant was %q; a service calling a service is not a user signing in", got)
	}
	if stub.sawForm.Get("client_id") != "cosmo-client" || stub.sawForm.Get("client_secret") != "cosmo-secret" {
		t.Fatalf("registration did not arrive: %v", stub.sawForm)
	}
	// Entra will not issue for an application without one.
	if stub.sawForm.Get("scope") != "api://sap/.default" {
		t.Fatalf("scope was dropped: %q", stub.sawForm.Get("scope"))
	}
}

// A token good for an hour fetched once per call would mean a round trip to the
// issuer before every tool call, which is most of the latency and all of the
// rate limit.
func TestAccessTokenIsReusedUntilItNearlyExpires(t *testing.T) {
	stub := newIssuer(t)
	repository := repositoryForIssuer(t, stub)
	credential := stub.credential("")

	for range 3 {
		if _, err := repository.accessToken(context.Background(), "tol_a", credential); err != nil {
			t.Fatalf("token request failed: %v", err)
		}
	}
	if stub.requests != 1 {
		t.Fatalf("the issuer was asked %d times for one live token", stub.requests)
	}
}

// A token that expires in less time than the refresh margin would be cached
// already expired, and every call would then fetch a fresh one and use it a
// moment too late.
func TestAccessTokenDoesNotCacheATokenShorterThanTheMargin(t *testing.T) {
	stub := newIssuer(t)
	stub.lifetime = 30 // shorter than oauthRefreshMargin
	repository := repositoryForIssuer(t, stub)
	credential := stub.credential("")

	for range 2 {
		if _, err := repository.accessToken(context.Background(), "tol_a", credential); err != nil {
			t.Fatalf("token request failed: %v", err)
		}
	}
	// Held, rather than discarded on arrival: a floor is applied so a short
	// token is still worth something.
	if stub.requests != 1 {
		t.Fatalf("a short-lived token was not held at all, issuer asked %d times", stub.requests)
	}
}

// Rotating the registration must not leave the old token in play: it will keep
// working until it expires, which is the worst way to find out a rotation did
// not take.
func TestAccessTokenIsDroppedWhenTheRegistrationChanges(t *testing.T) {
	stub := newIssuer(t)
	repository := repositoryForIssuer(t, stub)

	if _, err := repository.accessToken(context.Background(), "tol_a", stub.credential("")); err != nil {
		t.Fatalf("first token failed: %v", err)
	}
	rotated, _ := json.Marshal(oauthCredential{
		TokenURL: stub.server.URL, ClientID: "cosmo-client", ClientSecret: "rotated-secret",
	})
	stub.token = "issued-token-2"
	token, err := repository.accessToken(context.Background(), "tol_a", string(rotated))
	if err != nil {
		t.Fatalf("token after rotation failed: %v", err)
	}
	if token != "issued-token-2" {
		t.Fatalf("the token from the old registration survived: %q", token)
	}
	if stub.sawForm.Get("client_secret") != "rotated-secret" {
		t.Fatalf("the old secret was sent after rotation: %v", stub.sawForm)
	}
}

func TestAccessTokenRefusesAnIncompleteRegistration(t *testing.T) {
	stub := newIssuer(t)
	repository := repositoryForIssuer(t, stub)

	for _, stored := range []string{
		"",
		"not json at all",
		`{"token_url":"https://issuer.example.com/token"}`,
		`{"client_id":"a","client_secret":"b"}`,
	} {
		if _, err := repository.accessToken(context.Background(), "tol_a", stored); !errors.Is(err, ErrOAuthConfig) {
			t.Fatalf("%q should have been refused as incomplete, got %v", stored, err)
		}
	}
	if stub.requests != 0 {
		t.Fatal("an incomplete registration still reached the issuer")
	}
}

func TestAccessTokenReportsAnIssuerThatRefuses(t *testing.T) {
	stub := newIssuer(t)
	stub.status = http.StatusUnauthorized
	repository := repositoryForIssuer(t, stub)

	if _, err := repository.accessToken(context.Background(), "tol_a", stub.credential("")); !errors.Is(err, ErrOAuthToken) {
		t.Fatalf("a refusing issuer should be reported as such, got %v", err)
	}
}

// The token endpoint is a URL somebody typed. An issuer that could be pointed
// at the internal network would be a way around the egress guard rather than an
// exception to it.
func TestAccessTokenGuardsTheIssuerAddressToo(t *testing.T) {
	stub := newIssuer(t)
	repository := &Repository{egress: EgressPolicy{}} // names nothing

	_, err := repository.accessToken(context.Background(), "tol_a", stub.credential(""))
	if err == nil {
		t.Fatal("an issuer on a private address was called anyway")
	}
	if errors.Is(err, ErrOAuthToken) {
		t.Fatalf("refused for the wrong reason - the guard should have stopped it: %v", err)
	}
}

func TestValidateAuthAcceptsOAuthWithoutAHeaderName(t *testing.T) {
	kind, name, err := ValidateAuth(AuthOAuth, "")
	if err != nil {
		t.Fatalf("oauth2 should be a valid auth type: %v", err)
	}
	if kind != AuthOAuth || name != "" {
		t.Fatalf("unexpected result: %q %q", kind, name)
	}
}

// The MCP client and the plain HTTP caller authenticate through one function,
// so a credential that works for one works for the other.
func TestAuthoriseHandlesEveryKind(t *testing.T) {
	for _, kind := range []string{AuthNone, AuthBearer, AuthHeader, AuthOAuth} {
		if _, _, err := ValidateAuth(kind, "X-Api-Key"); err != nil {
			t.Fatalf("%s should be a valid auth type: %v", kind, err)
		}
	}
	if !strings.Contains(ErrOAuthConfig.Error(), "token_url") {
		t.Fatalf("the refusal should name the missing fields: %v", ErrOAuthConfig)
	}
}
