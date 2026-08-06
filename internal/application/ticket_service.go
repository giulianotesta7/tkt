package application

import (
	"context"
	"strings"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// TicketService implements the ticket use cases (ticket-management,
// ticket-state-machine, audit-log specs): create, transition, update, and
// read. Mutations are audited with the actor from the session (D14) and
// persisted through the store ports; numbering is the store's concern (D8).
type TicketService struct {
	tickets    TicketStore
	users      UserStore
	categories CategoryStore
	audits     AuditStore
	clock      domain.Clock
}

// NewTicketService wires the ticket use cases against the given ports.
func NewTicketService(tickets TicketStore, users UserStore, categories CategoryStore, audits AuditStore, clock domain.Clock) *TicketService {
	return &TicketService{
		tickets:    tickets,
		users:      users,
		categories: categories,
		audits:     audits,
		clock:      clock,
	}
}

// CreateTicketInput is the creation payload. UserID is optional (nil =
// unassigned). The requester fields are free text, not linked to a user
// account (ticket-management spec).
type CreateTicketInput struct {
	Title          string
	Description    string
	RequesterName  string
	RequesterEmail string
	CategoryID     int64
	UserID         *int64
	Priority       domain.Priority
}

// Create validates the payload, stores the ticket in state new (number and
// ID assigned by the store, D8), and appends the created audit event with
// the session actor.
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
		Title:          strings.TrimSpace(in.Title),
		Description:    in.Description,
		RequesterName:  in.RequesterName,
		RequesterEmail: in.RequesterEmail,
		CategoryID:     in.CategoryID,
		UserID:         in.UserID,
		Priority:       in.Priority,
		State:          domain.StateNew,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.tickets.Create(ctx, t); err != nil {
		return nil, err
	}
	event := domain.AuditEvent{
		TicketID:  t.ID,
		Actor:     actor.Name,
		Action:    domain.ActionCreated,
		CreatedAt: now,
	}
	if err := s.audits.Append(ctx, event); err != nil {
		return nil, err
	}
	return t, nil
}

// Transition moves the ticket through the domain state machine, stamps the
// audit event with the session actor, and persists ticket + audit event
// (atomicity is the store port's contract).
func (s *TicketService) Transition(ctx context.Context, actor domain.User, ticketID int64, to domain.State, reason string) (*domain.Ticket, error) {
	t, err := s.tickets.GetByID(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	event, err := t.Transition(to, reason, s.clock.Now())
	if err != nil {
		return nil, err
	}
	event.Actor = actor.Name
	if err := s.tickets.Update(ctx, t); err != nil {
		return nil, err
	}
	if err := s.audits.Append(ctx, *event); err != nil {
		return nil, err
	}
	return t, nil
}

// Update applies field edits. Category and user edits are validated as in
// creation (existence + active user). Each changed field appends its own
// audit event stamped with the session actor; a rejected edit changes
// nothing.
func (s *TicketService) Update(ctx context.Context, actor domain.User, ticketID int64, u domain.TicketUpdate) (*domain.Ticket, error) {
	t, err := s.tickets.GetByID(ctx, ticketID)
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
	if err := s.tickets.Update(ctx, t); err != nil {
		return nil, err
	}
	if len(events) > 0 {
		if err := s.audits.Append(ctx, events...); err != nil {
			return nil, err
		}
	}
	return t, nil
}

// GetByID returns a stored ticket or a NotFoundError.
func (s *TicketService) GetByID(ctx context.Context, id int64) (*domain.Ticket, error) {
	return s.tickets.GetByID(ctx, id)
}
