package movieclub

var transitions = map[State]map[State]bool{
	StatePlanned: {
		StatePollCreating: true,
		StateCancelled:    true,
	},
	StatePollCreating: {
		StateOpen:    true,
		StatePlanned: true,
		StateFailed:  true,
		StateUnknown: true,
	},
	StateOpen: {
		StateClosing:   true,
		StateSelecting: true,
		StateCancelled: true,
	},
	StateClosing: {
		StateOpen:      true,
		StateSelecting: true,
		StateFailed:    true,
		StateUnknown:   true,
	},
	StateSelecting: {
		StateSelecting: true,
		StateReady:     true,
		StateFailed:    true,
	},
	StateReady: {
		StatePublishing: true,
		StateCancelled:  true,
	},
	StatePublishing: {
		StatePublishing: true,
		StateReady:      true,
		StatePublished:  true,
		StateFailed:     true,
		StateUnknown:    true,
	},
	StateUnknown: {
		StateReady:     true,
		StatePublished: true,
		StateCancelled: true,
	},
}

func (s State) Valid() bool {
	_, ok := transitions[s]
	return ok || s == StatePublished || s == StateCancelled || s == StateFailed
}

func CanTransition(from, to State) bool {
	return from.Valid() && to.Valid() && transitions[from][to]
}
