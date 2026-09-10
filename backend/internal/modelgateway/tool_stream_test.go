package modelgateway

import (
	"context"
	"strings"
	"testing"
)

func TestToolStreamFragments(t *testing.T) {
	raw := `data: {"choices":[{"delta":{"reasoning_content":"Checking data"}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call1","function":{"name":"html__render_html","arguments":"{\"html\":"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"hello\"}"}}]},"finish_reason":"tool_calls"}]}

data: [DONE]
`
	var progress []Progress
	ctx := WithProgress(context.Background(), func(p Progress) { progress = append(progress, p) })
	_, calls, _, err := readToolStream(ctx, strings.NewReader(raw))
	if err != nil || len(calls) != 1 || calls[0].Arguments != `{"html":"hello"}` {
		t.Fatalf("%+v %v", calls, err)
	}
	if progress[0].Reasoning != "Checking data" || !progress[len(progress)-1].Done {
		t.Fatal("missing reasoning or final flush")
	}
	_, calls, _, err = readToolStream(ctx, strings.NewReader(strings.Split(raw, "data: [DONE]")[0]))
	if err == nil || len(calls) != 0 {
		t.Fatal("executed incomplete stream")
	}
}
