package httpapi

import (
	"strings"
	"testing"
)

func TestHTMLResultPreserved(t *testing.T) {
	raw := `{"html":{"title":"Dashboard","content":"` + strings.Repeat("a", 25000) + `"}}`
	if summariseResult(raw) != raw {
		t.Fatal("HTML result truncated")
	}
	if len(summariseResult(strings.Repeat("a", 25000))) > 310 {
		t.Fatal("ordinary results should still be summarized")
	}
}
