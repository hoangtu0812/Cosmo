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

// The reader's complaint, as a test: a question the knowledge base has nothing
// to say about still came back listing documents. Retrieval had run, so
// candidates existed, and the answer that ignored them was given three of them
// anyway.
func TestCitationsUsedByAnswerListsNothingWhenTheAnswerCitedNothing(t *testing.T) {
	candidates := []Citation{{Index: 1}, {Index: 2}, {Index: 3}, {Index: 4}}
	got := citationsUsedByAnswer("Câu trả lời không có citation inline.", candidates)
	if len(got) != 0 {
		t.Fatalf("an answer citing nothing was given %d sources", len(got))
	}
}

func TestCitationsUsedByAnswerIgnoresUnknownIndexes(t *testing.T) {
	got := citationsUsedByAnswer("Không có nguồn [99].", []Citation{{Index: 1}, {Index: 2}})
	if len(got) != 0 {
		t.Fatalf("a marker naming no candidate is not evidence: %#v", got)
	}
}

// One marker may name several passages. "[1, 2]" matched nothing before, which
// read as "cited nothing" - survivable while a fallback caught it, and a silent
// loss of the whole list now that none does.
func TestCitationsUsedByAnswerReadsGroupedMarkers(t *testing.T) {
	candidates := []Citation{{Index: 1, DocumentID: "a"}, {Index: 2, DocumentID: "b"}, {Index: 3, DocumentID: "c"}}
	got := citationsUsedByAnswer("Theo hai nguồn [1, 2] và thêm [3].", candidates)
	if len(got) != 3 {
		t.Fatalf("grouped marker lost: %#v", got)
	}
	if got[0].Index != 1 || got[1].Index != 2 || got[2].Index != 3 {
		t.Fatalf("order should follow the answer: %#v", got)
	}
}
