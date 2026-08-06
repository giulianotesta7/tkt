package domain

import (
	"strconv"
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

// TicketUpdate describes optional field edits (ticket-management spec: title,
// description, category, priority, assigned user). A nil pointer means "not
// provided"; a non-nil pointer means "set to this value".
type TicketUpdate struct {
	Title       *string
	Description *string
	CategoryID  *int64
	Priority    *Priority
	UserID      *int64
}

// ApplyUpdate applies only the provided fields whose value actually changed,
// refreshes UpdatedAt and appends exactly one audit event per changed field.
// On a validation error (blank title, unsupported priority) NO change is
// applied. Edits never alter ResolvedAt or ClosedAt — those belong to the
// state machine alone.
func (t *Ticket) ApplyUpdate(u TicketUpdate, now time.Time) ([]AuditEvent, error) {
	if u.Title != nil && strings.TrimSpace(*u.Title) == "" {
		return nil, &ValidationError{Field: "title", Message: ErrMsgTitleRequired}
	}
	if u.Priority != nil && !isValidPriority(*u.Priority) {
		return nil, &InvalidPriorityError{Field: "priority", Message: ErrMsgInvalidPriority}
	}

	var events []AuditEvent
	audit := func(field, from, to string) {
		events = append(events, AuditEvent{
			TicketID:  t.ID,
			Action:    ActionUpdate,
			Field:     ptr(field),
			FromValue: ptr(from),
			ToValue:   ptr(to),
			CreatedAt: now,
		})
	}

	if u.Title != nil && *u.Title != t.Title {
		audit("title", t.Title, *u.Title)
		t.Title = *u.Title
	}
	if u.Description != nil && *u.Description != t.Description {
		audit("description", t.Description, *u.Description)
		t.Description = *u.Description
	}
	if u.CategoryID != nil && *u.CategoryID != t.CategoryID {
		audit("category", strconv.FormatInt(t.CategoryID, 10), strconv.FormatInt(*u.CategoryID, 10))
		t.CategoryID = *u.CategoryID
	}
	if u.Priority != nil && *u.Priority != t.Priority {
		audit("priority", string(t.Priority), string(*u.Priority))
		t.Priority = *u.Priority
	}
	if u.UserID != nil {
		from := ""
		if t.UserID != nil {
			from = strconv.FormatInt(*t.UserID, 10)
		}
		to := strconv.FormatInt(*u.UserID, 10)
		if from != to {
			audit("user", from, to)
			userID := *u.UserID
			t.UserID = &userID
		}
	}

	if len(events) > 0 {
		t.UpdatedAt = now
	}
	return events, nil
}

func isValidPriority(p Priority) bool {
	switch p {
	case PriorityBaja, PriorityMedia, PriorityAlta, PriorityCritica:
		return true
	}
	return false
}

func ptr[T any](v T) *T { return &v }
