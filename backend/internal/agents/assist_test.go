package agents

import (
	"strings"
	"testing"
)

// A model asked for a bare title reliably supplies a label, quotes, a full
// stop, or all three.
func TestCleanTitleStripsWhatModelsAddAnyway(t *testing.T) {
	cases := map[string]string{
		`Title: "HTTP/3 and QUIC"`:           "HTTP/3 and QUIC",
		"  Quan hệ giữa HTTP/3 và QUIC.\n\n": "Quan hệ giữa HTTP/3 và QUIC",
		"“Đọc tài liệu vận hành”":            "Đọc tài liệu vận hành",
		"A title\nand a second line":         "A title",
		"":                                   "",
	}
	for reply, want := range cases {
		if got := CleanTitle(reply); got != want {
			t.Errorf("CleanTitle(%q) = %q, want %q", reply, got, want)
		}
	}
}

// A model that ignores the word limit must not write a paragraph into the
// sidebar. Runes, not bytes: a Vietnamese title would be cut mid-character.
func TestCleanTitleCapsLength(t *testing.T) {
	long := strings.Repeat("á", 200)
	got := CleanTitle(long)
	if runes := []rune(got); len(runes) != MaxTitleRunes {
		t.Fatalf("title kept %d runes, want %d", len(runes), MaxTitleRunes)
	}
}
