package runs

import "testing"

func TestRunTransitions(t *testing.T) {
	valid := [][2]Status{{Queued, Running}, {Running, WaitingApproval}, {WaitingApproval, Running}, {Running, Succeeded}, {Queued, Cancelled}}
	for _, transition := range valid {
		if !CanTransition(transition[0], transition[1]) {
			t.Errorf("expected %s -> %s to be valid", transition[0], transition[1])
		}
	}
	invalid := [][2]Status{{Succeeded, Running}, {Failed, Running}, {Queued, Succeeded}, {Cancelled, Running}}
	for _, transition := range invalid {
		if CanTransition(transition[0], transition[1]) {
			t.Errorf("expected %s -> %s to be invalid", transition[0], transition[1])
		}
	}
}

func TestTerminalStatuses(t *testing.T) {
	for _, status := range []Status{Succeeded, Failed, Cancelled, TimedOut} {
		if !status.Terminal() {
			t.Errorf("%s should be terminal", status)
		}
	}
	if Running.Terminal() || WaitingApproval.Terminal() {
		t.Fatal("active statuses must not be terminal")
	}
}
