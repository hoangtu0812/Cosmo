package tools

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRedirectNeverSendsCredentialOrBodyToAnotherOrigin(t *testing.T) {
	for _, code := range []int{302, 307, 308} {
		var received atomic.Int32
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1) }))
		source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, code) }))
		repo := repositoryFor(t, source)
		request, _ := http.NewRequest("POST", source.URL, strings.NewReader("client_secret=dummy"))
		request.Header.Set("X-API-Key", "dummy")
		response, err := repo.client().Do(request)
		if response != nil {
			response.Body.Close()
		}
		if !errors.Is(err, ErrRedirectOrigin) || received.Load() != 0 {
			t.Fatalf("redirect %d reached another origin: %v count=%d", code, err, received.Load())
		}
		source.Close()
		target.Close()
	}
}

func TestRedirectPolicyOriginAndDowngrade(t *testing.T) {
	client := EgressPolicy{}.guardedClient(nil)
	origin, _ := http.NewRequest("GET", "https://example.com/mcp", nil)
	for _, raw := range []string{"https://other.example/mcp", "https://example.com:444/mcp", "http://example.com/mcp"} {
		request, _ := http.NewRequest("GET", raw, nil)
		if !errors.Is(client.CheckRedirect(request, []*http.Request{origin}), ErrRedirectOrigin) {
			t.Fatalf("allowed %s", raw)
		}
	}
	request, _ := http.NewRequest("GET", "https://example.com/canonical", nil)
	if err := client.CheckRedirect(request, []*http.Request{origin}); err != nil {
		t.Fatal(err)
	}
}

func TestMCPTransportChecksOriginBeforeLoadingSecrets(t *testing.T) {
	transport := mcpAuthorisingTransport{repository: &Repository{}, tool: Tool{BaseURL: "https://example.com/mcp", AuthType: AuthBearer}}
	request := &http.Request{URL: &url.URL{Scheme: "https", Host: "elsewhere.example"}}
	if _, err := transport.RoundTrip(request); !errors.Is(err, ErrRedirectOrigin) {
		t.Fatal(err)
	}
}
