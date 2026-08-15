package application

import (
	"context"
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
		Actor:     actor.Name,
		Action:    domain.ActionCreated,
		CreatedAt: now,
	}
	if err := s.tx.Create(ctx, t, event); err != nil {
		return nil, err
	}
	return t, nil
}

// Transition moves the ticket through the domain state machine, stamps the
// audit event with the session actor, and persists ticket + audit event in
// ONE unit-of-work call (atomic; a failed audit append rolls the transition
// back). The read is scoped to the actor: an out-of-scope ticket is
// ErrNotFound before any state change (ticket-access spec).
func (s *TicketService) Transition(ctx context.Context, actor domain.User, ticketID int64, to domain.State, reason string) (*domain.Ticket, error) {
	t, err := s.tickets.GetByID(ctx, ticketID, scopedQuery(actor, TicketQuery{}))
	if err != nil {
		return nil, err
	}
	event, err := t.Transition(to, reason, s.clock.Now())
	if err != nil {
		return nil, err
	}
	event.Actor = actor.Name
	if err := s.tx.Update(ctx, t, *event); err != nil {
		return nil, err
	}
	return t, nil
}

// Update applies field edits. Category and user edits are validated as in
// creation (existence + active user). Each changed field appends its own
// audit event stamped with the session actor; a rejected edit changes
// nothing. Ticket + event batch persist in ONE unit-of-work call. The read
// is scoped to the actor: an out-of-scope ticket is ErrNotFound before any
// edit (ticket-access spec).
func (s *TicketService) Update(ctx context.Context, actor domain.User, ticketID int64, u domain.TicketUpdate) (*domain.Ticket, error) {
	t, err := s.tickets.GetByID(ctx, ticketID, scopedQuery(actor, TicketQuery{}))
	if err != nil {
		return nil, err
	}
	if u.CategoryID != nil {
		if _, err := s.categories.GetByID(ctx, *u.CategoryID); err != nil {
			return nil, err
		}
	}
	if u.UserID != nil {
		user, err := s.users.GetByID(ctx, *u.UserID)
		if err != nil {
			return nil, err
		}
		if !user.Active {
			return nil, domain.NewInactiveUserError("user")
		}
	}

	events, err := t.ApplyUpdate(u, s.clock.Now())
	if err != nil {
		return nil, err
	}
	for i := range events {
		events[i].Actor = actor.Name
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
// spec: direct lookup is denied for out-of-scope tickets).
func (s *TicketService) GetByID(ctx context.Context, actor domain.User, id int64) (*TicketView, error) {
	return s.builder.TicketView(ctx, id, scopedQuery(actor, TicketQuery{}))
}
