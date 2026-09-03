package tools

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Two built-ins for looking at numbers.
//
// A model reading a column of figures is guessing: it can tell you a total is
// "about" something, and it will draw you a chart in ASCII if you let it. So
// the arithmetic happens here, where it is exact, and the picture is described
// here, where it can be validated - and drawn by the client that has pixels.
//
// Neither reaches anything: the numbers arrive in the call. That keeps them
// in the same class as the calculator - no endpoint, no credential, no egress.

// maxSeriesPoints caps a chart. Past this a picture stops being read and
// starts being squinted at, and the model is usually pasting a whole table
// where it meant to summarise one.
const maxSeriesPoints = 60

// parseNumbers reads either a JSON array or a plain separated list, because a
// model asked for "values" will produce both and neither is wrong.
func parseNumbers(raw string) ([]float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("thiếu dãy số")
	}
	if strings.HasPrefix(raw, "[") {
		var values []float64
		if err := json.Unmarshal([]byte(raw), &values); err == nil {
			return values, nil
		}
	}
	fields := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || r == '\n' || r == '\t' })
	values := make([]float64, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(strings.ReplaceAll(field, " ", ""))
		if field == "" {
			continue
		}
		value, err := strconv.ParseFloat(field, 64)
		if err != nil {
			return nil, fmt.Errorf("không đọc được số %q", field)
		}
		values = append(values, value)
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("thiếu dãy số")
	}
	return values, nil
}

// parseLabels is the same tolerance for the text axis.
func parseLabels(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.HasPrefix(raw, "[") {
		var labels []string
		if err := json.Unmarshal([]byte(raw), &labels); err == nil {
			return labels
		}
	}
	parts := strings.Split(raw, ",")
	labels := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			labels = append(labels, part)
		}
	}
	return labels
}

func round(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return math.Round(value*1e6) / 1e6
}

// describeNumbers is the arithmetic a model should not be doing in its head.
func describeNumbers(arguments map[string]any) (string, error) {
	raw, _ := arguments["values"].(string)
	values, err := parseNumbers(raw)
	if err != nil {
		return "", err
	}
	labels := parseLabels(fmt.Sprint(arguments["labels"]))

	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	total := 0.0
	for _, value := range values {
		total += value
	}
	mean := total / float64(len(values))
	median := sorted[len(sorted)/2]
	if len(sorted)%2 == 0 {
		median = (sorted[len(sorted)/2-1] + sorted[len(sorted)/2]) / 2
	}
	variance := 0.0
	for _, value := range values {
		variance += (value - mean) * (value - mean)
	}
	variance /= float64(len(values))

	summary := map[string]any{
		"count":  len(values),
		"sum":    round(total),
		"mean":   round(mean),
		"median": round(median),
		"min":    round(sorted[0]),
		"max":    round(sorted[len(sorted)-1]),
		"range":  round(sorted[len(sorted)-1] - sorted[0]),
		"stddev": round(math.Sqrt(variance)),
	}
	// Naming the largest and smallest only where the caller said what the
	// numbers are: "max: 12.4" is a fact, "highest: Tháng 9" is an answer.
	if len(labels) == len(values) {
		highest, lowest := 0, 0
		for index, value := range values {
			if value > values[highest] {
				highest = index
			}
			if value < values[lowest] {
				lowest = index
			}
		}
		summary["highest_label"] = labels[highest]
		summary["lowest_label"] = labels[lowest]
	}
	payload, err := json.Marshal(summary)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

// ChartSpec is what a chart call returns: enough for a client to draw it, and
// readable enough that the model knows what it asked for.
//
// Returned rather than rendered because this process has no pixels and the one
// that does is already showing the tool call. The model gets its own words
// back, which is what stops it describing a chart it did not draw.
type ChartSpec struct {
	Type   string        `json:"type"`
	Title  string        `json:"title,omitempty"`
	Labels []string      `json:"labels"`
	Series []ChartSeries `json:"series"`
	// Several series piled rather than set side by side. Only meaningful for
	// the types that can hold a total; ignored elsewhere.
	IsStacked bool `json:"is_stacked,omitempty"`
}

type ChartSeries struct {
	Name   string    `json:"name,omitempty"`
	Values []float64 `json:"values"`
}

// chartTypes is the whole vocabulary, and it is deliberately small: each one
// answers a question people actually ask of a table. A shape nobody asks for
// is a shape the model will pick by accident.
var chartTypes = map[string]bool{
	"bar":   true, // a value per label
	"hbar":  true, // the same, where the labels are long
	"line":  true, // a value over an ordered axis
	"area":  true, // the same, where the total matters as much as the shape
	"pie":   true, // parts of one whole
	"donut": true, // the same, with room for the total in the middle
}

// stackable are the types where several series can be piled rather than set
// side by side. A pie is already a share of a whole; stacking one means
// nothing.
var stackable = map[string]bool{"bar": true, "hbar": true, "area": true}

// parseSeries reads either one list of values or several named ones.
//
// Two shapes because both arrive: a model asked for one column sends `values`,
// and a model comparing three departments sends `series`. Refusing either
// would mean the tool works only when the question is phrased the way it was
// written for.
func parseSeries(arguments map[string]any) ([]ChartSeries, error) {
	if raw := strings.TrimSpace(cleanString(arguments["series"])); raw != "" {
		var incoming []struct {
			Name   string `json:"name"`
			Values any    `json:"values"`
		}
		if err := json.Unmarshal([]byte(raw), &incoming); err != nil {
			return nil, fmt.Errorf("series phải là JSON dạng [{\"name\":…,\"values\":[…]}]")
		}
		series := make([]ChartSeries, 0, len(incoming))
		for _, item := range incoming {
			encoded, err := json.Marshal(item.Values)
			if err != nil {
				return nil, fmt.Errorf("không đọc được values của %q", item.Name)
			}
			values, err := parseNumbers(string(encoded))
			if err != nil {
				return nil, fmt.Errorf("%s: %w", item.Name, err)
			}
			series = append(series, ChartSeries{Name: strings.TrimSpace(item.Name), Values: values})
		}
		if len(series) == 0 {
			return nil, fmt.Errorf("series rỗng")
		}
		return series, nil
	}

	values, err := parseNumbers(cleanString(arguments["values"]))
	if err != nil {
		return nil, err
	}
	return []ChartSeries{{Name: strings.TrimSpace(cleanString(arguments["series_name"])), Values: values}}, nil
}

func drawChart(arguments map[string]any) (string, error) {
	kind := strings.ToLower(strings.TrimSpace(cleanString(arguments["type"])))
	if kind == "" {
		kind = "bar"
	}
	if !chartTypes[kind] {
		return "", fmt.Errorf("kiểu biểu đồ %q không hỗ trợ; dùng bar, hbar, line, area, pie hoặc donut", kind)
	}

	series, err := parseSeries(arguments)
	if err != nil {
		return "", err
	}
	width := len(series[0].Values)
	for _, item := range series {
		if len(item.Values) != width {
			return "", fmt.Errorf("các series phải cùng số điểm dữ liệu")
		}
	}
	if width > maxSeriesPoints {
		return "", fmt.Errorf("tối đa %d điểm dữ liệu cho một biểu đồ", maxSeriesPoints)
	}

	labels := parseLabels(cleanString(arguments["labels"]))
	if len(labels) == 0 {
		// A chart with no names on its axis is still a chart; numbering the
		// points beats refusing to draw it.
		labels = make([]string, width)
		for index := range labels {
			labels[index] = strconv.Itoa(index + 1)
		}
	}
	if len(labels) != width {
		return "", fmt.Errorf("có %d nhãn nhưng %d giá trị", len(labels), width)
	}

	if kind == "pie" || kind == "donut" {
		if len(series) > 1 {
			return "", fmt.Errorf("biểu đồ tròn chỉ nhận một series")
		}
		for _, value := range series[0].Values {
			if value < 0 {
				return "", fmt.Errorf("biểu đồ tròn không nhận giá trị âm")
			}
		}
	}

	stacked := readBool(arguments["stacked"]) && stackable[kind]
	for index, item := range series {
		rounded := make([]float64, len(item.Values))
		for position, value := range item.Values {
			rounded[position] = round(value)
		}
		series[index].Values = rounded
	}

	spec := ChartSpec{
		Type:      kind,
		Title:     strings.TrimSpace(cleanString(arguments["title"])),
		Labels:    labels,
		Series:    series,
		IsStacked: stacked,
	}
	payload, err := json.Marshal(map[string]any{"chart": spec})
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

// readBool accepts what a model actually sends for a flag: a real boolean, or
// the word.
func readBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		return err == nil && parsed
	}
	return false
}

// cleanString reads an optional argument without turning a missing one into
// the string "<nil>", which is what fmt.Sprint does with a nil any.
func cleanString(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}
