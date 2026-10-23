package application_test

import (
	"context"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// The follower fan-out (issue #418). A follower is told about the events of a
// ticket they can open: comments, state changes, priority changes, assignment
// changes, resolution answers and the automatic close. The three invariants that
// hold for every kind live here too:
//
//   - never the person who performed the action;
//   - once per person per event, even when they are assignee, follower and
//     mentioned at the same time;
//   - never somebody who can no longer open the ticket, or who is inactive, or
//     who cannot read an internal comment.

// fanOutHarness is the notification fixture with both notifying services: the
// ticket use cases and the comment use cases, each wired to the same follow use
// case.
type fanOutHarness struct {
	*commentNotifyHarness
}

func newFanOutHarness(t *testing.T) *fanOutHarness {
	t.Helper()
	return &fanOutHarness{commentNotifyHarness: newCommentNotifyHarness(t)}
}

// ticketSvc is the ticket use case of the fixture (the comment one is h.svc).
func (h *fanOutHarness) ticketSvc() *application.TicketService { return h.ticketHarness.svc }

// rows returns every notification the fixture recorded for one recipient, from
// the ticket unit of work and from the comment one.
func (h *fanOutHarness) rows(userID int64) []domain.Notification {
	out := h.notifyHarness.notified(userID)
	return append(out, h.commentTx.notified(userID)...)
}

// follow subscribes a staff member through the use case, so arranging a follower
// proves the same access predicate the fan-out uses.
func (h *fanOutHarness) follow(t *testing.T, who domain.User) {
	t.Helper()
	if err := h.followSvc.Follow(context.Background(), who, h.ticket.ID); err != nil {
		t.Fatalf("Follow(%s): %v", who.Name, err)
	}
}

func TestFollowersHearEveryTicketEvent(t *testing.T) {
	const closed = "closed"
	cases := []struct {
		name string
		// prep arranges the ticket state before the action.
		prep func(t *testing.T, h *fanOutHarness)
		// follower returns the staff member who follows the ticket and must be
		// told about the action.
		follower func(t *testing.T, h *fanOutHarness) domain.User
		act      func(t *testing.T, h *fanOutHarness)
		kind     domain.NotificationKind
		from, to string
	}{
		{
			name:     "public comment",
			follower: func(_ *testing.T, h *fanOutHarness) domain.User { return h.assignee },
			act: func(t *testing.T, h *fanOutHarness) {
				mustAddComment(t, h, h.admin, "Deployed to staging", "public")
			},
			kind: domain.NotificationCommentPublic,
		},
		{
			name:     "internal comment",
			follower: func(_ *testing.T, h *fanOutHarness) domain.User { return h.assignee },
			act: func(t *testing.T, h *fanOutHarness) {
				mustAddComment(t, h, h.admin, "Staff only: waiting on the vendor", "internal")
			},
			kind: domain.NotificationCommentInternal,
		},
		{
			name:     "state change",
			follower: func(_ *testing.T, h *fanOutHarness) domain.User { return h.assignee },
			act: func(t *testing.T, h *fanOutHarness) {
				if _, err := h.ticketSvc().Transition(context.Background(), h.admin, h.ticket.ID, domain.StateInProgress, ""); err != nil {
					t.Fatalf("Transition: %v", err)
				}
			},
			kind: domain.NotificationStateChanged, from: "new", to: "in_progress",
		},
		{
			name:     "the move into resolved is a plain state change for followers",
			follower: func(_ *testing.T, h *fanOutHarness) domain.User { return h.assignee },
			act: func(t *testing.T, h *fanOutHarness) {
				if _, err := h.ticketSvc().Transition(context.Background(), h.admin, h.ticket.ID, domain.StateResolved, ""); err != nil {
					t.Fatalf("Transition: %v", err)
				}
			},
			kind: domain.NotificationStateChanged, from: "new", to: "resolved",
		},
		{
			name:     "priority change",
			follower: func(_ *testing.T, h *fanOutHarness) domain.User { return h.assignee },
			act: func(t *testing.T, h *fanOutHarness) {
				priority := domain.PriorityHigh
				if _, err := h.ticketSvc().Update(context.Background(), h.admin, h.ticket.ID, domain.TicketUpdate{Priority: &priority}); err != nil {
					t.Fatalf("Update: %v", err)
				}
			},
			kind: domain.NotificationPriorityChanged, from: "medium", to: "high",
		},
		{
			name: "assignment names the new assignee",
			// The follower is an administrator, so the change does not move the
			// ticket away from them; the outgoing assignee is covered below.
			follower: func(t *testing.T, h *fanOutHarness) domain.User {
				return h.users.seedRole("Ari", "ari@example.com", domain.RoleAdmin, true)
			},
			act: func(t *testing.T, h *fanOutHarness) {
				bea := h.users.seedRole("Bea", "bea@example.com", domain.RoleAgent, true)
				if _, err := h.ticketSvc().Assign(context.Background(), h.admin, h.ticket.ID, &bea.ID, "handing over"); err != nil {
					t.Fatalf("Assign: %v", err)
				}
			},
			kind: domain.NotificationAssigneeChanged, to: "Bea",
		},
		{
			name: "unassignment tells the followers the ticket is free",
			follower: func(t *testing.T, h *fanOutHarness) domain.User {
				return h.users.seedRole("Ari", "ari@example.com", domain.RoleAdmin, true)
			},
			act: func(t *testing.T, h *fanOutHarness) {
				if _, err := h.ticketSvc().Assign(context.Background(), h.admin, h.ticket.ID, nil, ""); err != nil {
					t.Fatalf("Assign (clear): %v", err)
				}
			},
			kind: domain.NotificationAssigneeChanged,
		},
		{
			name: "resolution confirmed",
			prep: func(t *testing.T, h *fanOutHarness) {
				h.ticket.State = domain.StateResolved
				if err := h.tickets.Update(context.Background(), &h.ticket); err != nil {
					t.Fatalf("seed resolved: %v", err)
				}
			},
			follower: func(_ *testing.T, h *fanOutHarness) domain.User { return h.assignee },
			act: func(t *testing.T, h *fanOutHarness) {
				requester := domain.User{ID: h.requester.ID, Name: h.requester.Name, Role: h.requester.Role}
				if _, err := h.ticketSvc().ConfirmResolution(context.Background(), requester, h.ticket.ID); err != nil {
					t.Fatalf("ConfirmResolution: %v", err)
				}
			},
			kind: domain.NotificationResolutionConfirmed, from: "resolved", to: closed,
		},
		{
			name: "resolution rejected",
			prep: func(t *testing.T, h *fanOutHarness) {
				h.ticket.State = domain.StateResolved
				if err := h.tickets.Update(context.Background(), &h.ticket); err != nil {
					t.Fatalf("seed resolved: %v", err)
				}
			},
			follower: func(_ *testing.T, h *fanOutHarness) domain.User { return h.assignee },
			act: func(t *testing.T, h *fanOutHarness) {
				requester := domain.User{ID: h.requester.ID, Name: h.requester.Name, Role: h.requester.Role}
				if _, err := h.ticketSvc().RejectResolution(context.Background(), requester, h.ticket.ID); err != nil {
					t.Fatalf("RejectResolution: %v", err)
				}
			},
			kind: domain.NotificationResolutionRejected, from: "resolved", to: "in_progress",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newFanOutHarness(t)
			if tc.prep != nil {
				tc.prep(t, h)
			}
			follower := tc.follower(t, h)
			h.follow(t, follower)
			tc.act(t, h)

			got := h.rows(follower.ID)
			if len(got) != 1 {
				t.Fatalf("follower rows = %+v, want exactly one", got)
			}
			if got[0].Kind != tc.kind {
				t.Errorf("kind = %q, want %q", got[0].Kind, tc.kind)
			}
			assertFact(t, "from", got[0].FromValue, tc.from)
			assertFact(t, "to", got[0].ToValue, tc.to)
		})
	}
}

// mustAddComment writes one comment through the service under test.
func mustAddComment(t *testing.T, h *fanOutHarness, actor domain.User, body, visibility string) {
	t.Helper()
	if _, err := h.svc.Add(context.Background(), actor, h.ticket.ID, body, visibility); err != nil {
		t.Fatalf("Add(%s comment): %v", visibility, err)
	}
}

// TestFollowerHearsTheAutomaticClose pins the one event nobody performs: the
// hold expiry pass closes the ticket on its own, and the follower is told with
// the same kind the requester and the assignee receive.
func TestFollowerHearsTheAutomaticClose(t *testing.T) {
	h := newHoldExpiryHarness()
	ticket, _, assignee := h.seedHeld(t, domain.StateInProgress, h.clock.now)
	if err := application.NewFollowService(h.tickets, h.users, h.followers, h.clock).
		Follow(context.Background(), assignee, ticket.ID); err != nil {
		t.Fatalf("Follow: %v", err)
	}
	h.settings.holdAutoCloseDays = 2
	h.at(h.clock.now.Add(30 * 24 * time.Hour))

	if err := h.svc.Pass(context.Background()); err != nil {
		t.Fatalf("Pass: %v", err)
	}
	rows := h.notificationsFor(ticket.ID)
	told := 0
	for _, n := range rows {
		if n.UserID != assignee.ID {
			continue
		}
		told++
		if n.Kind != domain.NotificationHoldClosed {
			t.Errorf("follower kind = %q, want %q", n.Kind, domain.NotificationHoldClosed)
		}
	}
	if told != 1 {
		t.Fatalf("the follower has %d rows for the automatic close, want 1: %+v", told, rows)
	}
}

// TestFollowerIsToldOnceByPrecedence pins the ordering rule: a person who is the
// assignee, a follower and mentioned at once hears the event once, as a mention.
func TestFollowerIsToldOnceByPrecedence(t *testing.T) {
	h := newFanOutHarness(t)
	h.follow(t, h.assignee)

	mustAddComment(t, h, h.admin, "@Ana, could you check the logs?", "public")

	got := h.rows(h.assignee.ID)
	if len(got) != 1 {
		t.Fatalf("assignee+follower+mentioned rows = %+v, want exactly one", got)
	}
	if got[0].Kind != domain.NotificationMentioned {
		t.Fatalf("kind = %q, want %q", got[0].Kind, domain.NotificationMentioned)
	}
}

// TestFollowerWhoCannotOpenTheTicketIsSkipped covers the follower who lost
// access after subscribing: the row stays, the person is skipped.
func TestFollowerWhoCannotOpenTheTicketIsSkipped(t *testing.T) {
	h := newFanOutHarness(t)
	h.follow(t, h.assignee)
	// The ticket moves to somebody else, so the follower can no longer open it.
	h.ticket.UserID = ptr(h.admin.ID)
	if err := h.tickets.Update(context.Background(), &h.ticket); err != nil {
		t.Fatalf("reassign: %v", err)
	}

	if _, err := h.ticketSvc().Transition(context.Background(), h.admin, h.ticket.ID, domain.StateInProgress, ""); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	if got := h.rows(h.assignee.ID); len(got) != 0 {
		t.Fatalf("a follower without access was told: %+v", got)
	}
	if following, _ := h.followers.IsFollowing(context.Background(), h.ticket.ID, h.assignee.ID); !following {
		t.Fatal("the skipped follower's row was removed; skipping must not unsubscribe")
	}
}

// TestInactiveFollowerIsSkipped covers the account that was deactivated after it
// subscribed.
func TestInactiveFollowerIsSkipped(t *testing.T) {
	h := newFanOutHarness(t)
	h.follow(t, h.assignee)
	h.users.users[h.assignee.ID].Active = false

	if _, err := h.ticketSvc().Transition(context.Background(), h.admin, h.ticket.ID, domain.StateInProgress, ""); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	if got := h.rows(h.assignee.ID); len(got) != 0 {
		t.Fatalf("an inactive follower was told: %+v", got)
	}
}

// TestDowngradedFollowerIsNeverToldAboutAnInternalComment covers the follower
// whose role no longer reads internal comments. The internal branch checks the
// capability again, so this holds even though the access predicate already
// refuses a non-staff role.
func TestDowngradedFollowerIsNeverToldAboutAnInternalComment(t *testing.T) {
	h := newFanOutHarness(t)
	h.follow(t, h.assignee)
	h.users.users[h.assignee.ID].Role = domain.RoleUser

	mustAddComment(t, h, h.admin, "Staff only: refund approved", "internal")

	if got := h.rows(h.assignee.ID); len(got) != 0 {
		t.Fatalf("a downgraded follower was told about an internal comment: %+v", got)
	}
	if got := h.rows(h.requester.ID); len(got) != 0 {
		t.Fatalf("the requester was told about an internal comment: %+v", got)
	}
}

// TestFollowerIsNeverToldAboutTheirOwnAction pins the rule for followers: the
// person who moved the ticket is not told about it, however they are subscribed.
func TestFollowerIsNeverToldAboutTheirOwnAction(t *testing.T) {
	h := newFanOutHarness(t)
	h.follow(t, h.assignee)
	actor := domain.User{ID: h.assignee.ID, Name: h.assignee.Name, Role: h.assignee.Role}

	if _, err := h.ticketSvc().Transition(context.Background(), actor, h.ticket.ID, domain.StateInProgress, ""); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	if got := h.rows(h.assignee.ID); len(got) != 0 {
		t.Fatalf("the acting follower was told about their own action: %+v", got)
	}
	if got := h.rows(h.requester.ID); len(got) != 1 {
		t.Fatalf("the requester rows = %+v, want one", got)
	}
}

// TestUnfollowStopsEveryKind proves unfollowing is the end of the subscription
// for all the kinds the follower used to receive.
func TestUnfollowStopsEveryKind(t *testing.T) {
	h := newFanOutHarness(t)
	// An administrator follows, so every row she used to receive came from the
	// subscription and not from being the assignee or the requester.
	ari := h.users.seedRole("Ari", "ari@example.com", domain.RoleAdmin, true)
	h.follow(t, ari)
	if err := h.followSvc.Unfollow(context.Background(), ari, h.ticket.ID); err != nil {
		t.Fatalf("Unfollow: %v", err)
	}

	mustAddComment(t, h, h.admin, "Deployed to staging", "public")
	if _, err := h.ticketSvc().Transition(context.Background(), h.admin, h.ticket.ID, domain.StateInProgress, ""); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	priority := domain.PriorityHigh
	if _, err := h.ticketSvc().Update(context.Background(), h.admin, h.ticket.ID, domain.TicketUpdate{Priority: &priority}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := h.rows(ari.ID); len(got) != 0 {
		t.Fatalf("an ex-follower was told: %+v", got)
	}
	// The assignee still hears the same events, so the test proves the
	// subscription ended rather than the events stopped.
	if got := h.rows(h.assignee.ID); len(got) != 3 {
		t.Fatalf("assignee rows = %+v, want the comment, the state change and the priority change", got)
	}
}

// TestOutgoingAssigneeIsNotToldAboutTheAssignmentThatLocksThemOut covers the
// assignment fan-out at its sharpest: a follower who loses the ticket to the
// change itself is dropped, because the row would describe a ticket they can no
// longer open.
func TestOutgoingAssigneeIsNotToldAboutTheAssignmentThatLocksThemOut(t *testing.T) {
	h := newFanOutHarness(t)
	h.follow(t, h.assignee)
	bea := h.users.seedRole("Bea", "bea@example.com", domain.RoleAgent, true)

	if _, err := h.ticketSvc().Assign(context.Background(), h.admin, h.ticket.ID, &bea.ID, "handing over"); err != nil {
		t.Fatalf("Assign: %v", err)
	}
	if got := h.rows(h.assignee.ID); len(got) != 0 {
		t.Fatalf("the outgoing assignee was told about an assignment they lost: %+v", got)
	}
	got := h.rows(bea.ID)
	if len(got) != 1 || got[0].Kind != domain.NotificationAssigned {
		t.Fatalf("new assignee rows = %+v, want one assigned", got)
	}
}
