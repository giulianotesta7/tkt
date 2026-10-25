package application

import (
	"context"
	"strings"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// CommentService implements the comment timeline use cases
// (comment-timeline spec): add a comment to any existing ticket regardless
// of state, list a ticket's comments in creation order. Comments are
// append-only in the MVP — there is no update or delete.
type CommentService struct {
	tickets  TicketStore
	comments CommentStore
	// users resolves the notification policy facts (role, active state) of the
	// two S10 comment recipients: the ticket's requester and its assignee.
	users UserStore
	// tx writes the comment and its notifications as ONE transaction (issue
	// #344 specs S10, S12): a comment and the news about it commit or roll back
	// together.
	tx    CommentUnitOfWork
	clock domain.Clock
	// sla shifts the resolution SLA when the requester's reply ends a hold
	// (issue #352). Nil keeps the legacy wiring: the hold still ends, with no
	// shift, exactly like a TicketService built without an SLA service.
	sla *SLAService
	// followers resolves the staff a comment mentions and the staff subscribed
	// to the ticket (issue #418). Nil keeps the wiring without either fan-out.
	followers *FollowService
}

// NewCommentService wires the comment use cases against the given ports.
func NewCommentService(tickets TicketStore, comments CommentStore, users UserStore, tx CommentUnitOfWork, clock domain.Clock) *CommentService {
	return &CommentService{tickets: tickets, comments: comments, users: users, tx: tx, clock: clock}
}

// WithSLA wires the SLA service the hold resume uses (issue #352) and returns
// the service, so the composition root chains it onto the constructor.
func (s *CommentService) WithSLA(sla *SLAService) *CommentService {
	s.sla = sla
	return s
}

// WithFollowers wires the follower and mention collaborator (issue #418) and
// returns the service, so the composition root chains it onto the constructor.
// Without it a comment notifies the requester and the assignee only, and nothing
// is mentioned or subscribed.
func (s *CommentService) WithFollowers(followers *FollowService) *CommentService {
	s.followers = followers
	return s
}

// Add validates the body and visibility, checks the ticket exists within the
// actor's ticket access scope (ticket-access spec: comments live on tickets
// the actor can see), and stores the comment with the session user as author
// (D14) TOGETHER with the notifications it produces (issue #344 specs S10,
// S12): the comment, its notifications, and the full-text reindex the insert
// triggers all share one transaction, so a refused notification leaves no
// comment and a stored comment always carries its news. The comment also
// carries the actor's user id and role snapshot, so agent+ authorship stays
// provable for the first-response SLA milestone (issue #211). Visibility
// follows the comment-visibility spec: role user may only add public comments —
// an internal comment is denied (ForbiddenError) BEFORE any store call; roles
// agent+ may add public or internal comments.
func (s *CommentService) Add(ctx context.Context, actor domain.User, ticketID int64, body, visibility string) (*domain.Comment, error) {
	return s.AddWithAttachments(ctx, actor, ticketID, body, visibility, nil)
}

// AddWithAttachments is Add with the comment's files (issue #366): they are
// validated with the body and visibility, before any store call, and written
// in the comment's own transaction. Their visibility is the comment's.
func (s *CommentService) AddWithAttachments(ctx context.Context, actor domain.User, ticketID int64, body, visibility string, uploads []domain.AttachmentUpload) (*domain.Comment, error) {
	if strings.TrimSpace(body) == "" {
		return nil, &domain.ValidationError{Field: "body", Message: domain.ErrMsgCommentBodyRequired, Key: "error.comment_body_required"}
	}
	vis, err := parseCommentVisibility(visibility)
	if err != nil {
		return nil, err
	}
	files, err := stampedAttachments(uploads, actor, s.clock.Now())
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
		TicketID:     ticketID,
		Author:       actor.Name,
		AuthorUserID: &actor.ID,
		AuthorRole:   actor.Role,
		Body:         strings.TrimSpace(body),
		Visibility:   vis,
		CreatedAt:    s.clock.Now(),
	}
	// Comment notifications (S10 comment rows, S12): the recipients are
	// computed HERE from policy and roles and handed to the same write that
	// inserts the comment, so the comment and the news about it commit — or roll
	// back — together (S11). A recipient lookup failure aborts BEFORE the write:
	// a comment is never stored with a notification whose eligibility was not
	// proven.
	//
	// Mentions (issue #418) are part of that same write: only a STAFF author
	// mentions anybody — the mention list is a staff list, so a requester typing
	// "@Name" notifies nobody — and every mentioned person becomes a follower of
	// the ticket in the comment's transaction, so a comment that is not stored
	// subscribes nobody.
	r, err := ticketChangeRecipients(ctx, s.users, s.followers, t)
	if err != nil {
		return nil, err
	}
	var newFollowers []int64
	if NewPolicy().Capabilities(actor.Role).Require(CapCommentInternal) {
		mentioned, verified, merr := s.followers.mentionFanOut(ctx, actor, ticketID, c.Body)
		if merr != nil {
			return nil, merr
		}
		r.mentioned = verified
		for _, u := range mentioned {
			newFollowers = append(newFollowers, u.ID)
		}
	}
	notifications := commentNotifications(t, vis, &actor.ID, c.CreatedAt, r)
	resume, err := s.requesterReplyResume(ctx, actor, t, vis, c.CreatedAt)
	if err != nil {
		return nil, err
	}
	if err := s.tx.Add(ctx, CommentMutation{Comment: c, Notifications: notifications, Followers: newFollowers, Resume: resume, Attachments: files}); err != nil {
		return nil, err
	}
	return c, nil
}

// requesterReplyResume is the hold the requester's reply ends (issue #352,
// S1): a PUBLIC comment by the ticket's requester on a ticket on hold resumes
// it, at the comment's own instant, with the resolution SLA shifted by the
// working time spent on hold. Staff comments and internal comments never
// resume — the hold waits on the requester, not on the team. Anything else
// answers nil and the comment is written exactly as before.
func (s *CommentService) requesterReplyResume(ctx context.Context, actor domain.User, t *domain.Ticket, vis domain.CommentVisibility, at time.Time) (*HoldResume, error) {
	if vis != domain.CommentPublic || !t.IsOnHold() || !isTicketRequester(actor, t) {
		return nil, nil
	}
	expected := *t.OnHoldSince
	pausedAt, event, err := t.ResumeFromHold(at)
	if err != nil {
		return nil, err
	}
	event.Actor = actor.Name
	event.ActorUserID = &actor.ID
	var shifted *domain.TicketSLA
	if s.sla != nil {
		if shifted, err = s.sla.ShiftForHold(ctx, t.ID, pausedAt, at); err != nil {
			return nil, err
		}
	}
	return &HoldResume{Ticket: t, ExpectedOnHoldSince: &expected, ShiftedSLA: shifted, Events: []domain.AuditEvent{*event}}, nil
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
		return "", &domain.ValidationError{Field: "visibility", Message: domain.ErrMsgCommentVisibilityInvalid, Key: "error.comment_visibility_invalid"}
	}
}

// ListByTicket returns the ticket's comments in creation order (ASC),
// restricted to the actor's comment visibility: includeInternal=false
// excludes internal (staff-only) comments at the store boundary so a
// user-role actor never receives their content (comment-visibility spec).
func (s *CommentService) ListByTicket(ctx context.Context, ticketID int64, includeInternal bool) ([]domain.Comment, error) {
	return s.comments.ListByTicket(ctx, ticketID, includeInternal)
}
