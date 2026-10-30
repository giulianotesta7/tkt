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
	// desks answers the membership question the workflow fan-out asks about the
	// desk a claim still waits on. Nil keeps the service without that proof, and
	// an unproven desk recipient is never told (fail closed).
	desks DeskMembershipStore
	clock domain.Clock
}

// NewFollowService wires the follow and mention use cases against the given
// ports. The clock stamps the instant a follow was created.
func NewFollowService(tickets TicketStore, users UserStore, followers TicketFollowerStore, clock domain.Clock) *FollowService {
	return &FollowService{tickets: tickets, users: users, followers: followers, clock: clock}
}

// WithDesks wires the desk membership proof the workflow plan needs and returns
// the service, so the composition root chains it onto the constructor. Every
// other use case keeps working without it: membership is only read to decide
// whether a follower may open the ticket a claim LEAVES BEHIND.
func (s *FollowService) WithDesks(desks DeskMembershipStore) *FollowService {
	s.desks = desks
	return s
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

// MentionNameContext returns the display names the mention grammar may match
// when a ticket's timeline RENDERS its comments (issue #438, S4): the staff who
// can open the ticket right now, minus the reader.
//
// It is a presentation fact, never an authorization. Whoever can open the ticket
// may see which runs of the comment text it is already showing are somebody's
// name, because that text is the only thing the answer can change; the names
// themselves are never listed on the page. Autocomplete keeps its own, stricter
// gate: MentionCandidates stays staff-only and offers only other people, and no
// mention can be written, notified or subscribed through this method.
//
// The actor's own read is the gate, so a requester reading their own ticket gets
// the names its staff comments may carry, and a ticket the actor cannot open
// answers NotFound exactly like every other scoped read.
func (s *FollowService) MentionNameContext(ctx context.Context, actor domain.User, ticketID int64) ([]string, error) {
	if s == nil {
		return nil, nil
	}
	if _, err := s.tickets.GetByID(ctx, ticketID, readQuery(ctx, actor, TicketQuery{})); err != nil {
		return nil, err
	}
	candidates, err := s.candidateStaff(ctx, actor, ticketID)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(candidates))
	for _, c := range candidates {
		names = append(names, c.user.Name)
	}
	return names, nil
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
// comment mention fan-out. The actor's own staff access is checked first: the
// list describes this ticket's staff, so a person who cannot open the ticket gets
// NotFound rather than a few names.
func (s *FollowService) mentionableStaff(ctx context.Context, actor domain.User, ticketID int64) ([]staffCandidate, error) {
	ok, err := canStaffOpenTicket(ctx, s.tickets, actor, ticketID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &domain.NotFoundError{Kind: "ticket", ID: ticketID}
	}
	return s.candidateStaff(ctx, actor, ticketID)
}

// candidateStaff is the population itself: every OTHER active staff member who
// can open the ticket right now, ordered by display name, with the proof the
// notification batch requires. Callers own the gate — mentionableStaff applies
// the staff predicate, MentionNameContext applies the actor's own ticket read —
// so the population is queried exactly once, in one place.
func (s *FollowService) candidateStaff(ctx context.Context, actor domain.User, ticketID int64) ([]staffCandidate, error) {
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

// followerRecipientsForResulting reads the ticket's followers for a workflow plan
// and keeps the ones who may open the ticket the plan LEAVES BEHIND. It is the
// plan-time counterpart of followerRecipients, which judges the STORED row, and
// it exists because a claim is exactly what makes that stored proof stale: the
// completion assigns the ticket to the acting agent and moves the run past the
// claim step in the same transaction that notifies the followers, so every other
// member of that desk loses the ticket in the instant they would hear about it.
//
// Eligibility is therefore a property of the resulting facts — assignee, state
// and the workflow position the plan is about to persist — and the desk arm of it
// is decided by REAL membership of the desk the run still waits on. Nothing here
// infers access from a follower row, from the desk the ticket is leaving, or from
// two claim steps naming the same desk.
//
// The plan is the same-transaction input of the unit of work, which rechecks the
// snapshot's facts and fails a stale submission with zero writes, so these facts
// are the ones the transaction persists. One window stays open and is documented
// on canStaffOpenResultingTicket: a membership change between this read and the
// apply is not rechecked by the adapter, which inserts the rows it is given.
func (s *FollowService) followerRecipientsForResulting(ctx context.Context, after domain.Ticket, def domain.WorkflowDefinition, cursor int, runStatus string) ([]verifiedRecipient, error) {
	if s == nil {
		return nil, nil
	}
	followersOf, err := s.followers.ListFollowers(ctx, after.ID)
	if err != nil {
		return nil, err
	}
	deskID, claimPending := resultingClaimDesk(def, cursor, runStatus, after.State)
	var members map[int64]bool
	if claimPending {
		members, err = s.deskMemberIDs(ctx, deskID)
		if err != nil {
			return nil, err
		}
	}
	var out []verifiedRecipient
	for _, u := range followersOf {
		if !canStaffOpenResultingTicket(u, after, claimPending, members) {
			continue
		}
		out = append(out, verifiedRecipient{recipient: *recipientOf(&u)})
	}
	return out, nil
}

// canStaffOpenResultingTicket reports whether u may open the ticket a plan leaves
// behind, as the staff member their follower row says they are. It is the
// plan-time twin of canStaffOpenTicket: the same role gate and the same
// policy.ReadScope arms, applied to the resulting assignee instead of to the
// stored row. The claim arm needs the resulting desk's member set, which
// deskMemberIDs reads from the desk store; a service without that collaborator
// passes a nil set and admits nobody through it.
//
// Applying the same arms is what keeps following from ever granting access: an
// agent reads the result as its assignee or as a member of the desk a claim still
// waits on, a requester as their own ticket, and admin/root as the whole queue.
// A least_loaded plan selects its assignee inside the transaction, so the assignee
// this predicate sees there is the snapshot holder whose rows the plan already
// defers; the selected member is addressed by the routing step itself.
func canStaffOpenResultingTicket(u domain.User, after domain.Ticket, claimPending bool, members map[int64]bool) bool {
	if !u.Role.Valid() || !u.Active {
		return false
	}
	if !NewPolicy().Capabilities(u.Role).Require(CapCommentInternal) {
		return false
	}
	switch NewPolicy().ReadScope(u.Role) {
	case ScopeAll:
		return true
	case ScopeOwned:
		return after.RequesterUserID != nil && *after.RequesterUserID == u.ID
	case ScopeAssignedOrClaimable:
		if after.UserID != nil && *after.UserID == u.ID {
			return true
		}
		return claimPending && members[u.ID]
	default:
		return false
	}
}

// resultingClaimDesk returns the desk a claim step still waits on once the plan
// has run, and false when the plan leaves no claim pending. It mirrors the claim
// half of the agent read scope (the ticket store's claimable predicate): the run
// stays active, the ticket is not terminal, and the cursor points at an
// assign_to_desk step whose strategy is claim.
func resultingClaimDesk(def domain.WorkflowDefinition, cursor int, runStatus string, state domain.State) (int64, bool) {
	if runStatus != "active" || domain.IsClosed(state) || cursor < 0 || cursor >= len(def) {
		return 0, false
	}
	step := def[cursor]
	if step.Type != domain.StepAssignToDesk || step.AssignToDesk == nil || step.AssignToDesk.Strategy != domain.StrategyClaim {
		return 0, false
	}
	return step.AssignToDesk.DeskID, true
}

// deskMemberIDs reads the desk's members as the set the eligibility test needs.
// A service wired without the desk store proves nothing about a desk, so it
// answers no members: the claim arm stays closed rather than admitting a follower
// nobody proved.
func (s *FollowService) deskMemberIDs(ctx context.Context, deskID int64) (map[int64]bool, error) {
	if s.desks == nil {
		return nil, nil
	}
	members, err := s.desks.ListMembers(ctx, deskID)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]bool, len(members))
	for _, m := range members {
		out[m.ID] = true
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
	names := make([]string, 0, len(candidates))
	for _, c := range candidates {
		names = append(names, c.user.Name)
	}
	written := mentionedNames(body, names)
	var people []domain.User
	var verified []verifiedRecipient
	for _, c := range candidates {
		if !written[c.user.Name] {
			continue
		}
		people = append(people, c.user)
		verified = append(verified, c.verified)
	}
	return people, verified, nil
}
