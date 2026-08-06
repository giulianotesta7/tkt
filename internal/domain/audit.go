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
// application layer stamps Actor from the session (D14). Field names the
// changed field ("state" for transitions, or the edited field for updates).
type AuditEvent struct {
	TicketID  int64
	Actor     string
	Action    string
	Field     *string
	FromValue *string
	ToValue   *string
	Note      *string // reopen reason for cerrado -> en_progreso
	CreatedAt time.Time
}
