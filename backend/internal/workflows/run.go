package workflows

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cosmo/backend/internal/modelgateway"
)

// Step is one node's turn, reported as it happens. The editor draws the graph
// lighting up from these, which is why they are streamed rather than returned
// at the end: a run that takes half a minute should not look like a hang.
type Step struct {
	NodeID string `json:"node_id"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Output string `json:"output,omitempty"`
	// Which way a Condition went. Empty for every other kind.
	Branch     string `json:"branch,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

const (
	StatusRunning  = "running"
	StatusComplete = "complete"
	StatusError    = "error"
	StatusSkipped  = "skipped"
)

// What a node's output is trimmed to before it is shown or passed on. A model
// asked to summarise a novel would otherwise put the novel in the next prompt.
const maxOutputRunes = 4000

// Invoker is how a workflow reaches a tool. The tools package owns the
// invoking; this package owns the order, and neither imports the other's
// internals.
type Invoker interface {
	InvokeAction(ctx context.Context, toolID, actionID string, arguments map[string]any) (string, error)
}

// Run walks the graph in order and reports each step as it finishes. It stops
// at the first failure: a workflow is an order, and continuing past a step
// that did not happen produces an answer built on nothing.
func (repository *Repository) Run(
	ctx context.Context,
	graph Graph,
	input string,
	models *modelgateway.Client,
	options modelgateway.Options,
	tools Invoker,
	report func(Step),
) error {
	ctx, cancel := context.WithTimeout(ctx, RunTimeout)
	defer cancel()

	ordered := Order(graph)
	// What each node produced, so a later node can quote it.
	outputs := map[string]string{}
	// Edges a condition has closed. Everything a closed edge was the only way
	// into is skipped rather than run.
	closed := map[string]bool{}

	for _, node := range ordered {
		// Asked fresh each time, because the answer changes as conditions
		// decide - a node two branches meet at is reachable until both are
		// closed, and the last condition to run is what settles it.
		if !ReachableFromStart(graph, closed)[node.ID] {
			report(Step{NodeID: node.ID, Kind: node.Kind, Name: nameOf(node), Status: StatusSkipped,
				Error: "Nhánh này không được chọn."})
			continue
		}
		label := nameOf(node)
		report(Step{NodeID: node.ID, Kind: node.Kind, Name: label, Status: StatusRunning})

		if !Runnable(node.Kind) {
			// Reported rather than refused: the reader put a shell on the
			// canvas to see the shape, and the rest of the run is still worth
			// watching.
			report(Step{NodeID: node.ID, Kind: node.Kind, Name: label, Status: StatusSkipped,
				Error: ErrNotRunnable.Error()})
			continue
		}

		startedAt := time.Now()
		output, taken, err := repository.runNode(ctx, node, input, ordered, outputs, models, options, tools)
		elapsed := time.Since(startedAt).Milliseconds()
		if err != nil {
			report(Step{NodeID: node.ID, Kind: node.Kind, Name: label, Status: StatusError,
				Error: err.Error(), DurationMS: elapsed})
			return err
		}
		outputs[node.ID] = output
		// A condition closes every way out it did not take.
		if node.Kind == KindCondition {
			for _, edge := range graph.Edges {
				if edge.Source == node.ID && branchOf(edge) != taken {
					closed[edge.ID] = true
				}
			}
		}
		report(Step{NodeID: node.ID, Kind: node.Kind, Name: label, Status: StatusComplete,
			Output: output, Branch: taken, DurationMS: elapsed})
	}
	return nil
}

func (repository *Repository) runNode(
	ctx context.Context,
	node Node,
	input string,
	ordered []Node,
	outputs map[string]string,
	models *modelgateway.Client,
	options modelgateway.Options,
	tools Invoker,
) (string, string, error) {
	switch node.Kind {
	case KindStart:
		return input, "", nil

	case KindEnd:
		// The end says what the workflow answered. Given a template it uses
		// it; given nothing it repeats whatever fed it, which is what a reader
		// means by "the result" when they have not said otherwise.
		if template := text(node.Config, "template"); template != "" {
			return fill(template, input, outputs), "", nil
		}
		return everythingProduced(ordered, outputs), "", nil

	case KindLLM:
		prompt := fill(text(node.Config, "prompt"), input, outputs)
		if strings.TrimSpace(prompt) == "" {
			return "", "", fmt.Errorf("node %q has no prompt", node.Name)
		}
		messages := []modelgateway.Message{}
		if system := text(node.Config, "system"); system != "" {
			messages = append(messages, modelgateway.Message{Role: "system", Content: system})
		}
		messages = append(messages, modelgateway.Message{Role: "user", Content: prompt})

		nodeOptions := options
		if model := text(node.Config, "model"); model != "" {
			nodeOptions.Model = model
		}
		reply, err := models.Complete(ctx, messages, nodeOptions)
		if err != nil {
			return "", "", err
		}
		return trim(reply), "", nil

	case KindTool:
		toolID := text(node.Config, "tool_id")
		actionID := text(node.Config, "action_id")
		if toolID == "" || actionID == "" {
			return "", "", fmt.Errorf("node %q has no action chosen", node.Name)
		}
		arguments := map[string]any{}
		if raw, ok := node.Config["arguments"].(map[string]any); ok {
			for key, value := range raw {
				// Arguments are templates too, so a tool can be fed what an
				// earlier node produced.
				if asText, isText := value.(string); isText {
					arguments[key] = fill(asText, input, outputs)
					continue
				}
				arguments[key] = value
			}
		}
		if tools == nil {
			return "", "", ErrNotRunnable
		}
		reply, err := tools.InvokeAction(ctx, toolID, actionID, arguments)
		if err != nil {
			return "", "", err
		}
		return trim(reply), "", nil

	case KindCondition:
		left := fill(text(node.Config, "left"), input, outputs)
		right := fill(text(node.Config, "right"), input, outputs)
		taken := BranchFalse
		if Judge(left, text(node.Config, "operator"), right) {
			taken = BranchTrue
		}
		// The output is the decision itself, so a later node can quote it and
		// the reader can see it in the step without opening anything.
		return taken, taken, nil

	case KindLoop:
		items := SplitItems(fill(text(node.Config, "source"), input, outputs))
		prompt := text(node.Config, "prompt")
		if prompt == "" {
			return "", "", fmt.Errorf("node %q has no prompt", node.Name)
		}
		if len(items) == 0 {
			return "", "", nil
		}
		results := make([]string, 0, len(items))
		for _, item := range items {
			// {{item}} is what the loop adds; everything else a prompt can
			// reference still works, so a loop body can quote earlier nodes.
			body := strings.ReplaceAll(fill(prompt, input, outputs), "{{item}}", item)
			reply, err := models.Complete(ctx, []modelgateway.Message{{Role: "user", Content: body}}, options)
			if err != nil {
				return "", "", err
			}
			results = append(results, strings.TrimSpace(reply))
		}
		return trim(strings.Join(results, "\n")), "", nil
	}
	return "", "", ErrNotRunnable
}

func nameOf(node Node) string {
	if node.Name != "" {
		return node.Name
	}
	return node.Kind
}

// branchOf reads which way out an edge leaves by. An edge drawn before
// branches existed carries nothing, and counts as the true side so an older
// graph keeps working rather than silently closing every way forward.
func branchOf(edge Edge) string {
	if edge.Branch == "" {
		return BranchTrue
	}
	return edge.Branch
}

// fill replaces {{input}} and {{nodeID}} with what they hold. Deliberately not
// a template language: a workflow author is wiring steps together, not writing
// a program, and anything a program needs belongs in a Code node.
func fill(template, input string, outputs map[string]string) string {
	filled := strings.ReplaceAll(template, "{{input}}", input)
	for id, output := range outputs {
		filled = strings.ReplaceAll(filled, "{{"+id+"}}", output)
	}
	return filled
}

func text(config map[string]any, key string) string {
	if config == nil {
		return ""
	}
	value, _ := config[key].(string)
	return strings.TrimSpace(value)
}

func trim(raw string) string {
	runes := []rune(raw)
	if len(runes) > maxOutputRunes {
		return string(runes[:maxOutputRunes]) + "…"
	}
	return raw
}

// everythingProduced is what an End node repeats when it was given no
// template. Not "the last output": an End node with several inputs has no
// single last one. They are joined in the order they ran - a map has none, so
// the run's own order is what decides - and a reader who wants one of them
// writes a template.
func everythingProduced(ordered []Node, outputs map[string]string) string {
	parts := make([]string, 0, len(outputs))
	for _, node := range ordered {
		if output := outputs[node.ID]; strings.TrimSpace(output) != "" {
			parts = append(parts, output)
		}
	}
	return strings.Join(parts, "\n\n")
}
