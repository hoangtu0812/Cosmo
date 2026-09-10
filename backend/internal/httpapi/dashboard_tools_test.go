package httpapi

import (
	"cosmo/backend/internal/modelgateway"
	"testing"
)

func TestDashboardToolRouting(t *testing.T) {
	definitions := []modelgateway.ToolDefinition{{Name: "html__render_html"}, {Name: "chart__draw_chart"}, {Name: "data__describe_numbers"}}
	for _, tc := range []struct {
		request string
		count   int
	}{
		{"Bạn xem lại có thể vẽ dashboard theo yêu cầu trên không nhé", 2},
		{"Vẽ một biểu đồ doanh thu", 3},
		{"Không cần dashboard, chỉ vẽ chart", 3},
	} {
		history := []modelgateway.Message{{Role: "user", Content: tc.request}}
		_, got := dashboardToolHistory(history, definitions)
		if len(got) != tc.count {
			t.Fatalf("%s: %d tools", tc.request, len(got))
		}
	}
	history := []modelgateway.Message{{Role: "user", Content: "create dashboard"}, {Role: "user", Content: "vẽ chart"}}
	_, got := dashboardToolHistory(history, definitions)
	if len(got) != 3 {
		t.Fatal("old dashboard request affected current turn")
	}
	_, got = dashboardToolHistory(history[:1], definitions[1:])
	if len(got) != 2 {
		t.Fatal("removed chart when HTML unavailable")
	}
}
