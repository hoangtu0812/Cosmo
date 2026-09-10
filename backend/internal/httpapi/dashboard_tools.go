package httpapi

import (
	"cosmo/backend/internal/modelgateway"
	"cosmo/backend/internal/tools"
	"strings"
)

// A dashboard is one HTML artifact. Offering the single-chart renderer for the
// same request lets the model substitute a series of disconnected charts.
// Only inspect the current user request, never attachment text or old replies.
func dashboardToolHistory(history []modelgateway.Message, definitions []modelgateway.ToolDefinition) ([]modelgateway.Message, []modelgateway.ToolDefinition) {
	request := ""
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "user" {
			request = strings.ToLower(history[i].Content)
			break
		}
	}
	if !strings.Contains(request, "dashboard") {
		return history, definitions
	}
	// An explicit switch away from dashboards must keep the chart tool.
	for _, phrase := range []string{"không cần dashboard", "không làm dashboard", "không vẽ dashboard", "don't create a dashboard", "no dashboard"} {
		if strings.Contains(request, phrase) {
			return history, definitions
		}
	}
	hasHTML := false
	for _, definition := range definitions {
		_, action := tools.SplitCallName(definition.Name)
		if action == "render_html" {
			hasHTML = true
		}
	}
	if !hasHTML {
		return history, definitions
	}
	filtered := make([]modelgateway.ToolDefinition, 0, len(definitions))
	for _, definition := range definitions {
		_, action := tools.SplitCallName(definition.Name)
		if action != "draw_chart" {
			filtered = append(filtered, definition)
		}
	}
	guided := make([]modelgateway.Message, 0, len(history)+1)
	guided = append(guided, history...)
	guided = append(guided, modelgateway.Message{Role: "system", Content: "For this dashboard request, deliver one working HTML dashboard using the available render_html tool. It supports inline JavaScript, filters, multiple charts, KPIs and drill-down. You may use data tools first. Do not substitute standalone draw_chart results, a proposed layout, or Power BI instructions for the requested dashboard. Use available complete records and disclose partial data; do not invent missing data. If the user is asking only a question rather than requesting creation, answer that question. If render_html fails, report its actual error; do not claim success."})
	return guided, filtered
}
