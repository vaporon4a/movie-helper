package movieclub

import "testing"

func TestRoundTransitions(t *testing.T) {
	allowed := [][2]State{
		{StatePlanned, StatePollCreating},
		{StatePollCreating, StateOpen},
		{StateOpen, StateClosing},
		{StateClosing, StateSelecting},
		{StateSelecting, StateReady},
		{StateReady, StatePublishing},
		{StatePublishing, StatePublished},
		{StateUnknown, StateReady},
	}
	for _, transition := range allowed {
		if !CanTransition(transition[0], transition[1]) {
			t.Errorf("expected %s -> %s to be allowed", transition[0], transition[1])
		}
	}
	for _, transition := range [][2]State{
		{StatePlanned, StatePublished},
		{StateOpen, StateReady},
		{StatePublished, StatePlanned},
		{State("invalid"), StateReady},
	} {
		if CanTransition(transition[0], transition[1]) {
			t.Errorf("expected %s -> %s to be rejected", transition[0], transition[1])
		}
	}
}
