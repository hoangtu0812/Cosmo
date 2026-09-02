package modelgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Tool calling is a separate, non-streaming request rather than a variation of
// Stream. A stream exists to put words on the screen as they arrive; a tool
// round produces no words at all, only a decision about what to call. Keeping
// them apart means the streaming path stays as simple as it was.
type ToolDefinition struct {
	Name        string
	Description string
	// JSON Schema for the arguments. Built by whoever owns the tool, because
	// what an argument means is not something this package can know.
	Parameters map[string]any
}

// ToolCall is one invocation the model asked for.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// Decide runs one request offering the given tools. It returns whatever the
// model said and whatever it wants called; a reply with calls in it is the
// model asking to be given more before it answers.
func (c *Client) Decide(ctx context.Context, history []Message, definitions []ToolDefinition, options Options) (string, []ToolCall, error) {
	if !c.HasGateway() || c.ResolveModel(options) == "" {
		return "", nil, ErrNotConfigured
	}

	messages := make([]Message, 0, len(history)+1)
	if c.systemPrompt != "" {
		messages = append(messages, Message{Role: "system", Content: c.systemPrompt})
	}
	messages = append(messages, history...)

	body := map[string]any{
		"model":       c.ResolveModel(options),
		"messages":    messages,
		"temperature": 0.2,
	}
	if len(definitions) > 0 {
		encoded := make([]map[string]any, 0, len(definitions))
		for _, definition := range definitions {
			encoded = append(encoded, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        definition.Name,
					"description": definition.Description,
					"parameters":  definition.Parameters,
				},
			})
		}
		body["tools"] = encoded
		body["tool_choice"] = "auto"
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return "", nil, fmt.Errorf("encode model request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", nil, fmt.Errorf("create model request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("call model gateway: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", nil, fmt.Errorf("model gateway returned %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var decoded struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return "", nil, fmt.Errorf("decode model reply: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return "", nil, nil
	}

	calls := make([]ToolCall, 0, len(decoded.Choices[0].Message.ToolCalls))
	for _, raw := range decoded.Choices[0].Message.ToolCalls {
		calls = append(calls, ToolCall{ID: raw.ID, Name: raw.Function.Name, Arguments: raw.Function.Arguments})
	}
	return strings.TrimSpace(decoded.Choices[0].Message.Content), calls, nil
}
