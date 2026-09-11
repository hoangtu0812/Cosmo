package httpapi

import (
	"context"
	"cosmo/backend/internal/modelgateway"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func httpHandlerForProgress() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: " + `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"html__render_html","arguments":"{\"html\":"}}]}}]}` + "\n\n"))
		w.Write([]byte("data: " + `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"<h1>hello</h1>\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n\n"))
	})
}

func TestToolProgressIncludesLatestArguments(t *testing.T) {
	gateway := httptest.NewServer(httpHandlerForProgress())
	defer gateway.Close()
	recorder := httptest.NewRecorder()
	ctx := modelProgressContext(context.Background(), recorder, recorder)
	_, _, err := modelgateway.New(gateway.URL, "", "test", "", 0).Decide(ctx, nil, nil, modelgateway.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var last map[string]string
	for _, line := range strings.Split(recorder.Body.String(), "\n") {
		if strings.HasPrefix(line, "data: ") {
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &last); err != nil {
				t.Fatal(err)
			}
		}
	}
	if last["detail"] != `{"html":"<h1>hello</h1>"}` {
		t.Fatalf("final arguments missing: %v", last)
	}
}
