package httpapi

import (
	"context"
	"cosmo/backend/internal/modelgateway"
	"cosmo/backend/internal/runs"
	"encoding/json"
	"net/http/httptest"
	"testing"
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
