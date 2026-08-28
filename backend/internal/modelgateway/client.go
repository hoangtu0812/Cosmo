package modelgateway

import (
	"bufio"
	"bytes"
	"context"
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

// ResolveModel reports the model a request will actually use.
func (c *Client) ResolveModel(options Options) string {
	if options.Model != "" {
		return options.Model
	}
	return c.model
}

func (c *Client) Stream(ctx context.Context, history []Message, options Options, onDelta func(string) error) error {
	if !c.HasGateway() || c.ResolveModel(options) == "" {
		return ErrNotConfigured
	}
	messages := make([]Message, 0, len(history)+1)
	if c.systemPrompt != "" {
		messages = append(messages, Message{Role: "system", Content: c.systemPrompt})
	}
	messages = append(messages, history...)
	body := map[string]any{
		"model":       c.ResolveModel(options),
		"messages":    messages,
		"stream":      true,
		"temperature": 0.2,
	}
	if options.ReasoningEffort != "" {
		// The OpenAI-compatible spelling; LiteLLM maps it per provider.
		body["reasoning_effort"] = options.ReasoningEffort
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode model request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create model request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call model gateway: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("model gateway returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

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
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
			if err := onDelta(chunk.Choices[0].Delta.Content); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read model stream: %w", err)
	}
	return nil
}
