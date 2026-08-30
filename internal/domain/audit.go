package domain

import "time"

// Audit action identifiers (audit-log spec). ActionWorkflowStep is LEGACY:
// historical rows keep it readable, but no new event may emit it — human
// workflow completions use the closed semantic vocabulary below so the
// timeline can render each completion's exact meaning.
const (
	ActionCreated               = "created"
	ActionTransition            = "transition"
	ActionUpdate                = "update"
	ActionWorkflowStep          = "workflow_step" // legacy read-only
	ActionWorkflowAssignment    = "workflow_assignment"
	ActionWorkflowManualTask    = "workflow_manual_task"
	ActionWorkflowRequesterForm = "workflow_requester_form"
	ActionWorkflowAssigneeForm  = "workflow_assignee_form"
)

// Closure attribution values for the closure transition audit event (issue
// #55, audit-log spec Closure Attribution). The third closure path — the
// workflow terminal — carries NO closure_via value: it is attributed by the
// existing workflow actor convention (actor "workflow", NULL ActorUserID).
const (
	// ClosureViaRequesterConfirmation marks a resolved -> closed event
	// created by the ticket requester confirming the resolution
	// (TicketService.ConfirmResolution).
	ClosureViaRequesterConfirmation = "requester_confirmation"
	// ClosureViaManualAgent marks a resolved -> closed event created by an
	// authorized agent closing a requester-NULL ticket manually
	// (TicketService.Transition).
	ClosureViaManualAgent = "manual_agent"
)

// AuditEvent records a single mutation on a ticket. The domain fills
// TicketID, Action, Field, FromValue/ToValue, Note and CreatedAt; the
// application layer stamps Actor/ActorUserID from the session (D14) and
// Reason where a reason is mandatory (reassignment — ticket-access spec).
// Field names the changed field ("state" for transitions, or the edited
// field for updates).
type AuditEvent struct {
	TicketID int64
	Actor    string
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
	Reason *string
	// ClosureVia attributes the closure path of a closure transition event
	// (issue #55): ClosureViaRequesterConfirmation for a requester-confirmed
	// closure, ClosureViaManualAgent for a manual agent closure of a
	// requester-NULL ticket. NULL for every other event: workflow-terminal
	// closures stay NULL and remain attributed by the workflow actor
	// convention (actor "workflow", NULL ActorUserID), and all pre-0010 rows
	// read back NULL (no backfill — pre-attribution history keeps the actor
	// convention as its only provenance).
	ClosureVia *string
	// DeskID is the workflow desk context of a contextual workflow_assignment
	// event (the pinned assign_to_desk step's desk); NULL for every other
	// action and for legacy rows written before migration 0007.
	DeskID *int64
	// StepIndex is the sealed zero-based pinned step index of a semantic
	// workflow event (workflow_requester_form, workflow_assignee_form,
	// workflow_manual_task, contextual workflow_assignment); NULL for state
	// transitions, non-flow audits, and all pre-0008 rows. It is the ONLY
	// correlation key between an audit row and its pinned step context —
	// never timestamps or occurrence order.
	StepIndex *int
	CreatedAt time.Time
}
