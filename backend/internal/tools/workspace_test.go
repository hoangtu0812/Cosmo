package tools

import (
	"strings"
	"testing"
)

// The scope rule for tools is deliberately the same shape as the one knowledge
// bases use. If the two drift, a reader has to learn the rules twice - so the
// test states the four cases rather than trusting the resemblance.
func TestOfferedSQLCoversTheFourRungs(t *testing.T) {
	for _, fragment := range []string{
		"t.owner_workspace_id = $1",
		"t.visibility = 'everyone'",
		"t.visibility = 'selected'",
		"tool_shares",
	} {
		if !strings.Contains(offeredSQL, fragment) {
			t.Errorf("the offer rule says nothing about %q", fragment)
		}
	}
	// Private is offered to nobody, so it must not appear as a rung.
	if strings.Contains(offeredSQL, "'private'") {
		t.Error("private is a rung in the offer rule; it should reach only its owner")
	}
}

// A tool holding a key must not be reachable by a plain chat, and the query
// says so itself rather than trusting the switch that was set earlier - a tool
// can be given a key after it was switched on.
func TestAutoCallableRefusesKeyedToolsInTheQuery(t *testing.T) {
	repository := &Repository{}
	_ = repository
	// The guard lives in the SQL, so that is what is checked. Asserting on a
	// live database here would need one; asserting the clause exists catches
	// the removal, which is the failure that matters.
	source := autoCallableSQL()
	if !strings.Contains(source, "t.auth_secret IS NULL") {
		t.Error("a keyed tool is not excluded at read time")
	}
	if !strings.Contains(source, "wt.auto_call") {
		t.Error("the switch is not consulted")
	}
}
