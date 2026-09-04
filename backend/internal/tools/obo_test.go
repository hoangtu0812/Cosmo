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
	"time"
)

// oboIssuer answers a jwt-bearer exchange, and records the assertion it was
// given so a test can tell whose token was presented.
type oboIssuer struct {
	server        *httptest.Server
	requests      int
	sawAssertions []string
	sawGrant      string
	status        int
	body          string
}

func newOBOIssuer(t *testing.T) *oboIssuer {
	t.Helper()
	stub := &oboIssuer{status: http.StatusOK}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stub.requests++
		_ = r.ParseForm()
		stub.sawGrant = r.PostForm.Get("grant_type")
		stub.sawAssertions = append(stub.sawAssertions, r.PostForm.Get("assertion"))
		w.Header().Set("Content-Type", "application/json")
		if stub.status != http.StatusOK {
			w.WriteHeader(stub.status)
			_, _ = w.Write([]byte(stub.body))
			return
		}
		// Named after the assertion so a test can see whose authority came back.
		body, _ := json.Marshal(map[string]any{
			"access_token": "for:" + r.PostForm.Get("assertion"),
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
		_, _ = w.Write(body)
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

func (stub *oboIssuer) registration() string {
	body, _ := json.Marshal(oauthCredential{
		TokenURL:     stub.server.URL,
		ClientID:     "cosmo-client",
		ClientSecret: "cosmo-secret",
		Scope:        "api://sap/.default",
	})
	return string(body)
}

// repositoryForOBO answers every user with a token named after them.
func repositoryForOBO(t *testing.T, stub *oboIssuer) *Repository {
	t.Helper()
	parsed, err := url.Parse(stub.server.URL)
	if err != nil {
		t.Fatalf("issuer URL: %v", err)
	}
	repository := &Repository{egress: EgressPolicy{AllowedHosts: []string{parsed.Hostname()}}}
	repository.UseAssertions(func(_ context.Context, userID string) (Assertion, error) {
		return Assertion{Token: "assertion-" + userID, ExpiresAt: time.Now().Add(time.Hour)}, nil
	})
	return repository
}

func callerCtx(userID string) context.Context {
	return WithCaller(context.Background(), Caller{UserID: userID, UserName: userID})
}

func TestOBOExchangesTheUsersOwnToken(t *testing.T) {
	stub := newOBOIssuer(t)
	repository := repositoryForOBO(t, stub)

	token, err := repository.oboToken(callerCtx("usr_an"), "tol_sap", stub.registration())
	if err != nil {
		t.Fatalf("exchange failed: %v", err)
	}
	if stub.sawGrant != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
		t.Fatalf("wrong grant: %q", stub.sawGrant)
	}
	// The whole point: the server is told who is asking, not merely that Cosmo
	// asked.
	if stub.sawAssertions[0] != "assertion-usr_an" {
		t.Fatalf("someone else's token was presented: %q", stub.sawAssertions[0])
	}
	if token != "for:assertion-usr_an" {
		t.Fatalf("wrong token returned: %q", token)
	}
}

// The failure this feature exists to prevent, turned into a test. A cache keyed
// only by tool would hand the second caller the first caller's authority - and
// against an SAP policy table that means one person reading another's plant.
func TestOBOKeepsOnePersonsTokenFromAnother(t *testing.T) {
	stub := newOBOIssuer(t)
	repository := repositoryForOBO(t, stub)
	registration := stub.registration()

	first, err := repository.oboToken(callerCtx("usr_an"), "tol_sap", registration)
	if err != nil {
		t.Fatalf("first exchange failed: %v", err)
	}
	second, err := repository.oboToken(callerCtx("usr_binh"), "tol_sap", registration)
	if err != nil {
		t.Fatalf("second exchange failed: %v", err)
	}
	if first == second {
		t.Fatalf("two people shared one token: %q", first)
	}
	if stub.requests != 2 {
		t.Fatalf("the second caller was served from the first caller's cache entry")
	}
}

func TestOBOReusesOneUsersToken(t *testing.T) {
	stub := newOBOIssuer(t)
	repository := repositoryForOBO(t, stub)
	registration := stub.registration()

	for range 3 {
		if _, err := repository.oboToken(callerCtx("usr_an"), "tol_sap", registration); err != nil {
			t.Fatalf("exchange failed: %v", err)
		}
	}
	if stub.requests != 1 {
		t.Fatalf("the issuer was asked %d times for one live token", stub.requests)
	}
}

// An exchanged token may be granted an hour while the assertion behind it has
// minutes left. Outliving the assertion means calls that fail for a reason
// nobody can see.
func TestOBOTokenNeverOutlivesItsAssertion(t *testing.T) {
	stub := newOBOIssuer(t)
	parsed, _ := url.Parse(stub.server.URL)
	repository := &Repository{egress: EgressPolicy{AllowedHosts: []string{parsed.Hostname()}}}
	repository.UseAssertions(func(_ context.Context, userID string) (Assertion, error) {
		// Less than the refresh margin, so nothing may be cached at all.
		return Assertion{Token: "assertion-" + userID, ExpiresAt: time.Now().Add(20 * time.Second)}, nil
	})

	registration := stub.registration()
	for range 2 {
		if _, err := repository.oboToken(callerCtx("usr_an"), "tol_sap", registration); err != nil {
			t.Fatalf("exchange failed: %v", err)
		}
	}
	if stub.requests != 2 {
		t.Fatal("a token was cached past the life of the assertion it came from")
	}
}

// A scheduled run has nobody behind it. That is a tool used outside what it is
// for, and saying so is more use than a failed exchange.
func TestOBORefusesWithoutAUser(t *testing.T) {
	stub := newOBOIssuer(t)
	repository := repositoryForOBO(t, stub)

	_, err := repository.oboToken(context.Background(), "tol_sap", stub.registration())
	if !errors.Is(err, ErrOBONoUser) {
		t.Fatalf("a call with no caller should say so, got %v", err)
	}
	if stub.requests != 0 {
		t.Fatal("an exchange was attempted with no assertion to exchange")
	}
}

func TestOBORefusesWhenNoAssertionSourceIsConfigured(t *testing.T) {
	stub := newOBOIssuer(t)
	parsed, _ := url.Parse(stub.server.URL)
	repository := &Repository{egress: EgressPolicy{AllowedHosts: []string{parsed.Hostname()}}}

	_, err := repository.oboToken(callerCtx("usr_an"), "tol_sap", stub.registration())
	if !errors.Is(err, ErrOBOUnavailable) {
		t.Fatalf("expected the unavailable error, got %v", err)
	}
}

// "The exchange failed" sends a reader to check the secret. invalid_grant says
// the assertion is stale or addressed to the wrong audience, which is where the
// fix actually is.
func TestOBOKeepsTheIssuersOwnErrorCode(t *testing.T) {
	stub := newOBOIssuer(t)
	stub.status = http.StatusBadRequest
	stub.body = `{"error":"invalid_grant","error_description":"AADSTS500133: assertion is not within its valid time range"}`
	repository := repositoryForOBO(t, stub)

	_, err := repository.oboToken(callerCtx("usr_an"), "tol_sap", stub.registration())
	if !errors.Is(err, ErrOAuthToken) {
		t.Fatalf("should still be a token error: %v", err)
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("the issuer's own code should survive: %v", err)
	}
}

// The token endpoint is a URL somebody typed, here as much as anywhere.
func TestOBOGuardsTheIssuerAddress(t *testing.T) {
	stub := newOBOIssuer(t)
	repository := &Repository{egress: EgressPolicy{}}
	repository.UseAssertions(func(_ context.Context, userID string) (Assertion, error) {
		return Assertion{Token: "assertion", ExpiresAt: time.Now().Add(time.Hour)}, nil
	})

	_, err := repository.oboToken(callerCtx("usr_an"), "tol_sap", stub.registration())
	if err == nil {
		t.Fatal("an issuer on a private address was called anyway")
	}
	if errors.Is(err, ErrOAuthToken) {
		t.Fatalf("refused for the wrong reason - the guard should have stopped it: %v", err)
	}
}

func TestValidateAuthAcceptsOnBehalfOf(t *testing.T) {
	kind, name, err := ValidateAuth(AuthOBO, "")
	if err != nil || kind != AuthOBO || name != "" {
		t.Fatalf("oauth2_obo should be a valid auth type: %q %q %v", kind, name, err)
	}
}
