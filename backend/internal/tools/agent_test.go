package tools

import (
	"strings"
	"testing"
)

// Two tools with the same name is the case that produced the bug: the
// marketplace let the same toolkit be installed twice, and both copies then
// answered to the same call name.
func TestCallPrefixesSeparateCollidingNames(t *testing.T) {
	prefixes := callPrefixes([]Tool{
		{ID: "tol_LrOjbywKAlCpSYwP", Name: "Weather"},
		{ID: "tol_HMY5SNPIWwuWzuVG", Name: "Weather"},
		{ID: "tol_JokwqUIRe1xUS779", Name: "Calculator"},
	})
	if prefixes["tol_LrOjbywKAlCpSYwP"] == prefixes["tol_HMY5SNPIWwuWzuVG"] {
		t.Fatalf("colliding tools kept the same prefix: %q", prefixes["tol_LrOjbywKAlCpSYwP"])
	}
	// A name nothing collides with is left alone, so the common case reads the
	// way it always did.
	if prefixes["tol_JokwqUIRe1xUS779"] != "calculator" {
		t.Fatalf("uncontested name changed: %q", prefixes["tol_JokwqUIRe1xUS779"])
	}
}

func TestCallPrefixesAreStable(t *testing.T) {
	list := []Tool{{ID: "tol_aaaaaaaa", Name: "Weather"}, {ID: "tol_bbbbbbbb", Name: "Weather"}}
	first := callPrefixes(list)
	second := callPrefixes(list)
	for id, prefix := range first {
		if second[id] != prefix {
			t.Fatalf("prefix for %s moved between calls: %q then %q", id, prefix, second[id])
		}
	}
}

// The catalogue's Weather entry, end to end through the sentence a model
// actually reads: the description has to name the field the answer is in, or
// the model is back to reading past generationtime_ms to find a temperature.
func TestDefinitionDescriptionCarriesTheResult(t *testing.T) {
	entry, found := CatalogEntryByID("weather")
	if !found {
		t.Fatal("weather entry missing")
	}
	action := entry.Actions[0]
	description := entry.Description + ". " + action.Description
	if returns := describeResult(action); returns != "" {
		description += " " + returns
	}
	if !strings.Contains(description, "current.temperature_2m") {
		t.Fatalf("the model is not told where the temperature is: %q", description)
	}
}
