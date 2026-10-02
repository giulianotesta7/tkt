package domain

// State is the lifecycle state of a ticket (ticket-state-machine spec).
type State string

const (
	StateNew        State = "new"
	StateInProgress State = "in_progress"
	StateResolved   State = "resolved"
	StateClosed     State = "closed"
	StateCancelled  State = "cancelled"
)

// closedStates is the single source of truth for the terminal (read-only)
// states: resolved, closed, and cancelled. IsClosed and any adapter that must
// express terminality in SQL both derive from it, so a new terminal state is
// added in exactly one place.
var closedStates = []State{StateResolved, StateClosed, StateCancelled}

// IsClosed reports whether a ticket state is closed (read-only): resolved,
// closed, or cancelled. A closed ticket may no longer be edited, assigned,
// or commented on — only its state may change, and cancelled is fully
// terminal (no transitions at all, see transitions).
func IsClosed(s State) bool {
	for _, c := range closedStates {
		if s == c {
			return true
		}
	}
	return false
}

// ClosedStates returns the terminal (read-only) states as a copy. Callers that
// cannot use the Go predicate (for example a SQL predicate builder) use it to
// express the same set without duplicating the list.
func ClosedStates() []State {
	out := make([]State, len(closedStates))
	copy(out, closedStates)
	return out
}

// transitions is the single source of truth for legal moves.
// cancelled is terminal; no transition may move back into new.
var transitions = map[State]map[State]bool{
	StateNew: {
		StateInProgress: true,
		StateResolved:   true,
		StateCancelled:  true,
	},
	StateInProgress: {
		StateResolved:  true,
		StateCancelled: true,
	},
	StateResolved: {
		StateClosed:     true,
		StateInProgress: true, // reopen, no reason required
	},
	StateClosed: {
		StateInProgress: true, // reopen, reason required
	},
	StateCancelled: {},
}
