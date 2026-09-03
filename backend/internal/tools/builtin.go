package tools

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	// The clock reads IANA names like Asia/Ho_Chi_Minh, and the runtime image
	// is Alpine with no zoneinfo in it, so every name but UTC was an "unknown
	// timezone". Embedding the database puts the answer in the binary, where
	// it cannot be taken away by a change to the base image.
	_ "time/tzdata"
	"unicode"
)

// Some tools have no endpoint to call: the work happens here. Arithmetic is
// the clearest case - a model asked to multiply six-figure numbers will
// produce something confident and wrong, where this produces the answer - and
// the current time is the second, because a model has no idea what it is.
//
// A built-in reaches nothing, so the egress guard has nothing to guard and
// there is no credential to store. That is the whole appeal.
const KindBuiltin = "builtin"

type builtinFunc func(arguments map[string]any) (string, error)

// builtins is keyed by action name, which is what the model calls. A built-in
// tool's actions are fixed at install time and validated like any other, so a
// name here always has a matching action row.
var builtins = map[string]builtinFunc{
	"calculate": func(arguments map[string]any) (string, error) {
		expression, _ := arguments["expression"].(string)
		value, err := evaluate(expression)
		if err != nil {
			return "", err
		}
		return strconv.FormatFloat(value, 'f', -1, 64), nil
	},
	"current_time": func(arguments map[string]any) (string, error) {
		name, _ := arguments["timezone"].(string)
		if strings.TrimSpace(name) == "" {
			name = "UTC"
		}
		location, err := time.LoadLocation(name)
		if err != nil {
			return "", fmt.Errorf("unknown timezone %q", name)
		}
		now := time.Now().In(location)
		payload, _ := json.Marshal(map[string]string{
			"timezone": name,
			"iso8601":  now.Format(time.RFC3339),
			"readable": now.Format("Monday, 2 January 2006, 15:04"),
		})
		return string(payload), nil
	},
}

// invokeBuiltin runs one locally. It reports a failure as an error rather than
// a status, because there is no endpoint whose status could be reported.
func (repository *Repository) invokeBuiltin(action Action, arguments map[string]any) (CallResult, error) {
	started := time.Now()
	run, found := builtins[action.Name]
	if !found {
		return CallResult{}, ErrNotFound
	}
	body, err := run(arguments)
	if err != nil {
		return CallResult{
			Status:     400,
			DurationMS: time.Since(started).Milliseconds(),
			Body:       err.Error(),
		}, nil
	}
	return CallResult{
		Status:     200,
		DurationMS: time.Since(started).Milliseconds(),
		Body:       body,
	}, nil
}

// evaluate reads an arithmetic expression. It is a small recursive-descent
// parser rather than anything that executes code: the only things it can
// produce are numbers, so there is nothing for a hostile expression to reach.
//
// Supported: + - * / %, parentheses, unary minus, and decimal numbers.
func evaluate(expression string) (float64, error) {
	parser := &expressionParser{input: []rune(strings.TrimSpace(expression))}
	if len(parser.input) == 0 {
		return 0, fmt.Errorf("nothing to calculate")
	}
	value, err := parser.readSum()
	if err != nil {
		return 0, err
	}
	parser.skipSpace()
	if parser.position < len(parser.input) {
		return 0, fmt.Errorf("unexpected %q at position %d", string(parser.input[parser.position]), parser.position)
	}
	if math.IsInf(value, 0) || math.IsNaN(value) {
		return 0, fmt.Errorf("the result is not a number")
	}
	return value, nil
}

type expressionParser struct {
	input    []rune
	position int
}

func (parser *expressionParser) skipSpace() {
	for parser.position < len(parser.input) && unicode.IsSpace(parser.input[parser.position]) {
		parser.position++
	}
}

func (parser *expressionParser) readSum() (float64, error) {
	value, err := parser.readProduct()
	if err != nil {
		return 0, err
	}
	for {
		parser.skipSpace()
		if parser.position >= len(parser.input) {
			return value, nil
		}
		operator := parser.input[parser.position]
		if operator != '+' && operator != '-' {
			return value, nil
		}
		parser.position++
		right, err := parser.readProduct()
		if err != nil {
			return 0, err
		}
		if operator == '+' {
			value += right
		} else {
			value -= right
		}
	}
}

func (parser *expressionParser) readProduct() (float64, error) {
	value, err := parser.readTerm()
	if err != nil {
		return 0, err
	}
	for {
		parser.skipSpace()
		if parser.position >= len(parser.input) {
			return value, nil
		}
		operator := parser.input[parser.position]
		if operator != '*' && operator != '/' && operator != '%' {
			return value, nil
		}
		parser.position++
		right, err := parser.readTerm()
		if err != nil {
			return 0, err
		}
		switch operator {
		case '*':
			value *= right
		case '/':
			if right == 0 {
				return 0, fmt.Errorf("cannot divide by zero")
			}
			value /= right
		case '%':
			if right == 0 {
				return 0, fmt.Errorf("cannot take a remainder by zero")
			}
			value = math.Mod(value, right)
		}
	}
}

func (parser *expressionParser) readTerm() (float64, error) {
	parser.skipSpace()
	if parser.position >= len(parser.input) {
		return 0, fmt.Errorf("the expression ends where a number was expected")
	}

	switch parser.input[parser.position] {
	case '-':
		parser.position++
		value, err := parser.readTerm()
		return -value, err
	case '+':
		parser.position++
		return parser.readTerm()
	case '(':
		parser.position++
		value, err := parser.readSum()
		if err != nil {
			return 0, err
		}
		parser.skipSpace()
		if parser.position >= len(parser.input) || parser.input[parser.position] != ')' {
			return 0, fmt.Errorf("a bracket was opened and never closed")
		}
		parser.position++
		return value, nil
	}

	start := parser.position
	for parser.position < len(parser.input) &&
		(unicode.IsDigit(parser.input[parser.position]) || parser.input[parser.position] == '.') {
		parser.position++
	}
	if start == parser.position {
		return 0, fmt.Errorf("unexpected %q at position %d", string(parser.input[parser.position]), parser.position)
	}
	value, err := strconv.ParseFloat(string(parser.input[start:parser.position]), 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", string(parser.input[start:parser.position]))
	}
	return value, nil
}
