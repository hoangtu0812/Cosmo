package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"cosmo/backend/internal/modelgateway"
)

func TestTurnPlanValidatesQueryAndFallsBackWithoutDroppingKnowledge(t *testing.T) {
	question := "Quy định đó áp dụng khi nào?"
	for _, answer := range []string{
		`{}`, `{"search_query":"invented"}`, `{"needs_knowledge":"false"}`,
		`{"needs_knowledge":true,"search_query":""}`,
		`{"needs_knowledge":true,"search_query":"x","kb_ids":["outside"]}`,
		`{"needs_knowledge":false} {"needs_knowledge":true}`,
		"KHONG nhưng cần kiểm tra tài liệu", "invalid output with private details",
		fmt.Sprintf(`{"needs_knowledge":true,"search_query":%q}`, strings.Repeat("x", 2001)),
	} {
		plan := parseTurnPlan(answer, question, true)
		if !plan.NeedsKnowledge || plan.SearchQuery != question || plan.QueryRewritten {
			t.Fatalf("unsafe fallback for %q: %+v", answer, plan)
		}
		if strings.Contains(plan.Reason, "private details") {
			t.Fatal("raw model output leaked into status")
		}
	}
	answer := `{"needs_knowledge":true,"search_query":"Quy định QT-17 áp dụng khi nào?"}`
	plan := parseTurnPlan(answer, question, true)
	if !plan.QueryRewritten || plan.SearchQuery != "Quy định QT-17 áp dụng khi nào?" {
		t.Fatalf("lost follow-up query: %+v", plan)
	}
	plan = parseTurnPlan(answer, question, false)
	if plan.SearchQuery != question || plan.QueryRewritten {
		t.Fatalf("rewrote a first turn without context: %+v", plan)
	}
	if parseTurnPlan(`{"needs_knowledge":false,"search_query":""}`, "Xin chào", true).NeedsKnowledge {
		t.Fatal("valid no-search plan rejected")
	}
}

func TestPlanningHistoryBoundsContextAndExcludesCurrentQuestionAndTools(t *testing.T) {
	history := []modelgateway.Message{{Role: "system", Content: "agent persona"}}
	for i := 0; i < 20; i++ {
		history = append(history, modelgateway.Message{Role: "user", Content: fmt.Sprint(i) + strings.Repeat("ệ", 1500)})
	}
	history = append(history, modelgateway.Message{Role: "assistant", Content: "Chủ thể mới nhất: QT-17"}, modelgateway.Message{Role: "tool", Content: "tool private output"}, modelgateway.Message{Role: "user", Content: "quy định đó?"})
	recent := planningHistory(history, "quy định đó?")
	total := 0
	for _, m := range recent {
		total += len([]rune(m.Content))
		if m.Role == "system" || m.Role == "tool" || m.Content == "quy định đó?" || !utf8.ValidString(m.Content) {
			t.Fatalf("invalid planner history: %+v", m)
		}
	}
	if len(recent) > 6 || total > 6000 || recent[len(recent)-1].Content != "Chủ thể mới nhất: QT-17" {
		t.Fatalf("history budget/order violated: %+v", recent)
	}
	if history[len(history)-1].Content != "quy định đó?" {
		t.Fatal("mutated answer history")
	}
}

func TestTurnPlanGatewayFailureKeepsOriginalQueryAndHidesUpstreamDetails(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "secret-provider-details", 503) }))
	defer gateway.Close()
	models := modelgateway.New(gateway.URL, "", "model", "persona", time.Second)
	s := &Server{}
	plan := s.planTurn(context.Background(), models, modelgateway.Options{}, "Quy định QT-17?", nil, []string{"Policies"}, nil)
	if !plan.NeedsKnowledge || plan.SearchQuery != "Quy định QT-17?" || strings.Contains(plan.Reason, "secret-provider-details") {
		t.Fatalf("unsafe failure: %+v", plan)
	}
}
