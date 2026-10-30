package application

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Notification recipients (issue #344, spec S10). The policy lives HERE, in the
// application layer, because it is a matter of roles, identity, and ticket
// access — never of persistence. The store only inserts the rows this layer
// stamps into the mutation, inside the transaction of the change they describe
// (S11).
//
// Two rules prune every batch before it is persisted:
//
//  1. Never the person who performed the action. A notification about your own
//     action is noise the bell would count forever.
//  2. Never someone who cannot open the ticket. The recipient's ROLE derives
//     their ticket read scope (policy.go), and an inactive account cannot open
//     anything — telling them about a ticket would be a notification that can
//     only ever 404 (S16).
//
// Both rules are enforced for every kind, so a caller cannot forget one, and
// the domain's Validate is the second line of defense if a future kind bypasses
// this file.

// notificationBatch accumulates ONE ticket change's notifications, dropping
// the actor, unreachable recipients, and duplicate rows for the same person
// (a requester who is also the assignee is told once).
type notificationBatch struct {
	ticket      *domain.Ticket
	actorUserID *int64
	at          time.Time
	rows        []domain.Notification
	seen        map[int64]bool
}

func newNotificationBatch(t *domain.Ticket, actorUserID *int64, at time.Time) *notificationBatch {
	return &notificationBatch{ticket: t, actorUserID: actorUserID, at: at, seen: map[int64]bool{}}
}

// add appends one row for recipient, or drops it when the recipient is absent,
// is the actor, cannot open the ticket, or was already addressed in this batch.
// from/to are the structured change facts (nil when the kind carries none).
//
// It is the entry point for the recipients the TICKET itself names: its
// requester and its assignee. Followers and mentions arrive already proven and
// enter through addVerified instead.
func (b *notificationBatch) add(kind domain.NotificationKind, recipient *NotificationRecipient, from, to *string) {
	if b == nil || b.ticket == nil || recipient == nil || recipient.Service {
		return
	}
	if !canOpenTicket(recipient, b.ticket) {
		return
	}
	b.appendRow(kind, *recipient, from, to)
}

// addVerified appends one row for a recipient whose ticket access the CALLER
// already proved with canStaffOpenTicket — the ticket detail route's own read
// scope, evaluated at notification time. Followers and mentioned staff take
// this path because they are not the ticket's assignee: canOpenTicket admits an
// agent only as the assignee, which is the narrow rule the requester/assignee
// rows need and must keep.
//
// The proof is a verifiedRecipient, an unexported type only the follower and
// mention readers in this package can build, so a fan-out cannot address
// somebody whose access was never checked. The batch's own rules — never the
// actor, never the same person twice — still apply here.
func (b *notificationBatch) addVerified(kind domain.NotificationKind, v verifiedRecipient, from, to *string) {
	if !v.recipient.Active || !v.recipient.Role.Valid() || v.recipient.Service {
		return
	}
	b.appendRow(kind, v.recipient, from, to)
}

// appendRow writes the row for a recipient that passed one of the two admission
// rules above. It is where the rules every kind shares live, so no entry point
// can forget them: never the person who performed the action, and never the
// same person twice in one batch (the first add wins, which is why the builders
// add the mentioned row, then the requester/assignee row, then the followers).
func (b *notificationBatch) appendRow(kind domain.NotificationKind, recipient NotificationRecipient, from, to *string) {
	if b == nil || b.ticket == nil {
		return
	}
	if b.actorUserID != nil && *b.actorUserID == recipient.UserID {
		return
	}
	if b.seen[recipient.UserID] {
		return
	}
	b.seen[recipient.UserID] = true
	n := domain.Notification{
		UserID:    recipient.UserID,
		TicketID:  b.ticket.ID,
		Kind:      kind,
		CreatedAt: b.at,
	}
	if b.actorUserID != nil {
		actor := *b.actorUserID
		n.ActorUserID = &actor
	}
	n.FromValue = copyString(from)
	n.ToValue = copyString(to)
	b.rows = append(b.rows, n)
}

func (b *notificationBatch) list() []domain.Notification {
	if b == nil {
		return nil
	}
	return b.rows
}

// canOpenTicket applies the ticket READ policy to one candidate recipient:
// the role derives the scope, and the ticket facts decide whether this person
// is inside it. Unknown roles, inactive accounts, and a missing ticket answer
// false — the policy fails closed, so an unprovable recipient is never told.
func canOpenTicket(r *NotificationRecipient, t *domain.Ticket) bool {
	if r == nil || t == nil || !r.Role.Valid() || !r.Active {
		return false
	}
	switch NewPolicy().ReadScope(r.Role) {
	case ScopeOwned:
		return t.RequesterUserID != nil && *t.RequesterUserID == r.UserID
	case ScopeAssignedOrClaimable:
		// The notification candidate is the assignee, so the claimable half of
		// an agent's read scope adds nothing here: an agent can open a ticket
		// they are assigned to.
		return t.UserID != nil && *t.UserID == r.UserID
	case ScopeAll:
		return true
	default:
		return false
	}
}

// verifiedRecipient is a staff member whose ticket access the caller proved
// with canStaffOpenTicket at notification time. The type is unexported and only
// the follower and mention readers of this package build it, so the batch's
// addVerified cannot be handed a candidate that skipped the proof.
type verifiedRecipient struct{ recipient NotificationRecipient }

// recipients is every candidate ONE ticket change can address: the two people
// the ticket itself names, plus the staff who follow it and — for a comment —
// the staff it mentions. The follower and mention halves arrive already proven
// by canStaffOpenTicket.
type recipients struct {
	requester *NotificationRecipient
	assignee  *NotificationRecipient
	followers []verifiedRecipient
	mentioned []verifiedRecipient
}

// canStaffOpenTicket reports whether u may open the ticket RIGHT NOW, as a staff
// member. It is the ONE access predicate of the follower and mention paths (the
// follow use case, the mention candidate list, and the fan-out of every
// notification kind), so none of them can widen ticket access: following a
// ticket, being mentioned in it, or being named by somebody else never grants
// scope, because this is a read of the same scope the ticket detail route
// applies (policy.go ReadScope: an agent widens to assigned OR claim-pending on
// their desk, a requester to their own tickets, admin/root to the whole queue).
//
// The role must also hold CapCommentInternal, which is what makes the candidate
// STAFF: a requester is never a follower and never a mention. An inactive
// account can open nothing.
//
// A missing or out-of-scope ticket answers false — the store reports both as
// ErrNotFound, so a refusal leaks no existence either. Any OTHER store failure
// propagates: an unproven check must never be treated as a pass, because a
// notification nobody proved access for can only ever 404.
func canStaffOpenTicket(ctx context.Context, tickets TicketStore, u domain.User, ticketID int64) (bool, error) {
	if !u.Role.Valid() || !u.Active {
		return false, nil
	}
	if !NewPolicy().Capabilities(u.Role).Require(CapCommentInternal) {
		return false, nil
	}
	if _, err := tickets.GetByID(ctx, ticketID, readQuery(u, TicketQuery{})); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ticketChangeRecipients resolves the recipient candidates of ONE ticket
// change: the requester and the assignee the ticket names, and the staff who
// follow it. The follower half is read and proven at notification time, so a
// follower who lost access is simply absent from the batch — the row stays, the
// person is skipped.
func ticketChangeRecipients(ctx context.Context, users UserStore, follows *FollowService, t *domain.Ticket) (recipients, error) {
	requester, assignee, err := ticketRecipients(ctx, users, t)
	if err != nil {
		return recipients{}, err
	}
	followers, err := follows.followerRecipients(ctx, t.ID)
	if err != nil {
		return recipients{}, err
	}
	return recipients{requester: requester, assignee: assignee, followers: followers}, nil
}

// withoutRecipient drops one person from a proven recipient list. A change that
// takes the ticket away from somebody cannot address them: the fan-out is
// written in the same transaction as the change, so it is judged against the
// ticket the change leaves behind, not the one it read.
func withoutRecipient(list []verifiedRecipient, userID *int64) []verifiedRecipient {
	if userID == nil || len(list) == 0 {
		return list
	}
	kept := make([]verifiedRecipient, 0, len(list))
	for _, v := range list {
		if v.recipient.UserID != *userID {
			kept = append(kept, v)
		}
	}
	return kept
}

// recipientIDs lists the people of a proven recipient list, in order. It is how a
// plan hands a routing step the followers that step must tell, so the row can be
// written where the selection is made.
func recipientIDs(list []verifiedRecipient) []int64 {
	if len(list) == 0 {
		return nil
	}
	out := make([]int64, 0, len(list))
	for _, v := range list {
		out = append(out, v.recipient.UserID)
	}
	return out
}

// requiresInternalReader reports whether a comment kind may reach only a
// recipient whose role can read INTERNAL comments. A mention shares the rule
// with an internal comment: it is written from a comment written for staff, so
// a reader without that capability must not learn it exists. The fan-out
// applies it before writing the row, and the send-time email guard re-applies
// it, so a row written while somebody was staff never reaches them after they
// are not.
func requiresInternalReader(k domain.NotificationKind) bool {
	return k == domain.NotificationCommentInternal || k == domain.NotificationMentioned
}

// stateChangeNotifications builds the S10 rows for ONE net ticket state change
// from -> to: the requester and the assignee are told, except that a move into
// resolved asks the requester to CONFIRM the fix (the separate
// resolved_awaiting_confirmation kind) instead of reporting a bare change. The
// ticket's followers hear the plain state change — including the move into
// resolved, whose confirmation request is not theirs to receive. No change
// (from == to) tells nobody.
func stateChangeNotifications(t *domain.Ticket, from, to domain.State, actorUserID *int64, at time.Time, r recipients) []domain.Notification {
	if t == nil || from == to {
		return nil
	}
	b := newNotificationBatch(t, actorUserID, at)
	fromValue, toValue := string(from), string(to)
	if to == domain.StateResolved {
		b.add(domain.NotificationResolvedAwaitingConfirmation, r.requester, &fromValue, &toValue)
		b.add(domain.NotificationStateChanged, r.assignee, &fromValue, &toValue)
	} else {
		b.add(domain.NotificationStateChanged, r.requester, &fromValue, &toValue)
		b.add(domain.NotificationStateChanged, r.assignee, &fromValue, &toValue)
	}
	for _, f := range r.followers {
		b.addVerified(domain.NotificationStateChanged, f, &fromValue, &toValue)
	}
	return b.list()
}

// assignmentNotifications builds the rows one assignment change produces: the
// new assignee learns the ticket is theirs, and the ticket's followers learn who
// holds it now (assignee_changed). assigneeName is the new assignee's display
// name, snapshotted into the row so the panel and the email can name the person
// without looking them up again; a cleared assignment stores NULL there and the
// copy says the ticket has no assignee, which is why an unassignment still has
// something to tell the followers. A cleared assignment addresses no ASSIGNEE —
// nobody gained a ticket.
func assignmentNotifications(t *domain.Ticket, assignee *NotificationRecipient, assigneeName string, actorUserID *int64, at time.Time, followers []verifiedRecipient) []domain.Notification {
	b := newNotificationBatch(t, actorUserID, at)
	b.add(domain.NotificationAssigned, assignee, nil, nil)
	var to *string
	if name := strings.TrimSpace(assigneeName); name != "" {
		to = &name
	}
	for _, f := range followers {
		b.addVerified(domain.NotificationAssigneeChanged, f, nil, to)
	}
	return b.list()
}

// assignmentChangedNotifications builds the rows a change that puts the ticket on
// a person the APPLICATION knows owes the ticket's followers: a workflow claim,
// whose new holder is the acting agent and whose display name the plan already
// carries. existing are the rows this step already writes, so nobody is told
// twice: whoever the plan already addresses in this step keeps that row, and the
// assignment is the second-best news about the same event.
func assignmentChangedNotifications(t *domain.Ticket, assigneeName string, actorUserID *int64, at time.Time, followers []verifiedRecipient, existing []domain.Notification) []domain.Notification {
	b := newNotificationBatch(t, actorUserID, at)
	for _, n := range existing {
		b.seen[n.UserID] = true
	}
	var to *string
	if name := strings.TrimSpace(assigneeName); name != "" {
		to = &name
	}
	for _, f := range followers {
		b.addVerified(domain.NotificationAssigneeChanged, f, nil, to)
	}
	return b.list()
}

// priorityChangeNotifications builds the priority row for the requester, the
// assignee and the ticket's followers, carrying the raw priorities so the
// catalogue can render them.
func priorityChangeNotifications(t *domain.Ticket, from, to domain.Priority, actorUserID *int64, at time.Time, r recipients) []domain.Notification {
	if t == nil || from == to {
		return nil
	}
	b := newNotificationBatch(t, actorUserID, at)
	fromValue, toValue := string(from), string(to)
	b.add(domain.NotificationPriorityChanged, r.requester, &fromValue, &toValue)
	b.add(domain.NotificationPriorityChanged, r.assignee, &fromValue, &toValue)
	for _, f := range r.followers {
		b.addVerified(domain.NotificationPriorityChanged, f, &fromValue, &toValue)
	}
	return b.list()
}

// resolutionNotifications builds the S10 rows for the requester's answer to a
// resolution: the assignee learns whether the fix was confirmed or rejected, and
// the ticket's followers hear the same kind. The requester is the actor, so the
// rules keep them out of the batch.
func resolutionNotifications(t *domain.Ticket, kind domain.NotificationKind, from, to domain.State, actorUserID *int64, at time.Time, r recipients) []domain.Notification {
	b := newNotificationBatch(t, actorUserID, at)
	fromValue, toValue := string(from), string(to)
	b.add(kind, r.assignee, &fromValue, &toValue)
	for _, f := range r.followers {
		b.addVerified(kind, f, &fromValue, &toValue)
	}
	return b.list()
}

// onHoldNotifications builds the S7 row of issue #352: the REQUESTER learns
// their ticket waits for their reply. The assignee is staff and is usually the
// one who put it on hold, so they are not told; resuming tells nobody (the
// requester's own reply resumes it, and the assignee hears about that reply
// as a comment).
func onHoldNotifications(t *domain.Ticket, actorUserID *int64, at time.Time, requester *NotificationRecipient) []domain.Notification {
	b := newNotificationBatch(t, actorUserID, at)
	b.add(domain.NotificationOnHold, requester, nil, nil)
	return b.list()
}

// holdReminderNotifications builds the issue #353 S3 row: the REQUESTER alone
// learns the held ticket will close unless they reply. The action is
// automatic — nobody performs it — so the batch carries no actor and excludes
// nobody on that ground; a requester who can no longer open the ticket is
// still dropped, and an empty batch means the pass writes nothing.
func holdReminderNotifications(t *domain.Ticket, at time.Time, requester *NotificationRecipient) []domain.Notification {
	b := newNotificationBatch(t, nil, at)
	b.add(domain.NotificationHoldReminder, requester, nil, nil)
	return b.list()
}

// holdClosedNotifications builds the automatic-close rows of issue #353: the
// REQUESTER, the ASSIGNEE and the ticket's FOLLOWERS all learn it was closed because
// the requester never replied. Like the reminder this is an automatic action, so
// no actor is excluded by the batch's own-action rule; whoever cannot open the
// ticket is still dropped, and a requester who is also the assignee is told
// once.
func holdClosedNotifications(t *domain.Ticket, at time.Time, r recipients) []domain.Notification {
	b := newNotificationBatch(t, nil, at)
	b.add(domain.NotificationHoldClosed, r.requester, nil, nil)
	b.add(domain.NotificationHoldClosed, r.assignee, nil, nil)
	for _, f := range r.followers {
		b.addVerified(domain.NotificationHoldClosed, f, nil, nil)
	}
	return b.list()
}

// alertNotifications builds the issue #408 row for ONE operational alert: the
// ASSIGNED agent alone learns that their ticket reached an SLA threshold or
// stopped moving. Nobody performed this — the condition is the passage of time
// — so the batch carries no actor and excludes nobody on that ground; an
// assignee who can no longer open the ticket is still dropped, and an empty
// batch means the pass writes nothing and records no alert.
func alertNotifications(t *domain.Ticket, kind domain.NotificationKind, at time.Time, assignee *NotificationRecipient) []domain.Notification {
	b := newNotificationBatch(t, nil, at)
	b.add(kind, assignee, nil, nil)
	return b.list()
}

// NotificationTicketQuery is the ticket READ scope the notification panel uses
// to load the facts it shows for one row (the ticket number and its title). It
// is exactly the scope the ticket DETAIL route applies — an agent widens to
// their assigned and claimable tickets, a requester to their own, staff to the
// whole queue — so a row can never display facts about a ticket the reader
// cannot open, and opening the row re-checks the same scope on the detail route
// (issue #344, S16).
func NotificationTicketQuery(actor domain.User) TicketQuery {
	return readQuery(actor, TicketQuery{})
}

// recipientOf projects a stored user into the notification policy input. A
// nil user stays nil: there is no candidate, never a guessed one.
func recipientOf(u *domain.User) *NotificationRecipient {
	if u == nil {
		return nil
	}
	return &NotificationRecipient{UserID: u.ID, Role: u.Role, Active: u.Active, Service: u.IsService()}
}

// recipientFacts resolves the notification policy facts for ONE candidate
// recipient. A nil id or a user row that no longer exists answers nil — that
// person is simply not a candidate. Any other store failure propagates: a
// mutation must never persist a notification whose eligibility was not proven.
//
// It is a free function over the UserStore port because BOTH units of work that
// notify read the same facts: the ticket service (B2) and the comment service
// (B3). Parsing them twice would let the two drift.
func recipientFacts(ctx context.Context, users UserStore, userID *int64) (*NotificationRecipient, error) {
	if userID == nil {
		return nil, nil
	}
	u, err := users.GetByID(ctx, *userID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return recipientOf(u), nil
}

// ticketRecipients resolves both S10 candidates for one ticket: its requester
// and its assignee (either may be absent, e.g. a legacy ticket with no
// requester user or an unassigned ticket).
func ticketRecipients(ctx context.Context, users UserStore, t *domain.Ticket) (requester, assignee *NotificationRecipient, err error) {
	if requester, err = recipientFacts(ctx, users, t.RequesterUserID); err != nil {
		return nil, nil, err
	}
	if assignee, err = recipientFacts(ctx, users, t.UserID); err != nil {
		return nil, nil, err
	}
	return requester, assignee, nil
}

// commentNotifications builds the rows for one added comment. A public comment
// tells the requester, the assignee and the ticket's followers; an internal
// comment tells the assignee and the followers who may READ it — the requester
// must never learn that a staff-only conversation happened.
//
// The staff a comment mentions are added FIRST, so a person who is mentioned AND
// is the assignee or a follower is told ONCE, as a mention (issue #418, "one row
// per recipient per event").
//
// The author is the actor, so the batch's own rules keep them out, and anyone
// who cannot open the ticket is dropped. Internal comments add one more rule:
// the recipient's role must hold CapCommentInternal, the same capability that
// gates reading and writing them — applied to the assignee and to every follower
// and mention, so a legacy ticket naming a user-role assignee, or a follower
// whose account was downgraded after they followed, notifies nobody rather than
// leaking the hidden conversation.
func commentNotifications(t *domain.Ticket, visibility domain.CommentVisibility, actorUserID *int64, at time.Time, r recipients) []domain.Notification {
	if t == nil {
		return nil
	}
	b := newNotificationBatch(t, actorUserID, at)
	// A comment carries no state or priority change, so the rows hold the kind
	// and the comment id alone (S15: never a rendered sentence).
	internal := visibility == domain.CommentInternal
	for _, m := range r.mentioned {
		if internal && !canSeeInternalComments(&m.recipient) {
			continue
		}
		b.addVerified(domain.NotificationMentioned, m, nil, nil)
	}
	if internal {
		// The requester is absent from this branch by construction, not by a
		// filter: there is no path that hands an internal comment the requester
		// candidate.
		if canSeeInternalComments(r.assignee) {
			b.add(domain.NotificationCommentInternal, r.assignee, nil, nil)
		}
		for _, f := range r.followers {
			if !canSeeInternalComments(&f.recipient) {
				continue
			}
			b.addVerified(domain.NotificationCommentInternal, f, nil, nil)
		}
		return b.list()
	}
	b.add(domain.NotificationCommentPublic, r.requester, nil, nil)
	b.add(domain.NotificationCommentPublic, r.assignee, nil, nil)
	for _, f := range r.followers {
		b.addVerified(domain.NotificationCommentPublic, f, nil, nil)
	}
	return b.list()
}

// canSeeInternalComments reports whether r may be told about an INTERNAL
// comment. The role must hold CapCommentInternal: a recipient without it cannot
// read the comment, so learning one exists would already reveal it (S16).
func canSeeInternalComments(r *NotificationRecipient) bool {
	if r == nil || !r.Role.Valid() || !r.Active {
		return false
	}
	return NewPolicy().Capabilities(r.Role).Require(CapCommentInternal)
}

// copyString copies a fact for storage: each row owns its own value, so a
// later mutation of the caller's pointer can never rewrite a stored row.
func copyString(v *string) *string {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

// derefString reads an optional fact as a plain value (the empty string when
// absent), so a caller can hand a *string field straight to a typed builder.
func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// NotificationAccount is the account an account notice names, as far as the
// READER may see it (issue #419, S3/S12). It is the one non-ticket presentation
// fact of the notification center: the row's subject is an account, so nothing
// here can be borrowed from a ticket.
type NotificationAccount struct {
	UserID int64
	// Visible reports whether this reader may see the account's identity facts:
	// the name, the address and the provider identities. A reader who no longer
	// holds the pending-management capability keeps the notice (it was addressed
	// to them) and gets an explicit fallback instead of another person's data.
	Visible bool
	// Found reports whether the account still exists. A notice outlives state,
	// not deletion, so a missing account is rendered as such instead of as a
	// blank row.
	Found     bool
	Name      string
	Email     string
	Pending   bool
	Providers []CompanyProvider
}

// NotificationAccounts answers the account facts the account-kind notices render,
// keyed by account id, for the reader who is asking (issue #419, S3/S12).
//
// The pending-management capability — the same one the Pending tab answers to —
// decides whether ANY fact travels. A recipient who lost it keeps the stored
// notice and reads an explicit restricted sentence: a downgrade must never turn
// the bell into a way to read an account's name, address and provider. A
// composition without the read answers the same fallback, so missing wiring can
// never publish the facts by accident.
func NotificationAccounts(ctx context.Context, actor domain.User, store NotificationSubjectStore, userIDs []int64) (map[int64]NotificationAccount, error) {
	accounts := make(map[int64]NotificationAccount, len(userIDs))
	if len(userIDs) == 0 {
		return accounts, nil
	}
	if store == nil || !NewPolicy().Capabilities(actor.Role).Require(CapManageUsers) {
		for _, id := range userIDs {
			accounts[id] = NotificationAccount{UserID: id}
		}
		return accounts, nil
	}
	subjects, err := store.ListNotificationSubjects(ctx, uniqueAccountIDs(userIDs))
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]NotificationAccountSubject, len(subjects))
	for _, subject := range subjects {
		byID[subject.UserID] = subject
	}
	for _, id := range userIDs {
		subject, found := byID[id]
		accounts[id] = NotificationAccount{
			UserID:    id,
			Visible:   true,
			Found:     found,
			Name:      subject.Name,
			Email:     subject.Email,
			Pending:   subject.Pending,
			Providers: providersInDisplayOrder(subject.Providers),
		}
	}
	return accounts, nil
}

// uniqueAccountIDs keeps the requested ids in first-seen order and drops the
// repeats, so a panel of several notices about one account costs one read.
func uniqueAccountIDs(userIDs []int64) []int64 {
	seen := make(map[int64]bool, len(userIDs))
	unique := make([]int64, 0, len(userIDs))
	for _, id := range userIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	return unique
}

// providersInDisplayOrder answers the provider identities in the closed set's
// display order, so a row never reorders them per read. A value the set does not
// name keeps its place at the end instead of being dropped or renamed: the
// presentation layer labels it explicitly.
func providersInDisplayOrder(providers []CompanyProvider) []CompanyProvider {
	if len(providers) == 0 {
		return nil
	}
	ordered := make([]CompanyProvider, len(providers))
	copy(ordered, providers)
	sort.SliceStable(ordered, func(i, j int) bool {
		return providerDisplayIndex(ordered[i]) < providerDisplayIndex(ordered[j])
	})
	return ordered
}

// NotificationAccountMessage is the ident line and the sentence one account
// notice renders (issue #419, S3/S12), built from the stored facts alone.
//
// Three rules decide it, and each is a truthful statement about what is stored:
// an account that still waits reads as waiting; an account whose request was
// already answered reads in the past tense instead of claiming it still waits; and
// a reader who may not see the account, or an account that no longer exists,
// reads an explicit fallback rather than a blank line or another person's data.
func NotificationAccountMessage(account NotificationAccount) (title, body Msg) {
	switch {
	case !account.Visible:
		return MsgRef("notifications.account.anonymous.title"), MsgRef("notifications.account.restricted.body")
	case !account.Found:
		return MsgRef("notifications.account.anonymous.title"), MsgRef("notifications.account.unavailable.body")
	case account.Pending:
		return MsgRef("notifications.account.pending.title", Text(account.Name)),
			MsgRef("notifications.account.pending.body", Text(account.Name), Text(account.Email), notificationProviderPhrase(account.Providers))
	default:
		return MsgRef("notifications.account.resolved.title", Text(account.Name)),
			MsgRef("notifications.account.resolved.body", Text(account.Name), Text(account.Email), notificationProviderPhrase(account.Providers))
	}
}

// notificationProviderPhrase is the "signed in with …" half of the sentence,
// built from the stored identities alone (S12): one provider, the pair the
// closed provider set can hold, or an explicit "a company account" when no
// identity was recorded. A guessed method would be a fabricated fact.
func notificationProviderPhrase(providers []CompanyProvider) Arg {
	switch len(providers) {
	case 0:
		return Nested(MsgRef("notifications.account.method_unknown"))
	case 1:
		return Nested(MsgRef("notifications.account.method", Copy(CompanyProviderLabelKey(providers[0]))))
	default:
		// The provider column is a closed set of two, so a stored account holds
		// at most one identity per provider.
		return Nested(MsgRef("notifications.account.method_pair",
			Copy(CompanyProviderLabelKey(providers[0])), Copy(CompanyProviderLabelKey(providers[1]))))
	}
}

// CompanyProviderLabelKey answers the catalogue key of a provider's display
// name, for the reason the managed-user surfaces do: the presentation layer
// decides the language, and a provider the closed set does not name renders an
// explicit unknown label rather than a raw stored value.
func CompanyProviderLabelKey(provider CompanyProvider) string {
	switch provider {
	case ProviderGoogle:
		return "label.company_provider.google"
	case ProviderMicrosoft:
		return "label.company_provider.microsoft"
	default:
		return "label.company_provider.unknown"
	}
}
