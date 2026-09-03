package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

// A model sends what it feels like sending, so both shapes have to work.
func TestParseNumbersTakesEitherShape(t *testing.T) {
	for _, raw := range []string{"[1, 2.5, 3]", "1, 2.5, 3", "1;2.5;3"} {
		values, err := parseNumbers(raw)
		if err != nil || len(values) != 3 || values[1] != 2.5 {
			t.Fatalf("parseNumbers(%q) = %v, %v", raw, values, err)
		}
	}
	if _, err := parseNumbers("mot, hai"); err == nil {
		t.Error("words parsed as numbers")
	}
}

func TestDescribeNumbersNamesTheExtremes(t *testing.T) {
	body, err := describeNumbers(map[string]any{
		"values": "10, 40, 25",
		"labels": "Tháng 7, Tháng 8, Tháng 9",
	})
	if err != nil {
		t.Fatal(err)
	}
	var summary map[string]any
	if err := json.Unmarshal([]byte(body), &summary); err != nil {
		t.Fatal(err)
	}
	if summary["sum"] != 75.0 || summary["median"] != 25.0 {
		t.Fatalf("unexpected summary: %v", summary)
	}
	if summary["highest_label"] != "Tháng 8" || summary["lowest_label"] != "Tháng 7" {
		t.Fatalf("extremes not named: %v", summary)
	}
}

// The chart is validated here so a client never has to draw nonsense.
func TestDrawChartRefusesWhatCannotBeDrawn(t *testing.T) {
	if _, err := drawChart(map[string]any{"type": "radar", "labels": "a,b", "values": "1,2"}); err == nil {
		t.Error("an unknown type was accepted")
	}
	if _, err := drawChart(map[string]any{"labels": "a,b,c", "values": "1,2"}); err == nil {
		t.Error("labels and values of different lengths were accepted")
	}
	if _, err := drawChart(map[string]any{"type": "pie", "labels": "a,b", "values": "1,-2"}); err == nil {
		t.Error("a negative slice was accepted")
	}
	if _, err := drawChart(map[string]any{
		"type":   "line",
		"labels": "a,b",
		"series": `[{"name":"x","values":[1,2]},{"name":"y","values":[1]}]`,
	}); err == nil {
		t.Error("series of different lengths were accepted")
	}
}

func TestDrawChartReturnsWhatItDrew(t *testing.T) {
	body, err := drawChart(map[string]any{
		"type":    "hbar",
		"title":   "Doanh thu",
		"labels":  `["Q1","Q2"]`,
		"series":  `[{"name":"2025","values":[10,20]},{"name":"2026","values":[15,25]}]`,
		"stacked": "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Chart ChartSpec `json:"chart"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Chart.Type != "hbar" || !payload.Chart.IsStacked {
		t.Fatalf("chart not as asked: %+v", payload.Chart)
	}
	if len(payload.Chart.Series) != 2 || payload.Chart.Series[1].Name != "2026" {
		t.Fatalf("series lost: %+v", payload.Chart.Series)
	}
}

// Stacking a pie means nothing, and quietly accepting the flag would have the
// client drawing something nobody asked for.
func TestPieIgnoresStacking(t *testing.T) {
	body, err := drawChart(map[string]any{"type": "pie", "labels": "a,b", "values": "1,2", "stacked": "true"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, "is_stacked") {
		t.Errorf("a pie came back stacked: %s", body)
	}
}
