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

// Add validates the body and visibility, checks the ticket exists within the
// actor's ticket access scope (ticket-access spec: comments live on tickets
// the actor can see), and stores the comment with the session user as author
// (D14). Visibility follows the comment-visibility spec: role user may only
// add public comments — an internal comment is denied (ForbiddenError)
// BEFORE any store call; roles agent+ may add public or internal comments.
func (s *CommentService) Add(ctx context.Context, actor domain.User, ticketID int64, body, visibility string) (*domain.Comment, error) {
	if strings.TrimSpace(body) == "" {
		return nil, &domain.ValidationError{Field: "body", Message: domain.ErrMsgCommentBodyRequired}
	}
	vis, err := parseCommentVisibility(visibility)
	if err != nil {
		return nil, err
	}
	if vis == domain.CommentInternal && !NewPolicy().Capabilities(actor.Role).Require(CapCommentInternal) {
		return nil, domain.NewForbiddenError(domain.ErrMsgUserCannotCommentInternal)
	}
	if _, err := s.tickets.GetByID(ctx, ticketID, scopedQuery(actor, TicketQuery{})); err != nil {
		return nil, err
	}
	c := &domain.Comment{
		TicketID:   ticketID,
		Author:     actor.Name,
		Body:       strings.TrimSpace(body),
		Visibility: vis,
		CreatedAt:  s.clock.Now(),
	}
	if err := s.comments.Add(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// parseCommentVisibility maps the raw form value onto the domain type. An
// omitted value defaults to public (migration 0003 default: legacy comments
// backfill to public); anything else is rejected — a forged visibility never
// silently upgrades a comment's audience.
func parseCommentVisibility(raw string) (domain.CommentVisibility, error) {
	switch strings.TrimSpace(raw) {
	case "", string(domain.CommentPublic):
		return domain.CommentPublic, nil
	case string(domain.CommentInternal):
		return domain.CommentInternal, nil
	default:
		return "", &domain.ValidationError{Field: "visibility", Message: domain.ErrMsgCommentVisibilityInvalid}
	}
}

// ListByTicket returns the ticket's comments in creation order (ASC).
func (s *CommentService) ListByTicket(ctx context.Context, ticketID int64) ([]domain.Comment, error) {
	return s.comments.ListByTicket(ctx, ticketID)
}
