package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// SearchBackend is the one address web_search may reach.
//
// The other built-ins reach nothing, which is what made them safe to hand a
// model without a credential or an egress check. This one leaves the machine,
// so the property that replaces "reaches nothing" has to be stated: the model
// supplies a query, never a URL. The destination is fixed by whoever runs the
// deployment, so there is no address a question can steer this towards and
// nothing for the egress guard to catch that config has not already decided.
//
// Empty means no backend was configured, and the tool says so rather than
// guessing at one.
type SearchBackend struct {
	// BaseURL of a SearXNG instance - a metasearch front end that asks the
	// public engines and holds no key of its own. That is why it is the
	// default: a keyed search API could not be called automatically under the
	// rule that only keyless tools may be.
	BaseURL string
}

// networkBuiltinFunc is a built-in that leaves the process. It is a separate
// registry from builtins precisely so the ones that reach nothing stay
// obviously that, and are not quietly joined by one that does.
type networkBuiltinFunc func(ctx context.Context, backend SearchBackend, arguments map[string]any) (string, error)

var networkBuiltins = map[string]networkBuiltinFunc{
	"web_search": webSearch,
}

// How many results are worth carrying back. A search page shows ten because
// scrolling is free; a tool result is prompt, and the tail of a result list is
// mostly restatement of the head.
const (
	defaultSearchResults = 5
	maxSearchResults     = 10
	maxSnippetRunes      = 400
	searchTimeout        = 12 * time.Second
)

type searchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
	Source  string `json:"source,omitempty"`
}

// webSearch asks the configured instance and returns a short, flat list.
//
// SearXNG answers with every field its engines produced - scores, thumbnails,
// parsed dates, the engine list per result - which would spend a large part of
// a context window on things no answer needs. Four fields survive.
func webSearch(ctx context.Context, backend SearchBackend, arguments map[string]any) (string, error) {
	query := strings.TrimSpace(argumentText(arguments["query"]))
	if query == "" {
		return "", fmt.Errorf("cần một câu truy vấn để tìm")
	}
	base := strings.TrimSpace(backend.BaseURL)
	if base == "" {
		return "", fmt.Errorf("chưa cấu hình dịch vụ tìm kiếm cho hệ thống này")
	}

	wanted := defaultSearchResults
	if raw := strings.TrimSpace(argumentText(arguments["count"])); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			wanted = parsed
		}
	}
	if wanted > maxSearchResults {
		wanted = maxSearchResults
	}

	endpoint, err := url.Parse(strings.TrimRight(base, "/") + "/search")
	if err != nil {
		return "", fmt.Errorf("địa chỉ dịch vụ tìm kiếm không hợp lệ")
	}
	parameters := url.Values{}
	parameters.Set("q", query)
	parameters.Set("format", "json")
	// Filtering adult results is the operator's decision to make once, not the
	// model's to make per question.
	parameters.Set("safesearch", "1")
	if language := strings.TrimSpace(argumentText(arguments["language"])); language != "" {
		parameters.Set("language", language)
	}
	endpoint.RawQuery = parameters.Encode()

	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/json")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("không gọi được dịch vụ tìm kiếm: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("dịch vụ tìm kiếm trả về mã %d", response.StatusCode)
	}

	// A hostile or misconfigured instance should not be able to spend the whole
	// context window before the trimming below ever runs.
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return "", fmt.Errorf("không đọc được kết quả tìm kiếm: %w", err)
	}

	var payload struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
			Engine  string `json:"engine"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("kết quả tìm kiếm không đọc được: %w", err)
	}

	results := make([]searchResult, 0, wanted)
	seen := map[string]bool{}
	for _, item := range payload.Results {
		address := strings.TrimSpace(item.URL)
		// Metasearch means several engines answer the same question, so the
		// same page arrives more than once. It is worth one slot.
		if address == "" || seen[address] {
			continue
		}
		seen[address] = true
		results = append(results, searchResult{
			Title:   strings.TrimSpace(item.Title),
			URL:     address,
			Snippet: clipRunes(strings.TrimSpace(item.Content), maxSnippetRunes),
			Source:  strings.TrimSpace(item.Engine),
		})
		if len(results) == wanted {
			break
		}
	}

	encoded, err := json.Marshal(map[string]any{
		"query":   query,
		"results": results,
	})
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// argumentText reads a value the model may have sent as a string or a number.
func argumentText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case nil:
		return ""
	default:
		return fmt.Sprint(typed)
	}
}

// clipRunes shortens on runes, not bytes: a snippet cut mid-character is worse
// than one cut a word early.
func clipRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}
