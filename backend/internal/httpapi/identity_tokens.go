package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"cosmo/backend/internal/tools"
)

// The user's own Entra tokens, kept so a tool can act on behalf of the person
// who asked rather than as Cosmo itself.
//
// Client credentials makes every request look the same: one service principal,
// no groups, no user. Any resource server that scopes data per person cannot
// make that decision from an application token. On-behalf-of exchanges the
// user's own token for one addressed to that server, and this is where the
// token being exchanged comes from.
//
// Both values are sealed. A refresh token is a standing right to act as that
// person; it is the most dangerous thing in this database and it is never
// returned by any endpoint.

// storeIdentityToken keeps what sign-in received. A user signing in again
// replaces the row rather than adding one: a second standing grant is a second
// thing to revoke and nobody would know it existed.
func (s *Server) storeIdentityToken(ctx context.Context, userID string, token *oauth2.Token) error {
	if token == nil || strings.TrimSpace(token.AccessToken) == "" {
		return nil
	}
	if !s.secrets.Configured() {
		// Without a box there is nowhere safe to put it, and storing it in the
		// clear to make a feature work is not a trade this makes.
		return nil
	}
	access, err := s.secrets.Seal(token.AccessToken)
	if err != nil {
		return err
	}
	var refresh []byte
	if strings.TrimSpace(token.RefreshToken) != "" {
		if refresh, err = s.secrets.Seal(token.RefreshToken); err != nil {
			return err
		}
	}
	expiry := token.Expiry
	if expiry.IsZero() {
		// An issuer that does not say gets the shortest sensible life: being
		// wrong this way costs one refresh, the other way costs every call
		// until somebody investigates.
		expiry = time.Now().Add(5 * time.Minute)
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO user_identity_tokens (user_id, access_token, refresh_token, expires_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			access_token = EXCLUDED.access_token,
			-- A refresh happens without one, and overwriting a good refresh
			-- token with nothing would end the user's standing grant silently.
			refresh_token = COALESCE(EXCLUDED.refresh_token, user_identity_tokens.refresh_token),
			expires_at = EXCLUDED.expires_at,
			updated_at = NOW()`,
		userID, access, refresh, expiry)
	return err
}

// graphTokenFor mints a Microsoft Graph token from the refresh token.
//
// It exists because a token request carries one audience. Once sign-in asks
// for this application's own API - which on-behalf-of requires - the token it
// returns is addressed here, not to Graph, and the profile photo would quietly
// stop arriving. The refresh token is the one credential Entra will exchange
// for either.
func (s *Server) graphTokenFor(ctx context.Context, refreshToken string) (string, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return "", fmt.Errorf("no refresh token")
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", s.cfg.EntraClientID)
	form.Set("client_secret", s.cfg.EntraClientSecret)
	form.Set("scope", "https://graph.microsoft.com/User.Read")

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	endpoint := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", s.cfg.EntraTenantID)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if err != nil {
		return "", err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("graph token request returned %d", response.StatusCode)
	}
	var issued struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(raw, &issued); err != nil || issued.AccessToken == "" {
		return "", fmt.Errorf("graph token response had no access_token")
	}
	return issued.AccessToken, nil
}

// avatarToken picks the token that can actually read a profile photo. Without
// an API scope configured nothing has changed and sign-in's own token is a
// Graph token; with one, it is not, and the refresh token is the way back.
func (s *Server) avatarToken(ctx context.Context, token *oauth2.Token) string {
	if strings.TrimSpace(s.cfg.EntraAPIScope) == "" {
		return token.AccessToken
	}
	graph, err := s.graphTokenFor(ctx, token.RefreshToken)
	if err != nil {
		// The photo is a nicety and its absence is not worth failing a sign-in
		// over, but a silent absence is worth a line in the log.
		s.logger.Warn("mint Graph token for profile photo", "error", err)
		return ""
	}
	return graph
}

// assertionFor hands the tools package one user's own access token, refreshing
// it first when it has expired.
//
// It is given as a function so that package never learns what a session is or
// which identity provider is in play. It knows a user id, which the transport
// layer already vouched for; everything behind that stays here.
func (s *Server) assertionFor(ctx context.Context, userID string) (tools.Assertion, error) {
	if strings.TrimSpace(s.cfg.EntraAPIScope) == "" {
		// Sign-in never asked for a token addressed to this application, so
		// there is nothing an exchange would accept. Said plainly, because the
		// fix is a setting and not a retry.
		return tools.Assertion{}, tools.ErrOBOUnavailable
	}
	if !s.secrets.Configured() {
		return tools.Assertion{}, tools.ErrOBONoAssertion
	}

	var sealedAccess, sealedRefresh []byte
	var expiresAt time.Time
	err := s.db.QueryRow(ctx,
		`SELECT access_token, refresh_token, expires_at FROM user_identity_tokens WHERE user_id = $1`,
		userID).Scan(&sealedAccess, &sealedRefresh, &expiresAt)
	if err != nil {
		return tools.Assertion{}, tools.ErrOBONoAssertion
	}

	// Renewed a little early, for the same reason a tool token is: a call that
	// starts inside the window and arrives outside it fails invisibly.
	if time.Until(expiresAt) > time.Minute {
		access, err := s.secrets.Open(sealedAccess)
		if err != nil {
			return tools.Assertion{}, tools.ErrOBONoAssertion
		}
		return tools.Assertion{Token: access, ExpiresAt: expiresAt}, nil
	}

	if len(sealedRefresh) == 0 {
		// Nothing to renew with. The reader signs in again, which is the only
		// thing that can produce a new one.
		return tools.Assertion{}, tools.ErrOBONoAssertion
	}
	refresh, err := s.secrets.Open(sealedRefresh)
	if err != nil {
		return tools.Assertion{}, tools.ErrOBONoAssertion
	}
	renewed, err := s.refreshIdentityToken(ctx, refresh)
	if err != nil {
		s.logger.Warn("renew identity token", "user_id", userID, "error", err)
		return tools.Assertion{}, tools.ErrOBONoAssertion
	}
	if err := s.storeIdentityToken(ctx, userID, renewed); err != nil {
		s.logger.Warn("store renewed identity token", "user_id", userID, "error", err)
	}
	return tools.Assertion{Token: renewed.AccessToken, ExpiresAt: renewed.Expiry}, nil
}

// refreshIdentityToken asks Entra for a fresh token addressed to this
// application, using the standing grant from sign-in.
func (s *Server) refreshIdentityToken(ctx context.Context, refreshToken string) (*oauth2.Token, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", s.cfg.EntraClientID)
	form.Set("client_secret", s.cfg.EntraClientSecret)
	form.Set("scope", "offline_access "+strings.TrimSpace(s.cfg.EntraAPIScope))

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	endpoint := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", s.cfg.EntraTenantID)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("refresh returned %d", response.StatusCode)
	}
	var issued struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &issued); err != nil || issued.AccessToken == "" {
		return nil, fmt.Errorf("refresh response had no access_token")
	}
	return &oauth2.Token{
		AccessToken:  issued.AccessToken,
		RefreshToken: issued.RefreshToken,
		Expiry:       time.Now().Add(time.Duration(issued.ExpiresIn) * time.Second),
	}, nil
}
