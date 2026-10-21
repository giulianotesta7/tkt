package domain

import "time"

// NotificationKind is the closed vocabulary of notification events (issue
// #344, spec S10). A stored notification carries a KIND plus structured
// facts, never rendered text: copy is catalogue-backed and resolved at render
// time in the instance language (spec S15).
type NotificationKind string

const (
	// NotificationAssigned: the ticket was assigned to the recipient
	// (manual assign, claim by another person, or automatic least-loaded
	// routing).
	NotificationAssigned NotificationKind = "assigned"
	// NotificationStateChanged: the ticket's state changed, reopening
	// included.
	NotificationStateChanged NotificationKind = "state_changed"
	// NotificationResolvedAwaitingConfirmation: the ticket is resolved and
	// the requester has to confirm whether it is actually solved.
	NotificationResolvedAwaitingConfirmation NotificationKind = "resolved_awaiting_confirmation"
	// NotificationResolutionConfirmed: the requester confirmed the
	// resolution (the ticket was closed by that confirmation).
	NotificationResolutionConfirmed NotificationKind = "resolution_confirmed"
	// NotificationResolutionRejected: the requester rejected the
	// resolution (the ticket was reopened by that rejection).
	NotificationResolutionRejected NotificationKind = "resolution_rejected"
	// NotificationPriorityChanged: the ticket's priority changed.
	NotificationPriorityChanged NotificationKind = "priority_changed"
	// NotificationCommentPublic: a public comment was added to the ticket.
	NotificationCommentPublic NotificationKind = "comment_public"
	// NotificationCommentInternal: a staff-only (internal) comment was added
	// to the ticket. A requester must NEVER receive this kind (S10, S16).
	NotificationCommentInternal NotificationKind = "comment_internal"
	// NotificationFormWaiting: a workflow form step now waits on the
	// recipient (requester form or assignee form).
	NotificationFormWaiting NotificationKind = "form_waiting"
	// NotificationOnHold: the requester's ticket was put on hold and waits for
	// their reply (issue #352). The pause reason is not copied here: the panel
	// opens the ticket, whose banner shows the current reason.
	NotificationOnHold NotificationKind = "on_hold"
)

// notificationKinds is the single copy of the vocabulary. The migration's
// CHECK constraint lists the same values; Valid is the Go half of that closed
// set, so a kind the column accepts is always a kind the domain can render.
var notificationKinds = []NotificationKind{
	NotificationAssigned,
	NotificationStateChanged,
	NotificationResolvedAwaitingConfirmation,
	NotificationResolutionConfirmed,
	NotificationResolutionRejected,
	NotificationPriorityChanged,
	NotificationCommentPublic,
	NotificationCommentInternal,
	NotificationFormWaiting,
	NotificationOnHold,
}

// Valid reports whether k is one of the persisted kinds. Unknown values are
// invalid — a notification nobody can render is never stored.
func (k NotificationKind) Valid() bool {
	for _, known := range notificationKinds {
		if k == known {
			return true
		}
	}
	return false
}

// IsComment reports whether k describes a comment, the only kinds that carry
// a comment id and the only kinds whose visibility a requester must never
// learn about (S16).
func (k NotificationKind) IsComment() bool {
	return k == NotificationCommentPublic || k == NotificationCommentInternal
}

// Notification is one entry of a user's in-app notification center (issue
// #344). It records WHO has to learn something, WHICH ticket it is about, and
// WHAT happened as a kind plus structured facts — never a rendered sentence
// (S15), so the same row renders in English or Spanish at render time.
//
// Recipients are computed by the application layer (policy and roles); the
// store only inserts what it is given, inside the transaction of the ticket
// change being described (S11).
type Notification struct {
	ID int64
	// UserID is the recipient — the single user this row is addressed to.
	UserID int64
	// TicketID is the ticket the notification is about. The panel opens it,
	// and the ticket detail re-checks access on open (S16).
	TicketID int64
	Kind     NotificationKind
	// ActorUserID is the user whose action caused the notification; NULL
	// means the system did it (automatic assignment, workflow events).
	// It is never the recipient: nobody is notified about their own action.
	ActorUserID *int64
	// FromValue/ToValue carry the change for the kinds that describe one
	// (state_changed, priority_changed) — the raw domain values, so the copy
	// catalogue can translate them.
	FromValue *string
	ToValue   *string
	// CommentID links a comment kind to its comment. The body is never
	// copied here.
	CommentID *int64
	// StepIndex is the zero-based pinned workflow step of a form_waiting
	// notification (same meaning as audit_events.step_index, migration 0008).
	StepIndex *int
	CreatedAt time.Time
	// ReadAt is nil while the notification is unread; the bell count is the
	// number of the recipient's rows with read_at NULL.
	ReadAt *time.Time
}

// Validate enforces the invariants every stored notification must satisfy.
// The application layer filters recipients (S10); this is the last check
// before the row joins the ticket change's transaction, so a self-addressed
// or unrenderable notification can never be persisted.
//
// A failure here is a programming or policy bug, not a user mistake: the
// caller aborts the whole unit of work. It therefore returns
// NotificationInvariantError — never a ValidationError, which the i18n guard
// reserves for refusals a person reads and a form maps to a 422.
func (n Notification) Validate() error {
	if !n.Kind.Valid() {
		return &NotificationInvariantError{Field: "kind", Message: notifMsgKindInvalid}
	}
	if n.UserID <= 0 {
		return &NotificationInvariantError{Field: "user_id", Message: notifMsgRecipientRequired}
	}
	if n.TicketID <= 0 {
		return &NotificationInvariantError{Field: "ticket_id", Message: notifMsgTicketRequired}
	}
	if n.CreatedAt.IsZero() {
		return &NotificationInvariantError{Field: "created_at", Message: notifMsgCreatedAtRequired}
	}
	if n.ActorUserID != nil && *n.ActorUserID == n.UserID {
		return &NotificationInvariantError{Field: "actor_user_id", Message: notifMsgSelfRecipient}
	}
	if n.Kind.IsComment() && n.CommentID == nil {
		return &NotificationInvariantError{Field: "comment_id", Message: notifMsgCommentRequired}
	}
	if !n.Kind.IsComment() && n.CommentID != nil {
		return &NotificationInvariantError{Field: "comment_id", Message: notifMsgCommentUnexpected}
	}
	return nil
}

// NotificationInvariantError reports a notification that breaks a domain
// invariant before persistence. It is INTERNAL: no page renders it and no
// request maps it to a status — it exists so a policy bug aborts the ticket
// change's transaction instead of storing a notification nobody can render,
// or one addressed to the person who acted. Field names the offending value.
type NotificationInvariantError struct {
	Field   string
	Message string
}

func (e *NotificationInvariantError) Error() string { return e.Message }

// Internal wording for the notification invariant failures. They never reach a
// person, so they carry no catalogue key (contrast the ErrMsg* constants in
// errors.go, which are the English half of user-facing copy).
const (
	notifMsgKindInvalid       = "invalid notification kind"
	notifMsgRecipientRequired = "notification recipient is required"
	notifMsgTicketRequired    = "notification ticket is required"
	notifMsgCreatedAtRequired = "notification creation instant is required"
	notifMsgSelfRecipient     = "a notification cannot be sent to the actor who caused it"
	notifMsgCommentRequired   = "comment notification requires its comment id"
	notifMsgCommentUnexpected = "only comment notifications carry a comment id"
)
