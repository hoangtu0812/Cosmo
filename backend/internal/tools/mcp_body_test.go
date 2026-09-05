package tools

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type trackedMCPBody struct {
	io.Reader
	closed bool
}

func (body *trackedMCPBody) Close() error { body.closed = true; return nil }

func TestMCPResponseBodyBound(t *testing.T) {
	for _, size := range []int{7, 8, 9} {
		underlying := &trackedMCPBody{Reader: strings.NewReader(strings.Repeat("x", size))}
		body := &mcpResponseBody{ReadCloser: underlying, remaining: 8}
		data, err := io.ReadAll(body)
		if size <= 8 {
			if err != nil || len(data) != size {
				t.Fatalf("size %d: %d bytes, %v", size, len(data), err)
			}
		} else if !errors.Is(err, ErrMCPResponseTooLarge) || !underlying.closed || len(data) > 8 {
			t.Fatalf("oversized response accepted: %d bytes, closed=%v, %v", len(data), underlying.closed, err)
		}
	}
}

func TestMCPDiscoveryRejectsOversizedJSONAndSSE(t *testing.T) {
	for _, sse := range []bool{false, true} {
		fixture := newMCPFixture(t, sse)
		fixture.customPage = func(string) map[string]any {
			return map[string]any{"tools": []any{map[string]any{
				"name": "huge", "description": strings.Repeat("x", int(maxMCPResponseBytes)+1),
				"inputSchema": map[string]any{"type": "object"},
			}}}
		}
		repo := repositoryFor(t, fixture.server)
		actions, err := repo.DiscoverMCP(context.Background(), Tool{BaseURL: fixture.server.URL, AuthType: AuthNone})
		if err == nil || len(actions) != 0 {
			t.Fatalf("sse=%v: accepted oversized discovery: actions=%d, %v", sse, len(actions), err)
		}
	}
}
