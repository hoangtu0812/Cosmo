package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"cosmo/backend/internal/secrets"
)

// A static key is a credential that never changes. An OAuth server issues one
// that expires in an hour, which is the whole reason this exists: a token
// pasted into a field works until lunchtime and then the tool stops, with no
// way for anyone to tell that expiry is what happened.
//
// The MCP spec names OAuth 2.1 as its authorisation story, and a server built
// on the official SDKs turns that on by declaring AuthSettings. Cosmo is not a
// person clicking a consent screen; it is a service calling another service on
// its own behalf, so the grant is client credentials.
const AuthOAuth = "oauth2"

// AuthOAuthUser is the MCP-native authorization-code profile. Unlike OBO it
// does not exchange Cosmo's login token and therefore does not depend on the
// identity provider used to sign in to Cosmo.
const AuthOAuthUser = "oauth2_user"

// oauthCredential is the whole registration, and it is stored as one encrypted
// value rather than spread across columns.
//
// That is not a shortcut. An issuer, a client id, a secret and a scope are
// meaningless apart and are replaced together when the registration is
// rotated; splitting them would invite a half-rotated tool that authenticates
// as the old client against the new secret.
type oauthCredential struct {
	TokenURL     string `json:"token_url"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	// Entra wants "api://<app-id>/.default" here. Other issuers want a
	// space-separated list. Either is passed through untouched.
	Scope string `json:"scope,omitempty"`
}

func (credential oauthCredential) isComplete() bool {
	return strings.TrimSpace(credential.TokenURL) != "" &&
		strings.TrimSpace(credential.ClientID) != "" &&
		strings.TrimSpace(credential.ClientSecret) != ""
}

// fingerprint identifies this registration without being it. A cached token
// belongs to the registration that fetched it, so rotating the secret must not
// leave the old token in play - and comparing the secrets themselves would mean
// holding two of them in memory to do it.
func (credential oauthCredential) fingerprint() string {
	sum := sha256.Sum256([]byte(credential.TokenURL + "\x00" + credential.ClientID + "\x00" + credential.ClientSecret + "\x00" + credential.Scope))
	return hex.EncodeToString(sum[:])
}

// A token is refreshed before it expires rather than after: a call that starts
// inside the window and arrives outside it fails for a reason nobody can see.
const oauthRefreshMargin = 60 * time.Second

type cachedToken struct {
	value       string
	expiresAt   time.Time
	fingerprint string
}

// tokenCache holds one token per tool. It is small and process-local on
// purpose: a token is cheap to re-fetch and expensive to store badly, and a
// restart losing them costs one round trip.
type tokenCache struct {
	mutex  sync.Mutex
	tokens map[string]cachedToken
}

func (cache *tokenCache) read(toolID, fingerprint string) (string, bool) {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	entry, found := cache.tokens[toolID]
	if !found || entry.fingerprint != fingerprint {
		return "", false
	}
	if time.Now().After(entry.expiresAt) {
		return "", false
	}
	return entry.value, true
}

func (cache *tokenCache) write(toolID, fingerprint, value string, lifetime time.Duration) {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	if cache.tokens == nil {
		cache.tokens = map[string]cachedToken{}
	}
	expiresAt := time.Now().Add(lifetime - oauthRefreshMargin)
	cache.tokens[toolID] = cachedToken{value: value, expiresAt: expiresAt, fingerprint: fingerprint}
}

// authorise attaches whatever credential the tool carries. One place, because
// an MCP server and a plain HTTP endpoint are authenticated identically and
// having said so twice is how the two drift.
func (repository *Repository) authorise(ctx context.Context, tool Tool, request *http.Request) error {
	if tool.AuthType == AuthNone {
		return nil
	}
	secret, err := repository.secretFor(ctx, tool.ID)
	if err != nil {
		return err
	}
	if secret == "" {
		return nil
	}

	switch tool.AuthType {
	case AuthBearer:
		request.Header.Set("Authorization", "Bearer "+secret)
	case AuthHeader:
		request.Header.Set(tool.AuthHeaderName, secret)
	case AuthOAuth:
		token, err := repository.accessToken(ctx, tool.ID, secret)
		if err != nil {
			return err
		}
		request.Header.Set("Authorization", "Bearer "+token)
	case AuthOAuthUser:
		token, err := repository.oauthUserAccessToken(ctx, tool.ID, secret)
		if err != nil {
			return err
		}
		request.Header.Set("Authorization", "Bearer "+token)
	}
	return nil
}

// accessToken returns a live token for the tool, fetching one if the cache has
// nothing usable.
func (repository *Repository) accessToken(ctx context.Context, toolID, stored string) (string, error) {
	var credential oauthCredential
	if err := json.Unmarshal([]byte(stored), &credential); err != nil || !credential.isComplete() {
		return "", ErrOAuthConfig
	}
	fingerprint := credential.fingerprint()
	if token, found := repository.tokens.read(toolID, fingerprint); found {
		return token, nil
	}

	// The token endpoint is a URL somebody typed, so it goes through the same
	// guard as the tool it authenticates. An issuer that could be pointed at
	// the internal network would be a way around the guard, not an exception
	// to it.
	if err := repository.egress.CheckEgress(credential.TokenURL); err != nil {
		return "", err
	}

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", credential.ClientID)
	form.Set("client_secret", credential.ClientSecret)
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
	// Capped: an issuer answering with something enormous is not an issuer we
	// want to read into memory.
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if err != nil {
		return "", ErrOAuthToken
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", ErrOAuthToken
	}

	var issued struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &issued); err != nil || strings.TrimSpace(issued.AccessToken) == "" {
		return "", ErrOAuthToken
	}
	// An issuer that does not say gets the shortest sensible life rather than
	// an assumed hour: being wrong in this direction costs a round trip, and in
	// the other it costs every call until someone investigates.
	lifetime := time.Duration(issued.ExpiresIn) * time.Second
	if lifetime <= oauthRefreshMargin {
		lifetime = 2 * oauthRefreshMargin
	}
	repository.tokens.write(toolID, fingerprint, issued.AccessToken, lifetime)
	return issued.AccessToken, nil
}

// hintFor describes a stored credential well enough to tell one from another
// without revealing it.
//
// The generic hint is the last four characters, which for a key is exactly
// right and for an OAuth registration is the tail of its JSON - a reader saw
// «lt"} » and learned the storage format rather than which client is
// configured. The client id is not a secret and is the thing that identifies
// the registration, so that is what gets shown.
func hintFor(authType, stored string) string {
	if authType == AuthOAuth {
		var credential oauthCredential
		if err := json.Unmarshal([]byte(stored), &credential); err == nil {
			if id := strings.TrimSpace(credential.ClientID); id != "" {
				return secrets.Hint(id)
			}
		}
	}
	if authType == AuthOAuthUser {
		var credential oauthUserRegistration
		if err := json.Unmarshal([]byte(stored), &credential); err == nil {
			if id := strings.TrimSpace(credential.ClientID); id != "" {
				return secrets.Hint(id)
			}
		}
	}
	return secrets.Hint(stored)
}
