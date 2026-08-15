package domain

import "time"

// Audit action identifiers (audit-log spec).
const (
	ActionCreated    = "created"
	ActionTransition = "transition"
	ActionUpdate     = "update"
)

// AuditEvent records a single mutation on a ticket. The domain fills
// TicketID, Action, Field, FromValue/ToValue, Note and CreatedAt; the
// application layer stamps Actor/ActorUserID from the session (D14) and
// Reason where a reason is mandatory (reassignment — ticket-access spec).
// Field names the changed field ("state" for transitions, or the edited
// field for updates).
type AuditEvent struct {
	TicketID  int64
	Actor     string
	// ActorUserID is the acting session user's id (design: "Events store
	// session actor ID/snapshot"). NULL for legacy/backfill events whose
	// actor id is not provable.
	ActorUserID *int64
	Action      string
	Field       *string
	FromValue   *string
	ToValue     *string
	Note        *string // reopen reason for closed -> in_progress
	// Reason is the mandatory reason for a reassignment (person A -> B);
	// nil when no reason applies or the reason is not required (initial
	// assignment, ticket-access spec).
	Reason    *string
	CreatedAt time.Time
}
