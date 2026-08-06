package application

import (
	"context"
	"strings"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// CommentService implements the comment timeline use cases
// (comment-timeline spec): add a comment to any existing ticket regardless
// of state, list a ticket's comments in creation order. Comments are
// append-only in the MVP — there is no update or delete.
type CommentService struct {
	tickets  TicketStore
	comments CommentStore
	clock    domain.Clock
}

// NewCommentService wires the comment use cases against the given ports.
func NewCommentService(tickets TicketStore, comments CommentStore, clock domain.Clock) *CommentService {
	return &CommentService{tickets: tickets, comments: comments, clock: clock}
}

// Add validates the body, checks the ticket exists, and stores the comment
// with the session user as author (D14).
func (s *CommentService) Add(ctx context.Context, actor domain.User, ticketID int64, body string) (*domain.Comment, error) {
	if strings.TrimSpace(body) == "" {
		return nil, &domain.ValidationError{Field: "body", Message: domain.ErrMsgCommentBodyRequired}
	}
	if _, err := s.tickets.GetByID(ctx, ticketID); err != nil {
		return nil, err
	}
	c := &domain.Comment{
		TicketID:  ticketID,
		Author:    actor.Name,
		Body:      strings.TrimSpace(body),
		CreatedAt: s.clock.Now(),
	}
	if err := s.comments.Add(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// ListByTicket returns the ticket's comments in creation order (ASC).
func (s *CommentService) ListByTicket(ctx context.Context, ticketID int64) ([]domain.Comment, error) {
	return s.comments.ListByTicket(ctx, ticketID)
}
