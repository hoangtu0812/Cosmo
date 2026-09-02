package workflows

import (
	"strconv"
	"strings"
)

// Condition and Loop are the two that make a workflow more than a straight
// line, and they need different things from the runner, so both live here
// beside the reasoning rather than inside the switch.
//
// Condition decides which way to go. Loop repeats one step over a list. Both
// are deliberately small: a workflow author is wiring steps together, not
// writing a program, and anything a program needs belongs in a Code node.

// Comparisons a condition can make. Kept to what can be judged the same way by
// the person writing it and the machine running it - no regular expressions,
// no arithmetic on text that might not be a number.
const (
	OpContains  = "contains"
	OpEquals    = "equals"
	OpNotEmpty  = "not_empty"
	OpGreater   = "greater"
	OpLess      = "less"
	OpStartWith = "starts_with"
)

// Branch names, which are also the edge labels the editor draws.
const (
	BranchTrue  = "true"
	BranchFalse = "false"
)

// MaxLoopItems caps a run that would otherwise be as long as whatever a
// previous node happened to produce. A list past this is a sign the workflow
// wanted a different shape, not a longer wait.
const MaxLoopItems = 20

// Judge decides which branch a condition takes.
//
// Everything is compared as text unless both sides parse as numbers, because
// that is the rule a reader can hold in their head: "10" is less than "9" as
// text and greater as a number, and guessing differently in different places
// is worse than either.
func Judge(left, operator, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)

	switch operator {
	case OpNotEmpty:
		return left != ""
	case OpEquals:
		return strings.EqualFold(left, right)
	case OpStartWith:
		return strings.HasPrefix(strings.ToLower(left), strings.ToLower(right))
	case OpGreater, OpLess:
		leftNumber, leftOK := strconv.ParseFloat(left, 64)
		rightNumber, rightOK := strconv.ParseFloat(right, 64)
		if leftOK == nil && rightOK == nil {
			if operator == OpGreater {
				return leftNumber > rightNumber
			}
			return leftNumber < rightNumber
		}
		if operator == OpGreater {
			return left > right
		}
		return left < right
	}
	// Anything unrecognised falls to contains, which is the operator a reader
	// most often means and the least surprising thing to do with two strings.
	return strings.Contains(strings.ToLower(left), strings.ToLower(right))
}

// SplitItems turns a node's output into the list a Loop walks. Lines, because
// that is what a model produces when asked for a list and what a reader sees
// when they look at the output above.
func SplitItems(raw string) []string {
	items := []string{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		// Strip the bullet or number a model adds despite being asked not to.
		line = strings.TrimLeft(line, "-*• \t0123456789.)")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		items = append(items, line)
		if len(items) == MaxLoopItems {
			break
		}
	}
	return items
}

// ReachableFromStart names the nodes a run will actually arrive at, given the
// edges a condition has closed.
//
// Stated as reachability rather than as "what the abandoned branch leads to",
// because those are not the same thing: two branches that meet downstream must
// still run what they meet at, which is the whole point of letting them meet.
// Walking forward from Start over the edges that remain open answers that
// without any special case.
func ReachableFromStart(graph Graph, closed map[string]bool) map[string]bool {
	next := map[string][]string{}
	for _, edge := range graph.Edges {
		if closed[edge.ID] {
			continue
		}
		next[edge.Source] = append(next[edge.Source], edge.Target)
	}

	reachable := map[string]bool{}
	queue := []string{}
	fed := map[string]bool{}
	for _, edge := range graph.Edges {
		fed[edge.Target] = true
	}
	// Start, and anything nothing feeds - a node left unconnected still runs,
	// because the reader put it there.
	for _, node := range graph.Nodes {
		if !fed[node.ID] {
			queue = append(queue, node.ID)
		}
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if reachable[id] {
			continue
		}
		reachable[id] = true
		queue = append(queue, next[id]...)
	}
	return reachable
}
