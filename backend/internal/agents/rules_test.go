package agents

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	if _, err := ValidateName("   "); !errors.Is(err, ErrNameLength) {
		t.Fatalf("blank name should be rejected, got %v", err)
	}
	if _, err := ValidateName(strings.Repeat("a", MaxNameRunes+1)); !errors.Is(err, ErrNameLength) {
		t.Fatalf("overlong name should be rejected, got %v", err)
	}
	// The limit counts runes, not bytes: a Vietnamese name at the limit is
	// well over 120 bytes and must still be accepted.
	name := strings.Repeat("ế", MaxNameRunes)
	if got, err := ValidateName(name); err != nil || got != name {
		t.Fatalf("name at the rune limit should pass, got %q %v", got, err)
	}
	if got, _ := ValidateName("  Trợ lý  "); got != "Trợ lý" {
		t.Fatalf("name should be trimmed, got %q", got)
	}
}

func TestValidateIntroduction(t *testing.T) {
	if _, err := ValidateIntroduction(strings.Repeat("x", MaxIntroRunes+1)); !errors.Is(err, ErrIntroLength) {
		t.Fatal("overlong introduction should be rejected")
	}
	if got, err := ValidateIntroduction(""); err != nil || got != "" {
		t.Fatalf("an empty introduction is allowed, got %q %v", got, err)
	}
}

func TestNormalizeVisibilityFallsBackToPrivate(t *testing.T) {
	// Anything unrecognised must narrow, never widen who can see an agent.
	for _, input := range []string{"", "public", "everyone", "WORKSPACE"} {
		if got := NormalizeVisibility(input); got != Private {
			t.Fatalf("NormalizeVisibility(%q) = %q, want %q", input, got, Private)
		}
	}
	if got := NormalizeVisibility(Shared); got != Shared {
		t.Fatalf("NormalizeVisibility(%q) = %q", Shared, got)
	}
}

func TestCleanStringList(t *testing.T) {
	got := CleanStringList([]string{" a ", "", "   ", "b"}, 10, 40)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("blanks should be dropped and entries trimmed, got %#v", got)
	}
	if got := CleanStringList([]string{"a", "b", "c"}, 2, 40); len(got) != 2 {
		t.Fatalf("the limit should truncate the list, got %#v", got)
	}
	if got := CleanStringList([]string{strings.Repeat("ế", 10)}, 10, 4); len([]rune(got[0])) != 4 {
		t.Fatalf("entries should be truncated by rune, got %q", got[0])
	}
	if got := CleanStringList(nil, 10, 40); got == nil {
		t.Fatal("the result must never be nil, so JSON carries [] rather than null")
	}
}

func TestParseSuggestions(t *testing.T) {
	// The model is asked for bare lines but is not trusted to comply.
	got := ParseSuggestions("1. Câu một\n- Câu hai\n\n• Câu ba\nCâu bốn")
	if len(got) != MaxSuggestions {
		t.Fatalf("should stop at %d suggestions, got %#v", MaxSuggestions, got)
	}
	if got[0] != "Câu một" || got[1] != "Câu hai" || got[2] != "Câu ba" {
		t.Fatalf("numbering and bullets should be stripped, got %#v", got)
	}
	if got := ParseSuggestions("\n\n   \n"); len(got) != 0 {
		t.Fatalf("blank output should yield nothing, got %#v", got)
	}
	if got := ParseSuggestions(strings.Repeat("x", 201)); len(got) != 0 {
		t.Fatal("an overlong line is prose, not a question, and should be dropped")
	}
}

func TestDecodeStringListNeverNil(t *testing.T) {
	for _, raw := range [][]byte{nil, []byte(""), []byte("null"), []byte("not json")} {
		if got := decodeStringList(raw); got == nil {
			t.Fatalf("decodeStringList(%q) returned nil", raw)
		}
	}
	if got := decodeStringList([]byte(`["a","b"]`)); len(got) != 2 {
		t.Fatalf("valid json should decode, got %#v", got)
	}
}

func TestCapChangelog(t *testing.T) {
	if got := CapChangelog("  ghi chu  "); got != "ghi chu" {
		t.Fatalf("a note should be trimmed, got %q", got)
	}
	// Counted by rune, so a Vietnamese note at the limit survives whole
	// rather than being cut short by its byte length.
	full := strings.Repeat("ế", MaxChangelogRunes)
	if got := CapChangelog(full); len([]rune(got)) != MaxChangelogRunes {
		t.Fatalf("a note at the limit should pass, got %d runes", len([]rune(got)))
	}
	if got := CapChangelog(strings.Repeat("ế", MaxChangelogRunes+50)); len([]rune(got)) != MaxChangelogRunes {
		t.Fatalf("an overlong note should be cut to the limit, got %d runes", len([]rune(got)))
	}
	if got := CapChangelog("   "); got != "" {
		t.Fatalf("a blank note should become empty, got %q", got)
	}
}
