package modelgateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// capture starts a stub gateway that records the request body it receives and
// replies with a single SSE delta.
func capture(t *testing.T) (*httptest.Server, *map[string]any) {
	t.Helper()
	received := map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &received); err != nil {
			t.Errorf("gateway got invalid JSON: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	return server, &received
}

func TestStreamOmitsReasoningEffortWhenUnset(t *testing.T) {
	server, received := capture(t)
	client := New(server.URL, "", "workspace-default", "", 5*time.Second)

	if err := client.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, Options{}, func(string) error { return nil }); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if _, present := (*received)["reasoning_effort"]; present {
		t.Fatal("reasoning_effort must be absent when no level is chosen")
	}
	if got := (*received)["model"]; got != "workspace-default" {
		t.Fatalf("model: got %v want workspace-default", got)
	}
}

func TestStreamAppliesOverrides(t *testing.T) {
	server, received := capture(t)
	client := New(server.URL, "", "workspace-default", "", 5*time.Second)

	options := Options{Model: "picked-model", ReasoningEffort: "high"}
	if err := client.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, options, func(string) error { return nil }); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got := (*received)["model"]; got != "picked-model" {
		t.Fatalf("model: got %v want picked-model", got)
	}
	if got := (*received)["reasoning_effort"]; got != "high" {
		t.Fatalf("reasoning_effort: got %v want high", got)
	}
}

func TestResolveModelFallsBackToWorkspaceDefault(t *testing.T) {
	client := New("https://example.com/v1", "", "workspace-default", "", time.Second)
	if got := client.ResolveModel(Options{}); got != "workspace-default" {
		t.Fatalf("empty override: got %q", got)
	}
	if got := client.ResolveModel(Options{Model: "picked"}); got != "picked" {
		t.Fatalf("override: got %q", got)
	}
}

func TestStreamPrependsSystemPrompt(t *testing.T) {
	server, received := capture(t)
	client := New(server.URL, "", "m", "be helpful", 5*time.Second)

	if err := client.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, Options{}, func(string) error { return nil }); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	messages, ok := (*received)["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("messages: got %v", (*received)["messages"])
	}
	first, _ := messages[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "be helpful" {
		t.Fatalf("system prompt not prepended: %v", first)
	}
}
