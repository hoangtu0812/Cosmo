package workflows

import (
	"errors"
	"testing"
)

func graph(nodes []Node, edges []Edge) Graph { return Graph{Nodes: nodes, Edges: edges} }

// A graph that loops has no order to run in, and the runner would walk it
// forever. It is refused where it is saved, not where it is run, so the reader
// hears about it while they are still drawing.
func TestCleanGraphRefusesACycle(t *testing.T) {
	_, err := CleanGraph(graph(
		[]Node{{ID: "start", Kind: KindStart}, {ID: "a", Kind: KindLLM}, {ID: "b", Kind: KindLLM}},
		[]Edge{{Source: "start", Target: "a"}, {Source: "a", Target: "b"}, {Source: "b", Target: "a"}},
	))
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("got %v, want ErrCycle", err)
	}
}

// An edge into a node that is not there would leave the runner walking into
// nothing halfway through.
func TestCleanGraphRefusesAnEdgeIntoNowhere(t *testing.T) {
	_, err := CleanGraph(graph(
		[]Node{{ID: "start", Kind: KindStart}},
		[]Edge{{Source: "start", Target: "ghost"}},
	))
	if !errors.Is(err, ErrUnknownTarget) {
		t.Fatalf("got %v, want ErrUnknownTarget", err)
	}
}

func TestCleanGraphCountsStarts(t *testing.T) {
	if _, err := CleanGraph(graph([]Node{{ID: "a", Kind: KindLLM}}, nil)); !errors.Is(err, ErrNoStart) {
		t.Errorf("no start: got %v, want ErrNoStart", err)
	}
	if _, err := CleanGraph(graph(
		[]Node{{ID: "a", Kind: KindStart}, {ID: "b", Kind: KindStart}}, nil,
	)); !errors.Is(err, ErrManyStarts) {
		t.Errorf("two starts: got %v, want ErrManyStarts", err)
	}
	// A workflow nobody has started is not broken.
	if _, err := CleanGraph(Graph{}); err != nil {
		t.Errorf("empty graph refused: %v", err)
	}
}

// A node joined to itself and the same joint drawn twice are the editor
// slipping; both are dropped rather than refused, because neither is something
// the reader has to go and fix.
func TestCleanGraphDropsSelfAndDuplicateEdges(t *testing.T) {
	cleaned, err := CleanGraph(graph(
		[]Node{{ID: "start", Kind: KindStart}, {ID: "a", Kind: KindLLM}},
		[]Edge{
			{Source: "start", Target: "a"},
			{Source: "start", Target: "a"},
			{Source: "a", Target: "a"},
		},
	))
	if err != nil {
		t.Fatalf("clean: %v", err)
	}
	if len(cleaned.Edges) != 1 {
		t.Fatalf("kept %d edges, want 1", len(cleaned.Edges))
	}
}

// A node waits for everything that feeds it, and Start goes first even when
// something else is also unfed.
func TestOrderWaitsForEveryInput(t *testing.T) {
	ordered := Order(graph(
		[]Node{
			{ID: "end", Kind: KindEnd},
			{ID: "b", Kind: KindLLM},
			{ID: "start", Kind: KindStart},
			{ID: "a", Kind: KindLLM},
		},
		[]Edge{
			{Source: "start", Target: "a"},
			{Source: "start", Target: "b"},
			{Source: "a", Target: "end"},
			{Source: "b", Target: "end"},
		},
	))
	if len(ordered) != 4 {
		t.Fatalf("ordered %d nodes, want 4", len(ordered))
	}
	if ordered[0].ID != "start" {
		t.Errorf("start ran %s, want first", ordered[0].ID)
	}
	if ordered[3].ID != "end" {
		t.Errorf("end ran %s, want last - it waits for both branches", ordered[3].ID)
	}
}

// A node nothing feeds still runs: the reader put it on the canvas.
func TestOrderIncludesUnconnectedNodes(t *testing.T) {
	ordered := Order(graph(
		[]Node{{ID: "start", Kind: KindStart}, {ID: "loose", Kind: KindLLM}}, nil,
	))
	if len(ordered) != 2 {
		t.Fatalf("ordered %d nodes, want 2", len(ordered))
	}
}

// What runs and what is a shell is decided in one place, so the editor and the
// runner cannot disagree about which is which.
func TestRunnableCoversTheFourThatRun(t *testing.T) {
	for _, kind := range []string{KindStart, KindLLM, KindTool, KindEnd} {
		if !Runnable(kind) {
			t.Errorf("%s should run", kind)
		}
	}
	for _, kind := range []string{KindCondition, KindLoop, KindCode, KindHTTP, KindKnowledge, KindAgent, "invented"} {
		if Runnable(kind) {
			t.Errorf("%s claims to run", kind)
		}
	}
}
