package tools

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

// oauthUserRegistration is the deployment-owned OAuth client registration.
// Endpoints are intentionally absent: they come from RFC 9728 and RFC 8414
// discovery, so changing an issuer does not require changing Cosmo code.
type oauthUserRegistration struct {
	ClientID            string `json:"client_id"`
	ClientSecret        string `json:"client_secret,omitempty"`
	Scope               string `json:"scope,omitempty"`
	AuthorizationServer string `json:"authorization_server,omitempty"`
}

func (registration oauthUserRegistration) isComplete() bool {
	return strings.TrimSpace(registration.ClientID) != ""
}

func (registration oauthUserRegistration) fingerprint() string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		strings.TrimSpace(registration.ClientID), registration.ClientSecret,
		strings.TrimSpace(registration.Scope), strings.TrimRight(strings.TrimSpace(registration.AuthorizationServer), "/"),
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

// OAuthAuthorizationServer is the safe, public part of discovered RFC 8414
// metadata that the editor needs to show. Client secrets and tokens never
// leave the backend.
type OAuthAuthorizationServer struct {
	Issuer                        string   `json:"issuer"`
	AuthorizationEndpoint         string   `json:"authorization_endpoint"`
	TokenEndpoint                 string   `json:"token_endpoint"`
	RegistrationEndpoint          string   `json:"registration_endpoint,omitempty"`
	ScopesSupported               []string `json:"scopes_supported"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported"`
	tokenEndpointAuthMethods      []string
	requireIssuerResponse         bool
}

type OAuthConnectionInfo struct {
	Resource                    string                     `json:"resource"`
	ResourceName                string                     `json:"resource_name,omitempty"`
	ScopesSupported             []string                   `json:"scopes_supported"`
	AuthorizationServers        []OAuthAuthorizationServer `json:"authorization_servers"`
	SelectedAuthorizationServer string                     `json:"selected_authorization_server,omitempty"`
	CallbackURL                 string                     `json:"callback_url"`
	Configured                  bool                       `json:"configured"`
	Connected                   bool                       `json:"connected"`
	ExpiresAt                   *time.Time                 `json:"expires_at,omitempty"`
}

type protectedResource struct {
	Resource             string   `json:"resource"`
	ResourceName         string   `json:"resource_name,omitempty"`
	AuthorizationServers []string `json:"authorization_servers"`
	ScopesSupported      []string `json:"scopes_supported,omitempty"`
}

// OAuthConnection discovers the resource and authorization servers and adds
// only this user's connection state. It is safe to call before a client is
// configured, which lets the editor explain what registration is required.
func (repository *Repository) OAuthConnection(ctx context.Context, tool Tool, userID, callbackURL string) (OAuthConnectionInfo, error) {
	resource, servers, err := repository.discoverOAuth(ctx, tool.BaseURL)
	if err != nil {
		return OAuthConnectionInfo{}, err
	}
	info := OAuthConnectionInfo{
		Resource:             resource.Resource,
		ResourceName:         resource.ResourceName,
		ScopesSupported:      append([]string(nil), resource.ScopesSupported...),
		AuthorizationServers: servers,
		CallbackURL:          callbackURL,
	}

	registration, err := repository.oauthUserRegistration(ctx, tool.ID)
	if err != nil && !errors.Is(err, ErrOAuthRegistration) {
		return OAuthConnectionInfo{}, err
	}
	if err == nil {
		info.Configured = true
		info.SelectedAuthorizationServer = registration.AuthorizationServer
		if info.SelectedAuthorizationServer == "" && len(servers) == 1 {
			info.SelectedAuthorizationServer = servers[0].Issuer
		}
		var sealed []byte
		var expiresAt time.Time
		queryErr := repository.db.QueryRow(ctx,
			`SELECT token_secret, expires_at FROM tool_oauth_tokens WHERE tool_id = $1 AND user_id = $2`,
			tool.ID, userID).Scan(&sealed, &expiresAt)
		if queryErr == nil {
			var token oauthUserToken
			opened, openErr := repository.secrets.Open(sealed)
			if openErr == nil && json.Unmarshal([]byte(opened), &token) == nil && token.RegistrationFingerprint == registration.fingerprint() {
				info.Connected = time.Until(expiresAt) > 0 || strings.TrimSpace(token.RefreshToken) != ""
				info.ExpiresAt = &expiresAt
			}
		} else if !errors.Is(queryErr, pgx.ErrNoRows) {
			return OAuthConnectionInfo{}, queryErr
		}
	}
	return info, nil
}

func (repository *Repository) discoverOAuth(ctx context.Context, resourceURL string) (protectedResource, []OAuthAuthorizationServer, error) {
	if err := repository.egress.CheckEgress(resourceURL); err != nil {
		return protectedResource{}, nil, err
	}
	metadataURLs := repository.protectedResourceMetadataURLs(ctx, resourceURL)
	var resource protectedResource
	var found bool
	for _, metadataURL := range metadataURLs {
		candidate, ok := repository.fetchProtectedResource(ctx, metadataURL, resourceURL)
		if ok {
			resource, found = candidate, true
			break
		}
	}
	if !found || resource.Resource == "" || len(resource.AuthorizationServers) == 0 {
		return protectedResource{}, nil, ErrOAuthDiscovery
	}

	servers := make([]OAuthAuthorizationServer, 0, len(resource.AuthorizationServers))
	for _, issuer := range resource.AuthorizationServers {
		if !secureOAuthURL(issuer) || repository.egress.CheckEgress(issuer) != nil {
			continue
		}
		metadata, err := mcpauth.GetAuthServerMetadata(ctx, issuer, repository.client())
		if err != nil || metadata == nil {
			// Some established providers support PKCE but omit the optional
			// code_challenge_methods_supported metadata field. Keep discovery
			// provider-neutral and tolerate only that omission; Cosmo still
			// always sends S256 and an explicitly incompatible value is refused.
			metadata = repository.fetchAuthorizationServerMetadata(ctx, issuer)
		}
		if metadata == nil || (len(metadata.CodeChallengeMethodsSupported) > 0 && !slices.Contains(metadata.CodeChallengeMethodsSupported, "S256")) {
			continue
		}
		servers = append(servers, OAuthAuthorizationServer{
			Issuer: metadata.Issuer, AuthorizationEndpoint: metadata.AuthorizationEndpoint,
			TokenEndpoint: metadata.TokenEndpoint, RegistrationEndpoint: metadata.RegistrationEndpoint,
			ScopesSupported:               append([]string(nil), metadata.ScopesSupported...),
			CodeChallengeMethodsSupported: append([]string(nil), metadata.CodeChallengeMethodsSupported...),
			tokenEndpointAuthMethods:      append([]string(nil), metadata.TokenEndpointAuthMethodsSupported...),
			requireIssuerResponse:         metadata.AuthorizationResponseIssParameterSupported,
		})
	}
	if len(servers) == 0 {
		return protectedResource{}, nil, ErrOAuthDiscovery
	}
	return resource, servers, nil
}

func (repository *Repository) fetchAuthorizationServerMetadata(ctx context.Context, issuer string) *oauthex.AuthServerMeta {
	for _, metadataURL := range authorizationServerMetadataURLs(issuer) {
		if repository.egress.CheckEgress(metadataURL) != nil {
			continue
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
		if err != nil {
			continue
		}
		request.Header.Set("Accept", "application/json")
		response, err := repository.client().Do(request)
		if err != nil {
			continue
		}
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, 256*1024))
		response.Body.Close()
		if readErr != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
			continue
		}
		var metadata oauthex.AuthServerMeta
		if json.Unmarshal(raw, &metadata) != nil || !equalIssuer(metadata.Issuer, issuer) ||
			!secureOAuthURL(metadata.AuthorizationEndpoint) || !secureOAuthURL(metadata.TokenEndpoint) ||
			repository.egress.CheckEgress(metadata.AuthorizationEndpoint) != nil || repository.egress.CheckEgress(metadata.TokenEndpoint) != nil {
			continue
		}
		if !slices.Contains(metadata.ResponseTypesSupported, "code") ||
			(len(metadata.GrantTypesSupported) > 0 && !slices.Contains(metadata.GrantTypesSupported, "authorization_code")) {
			continue
		}
		if len(metadata.CodeChallengeMethodsSupported) > 0 && !slices.Contains(metadata.CodeChallengeMethodsSupported, "S256") {
			continue
		}
		return &metadata
	}
	return nil
}

func authorizationServerMetadataURLs(issuer string) []string {
	parsed, err := url.Parse(issuer)
	if err != nil {
		return nil
	}
	originalPath := strings.Trim(parsed.Path, "/")
	result := []string{}
	copyURL := *parsed
	copyURL.RawQuery, copyURL.Fragment, copyURL.RawPath = "", "", ""
	if originalPath == "" {
		copyURL.Path = "/.well-known/oauth-authorization-server"
		result = append(result, copyURL.String())
		copyURL.Path = "/.well-known/openid-configuration"
		return append(result, copyURL.String())
	}
	copyURL.Path = "/.well-known/oauth-authorization-server/" + originalPath
	result = append(result, copyURL.String())
	copyURL.Path = "/.well-known/openid-configuration/" + originalPath
	result = append(result, copyURL.String())
	copyURL.Path = "/" + originalPath + "/.well-known/openid-configuration"
	return append(result, copyURL.String())
}

// protectedResourceMetadataURLs prefers the resource_metadata challenge and
// then tries the two RFC 9728 well-known locations required by MCP.
func (repository *Repository) protectedResourceMetadataURLs(ctx context.Context, resourceURL string) []string {
	urls := []string{}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, resourceURL, nil)
	if err == nil {
		request.Header.Set("Accept", "application/json")
		if response, callErr := repository.client().Do(request); callErr == nil {
			challenges, _ := oauthex.ParseWWWAuthenticate(response.Header.Values("WWW-Authenticate"))
			response.Body.Close()
			for _, challenge := range challenges {
				if challenge.Scheme == "bearer" && challenge.Params["resource_metadata"] != "" {
					urls = append(urls, challenge.Params["resource_metadata"])
				}
			}
		}
	}
	parsed, err := url.Parse(resourceURL)
	if err != nil {
		return urls
	}
	path := strings.TrimLeft(parsed.Path, "/")
	withPath := *parsed
	withPath.RawQuery, withPath.Fragment = "", ""
	withPath.Path, withPath.RawPath = "/.well-known/oauth-protected-resource/"+path, ""
	urls = appendUnique(urls, withPath.String())
	root := *parsed
	root.RawQuery, root.Fragment, root.RawPath = "", "", ""
	root.Path = "/.well-known/oauth-protected-resource"
	urls = appendUnique(urls, root.String())
	return urls
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func (repository *Repository) fetchProtectedResource(ctx context.Context, metadataURL, requestedResource string) (protectedResource, bool) {
	if repository.egress.CheckEgress(metadataURL) != nil {
		return protectedResource{}, false
	}
	meta, metaErr := url.Parse(metadataURL)
	resource, resourceErr := url.Parse(requestedResource)
	if metaErr != nil || resourceErr != nil || (meta.Scheme != "https" && !sameOrigin(meta, resource)) {
		return protectedResource{}, false
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return protectedResource{}, false
	}
	request.Header.Set("Accept", "application/json")
	response, err := repository.client().Do(request)
	if err != nil {
		return protectedResource{}, false
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return protectedResource{}, false
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 256*1024))
	if err != nil {
		return protectedResource{}, false
	}
	var result protectedResource
	if json.Unmarshal(raw, &result) != nil {
		return protectedResource{}, false
	}
	requestedOrigin := *resource
	requestedOrigin.Path, requestedOrigin.RawPath, requestedOrigin.RawQuery, requestedOrigin.Fragment = "", "", "", ""
	if result.Resource != requestedResource && strings.TrimRight(result.Resource, "/") != strings.TrimRight(requestedOrigin.String(), "/") {
		return protectedResource{}, false
	}
	return result, true
}

func sameOrigin(first, second *url.URL) bool {
	return strings.EqualFold(first.Scheme, second.Scheme) && strings.EqualFold(first.Host, second.Host)
}

func secureOAuthURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	host := parsed.Hostname()
	return parsed.Scheme == "http" && (strings.EqualFold(host, "localhost") || net.ParseIP(host).IsLoopback())
}

func (repository *Repository) oauthUserRegistration(ctx context.Context, toolID string) (oauthUserRegistration, error) {
	stored, err := repository.secretFor(ctx, toolID)
	if err != nil {
		return oauthUserRegistration{}, err
	}
	var registration oauthUserRegistration
	if json.Unmarshal([]byte(stored), &registration) != nil || !registration.isComplete() {
		return oauthUserRegistration{}, ErrOAuthRegistration
	}
	registration.ClientID = strings.TrimSpace(registration.ClientID)
	registration.Scope = strings.TrimSpace(registration.Scope)
	registration.AuthorizationServer = strings.TrimRight(strings.TrimSpace(registration.AuthorizationServer), "/")
	return registration, nil
}

type oauthStateSecret struct {
	Verifier                string           `json:"verifier"`
	Resource                string           `json:"resource"`
	OmitResourceIndicator   bool             `json:"omit_resource_indicator,omitempty"`
	Issuer                  string           `json:"issuer"`
	AuthorizationEndpoint   string           `json:"authorization_endpoint"`
	TokenEndpoint           string           `json:"token_endpoint"`
	AuthStyle               oauth2.AuthStyle `json:"auth_style"`
	Scopes                  []string         `json:"scopes"`
	RegistrationFingerprint string           `json:"registration_fingerprint"`
	RequireIssuerResponse   bool             `json:"require_issuer_response"`
}

type OAuthStart struct {
	AuthorizationURL string `json:"authorization_url"`
}

// BeginOAuthAuthorization creates a ten-minute, single-use state and returns
// the provider URL. PKCE is always S256. RFC 8707 resource indicators are sent
// unless the discovered provider is a known compatibility profile that rejects
// the MCP resource URL in favor of its provider-qualified scope.
func (repository *Repository) BeginOAuthAuthorization(ctx context.Context, tool Tool, userID, workspaceID, callbackURL string) (OAuthStart, error) {
	registration, err := repository.oauthUserRegistration(ctx, tool.ID)
	if err != nil {
		return OAuthStart{}, err
	}
	resource, servers, err := repository.discoverOAuth(ctx, tool.BaseURL)
	if err != nil {
		return OAuthStart{}, err
	}
	issuer := registration.AuthorizationServer
	if issuer == "" {
		if len(servers) != 1 {
			return OAuthStart{}, ErrOAuthProvider
		}
		issuer = servers[0].Issuer
	}
	var selected *OAuthAuthorizationServer
	for index := range servers {
		if equalIssuer(servers[index].Issuer, issuer) {
			selected = &servers[index]
			break
		}
	}
	if selected == nil {
		return OAuthStart{}, ErrOAuthProvider
	}

	scopes := strings.Fields(registration.Scope)
	if len(scopes) == 0 {
		scopes = append([]string(nil), resource.ScopesSupported...)
	}
	verifier := oauth2.GenerateVerifier()
	state := rand.Text()
	style := tokenAuthStyle(selected, registration.ClientSecret)
	omitResourceIndicator := isMicrosoftEntraOAuthServer(selected)
	secret := oauthStateSecret{
		Verifier: verifier, Resource: resource.Resource, OmitResourceIndicator: omitResourceIndicator, Issuer: selected.Issuer,
		AuthorizationEndpoint: selected.AuthorizationEndpoint, TokenEndpoint: selected.TokenEndpoint,
		AuthStyle: style, Scopes: scopes, RegistrationFingerprint: registration.fingerprint(),
		RequireIssuerResponse: selected.requireIssuerResponse,
	}
	// RFC 9207 is optional. Metadata discovery validated the issuer already;
	// when an authorization response includes iss we still verify it below.
	sealedJSON, _ := json.Marshal(secret)
	sealed, err := repository.secrets.Seal(string(sealedJSON))
	if err != nil {
		return OAuthStart{}, ErrSecretsOff
	}
	_, _ = repository.db.Exec(ctx, `DELETE FROM tool_oauth_states WHERE expires_at <= NOW()`)
	_, err = repository.db.Exec(ctx, `
		INSERT INTO tool_oauth_states(state_hash, tool_id, user_id, workspace_id, state_secret, expires_at)
		VALUES($1, $2, $3, $4, $5, NOW() + INTERVAL '10 minutes')`,
		stateDigest(state), tool.ID, userID, workspaceID, sealed)
	if err != nil {
		return OAuthStart{}, err
	}
	configuration := oauth2.Config{
		ClientID: registration.ClientID, ClientSecret: registration.ClientSecret,
		RedirectURL: callbackURL, Scopes: scopes,
		Endpoint: oauth2.Endpoint{AuthURL: selected.AuthorizationEndpoint, TokenURL: selected.TokenEndpoint, AuthStyle: style},
	}
	authorizationOptions := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(verifier)}
	if !omitResourceIndicator {
		authorizationOptions = append(authorizationOptions, oauth2.SetAuthURLParam("resource", resource.Resource))
	}
	authorizationURL := configuration.AuthCodeURL(state, authorizationOptions...)
	return OAuthStart{AuthorizationURL: authorizationURL}, nil
}

// Microsoft Entra v2 chooses the access-token audience from the requested API
// scope. It interprets RFC 8707's resource parameter using legacy resource
// semantics and rejects an MCP URL that differs from the scope's App ID URI
// with AADSTS9010010. Keep this exception isolated to known Entra authorities;
// standards-compliant OAuth servers continue to receive the MCP resource URL.
func isMicrosoftEntraOAuthServer(server *OAuthAuthorizationServer) bool {
	for _, raw := range []string{server.Issuer, server.AuthorizationEndpoint, server.TokenEndpoint} {
		parsed, err := url.Parse(raw)
		if err != nil {
			continue
		}
		switch strings.ToLower(parsed.Hostname()) {
		case "login.microsoftonline.com", "login.microsoftonline.us", "login.microsoftonline.de",
			"login.partner.microsoftonline.cn", "login.chinacloudapi.cn":
			return true
		}
	}
	return false
}

func tokenAuthStyle(server *OAuthAuthorizationServer, clientSecret string) oauth2.AuthStyle {
	if clientSecret == "" {
		return oauth2.AuthStyleInParams
	}
	if slices.Contains(server.tokenEndpointAuthMethods, "client_secret_post") {
		return oauth2.AuthStyleInParams
	}
	if len(server.tokenEndpointAuthMethods) == 0 {
		return oauth2.AuthStyleAutoDetect
	}
	return oauth2.AuthStyleInHeader
}

func stateDigest(state string) string {
	sum := sha256.Sum256([]byte(state))
	return hex.EncodeToString(sum[:])
}

func equalIssuer(first, second string) bool {
	return strings.TrimRight(first, "/") == strings.TrimRight(second, "/")
}

type OAuthCallbackResult struct {
	ToolID      string
	WorkspaceID string
}

type oauthUserToken struct {
	AccessToken             string           `json:"access_token"`
	RefreshToken            string           `json:"refresh_token,omitempty"`
	TokenType               string           `json:"token_type,omitempty"`
	Issuer                  string           `json:"issuer"`
	TokenEndpoint           string           `json:"token_endpoint"`
	Resource                string           `json:"resource"`
	Scopes                  []string         `json:"scopes,omitempty"`
	AuthStyle               oauth2.AuthStyle `json:"auth_style"`
	RegistrationFingerprint string           `json:"registration_fingerprint"`
}

// CompleteOAuthAuthorization consumes state before exchanging the code. A
// replay therefore cannot obtain a second token even when the provider accepts
// the code twice. The callback is tied to the signed-in Cosmo user.
func (repository *Repository) CompleteOAuthAuthorization(ctx context.Context, userID, state, code, responseIssuer, providerError, callbackURL string) (OAuthCallbackResult, error) {
	var result OAuthCallbackResult
	var sealed []byte
	err := repository.db.QueryRow(ctx, `
		DELETE FROM tool_oauth_states
		WHERE state_hash = $1 AND user_id = $2 AND expires_at > NOW()
		RETURNING tool_id, workspace_id, state_secret`, stateDigest(state), userID).
		Scan(&result.ToolID, &result.WorkspaceID, &sealed)
	if err != nil {
		return result, ErrOAuthState
	}
	opened, err := repository.secrets.Open(sealed)
	if err != nil {
		return result, ErrOAuthState
	}
	var saved oauthStateSecret
	if json.Unmarshal([]byte(opened), &saved) != nil {
		return result, ErrOAuthState
	}
	if providerError != "" || strings.TrimSpace(code) == "" {
		return result, ErrOAuthConnection
	}
	if saved.RequireIssuerResponse && responseIssuer == "" {
		return result, ErrOAuthState
	}
	if responseIssuer != "" && !equalIssuer(responseIssuer, saved.Issuer) {
		return result, ErrOAuthState
	}
	registration, err := repository.oauthUserRegistration(ctx, result.ToolID)
	if err != nil || registration.fingerprint() != saved.RegistrationFingerprint {
		return result, ErrOAuthState
	}
	if repository.egress.CheckEgress(saved.TokenEndpoint) != nil {
		return result, ErrOAuthToken
	}
	configuration := oauth2.Config{
		ClientID: registration.ClientID, ClientSecret: registration.ClientSecret,
		RedirectURL: callbackURL, Scopes: saved.Scopes,
		Endpoint: oauth2.Endpoint{AuthURL: saved.AuthorizationEndpoint, TokenURL: saved.TokenEndpoint, AuthStyle: saved.AuthStyle},
	}
	oauthContext := context.WithValue(ctx, oauth2.HTTPClient, repository.client())
	exchangeOptions := []oauth2.AuthCodeOption{oauth2.VerifierOption(saved.Verifier)}
	if !saved.OmitResourceIndicator {
		exchangeOptions = append(exchangeOptions, oauth2.SetAuthURLParam("resource", saved.Resource))
	}
	token, err := configuration.Exchange(oauthContext, code, exchangeOptions...)
	if err != nil || strings.TrimSpace(token.AccessToken) == "" {
		return result, ErrOAuthToken
	}
	if err := repository.storeOAuthUserToken(ctx, result.ToolID, userID, token, oauthUserToken{
		Issuer: saved.Issuer, TokenEndpoint: saved.TokenEndpoint, Resource: saved.Resource,
		Scopes: saved.Scopes, AuthStyle: saved.AuthStyle, RegistrationFingerprint: saved.RegistrationFingerprint,
	}); err != nil {
		return result, err
	}
	return result, nil
}

func (repository *Repository) storeOAuthUserToken(ctx context.Context, toolID, userID string, token *oauth2.Token, details oauthUserToken) error {
	details.AccessToken, details.RefreshToken, details.TokenType = token.AccessToken, token.RefreshToken, token.TokenType
	expiresAt := token.Expiry
	if expiresAt.IsZero() {
		expiresAt = time.Now().Add(5 * time.Minute)
	}
	raw, _ := json.Marshal(details)
	sealed, err := repository.secrets.Seal(string(raw))
	if err != nil {
		return ErrSecretsOff
	}
	_, err = repository.db.Exec(ctx, `
		INSERT INTO tool_oauth_tokens(tool_id, user_id, token_secret, expires_at, updated_at)
		VALUES($1, $2, $3, $4, NOW())
		ON CONFLICT(tool_id, user_id) DO UPDATE SET
			token_secret = EXCLUDED.token_secret, expires_at = EXCLUDED.expires_at, updated_at = NOW()`,
		toolID, userID, sealed, expiresAt)
	return err
}

func (repository *Repository) oauthUserAccessToken(ctx context.Context, toolID, storedRegistration string) (string, error) {
	caller, ok := CallerFrom(ctx)
	if !ok {
		return "", ErrOAuthConnection
	}
	var registration oauthUserRegistration
	if json.Unmarshal([]byte(storedRegistration), &registration) != nil || !registration.isComplete() {
		return "", ErrOAuthConfig
	}
	var sealed []byte
	var expiresAt time.Time
	err := repository.db.QueryRow(ctx,
		`SELECT token_secret, expires_at FROM tool_oauth_tokens WHERE tool_id = $1 AND user_id = $2`,
		toolID, caller.UserID).Scan(&sealed, &expiresAt)
	if err != nil {
		return "", ErrOAuthConnection
	}
	opened, err := repository.secrets.Open(sealed)
	if err != nil {
		return "", ErrOAuthConnection
	}
	var saved oauthUserToken
	if json.Unmarshal([]byte(opened), &saved) != nil || saved.RegistrationFingerprint != registration.fingerprint() {
		return "", ErrOAuthConnection
	}
	if time.Until(expiresAt) > oauthRefreshMargin {
		return saved.AccessToken, nil
	}
	if saved.RefreshToken == "" {
		_, _ = repository.db.Exec(ctx, `DELETE FROM tool_oauth_tokens WHERE tool_id = $1 AND user_id = $2`, toolID, caller.UserID)
		return "", ErrOAuthConnection
	}
	if repository.egress.CheckEgress(saved.TokenEndpoint) != nil {
		return "", ErrOAuthToken
	}
	configuration := oauth2.Config{
		ClientID: registration.ClientID, ClientSecret: registration.ClientSecret, Scopes: saved.Scopes,
		Endpoint: oauth2.Endpoint{TokenURL: saved.TokenEndpoint, AuthStyle: saved.AuthStyle},
	}
	// Force refresh inside our one-minute safety window; oauth2's own window
	// is shorter and would otherwise hand the nearly expired token back.
	old := &oauth2.Token{AccessToken: saved.AccessToken, RefreshToken: saved.RefreshToken, TokenType: saved.TokenType, Expiry: time.Now().Add(-time.Second)}
	oauthContext := context.WithValue(ctx, oauth2.HTTPClient, repository.client())
	refreshed, err := configuration.TokenSource(oauthContext, old).Token()
	if err != nil || refreshed.AccessToken == "" {
		_, _ = repository.db.Exec(ctx, `DELETE FROM tool_oauth_tokens WHERE tool_id = $1 AND user_id = $2`, toolID, caller.UserID)
		return "", ErrOAuthConnection
	}
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = saved.RefreshToken
	}
	if err := repository.storeOAuthUserToken(ctx, toolID, caller.UserID, refreshed, saved); err != nil {
		return "", err
	}
	return refreshed.AccessToken, nil
}

func (repository *Repository) DisconnectOAuthUser(ctx context.Context, toolID, userID string) error {
	_, err := repository.db.Exec(ctx, `DELETE FROM tool_oauth_tokens WHERE tool_id = $1 AND user_id = $2`, toolID, userID)
	return err
}
