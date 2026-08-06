package domain

import "time"

// Audit action identifiers (audit-log spec).
const (
	ActionCreated    = "created"
	ActionTransition = "transition"
	ActionUpdate     = "update"
)

// AuditEvent records a single mutation on a ticket. The domain fills
// TicketID, Action, FromValue/ToValue, Note and CreatedAt; the application
// layer stamps Actor from the session (D14).
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
