package agents

import "testing"

// A question that opens with a number is not a numbered list. Trimming every
// leading digit turned "2+2 bằng mấy?" into "+2 bằng mấy?" - and did the same
// to "3+2", so two questions arrived as one broken one, twice.
func TestParseSuggestionsKeepsALeadingNumber(t *testing.T) {
	got := ParseSuggestions("2+2 bằng mấy?\n3+2 bằng mấy?\n2025 có gì mới?")
	want := []string{"2+2 bằng mấy?", "3+2 bằng mấy?", "2025 có gì mới?"}
	if len(got) != len(want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}
}

// The markers a model adds anyway still come off.
func TestParseSuggestionsStripsRealMarkers(t *testing.T) {
	got := ParseSuggestions("- Câu một\n2. Câu hai\n3) Câu ba")
	want := []string{"Câu một", "Câu hai", "Câu ba"}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}
}

func TestParseSuggestionsDropsDuplicates(t *testing.T) {
	got := ParseSuggestions("Câu một\nCâu một\nCâu hai")
	if len(got) != 2 {
		t.Fatalf("duplicates survived: %#v", got)
	}
}
