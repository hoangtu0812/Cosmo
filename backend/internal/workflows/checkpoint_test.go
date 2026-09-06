package workflows

import (
	"context"
	"cosmo/backend/internal/modelgateway"
	"errors"
	"testing"
)

type countedInvoker struct{ calls int }

func (i *countedInvoker) InvokeAction(context.Context, string, string, map[string]any) (string, error) {
	i.calls++
	return "saved-result", nil
}

func TestCheckpointResumeDoesNotRepeatCompletedTool(t *testing.T) {
	graph := Graph{Nodes: []Node{{ID: "start", Kind: KindStart}, {ID: "tool", Kind: KindTool, Config: map[string]any{"tool_id": "t", "action_id": "a"}}, {ID: "end", Kind: KindEnd, Config: map[string]any{"template": "{{tool}}"}}}, Edges: []Edge{{ID: "1", Source: "start", Target: "tool"}, {ID: "2", Source: "tool", Target: "end"}}}
	completed := map[string]Step{}
	invoker := &countedInvoker{}
	repo := &Repository{}
	stop := errors.New("stop between completed tool and end")
	err := repo.RunCheckpointed(context.Background(), graph, "input", nil, modelgateway.Options{}, invoker, nil, func(step Step) error {
		if step.NodeID == "end" {
			return stop
		}
		if step.Status == StatusComplete {
			completed[step.NodeID] = step
		}
		return nil
	}, func(Step) {})
	if !errors.Is(err, stop) || invoker.calls != 1 {
		t.Fatalf("first run: %v calls %d", err, invoker.calls)
	}
	var result string
	err = repo.RunCheckpointed(context.Background(), graph, "input", nil, modelgateway.Options{}, invoker, completed, nil, func(step Step) {
		if step.NodeID == "end" && step.Status == StatusComplete {
			result = step.Output
		}
	})
	if err != nil || invoker.calls != 1 || result != "saved-result" {
		t.Fatalf("replayed tool or lost result: %v %d %q", err, invoker.calls, result)
	}
	invoker.calls = 0
	err = repo.RunCheckpointed(context.Background(), graph, "input", nil, modelgateway.Options{}, invoker, nil, func(step Step) error {
		if step.NodeID == "tool" {
			return stop
		}
		return nil
	}, func(Step) {})
	if !errors.Is(err, stop) || invoker.calls != 0 {
		t.Fatal("side effect before checkpoint admission")
	}
}
