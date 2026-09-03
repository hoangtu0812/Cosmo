package httpapi

import "testing"

func TestValidPassword(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"StrongPass1", true},
		{"mậtkhẩumạnh2", true},
		{"short1", false},
		{"onlyletters", false},
		{"12345678901", false},
	}
	for _, test := range tests {
		if got := validPassword(test.value); got != test.want {
			t.Fatalf("validPassword(%q) = %v, want %v", test.value, got, test.want)
		}
	}
}

func TestValidEmail(t *testing.T) {
	if !validEmail("admin@cosmo.local") {
		t.Fatal("expected valid email")
	}
	if validEmail("not-an-email") {
		t.Fatal("expected invalid email")
	}
}

func TestCitationsUsedByAnswerKeepsReferencedOrder(t *testing.T) {
	candidates := []Citation{{Index: 1, DocumentID: "a"}, {Index: 2, DocumentID: "b"}, {Index: 3, DocumentID: "c"}}
	got := citationsUsedByAnswer("Theo quy trình [3], sau đó kiểm tra [1]. Xem lại [3].", candidates)
	if len(got) != 2 || got[0].Index != 3 || got[1].Index != 1 {
		t.Fatalf("unexpected citations: %#v", got)
	}
}

// A model that answers in headings rather than markers still used what it was
// given, so the candidates stand in - capped, because the whole retrieval set
// is not evidence. What keeps this honest is upstream: nothing is retrieved
// unless the turn decided the question was about the documents.
func TestCitationsUsedByAnswerCapsUnreferencedFallback(t *testing.T) {
	candidates := []Citation{{Index: 1}, {Index: 2}, {Index: 3}, {Index: 4}}
	got := citationsUsedByAnswer("Câu trả lời không có citation inline.", candidates)
	if len(got) != 3 {
		t.Fatalf("fallback returned %d citations, want 3", len(got))
	}
}

func TestCitationsUsedByAnswerIgnoresUnknownIndexes(t *testing.T) {
	got := citationsUsedByAnswer("Không có nguồn [99].", []Citation{{Index: 1}, {Index: 2}})
	if len(got) != 2 {
		t.Fatalf("unknown inline index should use capped fallback: %#v", got)
	}
}
