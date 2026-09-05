package modelgateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestModelAccountingDistinguishesMissingAndZeroUsage(t *testing.T) {
	for _, tc := range []struct {
		body          string
		known, failed bool
	}{
		{`{"choices":[{"message":{"content":"answer"}}]}`, false, false},
		{`{"choices":[{"message":{"content":"answer"}}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`, true, false},
		{`malformed`, false, true},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(tc.body)) }))
		var observations []CallObservation
		ctx := WithPhase(WithObserver(context.Background(), func(call CallObservation) { observations = append(observations, call) }), "tool_decision")
		client := New(server.URL, "", "model", "", time.Second)
		client.Decide(ctx, nil, nil, Options{})
		server.Close()
		if len(observations) != 1 {
			t.Fatal("model call not accounted exactly once")
		}
		got := observations[0]
		if (got.Usage != nil) != tc.known || got.Failed != tc.failed || got.Phase != "tool_decision" || got.Model != "model" {
			t.Fatalf("bad accounting: %+v", got)
		}
	}
}
