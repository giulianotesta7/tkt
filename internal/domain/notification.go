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
	// NotificationHoldReminder: the requester's held ticket will be closed
	// automatically unless they reply (issue #353, S3). Sent at most once per
	// hold, and only when the instance window is at least two working days.
	NotificationHoldReminder NotificationKind = "hold_reminder"
	// NotificationHoldClosed: the ticket was closed automatically because the
	// requester never replied (issue #353, S4). The requester and the assignee
	// both learn it.
	NotificationHoldClosed NotificationKind = "hold_closed"
	// The operational alert kinds (issue #408) are addressed to the ASSIGNED
	// agent alone. Each milestone has its own pair of kinds rather than one
	// kind plus a milestone fact, so "once per ticket per condition" is a
	// plain lookup by kind, and so each one carries copy a reader can act on.
	//
	// NotificationSLAFirstResponseAtRisk: the ticket reached the frozen
	// warning instant of its FIRST RESPONSE commitment and nobody has
	// answered yet.
	NotificationSLAFirstResponseAtRisk NotificationKind = "sla_first_response_at_risk"
	// NotificationSLAFirstResponseBreached: the first response commitment
	// came due with no answer.
	NotificationSLAFirstResponseBreached NotificationKind = "sla_first_response_breached"
	// NotificationSLAResolutionAtRisk: the ticket reached the frozen warning
	// instant of its RESOLUTION commitment and is still open.
	NotificationSLAResolutionAtRisk NotificationKind = "sla_resolution_at_risk"
	// NotificationSLAResolutionBreached: the resolution commitment came due
	// with the ticket still open.
	NotificationSLAResolutionBreached NotificationKind = "sla_resolution_breached"
	// NotificationNoMovement: the ticket had no comment and no change for the
	// inactivity window (domain.InactivityWorkingDays). A ticket waiting on
	// the requester (on hold) never reaches this state.
	NotificationNoMovement NotificationKind = "no_movement"
	// NotificationCompanyAccountPending: a company sign-in created an account
	// that waits for an administrator's approval (issue #419, S3). It is the
	// one kind that is not about a ticket: it announces the pending account
	// itself, so it names that account instead of a ticket.
	NotificationCompanyAccountPending NotificationKind = "company_account_pending"
	// NotificationMentioned: a staff member was mentioned in a comment on the
	// ticket (issue #418). It describes a comment, so it
	// carries its comment id and a requester must never receive it — only
	// staff who can already open the ticket can be mentioned, and a mention
	// never grants access.
	NotificationMentioned NotificationKind = "mentioned"
	// NotificationAssigneeChanged: the ticket was assigned or unassigned and a
	// FOLLOWER has to learn it (issue #418). ToValue carries the
	// new assignee's display name as a snapshot; nil means the ticket is now
	// unassigned. The new assignee keeps NotificationAssigned instead.
	NotificationAssigneeChanged NotificationKind = "assignee_changed"
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
	NotificationHoldReminder,
	NotificationHoldClosed,
	NotificationSLAFirstResponseAtRisk,
	NotificationSLAFirstResponseBreached,
	NotificationSLAResolutionAtRisk,
	NotificationSLAResolutionBreached,
	NotificationNoMovement,
	NotificationCompanyAccountPending,
	NotificationMentioned,
	NotificationAssigneeChanged,
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
// learn about (S16). A mention is one of them: it is written from a comment
// and addressed to staff only (issue #418).
func (k NotificationKind) IsComment() bool {
	return k == NotificationCommentPublic || k == NotificationCommentInternal || k == NotificationMentioned
}

// Notification is one entry of a user's in-app notification center (issue
// #344). It records WHO has to learn something, WHICH subject it is about — a
// ticket, or the account waiting for approval — and WHAT happened as a kind
// plus structured facts — never a rendered sentence (S15), so the same row
// renders in English or Spanish at render time.
//
// Recipients are computed by the application layer (policy and roles); the
// store only inserts what it is given, inside the transaction of the change
// being described (S11).
type Notification struct {
	ID int64
	// UserID is the recipient — the single user this row is addressed to.
	UserID int64
	// TicketID is the ticket the notification is about. The panel opens it,
	// and the ticket detail re-checks access on open (S16). It is 0 for the
	// account kind, which is about an account rather than a ticket and stores
	// no ticket at all.
	TicketID int64
	Kind     NotificationKind
	// PendingUserID is the account waiting for approval. It is set by the
	// account kind alone, whose subject is that account rather than a ticket;
	// every ticket kind leaves it nil.
	PendingUserID *int64
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
// before the row joins its change's transaction, so a self-addressed or
// unrenderable notification can never be persisted.
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
	// A stored notification names exactly one subject: the ticket it is about,
	// or — for the account kind — the account waiting for approval.
	if n.Kind == NotificationCompanyAccountPending {
		if n.TicketID != 0 {
			return &NotificationInvariantError{Field: "ticket_id", Message: notifMsgTicketUnexpected}
		}
		if n.PendingUserID == nil || *n.PendingUserID <= 0 {
			return &NotificationInvariantError{Field: "pending_user_id", Message: notifMsgPendingUserRequired}
		}
		if n.FromValue != nil {
			return &NotificationInvariantError{Field: "from_value", Message: notifMsgAccountFactUnexpected}
		}
		if n.ToValue != nil {
			return &NotificationInvariantError{Field: "to_value", Message: notifMsgAccountFactUnexpected}
		}
		if n.StepIndex != nil {
			return &NotificationInvariantError{Field: "step_index", Message: notifMsgAccountFactUnexpected}
		}
	} else {
		if n.TicketID <= 0 {
			return &NotificationInvariantError{Field: "ticket_id", Message: notifMsgTicketRequired}
		}
		if n.PendingUserID != nil {
			return &NotificationInvariantError{Field: "pending_user_id", Message: notifMsgPendingUserUnexpected}
		}
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
	notifMsgKindInvalid           = "invalid notification kind"
	notifMsgRecipientRequired     = "notification recipient is required"
	notifMsgTicketRequired        = "notification ticket is required"
	notifMsgTicketUnexpected      = "a company account notification is not about a ticket"
	notifMsgCreatedAtRequired     = "notification creation instant is required"
	notifMsgSelfRecipient         = "a notification cannot be sent to the actor who caused it"
	notifMsgCommentRequired       = "comment notification requires its comment id"
	notifMsgCommentUnexpected     = "only comment notifications carry a comment id"
	notifMsgPendingUserRequired   = "company account notification requires the account waiting for approval"
	notifMsgPendingUserUnexpected = "only a company account notification carries a pending account"
	notifMsgAccountFactUnexpected = "a company account notification carries no ticket facts"
)
