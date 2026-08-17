package application

import (
	"context"
	"strconv"
	"strings"

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
}

// NewTicketService wires the ticket use cases against the given ports: the
// ticket store (reads), user/category stores (validation refs), the
// unit-of-work (atomic ticket+audit mutations), the view builder (composed
// reads, D13), and the injected clock (D7).
func NewTicketService(tickets TicketStore, users UserStore, categories CategoryStore, tx TicketUnitOfWork, builder *ViewBuilder, clock domain.Clock) *TicketService {
	return &TicketService{
		tickets:    tickets,
		users:      users,
		categories: categories,
		tx:         tx,
		builder:    builder,
		clock:      clock,
	}
}

// CreateTicketInput is the creation payload. UserID is optional (nil =
// unassigned). There are no requester fields: the requester is ALWAYS the
// creating actor, derived from the session (ticket-management spec) — the
// caller can never file a ticket impersonating someone else.
type CreateTicketInput struct {
	Title       string
	Description string
	CategoryID  int64
	UserID      *int64
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
	// Assignment inputs are accepted only from agent+ roles; a user-role
	// actor's ticket always starts unassigned (ticket-management spec).
	if in.UserID != nil && !actor.Role.AtLeast(domain.RoleAgent) {
		return nil, &domain.ValidationError{Field: "user", Message: domain.ErrMsgUserRoleCannotAssign}
	}
	if _, err := s.categories.GetByID(ctx, in.CategoryID); err != nil {
		return nil, err
	}
	if in.UserID != nil {
		user, err := s.users.GetByID(ctx, *in.UserID)
		if err != nil {
			return nil, err
		}
		if !user.Active {
			return nil, domain.NewInactiveUserError("user")
		}
		// The assignment target rule applies at creation too: tickets are
		// assigned to agent-plus personnel only, never to a user-role
		// account (ticket-access-assignment spec).
		if !user.Role.AtLeast(domain.RoleAgent) {
			return nil, &domain.ValidationError{Field: "user", Message: domain.ErrMsgAssignTargetRole}
		}
	}

	now := s.clock.Now()
	t := &domain.Ticket{
		Title:           strings.TrimSpace(in.Title),
		Description:     in.Description,
		RequesterName:   actor.Name,
		RequesterEmail:  actor.Email,
		RequesterUserID: &actor.ID,
		CategoryID:      in.CategoryID,
		UserID:          in.UserID,
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
	if err := s.tx.Create(ctx, t, event); err != nil {
		return nil, err
	}
	return t, nil
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
	event, err := t.Transition(to, reason, s.clock.Now())
	if err != nil {
		return nil, err
	}
	event.Actor = actor.Name
	event.ActorUserID = &actor.ID
	if err := s.tx.Update(ctx, t, *event); err != nil {
		return nil, err
	}
	return t, nil
}

// Update applies field edits (title, category, priority). The description
// is immutable after creation — it exists on the aggregate but the update
// surface (TicketUpdate) does not carry it, so it can never be changed or
// audited here. Assignment changes do NOT belong here: they go through
// Assign, which enforces the reason and target rules
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
	if u.CategoryID != nil {
		if _, err := s.categories.GetByID(ctx, *u.CategoryID); err != nil {
			return nil, err
		}
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
	return s.builder.TicketView(ctx, id, scopedQuery(actor, TicketQuery{}), includeInternal)
}
