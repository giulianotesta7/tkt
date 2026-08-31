package application

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// TicketService implements the ticket use cases (ticket-management,
// ticket-state-machine, audit-log specs): create, transition, update, and
// read. Every mutation is audited with the actor from the session (D14) and
// persisted through the TicketUnitOfWork port, which applies the ticket
// write and its audit events atomically — a failed audit append rolls the
// ticket mutation back (no-silent-mutations contract). Read paths use the
// TicketStore port; numbering is the store's concern (D8).
type TicketService struct {
	tickets    TicketStore
	users      UserStore
	categories CategoryStore
	tx         TicketUnitOfWork
	builder    *ViewBuilder
	clock      domain.Clock
	// Workflow create path (design S5): resolved version store, runner, and
	// workflow unit of work. Wired only by
	// NewTicketServiceWithWorkflowCreate — nil keeps the legacy create path so
	// cmd/server and the HTTP harness compile unchanged until the SQLite
	// adapters land (PR5 Batch B).
	versions   WorkflowVersionStore
	runner     *WorkflowRunner
	workflowTx WorkflowUnitOfWork
}

// NewTicketService wires the ticket use cases against the given ports: the
// ticket store (reads), user/category stores (validation refs), the
// unit-of-work (atomic ticket+audit mutations), the view builder (composed
// reads, D13), and the injected clock (D7). This constructor keeps the legacy
// create path; wire the workflow create path with
// NewTicketServiceWithWorkflowCreate.
func NewTicketService(tickets TicketStore, users UserStore, categories CategoryStore, tx TicketUnitOfWork, builder *ViewBuilder, clock domain.Clock) *TicketService {
	return newTicketService(tickets, users, categories, tx, builder, clock, nil, nil, nil)
}

// NewTicketServiceWithWorkflowCreate wires the atomic create+pin+run path
// (design S5): Create resolves the category's current published version, pins
// that exact version id on the ticket, plans the initial automatic advancement
// with the WorkflowRunner, and submits ONE CreateTicketWithRun plan to the
// WorkflowUnitOfWork. A category without a published version is unavailable for
// new tickets (exact 422 category ValidationError, no writes). SQLite
// implementations of WorkflowVersionStore/WorkflowUnitOfWork arrive with PR5
// Batch B; the application contract is served by fakes in Batch A.
func NewTicketServiceWithWorkflowCreate(tickets TicketStore, users UserStore, categories CategoryStore, tx TicketUnitOfWork, builder *ViewBuilder, clock domain.Clock, versions WorkflowVersionStore, runner *WorkflowRunner, workflowTx WorkflowUnitOfWork) *TicketService {
	return newTicketService(tickets, users, categories, tx, builder, clock, versions, runner, workflowTx)
}

func newTicketService(tickets TicketStore, users UserStore, categories CategoryStore, tx TicketUnitOfWork, builder *ViewBuilder, clock domain.Clock, versions WorkflowVersionStore, runner *WorkflowRunner, workflowTx WorkflowUnitOfWork) *TicketService {
	return &TicketService{
		tickets:    tickets,
		users:      users,
		categories: categories,
		tx:         tx,
		builder:    builder,
		clock:      clock,
		versions:   versions,
		runner:     runner,
		workflowTx: workflowTx,
	}
}

// CreateTicketInput is the creation payload. There is NO assignee input:
// Amendment 2 makes creation strictly unassigned (the handler rejects any
// assignee-carrying request before binding) so no application path can
// smuggle a creation-time assignee — person assignment happens only later
// through the pinned category workflow flow. There are no requester fields:
// the requester is ALWAYS the creating actor, derived from the session
// (ticket-management spec) — the caller can never file a ticket impersonating
// someone else.
type CreateTicketInput struct {
	Title       string
	Description string
	CategoryID  int64
	Priority    domain.Priority
}

// Create validates the payload, then persists ticket + created audit event
// in ONE unit-of-work call: the store assigns the ticket's ID and number
// (D8), stamps the event's TicketID, and rolls the ticket back if the audit
// append fails.
func (s *TicketService) Create(ctx context.Context, actor domain.User, in CreateTicketInput) (*domain.Ticket, error) {
	if strings.TrimSpace(in.Title) == "" {
		return nil, &domain.ValidationError{Field: "title", Message: domain.ErrMsgTitleRequired}
	}
	if !domain.IsValidPriority(in.Priority) {
		return nil, &domain.InvalidPriorityError{Field: "priority", Message: domain.ErrMsgInvalidPriority}
	}
	if _, err := s.categories.GetByID(ctx, in.CategoryID); err != nil {
		return nil, err
	}

	now := s.clock.Now()
	// Workflow create path (design S5): a category must have a published workflow
	// version to accept new tickets; the create pins that version and applies the
	// planned initial automatic advancement in one unit-of-work call. Without
	// published workflows the exact 422 category message is returned and nothing
	// is written.
	if s.workflowTx != nil {
		return s.createWithWorkflow(ctx, actor, in, now)
	}

	t, event := newCreateTicket(actor, in, now)
	if err := s.tx.Create(ctx, t, event); err != nil {
		return nil, err
	}
	return t, nil
}

// createWithWorkflow orchestrates the atomic create+pin+run contract: resolve
// the immutable current version once, require it (exact 422 category
// ValidationError when the category has no published workflow), pin the exact
// version id on the ticket, plan the initial automatic advancement, and submit
// ONE fixed CreateTicketWithRun plan to the WorkflowUnitOfWork. The service
// never retries, never falls back to the legacy create, and never writes
// itself — persistence atomicity is entirely the WorkflowUnitOfWork's
// responsibility (design S5 all-or-nothing: ticket + pin + created audit +
// active run + planned automatic operations).
func (s *TicketService) createWithWorkflow(ctx context.Context, actor domain.User, in CreateTicketInput, now time.Time) (*domain.Ticket, error) {
	pv, err := s.versions.GetCurrentVersion(ctx, in.CategoryID)
	if err != nil {
		return nil, err
	}
	if pv == nil {
		return nil, &domain.ValidationError{Field: "category", Message: domain.ErrMsgCategoryWorkflowUnavailable}
	}

	t, event := newCreateTicket(actor, in, now)
	// Deep-snapshot the untrusted published definition EXACTLY ONCE at the
	// application trust boundary (createWithWorkflow): pv.Workflow is
	// store/caller-owned memory the adapter may cache or alias, and a concurrent
	// publisher can replace it between reads. The runner-planning definition and
	// the persisted CreateTicketWithRunInput.Workflow are two INDEPENDENT clones
	// derived from THIS single trusted snapshot, so a mutation of the original
	// can never yield operations from snapshot A and a persisted workflow from
	// snapshot B. pv.Workflow is never read again after this capture, and the
	// version id is pinned by value for the same reason.
	trusted := pv.Workflow.Clone()
	ver := pv.VersionID
	t.WorkflowVersionID = &ver
	plan, err := s.runner.PlanInitialAutomatic(ctx, *t, trusted.Clone())
	if err != nil {
		return nil, err
	}
	input := CreateTicketWithRunInput{
		CategoryID:        in.CategoryID,
		ExpectedVersionID: pv.VersionID,
		Workflow:          trusted.Clone(),
		Ticket:            t,
		CreatedAudit:      event,
		StartedAt:         now,
		ExpectedCursor:    0,
		ExpectedRunStatus: "active",
		Operations:        plan.Operations,
		NextCursor:        plan.NextCursor,
		NextRunStatus:     plan.NextRunStatus,
		NextTicketState:   plan.NextTicketState,
	}
	if plan.NextRunStatus == "completed" {
		ct := now
		input.CompletedAt = &ct
	}
	return s.workflowTx.CreateTicketWithRun(ctx, input)
}

// newCreateTicket builds the creation aggregate and its created audit event:
// requester is ALWAYS the creating session actor, timestamps come from the
// injected clock, and the created audit carries the session actor identity.
func newCreateTicket(actor domain.User, in CreateTicketInput, now time.Time) (*domain.Ticket, domain.AuditEvent) {
	t := &domain.Ticket{
		Title:           strings.TrimSpace(in.Title),
		Description:     in.Description,
		RequesterName:   actor.Name,
		RequesterEmail:  actor.Email,
		RequesterUserID: &actor.ID,
		CategoryID:      in.CategoryID,
		Priority:        in.Priority,
		State:           domain.StateNew,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	event := domain.AuditEvent{
		Actor:       actor.Name,
		ActorUserID: &actor.ID,
		Action:      domain.ActionCreated,
		CreatedAt:   now,
	}
	return t, event
}

// Assign sets or clears the ticket's assignee under the person-only
// assignment rules (ticket-access-assignment spec): only agent+ roles may
// assign (CapAssignTicket); the target must be an ACTIVE agent-plus person;
// the initial assignment (unassigned → person) never requires a reason,
// while a reassignment (person A → person B) ALWAYS requires a non-empty
// reason recorded in the audit event with the session actor as actor.
// Clearing the assignment (person → unassigned) is allowed without a
// reason. The read is scoped per role — agents may claim an unassigned
// ticket or reassign their OWN ticket (ScopeAssignable), admin/root any
// ticket (ScopeAll) — so another agent's ticket is ErrNotFound (no
// existence leak). Ticket + audit event persist in ONE unit-of-work call.
func (s *TicketService) Assign(ctx context.Context, actor domain.User, ticketID int64, assigneeID *int64, reason string) (*domain.Ticket, error) {
	if !NewPolicy().Capabilities(actor.Role).Require(CapAssignTicket) {
		return nil, &domain.ValidationError{Field: "user", Message: domain.ErrMsgUserRoleCannotAssign}
	}
	t, err := s.tickets.GetByID(ctx, ticketID, assignQuery(actor))
	if err != nil {
		return nil, err
	}
	// A closed ticket (resolved/closed/cancelled) is read-only except for its
	// state transition: assignment is refused BEFORE any store mutation
	// (closed-ticket read-only spec).
	if domain.IsClosed(t.State) {
		return nil, domain.NewForbiddenError(domain.ErrMsgClosedTicketReadOnly)
	}
	if assigneeID != nil {
		if actor.Role == domain.RoleAgent && t.UserID == nil && *assigneeID != actor.ID {
			return nil, domain.NewForbiddenError("agents may only claim tickets for themselves")
		}
		user, err := s.users.GetByID(ctx, *assigneeID)
		if err != nil {
			return nil, err
		}
		if !user.Active {
			return nil, domain.NewInactiveUserError("user")
		}
		if !user.Role.AtLeast(domain.RoleAgent) {
			return nil, &domain.ValidationError{Field: "user", Message: domain.ErrMsgAssignTargetRole}
		}
	}
	// Same assignee (or both unassigned): no-op — no event, no refresh.
	if t.UserID != nil && assigneeID != nil && *t.UserID == *assigneeID {
		return t, nil
	}
	// Reassignment (person A → person B) always requires a non-empty reason.
	if t.UserID != nil && assigneeID != nil && strings.TrimSpace(reason) == "" {
		return nil, domain.NewReassignReasonRequiredError()
	}

	now := s.clock.Now()
	from := ""
	if t.UserID != nil {
		from = strconv.FormatInt(*t.UserID, 10)
	}
	to := ""
	if assigneeID != nil {
		to = strconv.FormatInt(*assigneeID, 10)
	}
	field := "user"
	event := domain.AuditEvent{
		TicketID:    t.ID,
		Actor:       actor.Name,
		ActorUserID: &actor.ID,
		Action:      domain.ActionUpdate,
		Field:       &field,
		FromValue:   &from,
		ToValue:     &to,
		CreatedAt:   now,
	}
	if r := strings.TrimSpace(reason); r != "" {
		event.Reason = &r
	}
	t.UserID = assigneeID
	t.UpdatedAt = now
	if err := s.tx.Update(ctx, t, event); err != nil {
		return nil, err
	}
	return t, nil
}

// Transition moves the ticket through the domain state machine, stamps the
// audit event with the session actor, and persists ticket + audit event in
// ONE unit-of-work call (atomic; a failed audit append rolls the transition
// back). Authorization is enforced server-side BEFORE the read or any state
// change: role user never transitions (ticket-state-machine spec), and the
// scoped read restricts agents to their own assigned tickets — an
// out-of-scope ticket is ErrNotFound (ticket-access spec).
func (s *TicketService) Transition(ctx context.Context, actor domain.User, ticketID int64, to domain.State, reason string) (*domain.Ticket, error) {
	if !NewPolicy().Capabilities(actor.Role).Require(CapEditTicket) {
		return nil, domain.NewForbiddenError(domain.ErrMsgUserCannotTransition)
	}
	t, err := s.tickets.GetByID(ctx, ticketID, scopedQuery(actor, TicketQuery{}))
	if err != nil {
		return nil, err
	}
	// Manual-closure gate (state-machine delta): a manual resolved -> closed
	// transition on a ticket that HAS a requester is rejected for every actor —
	// closure of a requester-owned resolution is exclusively the requester's
	// confirmation path. Requester-NULL tickets keep manual agent closure.
	from := t.State
	if from == domain.StateResolved && to == domain.StateClosed && t.RequesterUserID != nil {
		return nil, domain.NewForbiddenError(ErrMsgClosureRequiresConfirmation)
	}
	event, err := t.Transition(to, reason, s.clock.Now())
	if err != nil {
		return nil, err
	}
	event.Actor = actor.Name
	event.ActorUserID = &actor.ID
	// Closure attribution (audit-log delta): a manual agent closure of a
	// requester-NULL ticket is stamped manual_agent; requester-confirmed and
	// workflow-terminal closures attribute themselves elsewhere (D1).
	if to == domain.StateClosed && from == domain.StateResolved {
		via := domain.ClosureViaManualAgent
		event.ClosureVia = &via
	}
	if err := s.tx.Update(ctx, t, *event); err != nil {
		return nil, err
	}
	return t, nil
}

// Update applies field edits (title, priority). The description and the
// category are immutable after creation — they exist on the aggregate but
// the update surface (TicketUpdate) does not carry them, so they can never
// be changed or audited here. Assignment changes do NOT belong here: they
// go through Assign, which enforces the reason and target rules
// (ticket-access-assignment spec) — Update rejects assignment fields so the
// reassignment-reason rule cannot be bypassed through a generic edit.
// Authorization is enforced server-side BEFORE the read: role user never
// edits (design route policy: edit requires an assigned agent or
// admin/root); the scoped read restricts agents to their own assigned
// tickets (ticket-access spec).
func (s *TicketService) Update(ctx context.Context, actor domain.User, ticketID int64, u domain.TicketUpdate) (*domain.Ticket, error) {
	if !NewPolicy().Capabilities(actor.Role).Require(CapEditTicket) {
		return nil, domain.NewForbiddenError(domain.ErrMsgUserCannotEdit)
	}
	if u.UserID != nil || u.ClearUserID {
		return nil, &domain.ValidationError{Field: "user", Message: domain.ErrMsgAssignmentViaAssign}
	}
	t, err := s.tickets.GetByID(ctx, ticketID, scopedQuery(actor, TicketQuery{}))
	if err != nil {
		return nil, err
	}
	// A closed ticket (resolved/closed/cancelled) is read-only except for its
	// state transition: field edits are refused BEFORE any store mutation
	// (closed-ticket read-only spec).
	if domain.IsClosed(t.State) {
		return nil, domain.NewForbiddenError(domain.ErrMsgClosedTicketReadOnly)
	}

	events, err := t.ApplyUpdate(u, s.clock.Now())
	if err != nil {
		return nil, err
	}
	for i := range events {
		events[i].Actor = actor.Name
		events[i].ActorUserID = &actor.ID
	}
	if err := s.tx.Update(ctx, t, events...); err != nil {
		return nil, err
	}
	return t, nil
}

// GetByID returns the composed detail view — ticket, category, assigned
// user (inactive users stay visible), comment timeline, and audit history
// (D13) — scoped to the actor's ticket access scope, or a NotFoundError
// when the ticket is absent OR outside the actor's scope (ticket-access
// spec: direct lookup is denied for out-of-scope tickets). The comment
// visibility scope derives from the same session role: only agents+ include
// internal (staff-only) comments (comment-visibility spec).
func (s *TicketService) GetByID(ctx context.Context, actor domain.User, id int64) (*TicketView, error) {
	includeInternal := NewPolicy().Capabilities(actor.Role).Require(CapCommentInternal)
	// Read-only detail uses the widened READ scope (ScopeAssignedOrClaimable for
	// agents) so a claim-pending ticket on the actor's desk is visible; the
	// mutation paths keep the strict scopedQuery and never inherit this widening.
	return s.builder.TicketView(ctx, id, readQuery(actor, TicketQuery{}), includeInternal)
}
