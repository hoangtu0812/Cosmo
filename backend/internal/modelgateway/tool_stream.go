package modelgateway

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"sort"
	"strings"
)

// Calls are assembled by index and may execute only after a complete stream.
func readToolStream(ctx context.Context, reader io.Reader) (string, []ToolCall, *Usage, error) {
	defer reportProgress(ctx, Progress{Done: true})
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var text strings.Builder
	calls := map[int]*ToolCall{}
	var usage *Usage
	finished := false
	total := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			if !finished && len(calls) > 0 {
				return "", nil, usage, ErrIncompleteStream
			}
			indexes := make([]int, 0, len(calls))
			for i := range calls {
				indexes = append(indexes, i)
			}
			sort.Ints(indexes)
			result := make([]ToolCall, 0, len(calls))
			for _, i := range indexes {
				c := calls[i]
				if c.ID == "" || c.Name == "" || !json.Valid([]byte(c.Arguments)) {
					return "", nil, usage, ErrInvalidStream
				}
				result = append(result, *c)
			}
			return strings.TrimSpace(text.String()), result, usage, nil
		}
		if data == "" {
			continue
		}
		var chunk struct {
			Error   json.RawMessage `json:"error"`
			Usage   *Usage          `json:"usage"`
			Choices []struct {
				Finish string `json:"finish_reason"`
				Delta  struct {
					Content   string `json:"content"`
					Reasoning string `json:"reasoning_content"`
					Summary   string `json:"reasoning"`
					Tools     []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil || (len(chunk.Error) > 0 && string(chunk.Error) != "null") {
			return "", nil, usage, ErrInvalidStream
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		if choice.Finish != "" {
			if choice.Finish != "stop" && choice.Finish != "tool_calls" {
				return "", nil, usage, ErrIncompleteStream
			}
			finished = true
		}
		text.WriteString(choice.Delta.Content)
		reasoning := choice.Delta.Reasoning
		if reasoning == "" {
			reasoning = choice.Delta.Summary
		}
		if reasoning != "" {
			reportProgress(ctx, Progress{Reasoning: reasoning})
		}
		total += len(choice.Delta.Content) + len(reasoning)
		for _, delta := range choice.Delta.Tools {
			if delta.Index < 0 || delta.Index >= 40 {
				return "", nil, usage, ErrInvalidStream
			}
			c := calls[delta.Index]
			if c == nil {
				c = &ToolCall{}
				calls[delta.Index] = c
			}
			c.ID += delta.ID
			c.Name += delta.Function.Name
			c.Arguments += delta.Function.Arguments
			total += len(delta.Function.Arguments)
			reportProgress(ctx, Progress{Tool: c.Name, ArgumentBytes: len(c.Arguments)})
		}
		if total > 2<<20 {
			return "", nil, usage, ErrInvalidStream
		}
	}
	if err := scanner.Err(); err != nil {
		return "", nil, usage, err
	}
	return "", nil, usage, ErrIncompleteStream
}
