package httpapi

import (
	"context"
	"cosmo/backend/internal/knowledge"
	"cosmo/backend/internal/modelgateway"
	"cosmo/backend/internal/runs"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestUsageSummaryScopeAndUnknownTokens(t *testing.T) {
	s, agent, owner, member := agentAccessFixture(t)
	s.runs = runs.NewRepository(s.db)
	ctx := context.Background()
	for _, user := range []User{owner, member} {
		run, _, err := s.runs.Create(ctx, runs.NewRun{WorkspaceID: agent.WorkspaceID, ActorUserID: user.ID, ResourceType: "conversation", ResourceID: "usage"})
		if err != nil {
			t.Fatal(err)
		}
		observe := s.observeChatModel(run.ID)
		observe(modelgateway.CallObservation{Model: "fixture", Phase: "answer", DurationMS: 10, Usage: &modelgateway.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12}})
		observe(modelgateway.CallObservation{Model: "fixture", Phase: "title", DurationMS: 2, Failed: true})
	}
	read := func(user User, audience string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/usage?workspace="+agent.WorkspaceID+"&days=7&audience="+audience, nil).WithContext(context.WithValue(ctx, userContextKey, user))
		s.usageSummary(w, r)
		return w
	}
	for _, audience := range []string{"me", "workspace"} {
		w := read(owner, audience)
		if w.Code != 200 {
			t.Fatalf("summary %d %s", w.Code, w.Body.String())
		}
		var data struct {
			Executions int      `json:"executions"`
			Calls      int      `json:"model_calls"`
			Unknown    int      `json:"unknown_usage_calls"`
			Tokens     *int     `json:"known_total_tokens"`
			Cost       *float64 `json:"cost"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		multiplier := 1
		if audience == "workspace" {
			multiplier = 2
		}
		if data.Executions != multiplier || data.Calls != 2*multiplier || data.Unknown != multiplier || data.Tokens == nil || *data.Tokens != 12*multiplier || data.Cost != nil {
			t.Fatalf("wrong aggregate: %+v", data)
		}
	}
	if read(member, "workspace").Code != 403 {
		t.Fatal("member saw workspace accounting")
	}
}

func TestConfiguredCostDoesNotHideMissingUsageOrRates(t *testing.T) {
	result := map[string]any{"cost": nil, "groups": []any{map[string]any{"model": "fixture", "calls": float64(2), "unknown_usage_calls": float64(1), "prompt_tokens": float64(1000000), "completion_tokens": float64(500000)}}}
	applyConfiguredCost(result, "workspace", `{"workspace":{"fixture":{"input":2,"output":4}}}`)
	if result["cost"] != nil || result["known_cost"] != float64(4) || result["unpriced_calls"] != 1 {
		t.Fatalf("partial cost: %v", result)
	}
	result["groups"].([]any)[0].(map[string]any)["unknown_usage_calls"] = float64(0)
	applyConfiguredCost(result, "workspace", `{"workspace":{"fixture":{"input":2,"output":4}}}`)
	if result["cost"] != float64(4) {
		t.Fatal("known cost missing")
	}
}

func TestRAGAccountingStreamsFailuresAndDeduplicates(t *testing.T) {
	s, agent, owner, member := agentAccessFixture(t)
	ctx := context.WithValue(context.Background(), userContextKey, owner)
	observation := knowledge.Observation{ID: "12345678-1234-1234-1234-" + randomID(9), Model: "embed", Phase: "rag:ingest:embedding", DurationMS: 8, Usage: &knowledge.TokenUsage{PromptTokens: 12, TotalTokens: 12}}
	raw, _ := json.Marshal(observation)
	rag := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ingest" {
			fmt.Fprintf(w, "{\"stage\":\"accounting\",\"observation\":%s}\n{\"stage\":\"done\",\"chunks\":1,\"storage_key\":\"original\"}\n", raw)
			return
		}
		w.WriteHeader(503)
		json.NewEncoder(w).Encode(map[string]any{"observations": []knowledge.Observation{{ID: "12345678-1234-1234-1234-" + randomID(9), Model: "rerank", Phase: "rag:search:rerank", Failed: true}}})
	}))
	defer rag.Close()
	client := knowledge.New(rag.URL, time.Second)
	client.Observer = s.observeRAGModel
	settings := knowledge.ModelSettings{EmbeddingScope: agent.WorkspaceID}
	for i := 0; i < 2; i++ {
		if _, err := client.Ingest(ctx, knowledge.IngestJob{DocumentID: "doc", StorageKey: "original"}, settings, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client.Search(ctx, "query", []string{"kb"}, 1, settings); err == nil {
		t.Fatal("expected search failure")
	}
	for _, user := range []User{owner, member} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/usage?workspace="+agent.WorkspaceID+"&days=7&audience=me", nil).WithContext(context.WithValue(ctx, userContextKey, user))
		s.usageSummary(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var got struct {
			Calls   int  `json:"model_calls"`
			Unknown int  `json:"unknown_usage_calls"`
			Tokens  *int `json:"known_total_tokens"`
		}
		json.Unmarshal(w.Body.Bytes(), &got)
		if user.ID == owner.ID {
			if got.Calls != 2 || got.Unknown != 1 || got.Tokens == nil || *got.Tokens != 12 {
				t.Fatal(got)
			}
		} else if got.Calls != 0 {
			t.Fatal("member saw another actor's calls")
		}
	}
}
