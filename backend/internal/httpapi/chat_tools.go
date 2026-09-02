package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"cosmo/backend/internal/modelgateway"
	"cosmo/backend/internal/runs"
)

// A model may ask for tools, be given results, and ask again. Three rounds is
// enough for "look this up, then look that up in what came back"; past that it
// is usually a loop, and the reader is waiting the whole time.
const maxToolRounds = 3

// runToolRounds gives the model its tools and runs whatever it asks for, then
// hands back the history to answer from.
//
// This happens before the answer is streamed rather than inside the stream. A
// tool round produces no words, so weaving it into the token stream would mean
// the reader watching an empty screen while the server works. Instead each call
// is announced as a status, and the answer streams once when there is something
// to say.
func (s *Server) runToolRounds(
	ctx context.Context,
	w http.ResponseWriter,
	flusher http.Flusher,
	agentID string,
	history []modelgateway.Message,
	definitions []modelgateway.ToolDefinition,
	options modelgateway.Options,
	models *modelgateway.Client,
	runID string,
) []modelgateway.Message {
	for round := 0; round < maxToolRounds; round++ {
		_, calls, err := models.Decide(ctx, history, definitions, options)
		if err != nil {
			// A failed round is not a failed answer: the model can still reply
			// from what it already has, so this is reported and stepped over.
			s.logger.Error("tool round failed", "agent_id", agentID, "error", err)
			writeSSE(w, "status", map[string]string{"stage": "tool_failed", "message": "Không gọi được tool."})
			flusher.Flush()
			return history
		}
		if len(calls) == 0 {
			return history
		}

		// The assistant turn that asked has to be echoed before its results, or
		// the gateway has nothing to attach them to.
		requested := make([]map[string]any, 0, len(calls))
		for _, call := range calls {
			requested = append(requested, map[string]any{
				"id":   call.ID,
				"type": "function",
				"function": map[string]any{
					"name":      call.Name,
					"arguments": call.Arguments,
				},
			})
		}
		history = append(history, modelgateway.Message{Role: "assistant", ToolCalls: requested})

		for _, call := range calls {
			writeSSE(w, "status", map[string]string{"stage": "tool", "message": fmt.Sprintf("Đang gọi %s…", call.Name)})
			flusher.Flush()

			step, stepErr := s.runs.CreateStep(ctx, runs.NewStep{
				RunID:     runID,
				NodeID:    "tool:" + call.Name,
				Type:      "tool",
				Name:      call.Name,
				TimeoutMS: 20000,
			})
			if stepErr == nil {
				step, stepErr = s.runs.TransitionStep(ctx, step.ID, runs.Running, nil, "", "", "")
			}

			result, callErr := s.tools.InvokeNamed(ctx, agentID, call.Name, call.Arguments)
			content := result.Body
			if callErr != nil {
				// The failure is handed to the model rather than hidden: told
				// what went wrong, it can try different arguments or say it
				// could not find out, which is better than inventing an answer.
				content = "Tool call failed: " + callErr.Error()
				if stepErr == nil {
					_, _ = s.runs.TransitionStep(ctx, step.ID, runs.Failed, nil, "", "tool_failed", callErr.Error())
				}
			} else if stepErr == nil {
				_, _ = s.runs.TransitionStep(ctx, step.ID, runs.Succeeded, map[string]any{
					"status":      result.Status,
					"duration_ms": result.DurationMS,
					"bytes":       len(result.Body),
				}, "", "", "")
			}
			if strings.TrimSpace(content) == "" {
				content = fmt.Sprintf("(empty response, status %d)", result.Status)
			}

			history = append(history, modelgateway.Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    content,
			})
		}
	}
	return history
}
