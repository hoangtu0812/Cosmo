package modelgateway

import (
	"encoding/json"
	"errors"
)

var ErrContextBudget = errors.New("Ngữ cảnh bắt buộc vượt giới hạn. Hãy rút ngắn câu hỏi, tệp hoặc cấu hình chỉ dẫn/tool.")
var ErrToolHistory = errors.New("Lịch sử tool-call/tool-result không đầy đủ; không thể tiếp tục lượt này.")

// BudgetReport measures serialized bytes, not provider token usage. Without a
// model tokenizer, one byte per token plus framing/output reserve is a cautious
// planning heuristic, not an exact proof that a provider will accept a prompt.
type BudgetReport struct {
	InputBytes      int `json:"input_bytes"`
	LimitBytes      int `json:"limit_bytes"`
	DroppedMessages int `json:"dropped_messages"`
	ContextWindow   int `json:"context_window,omitempty"`
	OutputReserve   int `json:"output_reserve"`
}

func budgetMessages(messages []Message, definitions any, options Options) ([]Message, *BudgetReport, error) {
	limit := 128 * 1024
	if options.MaxInputBytes > 0 {
		limit = min(limit, options.MaxInputBytes)
	}
	reserve := 4096
	if options.ContextWindow > 0 {
		reserve = min(reserve, options.ContextWindow/4)
		limit = min(limit, options.ContextWindow-reserve-1024)
	}
	report := &BudgetReport{LimitBytes: max(0, limit), ContextWindow: options.ContextWindow, OutputReserve: reserve}
	// Validate before trimming so a malformed tool history cannot become a
	// valid-looking prompt with missing or misattributed execution results.
	pending := map[string]bool{}
	for _, message := range messages {
		if message.Role == "tool" {
			if !pending[message.ToolCallID] || len(message.ToolCalls) > 0 {
				return nil, report, ErrToolHistory
			}
			delete(pending, message.ToolCallID)
			continue
		}
		if len(pending) > 0 || message.ToolCallID != "" {
			return nil, report, ErrToolHistory
		}
		if len(message.ToolCalls) > 0 && message.Role != "assistant" {
			return nil, report, ErrToolHistory
		}
		for _, call := range message.ToolCalls {
			id, _ := call["id"].(string)
			if id == "" || pending[id] {
				return nil, report, ErrToolHistory
			}
			pending[id] = true
		}
	}
	if len(pending) > 0 {
		return nil, report, ErrToolHistory
	}
	// Each user turn and everything following it is an indivisible group.
	// System/developer instructions and the newest group are never discarded.
	groups := make([][]int, 0)
	for i, message := range messages {
		if message.Role == "system" || message.Role == "developer" {
			continue
		}
		if message.Role == "user" || len(groups) == 0 {
			groups = append(groups, nil)
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], i)
	}
	removed := make([]bool, len(messages))
	kept := func() []Message {
		result := make([]Message, 0, len(messages))
		for i, message := range messages {
			if !removed[i] {
				result = append(result, message)
			}
		}
		return result
	}
	for drop := 0; ; drop++ {
		result := kept()
		payload, err := json.Marshal(map[string]any{"messages": result, "tools": definitions})
		if err != nil {
			return nil, report, err
		}
		report.InputBytes = len(payload)
		if len(payload) <= limit {
			return result, report, nil
		}
		if drop >= len(groups)-1 {
			return nil, report, ErrContextBudget
		}
		for _, index := range groups[drop] {
			removed[index] = true
			report.DroppedMessages++
		}
	}
}
