package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"cosmo/backend/internal/secrets"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestOAuthUserDatabaseRoundTrip crosses discovery, PKCE state persistence,
// code exchange and encrypted per-user token storage. It is opt-in because it
// needs a migrated Postgres database.
func TestOAuthUserDatabaseRoundTrip(t *testing.T) {
	databaseURL := os.Getenv("COSMO_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("database integration environment is not configured")
	}

	var sawVerifier, sawResource bool
	var provider *httptest.Server
	provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mcp":
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+provider.URL+`/resource-meta"`)
			w.WriteHeader(http.StatusUnauthorized)
		case "/resource-meta":
			writeTestJSON(t, w, map[string]any{
				"resource": provider.URL + "/mcp", "authorization_servers": []string{provider.URL + "/issuer"},
				"scopes_supported": []string{"orders.read"},
			})
		case "/.well-known/oauth-authorization-server/issuer":
			writeTestJSON(t, w, map[string]any{
				"issuer": provider.URL + "/issuer", "authorization_endpoint": provider.URL + "/authorize",
				"token_endpoint": provider.URL + "/token", "jwks_uri": provider.URL + "/jwks",
				"response_types_supported": []string{"code"}, "code_challenge_methods_supported": []string{"S256"},
				"token_endpoint_auth_methods_supported": []string{"none"},
			})
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse token request: %v", err)
			}
			sawVerifier = r.Form.Get("code_verifier") != ""
			sawResource = r.Form.Get("resource") == provider.URL+"/mcp"
			writeTestJSON(t, w, map[string]any{
				"access_token": "per-user-access", "refresh_token": "per-user-refresh",
				"token_type": "Bearer", "expires_in": 3600,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer provider.Close()

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	defer pool.Close()
	box, err := secrets.New("oauth-user-database-test-secret-that-is-long-enough")
	if err != nil {
		t.Fatalf("create secret box: %v", err)
	}

	userID, workspaceID, toolID := newID("usr_"), newID("wsp_"), newID("tol_")
	if _, err := pool.Exec(ctx, `INSERT INTO users(id, email, name) VALUES($1, $2, 'OAuth user test')`, userID, userID+"@test.invalid"); err != nil {
		t.Fatalf("create user: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	if _, err := pool.Exec(ctx, `INSERT INTO workspaces(id, name, slug, type) VALUES($1, 'OAuth user test', $2, 'personal')`, workspaceID, workspaceID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM workspaces WHERE id = $1`, workspaceID)

	registration := oauthUserRegistration{ClientID: "public-test-client", Scope: "orders.read", AuthorizationServer: provider.URL + "/issuer"}
	registrationJSON, _ := json.Marshal(registration)
	sealedRegistration, _ := box.Seal(string(registrationJSON))
	if _, err := pool.Exec(ctx, `
		INSERT INTO tools(id, name, owner_user_id, owner_workspace_id, kind, base_url, auth_type, auth_secret)
		VALUES($1, 'OAuth fixture', $2, $3, 'mcp', $4, 'oauth2_user', $5)`,
		toolID, userID, workspaceID, provider.URL+"/mcp", sealedRegistration); err != nil {
		t.Fatalf("create tool: %v", err)
	}

	repository := NewRepository(pool, nil, box, EgressPolicy{AllowedHosts: []string{mustURL(t, provider.URL).Hostname()}}, SearchBackend{})
	tool := Tool{ID: toolID, BaseURL: provider.URL + "/mcp", Kind: KindMCP, AuthType: AuthOAuthUser}
	callbackURL := "http://localhost:8080/api/tools/oauth/callback"
	started, err := repository.BeginOAuthAuthorization(ctx, tool, userID, workspaceID, callbackURL)
	if err != nil {
		t.Fatalf("begin authorization: %v", err)
	}
	authorizationURL, _ := url.Parse(started.AuthorizationURL)
	state := authorizationURL.Query().Get("state")
	if state == "" || authorizationURL.Query().Get("code_challenge_method") != "S256" || authorizationURL.Query().Get("resource") != provider.URL+"/mcp" {
		t.Fatalf("authorization request lacks state, PKCE or resource: %s", started.AuthorizationURL)
	}
	if _, err := repository.CompleteOAuthAuthorization(ctx, userID, state, "one-use-code", "", "", callbackURL); err != nil {
		t.Fatalf("complete authorization: %v", err)
	}
	if !sawVerifier || !sawResource {
		t.Fatalf("token exchange did not carry verifier/resource: verifier=%v resource=%v", sawVerifier, sawResource)
	}

	callContext := WithCaller(ctx, Caller{UserID: userID, WorkspaceID: workspaceID})
	access, err := repository.oauthUserAccessToken(callContext, toolID, string(registrationJSON))
	if err != nil || access != "per-user-access" {
		t.Fatalf("read stored user token: %q %v", access, err)
	}
	var encrypted []byte
	var expiresAt time.Time
	if err := pool.QueryRow(ctx, `SELECT token_secret, expires_at FROM tool_oauth_tokens WHERE tool_id = $1 AND user_id = $2`, toolID, userID).Scan(&encrypted, &expiresAt); err != nil {
		t.Fatalf("read encrypted token row: %v", err)
	}
	if strings.Contains(string(encrypted), "per-user-access") || time.Until(expiresAt) < 30*time.Minute {
		t.Fatalf("token was not safely persisted")
	}
	if _, err := repository.CompleteOAuthAuthorization(ctx, userID, state, "replay", "", "", callbackURL); err != ErrOAuthState {
		t.Fatalf("authorization state replay was not refused: %v", err)
	}
}
