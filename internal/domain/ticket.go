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
	RequesterUserID   *int64
	CategoryID        int64
	UserID            *int64 // nil = unassigned
	WorkflowVersionID *int64 // nil = legacy pre-workflow ticket
	Priority          Priority
	State             State
	CreatedAt         time.Time
	UpdatedAt         time.Time
	ResolvedAt        *time.Time
	ClosedAt          *time.Time
	// OnHoldSince is the instant the ticket was put on hold while it waits for
	// the requester (issue #352); nil when it is not on hold. A hold is an
	// ATTRIBUTE of an open ticket, not a lifecycle state: the state machine,
	// the terminal states and the metrics are untouched, and only PutOnHold
	// and ResumeFromHold set or clear it. OnHoldReason is the mandatory pause
	// reason, empty whenever OnHoldSince is nil.
	OnHoldSince  *time.Time
	OnHoldReason string
	// SLA is the SLA commitment FROZEN onto the ticket at creation (issue
	// #211, the WorkflowVersionID precedent): it is carried ONLY through the
	// creation path and written ONCE, in the ticket's own transaction; the
	// update paths never write it — the same mould as resolved_at/closed_at.
	//
	// It is deliberately NOT hydrated by the read paths: a Ticket read back
	// from the store has SLA == nil even when the ticket HAS a frozen
	// commitment. A nil SLA on a read-back ticket therefore means "not
	// hydrated", NOT "no SLA". The sanctioned way to read the frozen
	// commitment is SLAStore.TicketSLA (via SLAService.ForTicket).
	SLA *TicketSLA
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
	// No terminal ticket is left on hold (issue #352): every closing path
	// ends the hold first, through ResumeFromHold, so the paused resolution
	// clock is re-applied before the milestone is judged. A path that forgets
	// fails here instead of storing a resolved ticket that still reads paused.
	if IsClosed(to) && t.IsOnHold() {
		return nil, &ValidationError{Field: "state", Message: ErrMsgTicketOnHold, Key: "error.ticket_on_hold"}
	}

	var note *string
	if to == StateInProgress && from == StateClosed {
		// Reopening a closed ticket requires a reason; reopen from resolved
		// is reason-free (requester rejection / agent reopen), and cancelled
		// is terminal so it cannot reach here.
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

// IsOnHold reports whether the ticket waits for the requester (issue #352).
func (t *Ticket) IsOnHold() bool { return t.OnHoldSince != nil }

// PutOnHold puts an open ticket on hold while it waits for the requester
// (issue #352), recording the trimmed pause reason and the pause instant.
// Every rejection is decided before any field changes, so a refused hold
// leaves the ticket exactly as it was: a terminal ticket is read-only, a
// blank reason is refused (the reason is why the clock stops, it is
// mandatory), and a ticket already on hold keeps its original pause
// instant rather than restarting it.
func (t *Ticket) PutOnHold(reason string, now time.Time) (*AuditEvent, error) {
	if IsClosed(t.State) {
		return nil, NewForbiddenError(ErrMsgClosedTicketReadOnly)
	}
	if t.IsOnHold() {
		return nil, &ValidationError{Field: "reason", Message: ErrMsgTicketAlreadyOnHold, Key: "error.ticket_already_on_hold"}
	}
	r := strings.TrimSpace(reason)
	if r == "" {
		return nil, &ValidationError{Field: "reason", Message: ErrMsgHoldReasonRequired, Key: "error.hold_reason_required"}
	}
	since := now
	t.OnHoldSince = &since
	t.OnHoldReason = r
	t.UpdatedAt = now
	return &AuditEvent{
		TicketID:  t.ID,
		Action:    ActionHold,
		Field:     ptr("on_hold"),
		Note:      ptr(r),
		CreatedAt: now,
	}, nil
}

// ResumeFromHold takes the ticket off hold (issue #352) and returns the
// instant the hold began, which the caller needs to shift the frozen
// resolution SLA by the working time spent on hold. A ticket not on hold is
// refused with no change.
func (t *Ticket) ResumeFromHold(now time.Time) (time.Time, *AuditEvent, error) {
	if !t.IsOnHold() {
		return time.Time{}, nil, &ValidationError{Field: "state", Message: ErrMsgTicketNotOnHold, Key: "error.ticket_not_on_hold"}
	}
	pausedAt := *t.OnHoldSince
	t.OnHoldSince = nil
	t.OnHoldReason = ""
	t.UpdatedAt = now
	return pausedAt, &AuditEvent{
		TicketID:  t.ID,
		Action:    ActionResume,
		Field:     ptr("on_hold"),
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
		return nil, &ValidationError{Field: "title", Message: ErrMsgTitleRequired, Key: "error.title_required"}
	}
	if u.Priority != nil && !isValidPriority(*u.Priority) {
		return nil, &InvalidPriorityError{Field: "priority", Message: ErrMsgInvalidPriority}
	}
	// Assign and clear cannot both be requested: the intent is ambiguous.
	if u.ClearUserID && u.UserID != nil {
		return nil, &ValidationError{Field: "user", Message: ErrMsgConflictingUserAssignment, Key: "error.conflicting_user_assignment"}
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
