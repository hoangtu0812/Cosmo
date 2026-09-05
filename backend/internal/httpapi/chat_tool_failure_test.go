package httpapi

import (
	"context"
	"cosmo/backend/internal/modelgateway"
	"cosmo/backend/internal/runs"
	"cosmo/backend/internal/tools"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestChatToolFailuresAndMissingStepNeverSucceed(t *testing.T) {
	for _, missingStep := range []bool{false, true} {
		t.Run(fmt.Sprint(missingStep), func(t *testing.T) {
			s, agent, owner, _ := agentAccessFixture(t)
			ctx := context.Background()
			s.runs = runs.NewRepository(s.db)
			var invocations atomic.Int32
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				invocations.Add(1)
				w.WriteHeader(503)
				w.Write([]byte("temporarily unavailable"))
			}))
			defer endpoint.Close()
			parsed, _ := url.Parse(endpoint.URL)
			s.tools = tools.NewRepository(s.db, slog.Default(), nil, tools.EgressPolicy{AllowedHosts: []string{parsed.Hostname()}}, tools.SearchBackend{})
			tool := tools.Tool{ID: "fixture", Name: "fixture", Kind: tools.KindHTTP, BaseURL: endpoint.URL, AuthType: tools.AuthNone}
			actions := map[string][]tools.Action{tool.ID: {{Name: "probe", Method: "GET", Path: "/"}}}
			list := []tools.Tool{tool}
			definitions := tools.DescribeSet(list, actions)
			var decisions atomic.Int32
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				message := map[string]any{"content": ""}
				if decisions.Add(1) == 1 {
					message["tool_calls"] = []any{map[string]any{"id": "call_1", "function": map[string]any{"name": definitions[0].Name, "arguments": "{}"}}}
				}
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message}}})
			}))
			defer gateway.Close()
			run, _, err := s.runs.Create(ctx, runs.NewRun{WorkspaceID: agent.WorkspaceID, ActorUserID: owner.ID, ResourceType: "conversation", ResourceID: "test"})
			if err != nil {
				t.Fatal(err)
			}
			runID := run.ID
			if missingStep {
				runID = "missing-run"
			}
			recorder := httptest.NewRecorder()
			var answer strings.Builder
			_, calls := s.runToolRounds(ctx, recorder, recorder, toolSet{tools: list, actions: actions, definitions: definitions}, nil, modelgateway.Options{}, modelgateway.New(gateway.URL, "", "test", "", time.Second), runID, &answer)
			if len(calls) != 1 || calls[0].Status != "error" {
				t.Fatalf("tool error shown as success: %+v", calls)
			}
			if missingStep {
				if invocations.Load() != 0 {
					t.Fatal("called tool without recorded step")
				}
			} else {
				var status string
				s.db.QueryRow(ctx, `SELECT status FROM run_steps WHERE run_id=$1`, runID).Scan(&status)
				if status != "failed" {
					t.Fatalf("failed tool step status: %s", status)
				}
			}
		})
	}
}
