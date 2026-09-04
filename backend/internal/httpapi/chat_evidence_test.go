package httpapi

import (
	"strings"
	"testing"
)

func TestMissingKnowledgeDistinguishesFailureAndNoMatch(t *testing.T) {
	failed := missingKnowledgeAnswer(true, 0, true)
	empty := missingKnowledgeAnswer(true, 0, false)
	if failed == empty || !strings.Contains(failed, "chưa truy cập") || !strings.Contains(empty, "chưa tìm thấy") {
		t.Fatalf("lookup failure confused with no match: %q, %q", failed, empty)
	}
	for _, message := range []string{failed, empty} {
		if inlineCitationPattern.MatchString(message) {
			t.Fatal("missing evidence must not invent citations")
		}
	}
	if got := missingKnowledgeAnswer(false, 0, true); got != "" {
		t.Fatalf("blocked general conversation: %q", got)
	}
	if got := missingKnowledgeAnswer(true, 2, true); got != "" {
		t.Fatalf("discarded usable partial evidence: %q", got)
	}
}
