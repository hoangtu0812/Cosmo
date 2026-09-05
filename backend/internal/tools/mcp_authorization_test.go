package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestMCPChallengePreservesScopesWithoutUpstreamErrorText(t *testing.T) {
	response := &http.Response{StatusCode: 403, Header: http.Header{}}
	response.Header.Set("WWW-Authenticate", `Bearer error="insufficient_scope", scope="orders.read orders.write", error_description="sensitive-upstream-value"`)
	challenge := mcpAuthorizationChallenge(response)
	if !errors.Is(challenge, ErrToolUnauthorized) || challenge.Code != "insufficient_scope" || len(challenge.RequiredScopes) != 2 || strings.Contains(challenge.Error(), "sensitive-upstream-value") {
		t.Fatalf("bad challenge: %#v", challenge)
	}
}

func TestOAuthIssuerCallbackRequiresExactString(t *testing.T) {
	repo, tool, user := mcpDatabaseFixture(t, AuthOAuthUser, `{"client_id":"test"}`)
	ctx := context.Background()
	for _, mode := range []string{"slash", "case", "missing", "error"} {
		state := newID("state_")
		saved := oauthStateSecret{Issuer: "https://issuer.example/tenant", RequireIssuerResponse: true}
		raw, _ := json.Marshal(saved)
		sealed, _ := repo.secrets.Seal(string(raw))
		var workspace string
		repo.db.QueryRow(ctx, `SELECT owner_workspace_id FROM tools WHERE id=$1`, tool.ID).Scan(&workspace)
		if _, err := repo.db.Exec(ctx, `INSERT INTO tool_oauth_states(state_hash,user_id,tool_id,workspace_id,state_secret,expires_at) VALUES($1,$2,$3,$4,$5,NOW()+INTERVAL '1 minute')`, stateDigest(state), user, tool.ID, workspace, sealed); err != nil {
			t.Fatal(err)
		}
		issuer := saved.Issuer + "/"
		providerError := ""
		switch mode {
		case "case":
			issuer = "https://ISSUER.example/tenant"
		case "missing":
			issuer = ""
		case "error":
			providerError = "access_denied"
		}
		if _, err := repo.CompleteOAuthAuthorization(ctx, user, state, "code", issuer, providerError, "http://localhost/callback"); !errors.Is(err, ErrOAuthState) {
			t.Fatalf("%s callback accepted wrong issuer: %v", mode, err)
		}
	}
}
