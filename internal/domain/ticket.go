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
	// RequesterUserID is the immutable creating-session user id
	// (ticket-access spec): persisted from the session at creation, never
	// supplied or edited by a caller. NULL means "legacy ticket without a
	// provable creator" — visible to roles agent+ only, never attributed
	// to a guessed user.
	RequesterUserID *int64
	CategoryID      int64
	UserID          *int64 // nil = unassigned
	Priority        Priority
	State           State
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ResolvedAt      *time.Time
	ClosedAt        *time.Time
}

// Transition validates a move against the 5x5 matrix and, when legal, applies
// the state and lifecycle-timestamp changes, returning the audit event.
//
// Timestamp semantics: entering resolved stamps ResolvedAt; entering closed
// stamps ClosedAt; reopen resolved -> in_progress clears ResolvedAt; reopen
// closed -> in_progress clears both and requires a non-empty reason, which is
// recorded in the audit event note. Every successful transition also refreshes
// UpdatedAt: a transition is a modification (ticket-management spec).
func (t *Ticket) Transition(to State, reason string, now time.Time) (*AuditEvent, error) {
	from := t.State
	if !transitions[from][to] {
		return nil, NewInvalidTransitionError(from, to)
	}

	var note *string
	if to == StateInProgress && IsClosed(from) {
		// Reopening a closed ticket (resolved or closed) always requires a
		// reason; cancelled is terminal so it cannot reach here.
		if strings.TrimSpace(reason) == "" {
			return nil, NewReopenReasonRequiredError()
		}
		r := strings.TrimSpace(reason)
		note = &r
	}

	t.State = to
	switch to {
	case StateResolved:
		t.ResolvedAt = &now
	case StateClosed:
		t.ClosedAt = &now
	}
	if to == StateInProgress {
		t.ResolvedAt = nil
		if from == StateClosed {
			t.ClosedAt = nil
		}
	}
	t.UpdatedAt = now

	return &AuditEvent{
		TicketID:  t.ID,
		Action:    ActionTransition,
		Field:     ptr("state"), // the changed field for a transition is the state itself
		FromValue: ptr(string(from)),
		ToValue:   ptr(string(to)),
		Note:      note,
		CreatedAt: now,
	}, nil
}

// TicketUpdate describes optional field edits (ticket-management spec:
// title, priority, assigned user). The description AND the category are
// immutable after creation — they are NOT part of the update surface: the
// category is fixed once the ticket exists (future: categories get their
// own management flow), exactly like the description. A nil pointer means
// "not provided"; a non-nil pointer means "set to this value".
// Assignment is a tri-state: UserID non-nil assigns, ClearUserID=true
// clears the assignment, and both together is rejected as ambiguous.
type TicketUpdate struct {
	Title       *string
	Priority    *Priority
	UserID      *int64
	ClearUserID bool
}

// ApplyUpdate applies only the provided fields whose value actually changed,
// refreshes UpdatedAt and appends exactly one audit event per changed field.
// On a validation error (blank title, unsupported priority, ambiguous user
// assignment) NO change is applied. Edits never alter ResolvedAt or ClosedAt —
// those belong to the state machine alone.
func (t *Ticket) ApplyUpdate(u TicketUpdate, now time.Time) ([]AuditEvent, error) {
	if u.Title != nil && strings.TrimSpace(*u.Title) == "" {
		return nil, &ValidationError{Field: "title", Message: ErrMsgTitleRequired}
	}
	if u.Priority != nil && !isValidPriority(*u.Priority) {
		return nil, &InvalidPriorityError{Field: "priority", Message: ErrMsgInvalidPriority}
	}
	// Assign and clear cannot both be requested: the intent is ambiguous.
	if u.ClearUserID && u.UserID != nil {
		return nil, &ValidationError{Field: "user", Message: ErrMsgConflictingUserAssignment}
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
	if u.Priority != nil && *u.Priority != t.Priority {
		audit("priority", string(t.Priority), string(*u.Priority))
		t.Priority = *u.Priority
	}
	if u.ClearUserID {
		// Unassign: emit from = previous user id, to = ""; already unassigned
		// is a no-op (no event, no updated_at refresh).
		if t.UserID != nil {
			audit("user", strconv.FormatInt(*t.UserID, 10), "")
			t.UserID = nil
		}
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
	case PriorityLow, PriorityMedium, PriorityHigh, PriorityCritical:
		return true
	}
	return false
}

func ptr[T any](v T) *T { return &v }
