package domain

// State is the lifecycle state of a ticket (ticket-state-machine spec).
type State string

const (
	StateNuevo      State = "nuevo"
	StateEnProgreso State = "en_progreso"
	StateResuelto   State = "resuelto"
	StateCerrado    State = "cerrado"
	StateCancelado  State = "cancelado"
)

// transitions is the single source of truth for legal moves.
// cancelado is terminal; no transition may move back into nuevo.
var transitions = map[State]map[State]bool{
	StateNuevo: {
		StateEnProgreso: true,
		StateResuelto:   true,
		StateCancelado:  true,
	},
	StateEnProgreso: {
		StateResuelto:  true,
		StateCancelado: true,
	},
	StateResuelto: {
		StateCerrado:    true,
		StateEnProgreso: true, // reopen, no reason required
	},
	StateCerrado: {
		StateEnProgreso: true, // reopen, reason required
	},
	StateCancelado: {},
}
