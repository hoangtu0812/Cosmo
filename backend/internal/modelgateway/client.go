package modelgateway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var ErrNotConfigured = errors.New("model gateway is not configured")

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// Set on a tool result so the model can match it to the call it asked for.
	// Omitted everywhere else, because a gateway that does not know the field
	// should not be sent it.
	ToolCallID string `json:"tool_call_id,omitempty"`
	// Carried back verbatim on the assistant turn that requested the calls;
	// most gateways require the request to be echoed before its results.
	ToolCalls []map[string]any `json:"tool_calls,omitempty"`
}

type Client struct {
	baseURL      string
	apiKey       string
	model        string
	systemPrompt string
	httpClient   *http.Client
}

func New(baseURL, apiKey, model, systemPrompt string, timeout time.Duration) *Client {
	return &Client{
		baseURL:      strings.TrimRight(baseURL, "/"),
		apiKey:       apiKey,
		model:        model,
		systemPrompt: systemPrompt,
		httpClient:   &http.Client{Timeout: timeout},
	}
}

func (c *Client) Configured() bool {
	return c.HasGateway() && c.model != ""
}

// HasGateway reports whether the client has an endpoint to call. A workspace
// may intentionally omit the default model, because a member can choose one
// in the conversation composer.
func (c *Client) HasGateway() bool {
	return c.baseURL != ""
}

func (c *Client) Model() string {
	return c.model
}

// Options tune a single request. Both fields are optional: an empty Model uses
// the workspace default, and an empty ReasoningEffort omits the parameter
// entirely rather than guessing a level, because models that do not reason
// reject the field on some providers.
type Options struct {
	Model           string
	ReasoningEffort string
}

// Usage is what a turn cost, as the gateway counted it.
//
// Reported rather than estimated: counting tokens here would mean carrying a
// tokenizer per model and being quietly wrong whenever one changed. Zero means
// the gateway said nothing, which is a fact worth keeping distinct from "the
// turn was free".
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	// The model's own limit, where the gateway knows it. Without it a count is
	// a number with nothing to compare against.
	ContextWindow int `json:"context_window,omitempty"`
	// What each part of the prompt came to, in characters. Not tokens: the
	// gateway counts those and only for the whole prompt. Shares of a known
	// total say more than a total on its own.
	Parts map[string]int `json:"parts,omitempty"`
}

// ResolveModel reports the model a request will actually use.
func (c *Client) ResolveModel(options Options) string {
	if options.Model != "" {
		return options.Model
	}
	return c.model
}

// ExecutionFingerprint detects provider/model/instruction changes while a
// request waits in a durable queue. Credentials may rotate without changing
// the execution destination; no credential is included in this fingerprint.
func (c *Client) ExecutionFingerprint(options Options) string {
	payload, _ := json.Marshal([]string{c.baseURL, c.ResolveModel(options), c.systemPrompt})
	hash := sha256.Sum256(payload)
	return hex.EncodeToString(hash[:])
}

// Complete runs one request and returns the whole reply rather than streaming
// it. The client's system prompt is deliberately left out: its callers are
// utility passes - remembering, suggesting - that must answer in a fixed shape,
// and an agent persona would steer them away from it.
func (c *Client) Complete(ctx context.Context, messages []Message, options Options) (string, error) {
	plain := *c
	plain.systemPrompt = ""
	var reply strings.Builder
	err := plain.Stream(ctx, messages, options, func(delta string) error {
		reply.WriteString(delta)
		return nil
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(reply.String()), nil
}

// Stream keeps the old shape for callers that do not care what a turn cost.
func (c *Client) Stream(ctx context.Context, history []Message, options Options, onDelta func(string) error) error {
	_, err := c.StreamWithUsage(ctx, history, options, onDelta)
	return err
}

// StreamWithUsage is the same call, and hands back what the gateway counted.
func (c *Client) StreamWithUsage(ctx context.Context, history []Message, options Options, onDelta func(string) error) (Usage, error) {
	if !c.HasGateway() || c.ResolveModel(options) == "" {
		return Usage{}, ErrNotConfigured
	}
	messages := make([]Message, 0, len(history)+1)
	if c.systemPrompt != "" {
		messages = append(messages, Message{Role: "system", Content: c.systemPrompt})
	}
	messages = append(messages, history...)
	body := map[string]any{
		"model":    c.ResolveModel(options),
		"messages": messages,
		"stream":   true,
		// The final chunk carries the counts. Without this the stream ends
		// with no usage at all, which is why a turn's cost was invisible.
		"stream_options": map[string]any{"include_usage": true},
		"temperature":    0.2,
	}
	if options.ReasoningEffort != "" {
		// The OpenAI-compatible spelling; LiteLLM maps it per provider.
		body["reasoning_effort"] = options.ReasoningEffort
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return Usage{}, fmt.Errorf("encode model request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return Usage{}, fmt.Errorf("create model request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Usage{}, fmt.Errorf("call model gateway: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Usage{}, fmt.Errorf("model gateway returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var usage Usage

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			// The usage chunk arrives last and carries no choices at all.
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				TotalTokens      int `json:"total_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Usage != nil {
			usage.PromptTokens = chunk.Usage.PromptTokens
			usage.CompletionTokens = chunk.Usage.CompletionTokens
			usage.TotalTokens = chunk.Usage.TotalTokens
		}
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
			if err := onDelta(chunk.Choices[0].Delta.Content); err != nil {
				return usage, err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return usage, fmt.Errorf("read model stream: %w", err)
	}
	return usage, nil
}
