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

// A shared static key must not be reachable by a plain chat. A user OAuth
// registration is different: the call still requires the current person's
// private grant, so that profile is intentionally admitted.
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
	if !strings.Contains(source, "t.auth_type = 'oauth2_user'") {
		t.Error("a per-user OAuth grant cannot be enabled in chat")
	}
	if !strings.Contains(source, "wt.auto_call") {
		t.Error("the switch is not consulted")
	}
}

// The shared projection is pasted into queries that pass different arguments -
// an agent's tools by agent id, a workspace's by workspace id. A placeholder
// inside it therefore means a number that is right in one query and wrong in
// the others, which is how the agent's own tools once stopped loading.
func TestSharedColumnsCarryNoPlaceholder(t *testing.T) {
	if strings.Contains(columns, "$") {
		t.Error("the shared projection names a placeholder; it is pasted into queries with different arguments")
	}
	// The workspace-framed reads add their own, told where the workspace sits
	// among their arguments - $2 for a listing, $1 for a read that starts from
	// the workspace itself.
	for _, param := range []string{"$1", "$2"} {
		if strings.Count(workspaceColumns(param), param) != 2 {
			t.Errorf("workspaceColumns(%s) does not use it for both the count and the install", param)
		}
	}
}
