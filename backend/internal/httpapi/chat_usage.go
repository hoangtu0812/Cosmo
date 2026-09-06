package httpapi

import (
	"context"
	"cosmo/backend/internal/modelgateway"
	"cosmo/backend/internal/runs"
	"sync"
	"time"
)

func (s *Server) observeChatModel(runID string) func(modelgateway.CallObservation) {
	var mutex sync.Mutex
	attempts := map[string]int{}
	return func(call modelgateway.CallObservation) {
		// Several tool decisions share a phase; auxiliary calls can finish concurrently.
		mutex.Lock()
		attempts[call.Phase]++
		attempt := attempts[call.Phase]
		mutex.Unlock()
		// Cancellation must not erase accounting for an already issued request.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		step, err := s.runs.CreateStep(ctx, runs.NewStep{RunID: runID, NodeID: "model_call:" + call.Phase, Type: "model_call", Name: call.Phase, Attempt: attempt})
		if err == nil {
			_, err = s.runs.TransitionStep(ctx, step.ID, runs.Running, nil, "", "", "")
		}
		if err == nil {
			status := runs.Succeeded
			code := ""
			if call.Failed {
				status = runs.Failed
				code = "model_call_failed"
			}
			_, err = s.runs.TransitionStep(ctx, step.ID, status, call, "", code, "")
		}
		if err != nil {
			s.logger.Warn("model accounting unavailable", "run_id", runID)
		}
	}
}
