package modelgateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStreamRequiresTerminalAndValidFrames(t *testing.T) {
	delta := "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"
	for _, tc := range []struct {
		name, body string
		want       error
	}{
		{"eof", delta, ErrIncompleteStream},
		{"invalid-json", delta + "data: broken\n\n", ErrInvalidStream},
		{"upstream-error", delta + "data: {\"error\":{\"message\":\"secret\"}}\n\n", ErrInvalidStream},
		{"token-limit", delta + "data: {\"choices\":[{\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n", ErrIncompleteStream},
		{"finished", delta + "data: [DONE]\n\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client := New(server.URL, "", "test", "", time.Second)
			var answer strings.Builder
			err := client.Stream(context.Background(), nil, Options{}, func(s string) error { answer.WriteString(s); return nil })
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}

func TestStreamReturnsAtDoneWithoutWaitingForSocketClose(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("data: [DONE]\n\n"))
		w.(http.Flusher).Flush()
		<-release
	}))
	defer server.Close()
	defer close(release)
	client := New(server.URL, "", "test", "", 200*time.Millisecond)
	if err := client.Stream(context.Background(), nil, Options{}, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
}
