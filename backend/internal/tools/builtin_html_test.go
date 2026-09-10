package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderHTML(t *testing.T) {
	source := `<h1>Doanh thu</h1><script>document.body.dataset.ready = "yes"</script>`
	raw, err := renderHTML(map[string]any{"title": "Báo cáo", "html": source})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		HTML struct {
			Title   string
			Content string
		}
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if result.HTML.Content != source || result.HTML.Title != "Báo cáo" {
		t.Fatalf("unexpected result: %s", raw)
	}
	for _, input := range []any{nil, 42, " ", strings.Repeat("x", MaxHTMLResultBytes+1), strings.Repeat("<", MaxHTMLResultBytes/2)} {
		if _, err := renderHTML(map[string]any{"html": input}); err == nil {
			t.Fatalf("accepted invalid or oversized input")
		}
	}
}
