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
