package tools

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestEvaluateArithmetic(t *testing.T) {
	// Precedence and association are the whole point: a model asked to do this
	// in its head is exactly what the tool exists to replace.
	cases := map[string]float64{
		"2 + 3 * 4":           14,
		"(2 + 3) * 4":         20,
		"10 - 4 - 3":          3,
		"100 / 4 / 5":         5,
		"-5 + 2":              -3,
		"2 * -3":              -6,
		"7 % 3":               1,
		"1.5 * 4":             6,
		"((1 + 2) * (3 + 4))": 21,
		"  42  ":              42,
		"1234567 * 7654321":   9449772114007,
	}
	for expression, want := range cases {
		got, err := evaluate(expression)
		if err != nil {
			t.Fatalf("evaluate(%q) failed: %v", expression, err)
		}
		if math.Abs(got-want) > 1e-9 {
			t.Fatalf("evaluate(%q) = %v, want %v", expression, got, want)
		}
	}
}

func TestEvaluateRefusesWhatItCannotAnswer(t *testing.T) {
	// Each of these has a right answer that is "no", and saying so is more use
	// than returning a number that looks plausible.
	for _, expression := range []string{
		"",
		"1 / 0",
		"5 % 0",
		"2 +",
		"(1 + 2",
		"1 + 2)",
		"drop table tools",
		"2 ** 3",
		"exp(1)",
	} {
		if got, err := evaluate(expression); err == nil {
			t.Fatalf("evaluate(%q) should have been refused, got %v", expression, got)
		}
	}
}

func TestBuiltinCalculate(t *testing.T) {
	repository := &Repository{}
	action := Action{Name: "calculate"}

	result, err := repository.Invoke(context.Background(),
		Tool{Kind: KindBuiltin}, action, map[string]any{"expression": "(120 + 80) * 3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != 200 || result.Body != "600" {
		t.Fatalf("unexpected result: %#v", result)
	}

	// A bad expression is reported to the model as a failed call rather than
	// as a transport error, so it can correct itself and try again.
	failed, err := repository.Invoke(context.Background(),
		Tool{Kind: KindBuiltin}, action, map[string]any{"expression": "1/0"})
	if err != nil {
		t.Fatalf("a bad expression should not be a transport error: %v", err)
	}
	if failed.Status != 400 || !strings.Contains(failed.Body, "divide by zero") {
		t.Fatalf("the reason should reach the model: %#v", failed)
	}
}

func TestBuiltinCurrentTime(t *testing.T) {
	repository := &Repository{}
	result, err := repository.Invoke(context.Background(),
		Tool{Kind: KindBuiltin}, Action{Name: "current_time"},
		map[string]any{"timezone": "Asia/Ho_Chi_Minh"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var decoded map[string]string
	if err := json.Unmarshal([]byte(result.Body), &decoded); err != nil {
		t.Fatalf("the reply should be JSON a model can read, got %q", result.Body)
	}
	if decoded["timezone"] != "Asia/Ho_Chi_Minh" || decoded["iso8601"] == "" {
		t.Fatalf("unexpected reply: %#v", decoded)
	}
}

func TestBuiltinRefusesAnUnknownAction(t *testing.T) {
	// A built-in tool's actions are fixed, so a name with no function behind it
	// is a missing tool rather than an empty answer.
	repository := &Repository{}
	if _, err := repository.Invoke(context.Background(),
		Tool{Kind: KindBuiltin}, Action{Name: "rm_rf"}, nil); err == nil {
		t.Fatal("an unknown built-in should have been refused")
	}
}
