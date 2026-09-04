package httpapi

import (
	"reflect"
	"strings"
	"testing"

	"cosmo/backend/internal/knowledge"
)

func TestKnowledgeFusionIgnoresIncomparableScoreScales(t *testing.T) {
	a := []knowledge.Passage{{KBID: "a", Text: "A1", Score: .02}, {KBID: "a", Text: "A2", Score: .01}}
	b := []knowledge.Passage{{KBID: "b", Text: "B1", Score: 900}, {KBID: "b", Text: "B2", Score: 800}}
	want := []string{"A1", "B1", "A2"}
	for _, lists := range [][][]knowledge.Passage{{a, b}, {b, a}} {
		got := fuseKnowledgeRanks(lists, 3)
		var texts []string
		for _, p := range got {
			texts = append(texts, p.Text)
		}
		if !reflect.DeepEqual(texts, want) {
			t.Fatalf("fusion depends on scale or arrival order: %v", texts)
		}
		if got[0].Score != .02 || got[1].Score != 900 || got[0].FusionScore != got[1].FusionScore || got[2].LocalRank != 2 {
			t.Fatalf("lost local score/rank provenance: %+v", got)
		}
	}
}

func TestKnowledgeFusionDeduplicatesWholePassageAndPreservesProvenance(t *testing.T) {
	base := knowledge.Passage{KBID: "a", DocumentID: "doc", Text: strings.Repeat("prefix ", 100) + " first rule"}
	changed := base
	changed.Text = strings.Repeat("prefix ", 100) + " different rule"
	otherSource := base
	otherSource.DocumentID = "other-document"
	got := fuseKnowledgeRanks([][]knowledge.Passage{{{KBID: "a", Text: " "}, base, base, changed, otherSource}}, 20)
	if len(got) != 3 || got[1].Text != changed.Text || got[2].DocumentID != otherSource.DocumentID || got[2].LocalRank != 3 {
		t.Fatalf("dedup removed distinct content/provenance or kept duplicates: %+v", got)
	}
}
