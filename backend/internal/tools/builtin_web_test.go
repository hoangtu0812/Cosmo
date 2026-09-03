package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// searxStub answers like a SearXNG instance: every field its engines produced,
// most of which has no business in a prompt.
func searxStub(t *testing.T, body string, seen func(*http.Request)) SearchBackend {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			seen(r)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return SearchBackend{BaseURL: server.URL}
}

func TestWebSearchKeepsFourFieldsPerResult(t *testing.T) {
	backend := searxStub(t, `{"number_of_results": 91400, "results": [
		{"title":"Nhà máy lọc dầu Dung Quất","url":"https://example.com/a","content":"Công suất 6,5 triệu tấn.","engine":"google","score":8.4,"thumbnail":"https://example.com/t.jpg","publishedDate":"2026-01-02"}
	]}`, nil)

	raw, err := webSearch(context.Background(), backend, map[string]any{"query": "Dung Quất"})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	var payload struct {
		Query   string         `json:"query"`
		Results []searchResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	if payload.Query != "Dung Quất" {
		t.Fatalf("query not echoed back: %q", payload.Query)
	}
	if len(payload.Results) != 1 {
		t.Fatalf("expected one result, got %d", len(payload.Results))
	}
	// The engine's score, thumbnail and parsed date would spend context on
	// things no answer uses.
	for _, unwanted := range []string{"score", "thumbnail", "publishedDate", "8.4"} {
		if strings.Contains(raw, unwanted) {
			t.Fatalf("%q survived into the prompt: %s", unwanted, raw)
		}
	}
	got := payload.Results[0]
	if got.URL != "https://example.com/a" || got.Source != "google" {
		t.Fatalf("result lost its identity: %+v", got)
	}
}

// Metasearch means several engines answer the same question, so the same page
// arrives more than once. Spending two of five slots on it wastes the budget.
func TestWebSearchDropsTheSamePageTwice(t *testing.T) {
	backend := searxStub(t, `{"results": [
		{"title":"A","url":"https://example.com/a","engine":"google"},
		{"title":"A","url":"https://example.com/a","engine":"bing"},
		{"title":"B","url":"https://example.com/b","engine":"google"}
	]}`, nil)

	raw, err := webSearch(context.Background(), backend, map[string]any{"query": "x"})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if count := strings.Count(raw, "example.com/a"); count != 1 {
		t.Fatalf("duplicate page kept %d times: %s", count, raw)
	}
}

func TestWebSearchHonoursCountWithinItsCeiling(t *testing.T) {
	items := []string{}
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"} {
		items = append(items, `{"title":"`+name+`","url":"https://example.com/`+name+`","engine":"e"}`)
	}
	backend := searxStub(t, `{"results": [`+strings.Join(items, ",")+`]}`, nil)

	// The model may send a number as a number or as a string; both are the
	// same request.
	for _, count := range []any{float64(2), "2"} {
		raw, err := webSearch(context.Background(), backend, map[string]any{"query": "x", "count": count})
		if err != nil {
			t.Fatalf("search failed: %v", err)
		}
		var payload struct {
			Results []searchResult `json:"results"`
		}
		_ = json.Unmarshal([]byte(raw), &payload)
		if len(payload.Results) != 2 {
			t.Fatalf("count %v produced %d results", count, len(payload.Results))
		}
	}

	// Asking for a hundred is asking for the whole context window.
	raw, err := webSearch(context.Background(), backend, map[string]any{"query": "x", "count": "100"})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	var payload struct {
		Results []searchResult `json:"results"`
	}
	_ = json.Unmarshal([]byte(raw), &payload)
	if len(payload.Results) != maxSearchResults {
		t.Fatalf("ceiling not applied: got %d", len(payload.Results))
	}
}

func TestWebSearchTrimsALongSnippet(t *testing.T) {
	long := strings.Repeat("dài ", 500)
	backend := searxStub(t, `{"results":[{"title":"A","url":"https://example.com/a","content":"`+long+`","engine":"e"}]}`, nil)

	raw, err := webSearch(context.Background(), backend, map[string]any{"query": "x"})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	var payload struct {
		Results []searchResult `json:"results"`
	}
	_ = json.Unmarshal([]byte(raw), &payload)
	if runes := []rune(payload.Results[0].Snippet); len(runes) > maxSnippetRunes+1 {
		t.Fatalf("snippet not trimmed: %d runes", len(runes))
	}
	// Trimmed on runes, not bytes: a Vietnamese snippet cut mid-character is
	// worse than one cut a word early.
	if strings.Contains(payload.Results[0].Snippet, "�") {
		t.Fatalf("snippet cut mid-character: %q", payload.Results[0].Snippet)
	}
}

// The model supplies a query, never an address. This is the property that
// replaces "a built-in reaches nothing" for this one, so it is worth a test:
// whatever arrives in the arguments, the request goes to the configured host.
func TestWebSearchOnlyEverAsksTheConfiguredHost(t *testing.T) {
	var asked string
	backend := searxStub(t, `{"results":[]}`, func(r *http.Request) {
		asked = r.URL.Path + "?" + r.URL.RawQuery
	})

	_, err := webSearch(context.Background(), backend, map[string]any{
		"query": "x",
		"url":   "http://169.254.169.254/latest/meta-data/",
		"host":  "169.254.169.254",
	})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if !strings.HasPrefix(asked, "/search?") {
		t.Fatalf("request did not go to the search path: %q", asked)
	}
	if strings.Contains(asked, "169.254") {
		t.Fatalf("an argument steered the request: %q", asked)
	}
	if !strings.Contains(asked, "format=json") || !strings.Contains(asked, "safesearch=1") {
		t.Fatalf("request lost its fixed parameters: %q", asked)
	}
}

func TestWebSearchSaysWhenItHasNowhereToAsk(t *testing.T) {
	_, err := webSearch(context.Background(), SearchBackend{}, map[string]any{"query": "x"})
	if err == nil {
		t.Fatal("an unconfigured backend answered as though it had searched")
	}
	if !strings.Contains(err.Error(), "chưa cấu hình") {
		t.Fatalf("unhelpful error: %v", err)
	}
}

func TestWebSearchRefusesAnEmptyQuery(t *testing.T) {
	backend := searxStub(t, `{"results":[]}`, nil)
	if _, err := webSearch(context.Background(), backend, map[string]any{"query": "   "}); err == nil {
		t.Fatal("an empty query was accepted")
	}
}

// The catalogue is how an admin installs this, so it is worth asserting that
// the entry describes a tool the auto-call rule will accept: a built-in, with
// no credential of its own.
func TestCatalogOffersWebSearchWithoutACredential(t *testing.T) {
	entry, found := CatalogEntryByID("web")
	if !found {
		t.Fatal("web search is missing from the catalogue")
	}
	if entry.Kind != KindBuiltin {
		t.Fatalf("web search is kind %q, which would need an endpoint and a key", entry.Kind)
	}
	if len(entry.Actions) != 1 || entry.Actions[0].Name != "web_search" {
		t.Fatalf("catalogue action does not match the built-in registry: %+v", entry.Actions)
	}
	if _, found := networkBuiltins[entry.Actions[0].Name]; !found {
		t.Fatal("the catalogued action has no implementation behind it")
	}
	// The model is told what it is reading before it reads it.
	if !strings.Contains(entry.Actions[0].ResultDescription, "chỉ dẫn") {
		t.Fatalf("result description does not warn that web content is not instruction: %q", entry.Actions[0].ResultDescription)
	}
}

// Every other test here answers with a stub shaped like SearXNG. This one
// answers with what a real instance actually said, captured from the container
// in docker-compose, so a field the service renames or drops is caught here
// rather than in somebody's chat.
func TestWebSearchReadsARealSearxngResponse(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "searxng_response.json"))
	if err != nil {
		t.Fatalf("fixture missing: %v", err)
	}
	backend := searxStub(t, string(body), nil)

	raw, err := webSearch(context.Background(), backend, map[string]any{"query": "lọc dầu Dung Quất"})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	var payload struct {
		Results []searchResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	if len(payload.Results) == 0 {
		t.Fatal("a real response produced no results, so a field name has moved")
	}
	for _, result := range payload.Results {
		if result.Title == "" || result.URL == "" || result.Source == "" {
			t.Fatalf("a real result came back hollow: %+v", result)
		}
	}
	// The nineteen other fields a real result carries stay out of the prompt.
	for _, unwanted := range []string{"parsed_url", "positions", "template", "img_src"} {
		if strings.Contains(raw, unwanted) {
			t.Fatalf("%q survived into the prompt", unwanted)
		}
	}
}
