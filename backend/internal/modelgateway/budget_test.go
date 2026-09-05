package modelgateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func toolPair(id string) []Message {
	return []Message{{Role: "assistant", ToolCalls: []map[string]any{{"id": id, "type": "function", "function": map[string]any{"name": "lookup", "arguments": "{}"}}}}, {Role: "tool", ToolCallID: id, Content: "result"}}
}

func TestBudgetDropsWholeOldTurnsAndKeepsCurrentToolResults(t *testing.T) {
	messages := []Message{{Role: "system", Content: "instructions"}, {Role: "user", Content: strings.Repeat("old", 400)}}
	messages = append(messages, toolPair("old-call")...)
	messages = append(messages, Message{Role: "assistant", Content: "previous answer"}, Message{Role: "user", Content: "Câu hỏi hiện tại 🧪"})
	messages = append(messages, toolPair("new-call")...)
	before, _ := json.Marshal(messages)
	kept, report, err := budgetMessages(messages, nil, Options{MaxInputBytes: 650})
	if err != nil || report.DroppedMessages != 4 || report.InputBytes > report.LimitBytes {
		t.Fatalf("budget: %+v %v", report, err)
	}
	want := append([]Message{messages[0]}, messages[5:]...)
	if !reflect.DeepEqual(kept, want) {
		t.Fatalf("changed mandatory context: %+v", kept)
	}
	after, _ := json.Marshal(messages)
	if string(before) != string(after) {
		t.Fatal("mutated source history")
	}
}

func TestBudgetProtectsSystemCurrentQuestionAndToolSchemas(t *testing.T) {
	for _, messages := range [][]Message{
		{{Role: "system", Content: strings.Repeat("s", 1000)}, {Role: "user", Content: "current"}},
		{{Role: "system", Content: "system"}, {Role: "user", Content: strings.Repeat("最新", 300)}},
	} {
		if _, _, err := budgetMessages(messages, nil, Options{MaxInputBytes: 400}); !errors.Is(err, ErrContextBudget) {
			t.Fatalf("discarded protected content: %v", err)
		}
	}
	if _, _, err := budgetMessages([]Message{{Role: "user", Content: "q"}}, map[string]any{"schema": strings.Repeat("x", 1000)}, Options{MaxInputBytes: 400}); !errors.Is(err, ErrContextBudget) {
		t.Fatalf("tool definitions unbudgeted: %v", err)
	}
}

func TestBudgetRejectsIncompleteAndOrphanToolResults(t *testing.T) {
	pair := toolPair("call")
	for _, messages := range [][]Message{pair[:1], pair[1:], {pair[0], {Role: "user", Content: "interrupted"}, pair[1]}, {pair[0], pair[1], pair[1]}} {
		if _, _, err := budgetMessages(messages, nil, Options{}); !errors.Is(err, ErrToolHistory) {
			t.Fatalf("malformed tool chain accepted: %v", err)
		}
	}
}

func TestBudgetReservesOutputAndAllowsParallelToolResults(t *testing.T) {
	first, second := toolPair("a"), toolPair("b")
	first[0].ToolCalls = append(first[0].ToolCalls, second[0].ToolCalls...)
	messages := []Message{{Role: "user", Content: "q"}, first[0], second[1], first[1]}
	_, report, err := budgetMessages(messages, nil, Options{ContextWindow: 8192})
	if err != nil || report.OutputReserve != 2048 || report.LimitBytes != 5120 {
		t.Fatalf("reserve/parallel calls: %+v %v", report, err)
	}
}

func TestBudgetAppliesBeforeStreamingAndDecisionRequests(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var payload struct {
			Messages []Message `json:"messages"`
			Stream   bool      `json:"stream"`
		}
		json.NewDecoder(r.Body).Decode(&payload)
		if len(payload.Messages) != 2 || payload.Messages[0].Content != "system" || payload.Messages[1].Content != "current" {
			t.Errorf("bad outbound messages: %+v", payload.Messages)
		}
		if payload.Stream {
			fmtBody := `data: {"choices":[{"delta":{"content":"answer"}}]}` + "\n\ndata: [DONE]\n\n"
			io.WriteString(w, fmtBody)
		} else {
			io.WriteString(w, `{"choices":[{"message":{"content":"answer"}}]}`)
		}
	}))
	defer server.Close()
	client := New(server.URL, "", "test", "system", time.Second)
	history := []Message{{Role: "user", Content: strings.Repeat("x", 1000)}, {Role: "assistant", Content: "old answer"}, {Role: "user", Content: "current"}}
	var observations []CallObservation
	ctx := WithObserver(context.Background(), func(call CallObservation) { observations = append(observations, call) })
	options := Options{MaxInputBytes: 400}
	if err := client.Stream(ctx, history, options, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.Decide(ctx, history, nil, options); err != nil {
		t.Fatal(err)
	}
	oversized := []Message{{Role: "user", Content: strings.Repeat("x", 1000)}}
	if err := client.Stream(ctx, oversized, options, func(string) error { return nil }); !errors.Is(err, ErrContextBudget) {
		t.Fatal(err)
	}
	if _, _, err := client.Decide(ctx, oversized, nil, options); !errors.Is(err, ErrContextBudget) {
		t.Fatal(err)
	}
	if requests.Load() != 2 || len(observations) != 4 || observations[0].Budget.DroppedMessages != 2 || !observations[3].Failed {
		t.Fatalf("requests=%d observations=%+v", requests.Load(), observations)
	}
}
