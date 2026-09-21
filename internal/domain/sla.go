package domain

import "time"

// SLAPolicy is one priority's SLA commitment (issue #211): the first
// response and resolution targets, in seconds. It is a pure value — no
// clock reads, no persistence concerns. The same type answers the global
// defaults (sla_defaults) and one row of a category's materialized matrix
// (sla_policies).
type SLAPolicy struct {
	Priority             Priority
	FirstResponseSeconds int
	ResolveSeconds       int
}

// TicketSLA is the SLA commitment FROZEN onto one ticket at creation (the
// WorkflowVersionID precedent): it is written once and never updated, so a
// later policy edit cannot rewrite history. It is a pure value — no clock
// reads, no persistence concerns. A ticket without a TicketSLA has no SLA
// commitment at all (legacy tickets, or tickets created while SLA was
// disabled); it renders as "no SLA", never as retroactively breached.
//
// The two duration fields are the commitment the UI displays. The four
// instant fields are the SAME commitment resolved against the working
// calendar at freeze time (issue #211): with a calendar, "elapsed" is
// WORKING time, so the due and warning points must be frozen as instants —
// deriving them at projection time would force the calendar into
// ProjectSLA and destroy its purity (no clock reads, no I/O). Because the
// warning point is frozen too, a later sla_warning_percent edit applies
// to tickets created from then on — the same freeze rule the targets
// already follow. All four instants are UTC; the zero time marks the
// pre-0017 rows (see the migration) and reads as "no frozen SLA".
type TicketSLA struct {
	FirstResponseSeconds int
	ResolveSeconds       int
	// WarnFirstResponseAt is the frozen at_risk point of the first-response
	// milestone: the warning share of the target, in working time.
	WarnFirstResponseAt time.Time
	// DueFirstResponseAt is the frozen due point of the first-response
	// milestone.
	DueFirstResponseAt time.Time
	// WarnResolveAt is the frozen at_risk point of the resolution milestone.
	WarnResolveAt time.Time
	// DueResolveAt is the frozen due point of the resolution milestone.
	DueResolveAt time.Time
	// StartedAt anchors both milestone clocks (the ticket-creation instant).
	StartedAt time.Time
	// PolicySnapshotAt records when the targets were frozen onto the
	// ticket. Informational only: the targets are frozen, so the activation
	// instant is never load-bearing.
	PolicySnapshotAt time.Time
}
