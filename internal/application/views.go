package application

import (
	"context"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// TicketView is the detail-page view model (D13): the store ports return
// domain types and the application assembles the view via ref lookups.
// AssignedUser may be inactive — historical display (user-management spec).
type TicketView struct {
	Ticket       *domain.Ticket
	Category     *domain.Category
	AssignedUser *domain.User // nil when the ticket is unassigned
	Comments     []domain.Comment
	AuditEvents  []domain.AuditEvent
}

// ViewBuilder assembles TicketViews. N+1 lookups (~3 queries per page) are
// fine at MVP scale (D13).
type ViewBuilder struct {
	tickets    TicketStore
	users      UserStore
	categories CategoryStore
	comments   CommentStore
	audits     AuditStore
}

// NewViewBuilder wires the view assembly against the given ports.
func NewViewBuilder(tickets TicketStore, users UserStore, categories CategoryStore, comments CommentStore, audits AuditStore) *ViewBuilder {
	return &ViewBuilder{
		tickets:    tickets,
		users:      users,
		categories: categories,
		comments:   comments,
		audits:     audits,
	}
}

// TicketView composes the ticket with its category, assigned user (if any),
// chronological comment timeline, and audit history.
func (b *ViewBuilder) TicketView(ctx context.Context, ticketID int64) (*TicketView, error) {
	t, err := b.tickets.GetByID(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	cat, err := b.categories.GetByID(ctx, t.CategoryID)
	if err != nil {
		return nil, err
	}
	view := &TicketView{Ticket: t, Category: cat}
	if t.UserID != nil {
		user, err := b.users.GetByID(ctx, *t.UserID)
		if err != nil {
			return nil, err
		}
		view.AssignedUser = user
	}
	comments, err := b.comments.ListByTicket(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	view.Comments = comments
	events, err := b.audits.ListByTicket(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	view.AuditEvents = events
	return view, nil
}
