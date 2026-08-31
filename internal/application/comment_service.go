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
	t, err := s.tickets.GetByID(ctx, ticketID, scopedQuery(actor, TicketQuery{}))
	if err != nil {
		return nil, err
	}
	// Closed-state guard (comment-timeline delta): closed and cancelled tickets
	// reject every actor, and a requester-NULL resolved ticket also has no one to
	// admit — the identity predicate is false without a requester. The one
	// carve-out: the requester of a RESOLVED ticket keeps an active voice while
	// awaiting confirmation. The guard runs at the application boundary BEFORE
	// any comment store call, so a forged POST cannot append to a closed ticket;
	// the HTTP layer maps the rejection to 403.
	if domain.IsClosed(t.State) {
		if t.State == domain.StateResolved && isTicketRequester(actor, t) {
			// Carve-out: fall through to the normal visibility/author rules.
		} else {
			return nil, domain.NewForbiddenError(domain.ErrMsgCommentOnClosedTicket)
		}
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

// ListByTicket returns the ticket's comments in creation order (ASC),
// restricted to the actor's comment visibility: includeInternal=false
// excludes internal (staff-only) comments at the store boundary so a
// user-role actor never receives their content (comment-visibility spec).
func (s *CommentService) ListByTicket(ctx context.Context, ticketID int64, includeInternal bool) ([]domain.Comment, error) {
	return s.comments.ListByTicket(ctx, ticketID, includeInternal)
}
