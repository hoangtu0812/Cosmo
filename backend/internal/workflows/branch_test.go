package workflows

import "testing"

// The rule a reader has to hold in their head: text unless both sides are
// numbers. "10" beats "9" as a number and loses as text, so the two cases are
// spelled out rather than left to whichever the implementation happened to do.
func TestJudge(t *testing.T) {
	cases := []struct {
		left, op, right string
		want            bool
	}{
		{"10", OpGreater, "9", true},
		{"10", OpGreater, "chín", false},
		{"beta", OpGreater, "alpha", true},
		{"Xong", OpEquals, "xong", true},
		{"", OpNotEmpty, "", false},
		{"có gì đó", OpNotEmpty, "", true},
		{"Hà Nội hôm nay", OpContains, "hà nội", true},
		{"báo cáo quý 4", OpStartWith, "Báo cáo", true},
		// Anything unrecognised falls to contains rather than failing closed.
		{"một hai ba", "invented", "hai", true},
	}
	for _, item := range cases {
		if got := Judge(item.left, item.op, item.right); got != item.want {
			t.Errorf("Judge(%q, %q, %q) = %v, want %v", item.left, item.op, item.right, got, item.want)
		}
	}
}

// A model asked for a list supplies bullets and numbers despite being asked
// not to, and the cap is what stops a run being as long as whatever the
// previous node happened to produce.
func TestSplitItems(t *testing.T) {
	got := SplitItems("- một\n2. hai\n\n  • ba  \n")
	want := []string{"một", "hai", "ba"}
	if len(got) != len(want) {
		t.Fatalf("split into %d items, want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("item %d = %q, want %q", i, got[i], want[i])
		}
	}

	long := ""
	for i := 0; i < MaxLoopItems+10; i++ {
		long += "dòng\n"
	}
	if capped := SplitItems(long); len(capped) != MaxLoopItems {
		t.Errorf("kept %d items, want the cap of %d", len(capped), MaxLoopItems)
	}
}

// The case that makes reachability worth stating as reachability: two branches
// that meet again must still run what they meet at.
func TestReachableFromStartKeepsWhatBranchesMeetAt(t *testing.T) {
	g := Graph{
		Nodes: []Node{
			{ID: "start", Kind: KindStart},
			{ID: "if", Kind: KindCondition},
			{ID: "yes", Kind: KindLLM},
			{ID: "no", Kind: KindLLM},
			{ID: "end", Kind: KindEnd},
		},
		Edges: []Edge{
			{ID: "e0", Source: "start", Target: "if"},
			{ID: "e1", Source: "if", Target: "yes", Branch: BranchTrue},
			{ID: "e2", Source: "if", Target: "no", Branch: BranchFalse},
			{ID: "e3", Source: "yes", Target: "end"},
			{ID: "e4", Source: "no", Target: "end"},
		},
	}
	// The condition went true, so the false edge is closed.
	reachable := ReachableFromStart(g, map[string]bool{"e2": true})
	if !reachable["yes"] {
		t.Error("the taken branch was skipped")
	}
	if reachable["no"] {
		t.Error("the abandoned branch still ran")
	}
	if !reachable["end"] {
		t.Error("the node both branches meet at was skipped")
	}
}

// A node nothing feeds still runs, whatever the conditions decided: the reader
// put it on the canvas.
func TestReachableFromStartKeepsUnconnectedNodes(t *testing.T) {
	g := Graph{
		Nodes: []Node{{ID: "start", Kind: KindStart}, {ID: "loose", Kind: KindLLM}},
	}
	if !ReachableFromStart(g, map[string]bool{})["loose"] {
		t.Error("an unconnected node was skipped")
	}
}
