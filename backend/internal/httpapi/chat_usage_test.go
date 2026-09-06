package httpapi

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"cosmo/backend/internal/modelgateway"
	"cosmo/backend/internal/runs"
)

func TestChatAccountingRetainsRepeatedAndConcurrentPhases(t *testing.T) {
	s, agent, owner, _ := agentAccessFixture(t)
	s.runs = runs.NewRepository(s.db)
	ctx := context.Background()
	run, _, err := s.runs.Create(ctx, runs.NewRun{WorkspaceID: agent.WorkspaceID, ActorUserID: owner.ID, ResourceType: "conversation", ResourceID: "accounting-test"})
	if err != nil {
		t.Fatal(err)
	}
	observe := s.observeChatModel(run.ID)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var usage *modelgateway.Usage
			if i%2 == 0 {
				usage = &modelgateway.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12}
			}
			observe(modelgateway.CallObservation{Phase: "tool_decision", Model: "fixture", DurationMS: int64(i + 1), Failed: i%2 != 0, Usage: usage})
		}(i)
	}
	wg.Wait()
	observe(modelgateway.CallObservation{Phase: "answer", Model: "fixture", Usage: &modelgateway.Usage{}})
	steps, err := s.runs.Steps(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 9 {
		t.Fatalf("lost accounting: %d steps", len(steps))
	}
	attempts := map[int]bool{}
	var known, missing int
	var tokens, duration int64
	for _, step := range steps {
		var call modelgateway.CallObservation
		if err := json.Unmarshal(step.Output, &call); err != nil {
			t.Fatal(err)
		}
		if step.NodeID == "model_call:answer" {
			if step.Attempt != 1 || call.Usage == nil || call.Usage.TotalTokens != 0 {
				t.Fatal("zero usage or phase identity lost")
			}
			continue
		}
		if attempts[step.Attempt] || step.Attempt < 1 || step.Attempt > 8 {
			t.Fatal("reused attempt")
		}
		attempts[step.Attempt] = true
		duration += call.DurationMS
		if call.Usage != nil {
			known++
			tokens += int64(call.Usage.TotalTokens)
		} else {
			missing++
		}
		if (step.Status == runs.Failed) != call.Failed {
			t.Fatal("lost failed status")
		}
	}
	if known != 4 || missing != 4 || tokens != 48 || duration != 36 {
		t.Fatalf("incorrect accounting: known=%d missing=%d tokens=%d duration=%d", known, missing, tokens, duration)
	}
}
