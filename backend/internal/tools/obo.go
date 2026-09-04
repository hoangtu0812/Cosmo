package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client credentials makes Cosmo call as Cosmo. Every request arrives as one
// service principal, carrying no user and no groups, so a resource server that
// scopes data per person is handed nothing to decide with. Its per-user rules
// collapse into one rule for the platform, and its audit log records "Cosmo"
// for work somebody did.
//
// On-behalf-of exchanges the user's own access token for one addressed to that
// server. The resource server then sees the person: their object id, their
// groups, their consent. The policy model it was built around works, and the
// audit trail is true.
//
// RFC 8693 calls this token exchange; Entra implements the jwt-bearer form.
const AuthOBO = "oauth2_obo"

// Assertion is the user's own token, and how long it is good for. It is
// fetched per call rather than held, because the thing it authorises is one
// person's request and holding it would outlive that.
type Assertion struct {
	Token     string
	ExpiresAt time.Time
}

// AssertionSource hands back the signed-in user's own access token.
//
// The tools package must not learn how sessions or identity providers work, so
// it is given a function rather than a database. The transport layer already
// knows who is asking; this is the same fact, one step further in.
type AssertionSource func(ctx context.Context, userID string) (Assertion, error)

// UseAssertions supplies the source. Absent, on-behalf-of tools refuse at call
// time with a message that says what is missing rather than a failed exchange
// nobody can read.
func (repository *Repository) UseAssertions(source AssertionSource) {
	repository.assertions = source
}

// oboToken exchanges one user's token for one addressed to the tool's server.
//
// Cached per user, not per tool alone: two people using the same tool must not
// share a token, because the whole point is that the resource server can tell
// them apart. A cache keyed only by tool would hand the second caller the
// first caller's authority - the exact failure this feature exists to remove.
func (repository *Repository) oboToken(ctx context.Context, toolID string, stored string) (string, error) {
	var credential oauthCredential
	if err := json.Unmarshal([]byte(stored), &credential); err != nil || !credential.isComplete() {
		return "", ErrOAuthConfig
	}
	caller, ok := CallerFrom(ctx)
	if !ok || strings.TrimSpace(caller.UserID) == "" {
		// A scheduled run has no person behind it. That is not a failure to
		// authenticate but a tool used outside what it is for, and saying so
		// is more use than "the exchange failed".
		return "", ErrOBONoUser
	}
	if repository.assertions == nil {
		return "", ErrOBOUnavailable
	}

	cacheKey := toolID + "\x00" + caller.UserID
	fingerprint := credential.fingerprint()
	if token, found := repository.tokens.read(cacheKey, fingerprint); found {
		return token, nil
	}

	assertion, err := repository.assertions(ctx, caller.UserID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(assertion.Token) == "" {
		return "", ErrOBONoAssertion
	}

	if err := repository.egress.CheckEgress(credential.TokenURL); err != nil {
		return "", err
	}

	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	form.Set("client_id", credential.ClientID)
	form.Set("client_secret", credential.ClientSecret)
	form.Set("assertion", assertion.Token)
	form.Set("requested_token_use", "on_behalf_of")
	if scope := strings.TrimSpace(credential.Scope); scope != "" {
		form.Set("scope", scope)
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, credential.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", ErrOAuthToken
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")

	response, err := repository.client().Do(request)
	if err != nil {
		return "", ErrOAuthToken
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if err != nil {
		return "", ErrOAuthToken
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Entra answers a refused exchange with a code worth repeating:
		// invalid_grant means the assertion is stale or has the wrong
		// audience, which is a different fix from a wrong secret.
		return "", oboFailure(raw, response.StatusCode)
	}

	var issued struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &issued); err != nil || strings.TrimSpace(issued.AccessToken) == "" {
		return "", ErrOAuthToken
	}
	lifetime := time.Duration(issued.ExpiresIn) * time.Second
	if lifetime <= oauthRefreshMargin {
		lifetime = 2 * oauthRefreshMargin
	}
	// Never outlive the assertion it was minted from: the exchanged token may
	// be granted an hour while the assertion has ten minutes left, and reusing
	// it after that is a call that fails for a reason nobody can see.
	if remaining := time.Until(assertion.ExpiresAt); !assertion.ExpiresAt.IsZero() && remaining < lifetime {
		lifetime = remaining
	}
	if lifetime > oauthRefreshMargin {
		repository.tokens.write(cacheKey, fingerprint, issued.AccessToken, lifetime)
	}
	return issued.AccessToken, nil
}

// oboFailure keeps the issuer's own error code. "The exchange failed" sends a
// reader to look at the secret; "invalid_grant" tells them the assertion is
// the problem, which is a different afternoon.
func oboFailure(raw []byte, status int) error {
	var reported struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(raw, &reported); err == nil && reported.Error != "" {
		return fmt.Errorf("%w (%s)", ErrOAuthToken, reported.Error)
	}
	return fmt.Errorf("%w (HTTP %d)", ErrOAuthToken, status)
}
