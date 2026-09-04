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
)

func TestDiscoverOAuthUsesMCPChallengeAndStandardMetadata(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mcp":
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+server.URL+`/oauth-resource"`)
			w.WriteHeader(http.StatusUnauthorized)
		case "/oauth-resource":
			writeTestJSON(t, w, map[string]any{
				"resource": server.URL + "/mcp", "resource_name": "Neutral MCP",
				"authorization_servers": []string{server.URL + "/issuer"},
				"scopes_supported":      []string{"catalog.read"},
			})
		case "/.well-known/oauth-authorization-server/issuer":
			writeTestJSON(t, w, map[string]any{
				"issuer": server.URL + "/issuer", "authorization_endpoint": server.URL + "/authorize",
				"token_endpoint": server.URL + "/token", "jwks_uri": server.URL + "/jwks",
				"response_types_supported":              []string{"code"},
				"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
				"token_endpoint_auth_methods_supported": []string{"none", "client_secret_post"},
				"code_challenge_methods_supported":      []string{"S256"},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	host := mustURL(t, server.URL).Hostname()
	repository := &Repository{egress: EgressPolicy{AllowedHosts: []string{host}}}
	resource, providers, err := repository.discoverOAuth(context.Background(), server.URL+"/mcp")
	if err != nil {
		t.Fatalf("discoverOAuth: %v", err)
	}
	if resource.ResourceName != "Neutral MCP" || len(resource.ScopesSupported) != 1 || resource.ScopesSupported[0] != "catalog.read" {
		t.Fatalf("protected resource metadata changed: %#v", resource)
	}
	if len(providers) != 1 || providers[0].Issuer != server.URL+"/issuer" || providers[0].TokenEndpoint != server.URL+"/token" {
		t.Fatalf("authorization server metadata changed: %#v", providers)
	}
	if !strings.Contains(strings.Join(providers[0].CodeChallengeMethodsSupported, " "), "S256") {
		t.Fatalf("PKCE S256 was not retained: %#v", providers[0])
	}
}

func TestDiscoverOAuthRejectsAuthorizationServerWithIncompatiblePKCE(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			writeTestJSON(t, w, map[string]any{
				"resource": server.URL + "/mcp", "authorization_servers": []string{server.URL + "/issuer"},
			})
		case "/.well-known/oauth-authorization-server/issuer":
			writeTestJSON(t, w, map[string]any{
				"issuer": server.URL + "/issuer", "authorization_endpoint": server.URL + "/authorize",
				"token_endpoint": server.URL + "/token", "jwks_uri": server.URL + "/jwks",
				"response_types_supported":         []string{"code"},
				"code_challenge_methods_supported": []string{"plain"},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	repository := &Repository{egress: EgressPolicy{AllowedHosts: []string{mustURL(t, server.URL).Hostname()}}}
	if _, _, err := repository.discoverOAuth(context.Background(), server.URL+"/mcp"); err != ErrOAuthDiscovery {
		t.Fatalf("server that explicitly rejects S256 must be refused, got %v", err)
	}
}

func TestOAuthUserRegistrationAllowsPublicClients(t *testing.T) {
	registration := oauthUserRegistration{ClientID: "public-client", Scope: "catalog.read"}
	if !registration.isComplete() {
		t.Fatal("a PKCE public client must not require a client secret")
	}
	kind, _, err := ValidateAuth(AuthOAuthUser, "")
	if err != nil || kind != AuthOAuthUser {
		t.Fatalf("provider-neutral user OAuth auth type was refused: %q %v", kind, err)
	}
}

// Opt-in because SAP-MCP and its authorization server are deployment
// dependencies. It exercises the same RFC 9728/RFC 8414 discovery used by the
// editor without needing a token or changing application data.
func TestLiveMCPAuthorizationDiscovery(t *testing.T) {
	rawURL := strings.TrimSpace(os.Getenv("COSMO_MCP_OAUTH_LIVE_URL"))
	if rawURL == "" {
		t.Skip("set COSMO_MCP_OAUTH_LIVE_URL to run against a deployed MCP server")
	}
	host := mustURL(t, rawURL).Hostname()
	repository := &Repository{egress: EgressPolicy{AllowedHosts: []string{host}}}
	resource, providers, err := repository.discoverOAuth(context.Background(), rawURL)
	if err != nil {
		t.Fatalf("live OAuth discovery: %v", err)
	}
	if resource.Resource == "" || len(providers) == 0 {
		t.Fatalf("incomplete live metadata: resource=%#v providers=%#v", resource, providers)
	}
	t.Logf("resource=%s issuer=%s scopes=%v", resource.Resource, providers[0].Issuer, resource.ScopesSupported)
}

func writeTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	return parsed
}
