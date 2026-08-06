package domain

import (
	"strings"
	"time"
)

// Ticket is the aggregate root (ticket-management spec). resolved_at and
// closed_at are lifecycle timestamps set and cleared ONLY by Transition —
// field updates never touch them.
type Ticket struct {
	ID             int64
	Number         int
	Title          string
	Description    string
	RequesterName  string
	RequesterEmail string
	CategoryID     int64
	UserID         *int64 // nil = unassigned
	Priority       Priority
	State          State
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ResolvedAt     *time.Time
	ClosedAt       *time.Time
}

// Transition validates a move against the 5x5 matrix and, when legal, applies
// the state and lifecycle-timestamp changes, returning the audit event.
//
// Timestamp semantics: entering resuelto stamps ResolvedAt; entering cerrado
// stamps ClosedAt; reopen resuelto -> en_progreso clears ResolvedAt; reopen
// cerrado -> en_progreso clears both and requires a non-empty reason, which is
// recorded in the audit event note.
func (t *Ticket) Transition(to State, reason string, now time.Time) (*AuditEvent, error) {
	from := t.State
	if !transitions[from][to] {
		return nil, NewInvalidTransitionError(from, to)
	}

	var note *string
	if from == StateCerrado && to == StateEnProgreso {
		if strings.TrimSpace(reason) == "" {
			return nil, NewReopenReasonRequiredError()
		}
		r := strings.TrimSpace(reason)
		note = &r
	}

	t.State = to
	switch to {
	case StateResuelto:
		t.ResolvedAt = &now
	case StateCerrado:
		t.ClosedAt = &now
	}
	if to == StateEnProgreso {
		t.ResolvedAt = nil
		if from == StateCerrado {
			t.ClosedAt = nil
		}
	}

	return &AuditEvent{
		TicketID:  t.ID,
		Action:    ActionTransition,
		FromValue: ptr(string(from)),
		ToValue:   ptr(string(to)),
		Note:      note,
		CreatedAt: now,
	}, nil
}

func ptr[T any](v T) *T { return &v }
