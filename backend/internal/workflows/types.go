// Package workflows stores and runs a graph of steps.
//
// A workflow is the answer to work whose order is known in advance: an agent
// decides what to do next, a workflow was told. The two are not competitors -
// a workflow step can call an agent - but they fail differently, and a fixed
// order is worth having when the order is genuinely fixed.
package workflows

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// Node kinds. Six run; the rest exist so the shape of the
// thing being built is visible in the library rather than sprung on the reader
// later. What runs and what does not is decided in one place - see Runnable -
// so the editor and the runner cannot disagree about it.
const (
	KindStart = "start"
	KindLLM   = "llm"
	KindTool  = "tool"
	KindEnd   = "end"

	KindKnowledge = "knowledge"
	KindCondition = "condition"
	KindLoop      = "loop"
	KindCode      = "code"
	KindHTTP      = "http"
	KindAgent     = "agent"
)

// Runnable reports whether a node kind does anything yet. The editor greys out
// the rest and the runner refuses them, both from this.
func Runnable(kind string) bool {
	switch kind {
	case KindStart, KindLLM, KindTool, KindEnd, KindCondition, KindLoop:
		return true
	}
	return false
}

const (
	MaxNameRunes        = 120
	MaxDescriptionRunes = 512
	// A graph past this size is not a workflow anyone is reading, and the
	// runner walks it in one request.
	MaxNodes = 60
	MaxEdges = 120
	// Long enough for a chain of model calls, short enough that a run cannot
	// hold a connection open all afternoon.
	RunTimeout = 3 * time.Minute
)

var (
	ErrNameRequired  = errors.New("Workflow cần có tên.")
	ErrNameTooLong   = errors.New("Tên workflow quá dài.")
	ErrTooLong       = errors.New("Mô tả quá dài.")
	ErrNotFound      = errors.New("Không tìm thấy workflow.")
	ErrTooManyNodes  = errors.New("Workflow có quá nhiều node.")
	ErrTooManyEdges  = errors.New("Workflow có quá nhiều kết nối.")
	ErrNoStart       = errors.New("Workflow cần một node Bắt đầu.")
	ErrManyStarts    = errors.New("Workflow chỉ được có một node Bắt đầu.")
	ErrCycle         = errors.New("Workflow không được có vòng lặp.")
	ErrNotRunnable   = errors.New("Node này chưa chạy được.")
	ErrUnknownTarget = errors.New("Kết nối trỏ tới node không tồn tại.")
)

// Node is one step. Config is deliberately loose: each kind reads the keys it
// needs and ignores the rest, so adding a kind does not change this struct or
// the column it is stored in.
type Node struct {
	ID     string         `json:"id"`
	Kind   string         `json:"kind"`
	Name   string         `json:"name"`
	X      float64        `json:"x"`
	Y      float64        `json:"y"`
	Config map[string]any `json:"config,omitempty"`
}

// Edge is a connection. Only the two ends are stored; how it is drawn is the
// editor's business.
type Edge struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
	// Which way out of a Condition this edge leaves by: "true", "false", or
	// empty for every other kind of node, which has only one way out.
	Branch string `json:"branch,omitempty"`
}

type Graph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

type Workflow struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Icon        string    `json:"icon"`
	OwnerUserID string    `json:"owner_user_id"`
	OwnerName   string    `json:"owner_name"`
	WorkspaceID string    `json:"workspace_id"`
	Visibility  string    `json:"visibility"`
	Graph       Graph     `json:"graph"`
	IsEditable  bool      `json:"is_editable"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func ValidateName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", ErrNameRequired
	}
	if utf8.RuneCountInString(name) > MaxNameRunes {
		return "", ErrNameTooLong
	}
	return name, nil
}

func ValidateDescription(raw string) (string, error) {
	text := strings.TrimSpace(raw)
	if utf8.RuneCountInString(text) > MaxDescriptionRunes {
		return "", ErrTooLong
	}
	return text, nil
}

// CleanGraph is the one gate a graph passes before it is stored or run. It
// refuses what the runner cannot walk - a missing start, two starts, an edge
// into nowhere, a cycle - rather than letting the runner discover it halfway
// through and leave the reader with a half-finished run.
func CleanGraph(graph Graph) (Graph, error) {
	if len(graph.Nodes) > MaxNodes {
		return Graph{}, ErrTooManyNodes
	}
	if len(graph.Edges) > MaxEdges {
		return Graph{}, ErrTooManyEdges
	}

	known := map[string]bool{}
	starts := 0
	nodes := make([]Node, 0, len(graph.Nodes))
	for _, node := range graph.Nodes {
		id := strings.TrimSpace(node.ID)
		if id == "" || known[id] {
			continue
		}
		known[id] = true
		if node.Kind == KindStart {
			starts++
		}
		if node.Config == nil {
			node.Config = map[string]any{}
		}
		node.ID = id
		node.Name = strings.TrimSpace(node.Name)
		nodes = append(nodes, node)
	}
	// An empty graph is a workflow nobody has started, not a broken one.
	if len(nodes) > 0 {
		if starts == 0 {
			return Graph{}, ErrNoStart
		}
		if starts > 1 {
			return Graph{}, ErrManyStarts
		}
	}

	seen := map[string]bool{}
	edges := make([]Edge, 0, len(graph.Edges))
	for _, edge := range graph.Edges {
		if !known[edge.Source] || !known[edge.Target] {
			return Graph{}, ErrUnknownTarget
		}
		// A node joined to itself, and the same joint twice, are both the
		// editor slipping rather than something to run.
		key := edge.Source + "->" + edge.Target
		if edge.Source == edge.Target || seen[key] {
			continue
		}
		seen[key] = true
		if strings.TrimSpace(edge.ID) == "" {
			edge.ID = key
		}
		edges = append(edges, edge)
	}

	cleaned := Graph{Nodes: nodes, Edges: edges}
	if hasCycle(cleaned) {
		return Graph{}, ErrCycle
	}
	return cleaned, nil
}

// hasCycle walks the graph depth-first. A workflow that loops has no order to
// run in, and Loop is a node kind rather than a shape of the graph precisely
// so that this can stay true.
func hasCycle(graph Graph) bool {
	next := map[string][]string{}
	for _, edge := range graph.Edges {
		next[edge.Source] = append(next[edge.Source], edge.Target)
	}
	const (
		unvisited = 0
		open      = 1
		closed    = 2
	)
	state := map[string]int{}

	var walk func(string) bool
	walk = func(id string) bool {
		switch state[id] {
		case open:
			return true
		case closed:
			return false
		}
		state[id] = open
		for _, target := range next[id] {
			if walk(target) {
				return true
			}
		}
		state[id] = closed
		return false
	}
	for _, node := range graph.Nodes {
		if walk(node.ID) {
			return true
		}
	}
	return false
}

// Order returns the nodes in the order they run: a node waits for everything
// that feeds it. The graph is known acyclic by the time this is called, so the
// queue cannot stall.
func Order(graph Graph) []Node {
	byID := map[string]Node{}
	waiting := map[string]int{}
	for _, node := range graph.Nodes {
		byID[node.ID] = node
		waiting[node.ID] = 0
	}
	next := map[string][]string{}
	for _, edge := range graph.Edges {
		next[edge.Source] = append(next[edge.Source], edge.Target)
		waiting[edge.Target]++
	}

	// Start first, then anything else nothing feeds - a node left unconnected
	// still runs, because the reader put it there.
	queue := []string{}
	for _, node := range graph.Nodes {
		if waiting[node.ID] == 0 && node.Kind == KindStart {
			queue = append(queue, node.ID)
		}
	}
	for _, node := range graph.Nodes {
		if waiting[node.ID] == 0 && node.Kind != KindStart {
			queue = append(queue, node.ID)
		}
	}

	ordered := make([]Node, 0, len(graph.Nodes))
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		ordered = append(ordered, byID[id])
		for _, target := range next[id] {
			waiting[target]--
			if waiting[target] == 0 {
				queue = append(queue, target)
			}
		}
	}
	return ordered
}
