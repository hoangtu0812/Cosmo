package httpapi

import (
	"strings"
	"testing"

	"cosmo/backend/internal/tools"
)

// A retrieval that found nothing has to say so. "Đã tra Knowledge Base" with an
// empty detail reads as though it found something and is hiding it.
func TestDescribePassagesSaysWhenNothingMatched(t *testing.T) {
	if got := describePassages(nil); got != "không có đoạn nào khớp" {
		t.Fatalf("empty retrieval described as %q", got)
	}
}

func TestDescribePassagesNamesTheDocuments(t *testing.T) {
	got := describePassages([]knowledgePassage{
		{Title: "Quy chế nội bộ"},
		{Title: "Quy chế nội bộ"}, // the same document twice is one name
		{Title: "", Source: "bao-cao-2025.pdf"},
	})
	if !strings.HasPrefix(got, "3 đoạn · ") {
		t.Fatalf("passage count missing from %q", got)
	}
	if strings.Count(got, "Quy chế nội bộ") != 1 {
		t.Fatalf("repeated document named twice: %q", got)
	}
	// A passage with no title falls back to where it came from rather than
	// leaving a gap in the list.
	if !strings.Contains(got, "bao-cao-2025.pdf") {
		t.Fatalf("untitled passage lost its source: %q", got)
	}
}

// A status line is one line. Ten document names is a list nobody reads there,
// and the citations under the answer carry the rest.
func TestDescribePassagesStopsAtThreeNames(t *testing.T) {
	passages := []knowledgePassage{}
	for _, title := range []string{"A", "B", "C", "D", "E"} {
		passages = append(passages, knowledgePassage{Title: title})
	}
	got := describePassages(passages)
	if !strings.Contains(got, "…") {
		t.Fatalf("long list not trimmed: %q", got)
	}
	if strings.Contains(got, "D") || strings.Contains(got, "E") {
		t.Fatalf("trimmed list still names everything: %q", got)
	}
	if !strings.HasPrefix(got, "5 đoạn · ") {
		t.Fatalf("count should still be the true one: %q", got)
	}
}

// Where the permission came from matters as much as what was permitted: an
// agent's own attachments and the workspace's installed tools are different
// answers to "why could it call that".
func TestDescribeToolSetNamesTheOrigin(t *testing.T) {
	set := toolSet{source: "agent", tools: []tools.Tool{{Name: "Chart"}, {Name: "Data"}}}
	if got := describeToolSet(set); got != "agent: Chart, Data" {
		t.Fatalf("agent tools described as %q", got)
	}
	set.source = "workspace"
	if got := describeToolSet(set); !strings.HasPrefix(got, "workspace: ") {
		t.Fatalf("workspace tools described as %q", got)
	}
}

func TestDescribeToolSetStopsAtSixNames(t *testing.T) {
	set := toolSet{source: "workspace"}
	for _, name := range []string{"A", "B", "C", "D", "E", "F", "G", "H"} {
		set.tools = append(set.tools, tools.Tool{Name: name})
	}
	got := describeToolSet(set)
	if !strings.Contains(got, "…") {
		t.Fatalf("long tool list not trimmed: %q", got)
	}
	if strings.Contains(got, "G") || strings.Contains(got, "H") {
		t.Fatalf("trimmed tool list still names everything: %q", got)
	}
}
