package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"cosmo/backend/internal/modelgateway"
	"cosmo/backend/internal/runs"
	"cosmo/backend/internal/tools"
)

// toolSet is what a turn may call, and where it came from.
//
// The runner used to be handed an agent id, which is the one thing a plain
// chat has not got. It is handed the set instead: an agent's attachments, or
// what the workspace has installed and switched on. Describing and calling
// them is the same either way, so only the gathering differs.
type toolSet struct {
	// "agent" or "workspace", for the log - the two fail differently and it is
	// worth knowing which one was in play.
	source      string
	tools       []tools.Tool
	actions     map[string][]tools.Action
	definitions []modelgateway.ToolDefinition
}

func (set toolSet) isEmpty() bool { return len(set.definitions) == 0 }

// ToolCall is one call as the reader sees it: which tool, which action, how it
// went and how long it took. It is streamed twice - once running, once
// settled - and the settled set is stored on the message it produced.
type ToolCall struct {
	ID     string `json:"id"`
	Tool   string `json:"tool"`
	Action string `json:"action"`
	Status string `json:"status"`
	// Arguments as the model wrote them, so the reader can see what was asked
	// and not only what came back.
	Arguments  string `json:"arguments,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Detail     string `json:"detail,omitempty"`
	// How many runes of the answer had been written when this call was made.
	// The transcript splits the text at these points and puts the call back
	// where it happened, rather than gathering every call above the answer.
	At int `json:"at"`
}

// A tool answer can be a whole document. What goes on screen is a glance at it;
// the run inspector holds the rest.
const toolDetailRunes = 300

func summarise(raw string) string {
	trimmed := strings.TrimSpace(raw)
	runes := []rune(trimmed)
	if len(runes) > toolDetailRunes {
		return string(runes[:toolDetailRunes]) + "…"
	}
	return trimmed
}

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
	set toolSet,
	history []modelgateway.Message,
	options modelgateway.Options,
	models *modelgateway.Client,
	runID string,
	answer *strings.Builder,
) ([]modelgateway.Message, []ToolCall) {
	reported := []ToolCall{}
	for round := 0; round < maxToolRounds; round++ {
		narration, calls, err := models.Decide(ctx, history, set.definitions, options)
		if err != nil {
			// A failed round is not a failed answer: the model can still reply
			// from what it already has, so this is reported and stepped over.
			s.logger.Error("tool round failed", "source", set.source, "error", err)
			writeSSE(w, "status", map[string]string{"stage": "tool_failed", "message": "Không gọi được tool."})
			flusher.Flush()
			return history, reported
		}
		if len(calls) == 0 {
			return history, reported
		}

		// What the model said on its way to calling is part of the answer, not
		// scaffolding: "let me look that up" is what makes the pause legible.
		if trimmed := strings.TrimSpace(narration); trimmed != "" {
			if answer.Len() > 0 {
				trimmed = "\n\n" + trimmed
			}
			answer.WriteString(trimmed)
			writeSSE(w, "delta", map[string]string{"content": trimmed})
			flusher.Flush()
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
			toolName, actionName := tools.SplitCallName(call.Name)
			shown := ToolCall{
				ID:        call.ID,
				Tool:      toolName,
				Action:    actionName,
				Status:    "running",
				Arguments: call.Arguments,
				At:        len([]rune(answer.String())),
			}
			writeSSE(w, "tool", shown)
			// The one-line status stays for readers of the plain stream; the
			// event above is what the transcript draws from.
			writeSSE(w, "status", map[string]string{"stage": "tool", "message": fmt.Sprintf("Đang gọi %s…", call.Name)})
			flusher.Flush()
			startedAt := time.Now()

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

			result, callErr := s.tools.InvokeInSet(ctx, set.tools, set.actions, call.Name, call.Arguments)
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

			shown.DurationMS = time.Since(startedAt).Milliseconds()
			if callErr != nil {
				shown.Status = "error"
				shown.Detail = callErr.Error()
			} else {
				shown.Status = "complete"
				shown.Detail = summarise(result.Body)
			}
			reported = append(reported, shown)
			writeSSE(w, "tool", shown)
			flusher.Flush()

			history = append(history, modelgateway.Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    content,
			})
		}
	}
	return history, reported
}

// toolSetFor gathers what a turn may call.
//
// An agent brings what it was wired to - frozen by version when the
// conversation is pinned, live when it is a draft. A plain chat brings what
// the workspace installed *and* switched on, which is two deliberate acts by
// somebody with the right to perform them, and never a tool holding a
// credential.
//
// A workspace with nothing installed produces an empty set, and an empty set
// skips the whole tool phase - so an ordinary chat costs exactly what it cost
// before this existed.
func (s *Server) toolSetFor(ctx context.Context, agentID, workspaceID string, pinned []string) (toolSet, error) {
	if agentID != "" {
		list, actions, err := s.tools.AttachedTools(ctx, agentID, pinned)
		if err != nil {
			return toolSet{}, err
		}
		return toolSet{source: "agent", tools: list, actions: actions,
			definitions: tools.DescribeSet(list, actions)}, nil
	}
	list, actions, err := s.tools.AutoCallable(ctx, workspaceID)
	if err != nil {
		return toolSet{}, err
	}
	return toolSet{source: "workspace", tools: list, actions: actions,
		definitions: tools.DescribeSet(list, actions)}, nil
}
