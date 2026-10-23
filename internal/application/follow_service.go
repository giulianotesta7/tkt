package application

import (
	"context"
	"sort"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// ErrMsgFollowersStaffOnly refuses a follow operation to a requester. Following
// a ticket is a staff subscription to its events, so only a role that already
// works inside the ticket can hold one.
const ErrMsgFollowersStaffOnly = "only staff can follow a ticket"

// FollowService implements ticket following and mentions (issue #418).
//
// Following is a personal subscription, never an access grant: the actor must
// already be able to open the ticket (canStaffOpenTicket — the ticket detail
// route's own read scope), and every notification path re-checks the same
// predicate before it tells a follower anything. Two people can therefore never
// learn about a ticket through following: a follower whose role or scope stops
// reaching the ticket is skipped silently, and their row stays behind.
//
// Following and unfollowing are idempotent, because both describe a relation
// ("this person follows this ticket") rather than an event. Unfollowing stays
// available to a staff member who lost access to the ticket, so nobody is stuck
// subscribed to a ticket they cannot open.
//
// Mentions reuse the same population: MentionCandidates is every OTHER active
// staff member who can open the ticket right now, and that one list backs both
// ways the interface offers a mention (typing @ or picking the person). The
// grammar that decides whether a comment mentions somebody lives in mentions.go.
type FollowService struct {
	tickets   TicketStore
	users     UserStore
	followers TicketFollowerStore
	clock     domain.Clock
}

// NewFollowService wires the follow and mention use cases against the given
// ports. The clock stamps the instant a follow was created.
func NewFollowService(tickets TicketStore, users UserStore, followers TicketFollowerStore, clock domain.Clock) *FollowService {
	return &FollowService{tickets: tickets, users: users, followers: followers, clock: clock}
}

// Follow subscribes the actor to the ticket's events.
//
// Anyone who fails the access predicate is answered with NotFound, the same
// answer an out-of-scope ticket gives everywhere else: a requester, an agent the
// ticket is not assigned to, an inactive account and a missing ticket are
// indistinguishable, so the follow control reveals nothing about a ticket the
// actor cannot open.
func (s *FollowService) Follow(ctx context.Context, actor domain.User, ticketID int64) error {
	ok, err := canStaffOpenTicket(ctx, s.tickets, actor, ticketID)
	if err != nil {
		return err
	}
	if !ok {
		return &domain.NotFoundError{Kind: "ticket", ID: ticketID}
	}
	return s.followers.Follow(ctx, ticketID, actor.ID, s.clock.Now())
}

// Unfollow removes the actor's subscription. It is allowed even when the actor
// can no longer open the ticket: leaving a list is not reading the ticket, and a
// follower who was reassigned away must be able to stop the subscription without
// asking anybody. Only staff can hold a subscription in the first place.
func (s *FollowService) Unfollow(ctx context.Context, actor domain.User, ticketID int64) error {
	if !NewPolicy().Capabilities(actor.Role).Require(CapCommentInternal) {
		return domain.NewForbiddenError(ErrMsgFollowersStaffOnly)
	}
	return s.followers.Unfollow(ctx, ticketID, actor.ID)
}

// IsFollowing reports whether the actor follows the ticket. The read is scoped
// like the follow itself, so a person who cannot open the ticket learns nothing
// — not even whether they are (still) on its list.
func (s *FollowService) IsFollowing(ctx context.Context, actor domain.User, ticketID int64) (bool, error) {
	ok, err := canStaffOpenTicket(ctx, s.tickets, actor, ticketID)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, &domain.NotFoundError{Kind: "ticket", ID: ticketID}
	}
	return s.followers.IsFollowing(ctx, ticketID, actor.ID)
}

// Followers returns the ticket's followers for the Properties row, ordered by
// display name. Only somebody who can open the ticket may read the list.
func (s *FollowService) Followers(ctx context.Context, actor domain.User, ticketID int64) ([]domain.User, error) {
	ok, err := canStaffOpenTicket(ctx, s.tickets, actor, ticketID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &domain.NotFoundError{Kind: "ticket", ID: ticketID}
	}
	return s.followers.ListFollowers(ctx, ticketID)
}

// MentionCandidates returns the staff a comment on the ticket may mention: every
// OTHER active staff member who can open the ticket right now, ordered by display
// name. The list is the offer of the @ completion and of the mention control, so
// both show the same people, and nobody can be mentioned who would not be able
// to read the comment.
//
// The actor's own access is checked first: the list describes this ticket's
// staff, and a person who cannot open the ticket gets NotFound rather than a few
// names.
func (s *FollowService) MentionCandidates(ctx context.Context, actor domain.User, ticketID int64) ([]domain.User, error) {
	candidates, err := s.mentionableStaff(ctx, actor, ticketID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.User, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, c.user)
	}
	return out, nil
}

// staffCandidate is one staff member who can open the ticket right now, with the
// proof the notification batch requires. Keeping the proof and the person
// together is what stops the mention fan-out from subscribing or notifying
// anybody the candidate list would not have offered.
type staffCandidate struct {
	user     domain.User
	verified verifiedRecipient
}

// mentionableStaff is the shared candidate query of MentionCandidates and of the
// comment mention fan-out.
func (s *FollowService) mentionableStaff(ctx context.Context, actor domain.User, ticketID int64) ([]staffCandidate, error) {
	ok, err := canStaffOpenTicket(ctx, s.tickets, actor, ticketID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &domain.NotFoundError{Kind: "ticket", ID: ticketID}
	}
	staff, err := s.users.ListActive(ctx)
	if err != nil {
		return nil, err
	}
	var out []staffCandidate
	for _, u := range staff {
		if u.ID == actor.ID {
			continue
		}
		// The predicate is re-evaluated for every candidate at THIS instant, so a
		// downgraded, deactivated or reassigned person drops out of the list.
		can, err := canStaffOpenTicket(ctx, s.tickets, u, ticketID)
		if err != nil {
			return nil, err
		}
		if !can {
			continue
		}
		out = append(out, staffCandidate{user: u, verified: verifiedRecipient{recipient: *recipientOf(&u)}})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].user.Name != out[j].user.Name {
			return out[i].user.Name < out[j].user.Name
		}
		return out[i].user.ID < out[j].user.ID
	})
	return out, nil
}

// followerRecipients reads the ticket's followers and keeps the ones who may
// still be told about it. Each follower goes through the same predicate as the
// follow control, at notification time: a follower who was reassigned away,
// demoted or deactivated is simply missing from the batch, and their row is left
// untouched for the day they can open the ticket again.
//
// A nil service is the wiring without follower fan-out, so every caller can
// chain the collaborator away and keep the pre-existing behaviour.
func (s *FollowService) followerRecipients(ctx context.Context, ticketID int64) ([]verifiedRecipient, error) {
	if s == nil {
		return nil, nil
	}
	followersOf, err := s.followers.ListFollowers(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	var out []verifiedRecipient
	for _, u := range followersOf {
		can, err := canStaffOpenTicket(ctx, s.tickets, u, ticketID)
		if err != nil {
			return nil, err
		}
		if !can {
			continue
		}
		out = append(out, verifiedRecipient{recipient: *recipientOf(&u)})
	}
	return out, nil
}

// mentionFanOut resolves the mentions of one comment: the people the body names,
// in candidate order, together with the proof their access carries. It returns
// both lists because the comment needs both — the mentioned people are notified,
// and the same people become followers in the comment's transaction.
//
// Only a candidate can be mentioned: an @Name of anybody else does nothing, so a
// mention can never reach a requester, an inactive account or a staff member the
// ticket is out of reach for.
func (s *FollowService) mentionFanOut(ctx context.Context, actor domain.User, ticketID int64, body string) ([]domain.User, []verifiedRecipient, error) {
	if s == nil {
		return nil, nil, nil
	}
	candidates, err := s.mentionableStaff(ctx, actor, ticketID)
	if err != nil {
		return nil, nil, err
	}
	var people []domain.User
	var verified []verifiedRecipient
	for _, c := range candidates {
		if !mentionsName(body, c.user.Name) {
			continue
		}
		people = append(people, c.user)
		verified = append(verified, c.verified)
	}
	return people, verified, nil
}
