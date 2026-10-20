package application

import (
	"context"
	"errors"
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
func (b *notificationBatch) add(kind domain.NotificationKind, recipient *NotificationRecipient, from, to *string) {
	if b == nil || b.ticket == nil || recipient == nil {
		return
	}
	if b.actorUserID != nil && *b.actorUserID == recipient.UserID {
		return
	}
	if !canOpenTicket(recipient, b.ticket) {
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

// stateChangeNotifications builds the S10 rows for ONE net ticket state change
// from -> to: the requester and the assignee are told, except that a move into
// resolved asks the requester to CONFIRM the fix (the separate
// resolved_awaiting_confirmation kind) instead of reporting a bare change. No
// change (from == to) tells nobody.
func stateChangeNotifications(t *domain.Ticket, from, to domain.State, actorUserID *int64, at time.Time, requester, assignee *NotificationRecipient) []domain.Notification {
	if t == nil || from == to {
		return nil
	}
	b := newNotificationBatch(t, actorUserID, at)
	fromValue, toValue := string(from), string(to)
	if to == domain.StateResolved {
		b.add(domain.NotificationResolvedAwaitingConfirmation, requester, &fromValue, &toValue)
		b.add(domain.NotificationStateChanged, assignee, &fromValue, &toValue)
		return b.list()
	}
	b.add(domain.NotificationStateChanged, requester, &fromValue, &toValue)
	b.add(domain.NotificationStateChanged, assignee, &fromValue, &toValue)
	return b.list()
}

// assignmentNotifications builds the S10 "assigned" row: the new assignee
// learns about it. A cleared assignment tells nobody — nobody gained a ticket.
func assignmentNotifications(t *domain.Ticket, assignee *NotificationRecipient, actorUserID *int64, at time.Time) []domain.Notification {
	if assignee == nil {
		return nil
	}
	b := newNotificationBatch(t, actorUserID, at)
	b.add(domain.NotificationAssigned, assignee, nil, nil)
	return b.list()
}

// priorityChangeNotifications builds the S10 priority row for the requester and
// the assignee, carrying the raw priorities so the catalogue can render them.
func priorityChangeNotifications(t *domain.Ticket, from, to domain.Priority, actorUserID *int64, at time.Time, requester, assignee *NotificationRecipient) []domain.Notification {
	if t == nil || from == to {
		return nil
	}
	b := newNotificationBatch(t, actorUserID, at)
	fromValue, toValue := string(from), string(to)
	b.add(domain.NotificationPriorityChanged, requester, &fromValue, &toValue)
	b.add(domain.NotificationPriorityChanged, assignee, &fromValue, &toValue)
	return b.list()
}

// resolutionNotifications builds the S10 rows for the requester's answer to a
// resolution: the assignee learns whether the fix was confirmed or rejected.
// The requester is the actor, so the rules keep them out of the batch.
func resolutionNotifications(t *domain.Ticket, kind domain.NotificationKind, from, to domain.State, actorUserID *int64, at time.Time, assignee *NotificationRecipient) []domain.Notification {
	b := newNotificationBatch(t, actorUserID, at)
	fromValue, toValue := string(from), string(to)
	b.add(kind, assignee, &fromValue, &toValue)
	return b.list()
}

// recipientOf projects a stored user into the notification policy input. A
// nil user stays nil: there is no candidate, never a guessed one.
func recipientOf(u *domain.User) *NotificationRecipient {
	if u == nil {
		return nil
	}
	return &NotificationRecipient{UserID: u.ID, Role: u.Role, Active: u.Active}
}

// recipientFacts resolves the notification policy facts for ONE candidate
// recipient. A nil id or a user row that no longer exists answers nil — that
// person is simply not a candidate. Any other store failure propagates: a
// mutation must never persist a notification whose eligibility was not proven.
func (s *TicketService) recipientFacts(ctx context.Context, userID *int64) (*NotificationRecipient, error) {
	if userID == nil {
		return nil, nil
	}
	u, err := s.users.GetByID(ctx, *userID)
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
func (s *TicketService) ticketRecipients(ctx context.Context, t *domain.Ticket) (requester, assignee *NotificationRecipient, err error) {
	if requester, err = s.recipientFacts(ctx, t.RequesterUserID); err != nil {
		return nil, nil, err
	}
	if assignee, err = s.recipientFacts(ctx, t.UserID); err != nil {
		return nil, nil, err
	}
	return requester, assignee, nil
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
