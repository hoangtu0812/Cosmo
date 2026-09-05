package tools

import (
	"context"
	"encoding/json"
	"errors"
	"golang.org/x/oauth2"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOAuthRefreshSerializesAcrossRepositories(t *testing.T) {
	registration := oauthUserRegistration{ClientID: "test-client"}
	raw, _ := json.Marshal(registration)
	repo, tool, user := mcpDatabaseFixture(t, AuthOAuthUser, string(raw))
	ctx := WithCaller(context.Background(), Caller{UserID: user})
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		time.Sleep(40 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		if n > 1 {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		w.Write([]byte(`{"access_token":"new","refresh_token":"rotated","token_type":"Bearer","expires_in":3600}`))
	}))
	defer provider.Close()
	details := oauthUserToken{TokenEndpoint: provider.URL, AuthStyle: oauth2.AuthStyleInParams, RegistrationFingerprint: registration.fingerprint()}
	if err := repo.storeOAuthUserToken(ctx, tool.ID, user, &oauth2.Token{AccessToken: "old", RefreshToken: "one-use", Expiry: time.Now().Add(-time.Minute)}, details); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			other := &Repository{db: repo.db, secrets: repo.secrets, egress: repo.egress}
			token, err := other.oauthUserAccessToken(ctx, tool.ID, string(raw))
			if err != nil || token != "new" {
				t.Errorf("refresh %v", err)
			}
		}()
	}
	wg.Wait()
	if requests.Load() != 1 {
		t.Fatalf("refresh count %d", requests.Load())
	}
	var count int
	repo.db.QueryRow(ctx, `SELECT count(*) FROM tool_oauth_tokens WHERE tool_id=$1`, tool.ID).Scan(&count)
	if count != 1 {
		t.Fatal("valid token deleted")
	}
}

func TestOAuthRefreshPreservesTransientFailures(t *testing.T) {
	for _, mode := range []string{"temporary", "invalid_grant", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			registration := oauthUserRegistration{ClientID: "test"}
			raw, _ := json.Marshal(registration)
			repo, tool, user := mcpDatabaseFixture(t, AuthOAuthUser, string(raw))
			ctx := WithCaller(context.Background(), Caller{UserID: user})
			callCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch mode {
				case "invalid_grant":
					w.WriteHeader(400)
					w.Write([]byte(`{"error":"invalid_grant"}`))
				case "cancel":
					cancel()
					time.Sleep(20 * time.Millisecond)
				default:
					w.WriteHeader(503)
					w.Write([]byte(`{"error":"temporarily_unavailable"}`))
				}
			}))
			defer provider.Close()
			details := oauthUserToken{TokenEndpoint: provider.URL, AuthStyle: oauth2.AuthStyleInParams, RegistrationFingerprint: registration.fingerprint()}
			repo.storeOAuthUserToken(ctx, tool.ID, user, &oauth2.Token{AccessToken: "old", RefreshToken: "refresh", Expiry: time.Now().Add(-time.Minute)}, details)
			_, err := repo.oauthUserAccessToken(callCtx, tool.ID, string(raw))
			if err == nil {
				t.Fatal("expected refresh error")
			}
			if mode == "invalid_grant" && !errors.Is(err, ErrOAuthConnection) {
				t.Fatal(err)
			}
			var count int
			repo.db.QueryRow(ctx, `SELECT count(*) FROM tool_oauth_tokens WHERE tool_id=$1`, tool.ID).Scan(&count)
			want := 1
			if mode == "invalid_grant" {
				want = 0
			}
			if count != want {
				t.Fatalf("token rows %d want %d", count, want)
			}
		})
	}
}
