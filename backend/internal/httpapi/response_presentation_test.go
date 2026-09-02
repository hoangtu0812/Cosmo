package httpapi

import (
	"strings"
	"testing"

	"cosmo/backend/internal/modelgateway"
)

func TestWithResponsePresentationPrependsSystemGuide(t *testing.T) {
	history := []modelgateway.Message{{Role: "user", Content: "Compare the services"}}
	got := withResponsePresentation(history)
	// Compared field by field rather than whole: a Message carries a slice of
	// tool calls now, so the struct is no longer comparable with !=.
	if len(got) != 2 || got[0].Role != "system" || got[1].Role != history[0].Role || got[1].Content != history[0].Content {
		t.Fatalf("unexpected presented history: %#v", got)
	}
	for _, instruction := range []string{"Markdown table", "Do not use a table for a single fact", "Do not decorate every heading"} {
		if !strings.Contains(got[0].Content, instruction) {
			t.Errorf("presentation guide is missing %q", instruction)
		}
	}
}

func TestWithResponsePresentationDoesNotMutateHistory(t *testing.T) {
	history := []modelgateway.Message{{Role: "user", Content: "Original"}}
	_ = withResponsePresentation(history)
	if history[0].Role != "user" || history[0].Content != "Original" {
		t.Fatalf("history was mutated: %#v", history)
	}
}
