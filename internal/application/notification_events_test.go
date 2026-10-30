package application_test

import (
	"context"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Issue #344 slice B2: who is notified, per the S10 table (every row except
// the two comment rows, which slice B3 owns). The recipients are computed by
// the APPLICATION layer from policy and roles and stamped into the mutation
// the unit of work persists — so these tests read the notifications the fake
// unit of work recorded, which is the only place the policy is observable at
// this layer. Written before the policy existed (strict TDD RED).

// notifyHarness is a ticket whose requester is a user-role person and whose
// assignee is an agent, plus the two of them and an admin that acts on neither.
type notifyHarness struct {
	*ticketHarness
	requester domain.User
	assignee  domain.User
	admin     domain.User
	ticket    domain.Ticket
	// followers is the follower port of the ticket service under test (issue
	// #418); followSvc is the same collaborator, so a test can subscribe somebody
	// through the public use case instead of the fake.
	followers *fakeFollowerStore
	followSvc *application.FollowService
}

func newNotifyHarness(t *testing.T) *notifyHarness {
	t.Helper()
	h := &notifyHarness{ticketHarness: newTicketHarness()}
	h.followers = newFakeFollowerStore(h.users)
	h.followSvc = application.NewFollowService(h.tickets, h.users, h.followers, h.clock)
	h.svc.WithFollowers(h.followSvc)
	cat := h.categories.seed("Bugs")
	h.requester = h.users.seedRole("Rita", "rita@example.com", domain.RoleUser, true)
	h.assignee = h.users.seedRole("Ana", "ana@example.com", domain.RoleAgent, true)
	h.admin = h.users.seedRole("Ada", "ada@example.com", domain.RoleAdmin, true)
	h.ticket = h.tickets.seed(domain.Ticket{
		Title: "VPN drops", CategoryID: cat.ID, RequesterUserID: ptr(h.requester.ID),
		UserID: ptr(h.assignee.ID), Priority: domain.PriorityMedium, State: domain.StateNew,
		CreatedAt: h.clock.now, UpdatedAt: h.clock.now,
	})
	return h
}

// notified returns the recorded notifications for one recipient, in order.
func (h *notifyHarness) notified(userID int64) []domain.Notification {
	var out []domain.Notification
	for _, n := range h.tx.notifications {
		if n.UserID == userID {
			out = append(out, n)
		}
	}
	return out
}

func (h *notifyHarness) requireOne(t *testing.T, userID int64, kind domain.NotificationKind, from, to string) domain.Notification {
	t.Helper()
	got := h.notified(userID)
	if len(got) != 1 {
		t.Fatalf("%d notifications for user %d, want 1: %+v", len(got), userID, got)
	}
	n := got[0]
	if n.Kind != kind {
		t.Errorf("kind = %q, want %q", n.Kind, kind)
	}
	if n.TicketID != h.ticket.ID {
		t.Errorf("ticket = %d, want %d", n.TicketID, h.ticket.ID)
	}
	assertFact(t, "from", n.FromValue, from)
	assertFact(t, "to", n.ToValue, to)
	if n.CreatedAt.IsZero() {
		t.Error("notification has no creation instant")
	}
	return n
}

func assertFact(t *testing.T, name string, got *string, want string) {
	t.Helper()
	if want == "" {
		if got != nil {
			t.Errorf("%s = %q, want absent", name, *got)
		}
		return
	}
	if got == nil || *got != want {
		t.Errorf("%s = %v, want %q", name, got, want)
	}
}

// TestNotificationOnAssignment covers the S10 "ticket assigned to someone"
// row: the new assignee learns about it, an assignment to himself or a
// cleared assignment tells nobody.
func TestNotificationOnAssignment(t *testing.T) {
	t.Run("manual assign notifies the new assignee", func(t *testing.T) {
		h := newNotifyHarness(t)
		other := h.users.seedRole("Bea", "bea@example.com", domain.RoleAgent, true)
		if _, err := h.svc.Assign(context.Background(), h.admin, h.ticket.ID, &other.ID, "handing over"); err != nil {
			t.Fatalf("Assign: %v", err)
		}
		n := h.requireOne(t, other.ID, domain.NotificationAssigned, "", "")
		if n.ActorUserID == nil || *n.ActorUserID != h.admin.ID {
			t.Errorf("actor = %v, want the assigning admin %d", n.ActorUserID, h.admin.ID)
		}
		// The previous assignee lost the ticket; nobody else is told.
		if got := h.tx.notifications; len(got) != 1 {
			t.Fatalf("recorded %d notifications, want only the new assignee's: %+v", len(got), got)
		}
	})

	t.Run("claim by the assignee himself notifies nobody", func(t *testing.T) {
		h := newNotifyHarness(t)
		unassigned := h.tickets.seed(domain.Ticket{
			Title: "queue", CategoryID: h.ticket.CategoryID, RequesterUserID: ptr(h.requester.ID),
			Priority: domain.PriorityLow, State: domain.StateNew, CreatedAt: h.clock.now, UpdatedAt: h.clock.now,
		})
		actor := domain.User{ID: h.assignee.ID, Name: h.assignee.Name, Role: domain.RoleAgent}
		if _, err := h.svc.Assign(context.Background(), actor, unassigned.ID, &actor.ID, ""); err != nil {
			t.Fatalf("Assign (self claim): %v", err)
		}
		if len(h.tx.notifications) != 0 {
			t.Fatalf("self claim recorded %+v, want nobody — never the person who performed the action", h.tx.notifications)
		}
	})

	t.Run("clearing the assignment notifies nobody", func(t *testing.T) {
		h := newNotifyHarness(t)
		if _, err := h.svc.Assign(context.Background(), h.admin, h.ticket.ID, nil, ""); err != nil {
			t.Fatalf("Assign (clear): %v", err)
		}
		if len(h.tx.notifications) != 0 {
			t.Fatalf("clearing recorded %+v, want nobody", h.tx.notifications)
		}
	})
}

// TestNotificationOnStateChange covers the S10 state row: requester and
// assignee, each addressed in the direction of the change.
func TestNotificationOnStateChange(t *testing.T) {
	t.Run("both are told when a third person moves the ticket", func(t *testing.T) {
		h := newNotifyHarness(t)
		if _, err := h.svc.Transition(context.Background(), h.admin, h.ticket.ID, domain.StateInProgress, ""); err != nil {
			t.Fatalf("Transition: %v", err)
		}
		req := h.requireOne(t, h.requester.ID, domain.NotificationStateChanged, "new", "in_progress")
		if req.ActorUserID == nil || *req.ActorUserID != h.admin.ID {
			t.Errorf("requester row actor = %v, want %d", req.ActorUserID, h.admin.ID)
		}
		h.requireOne(t, h.assignee.ID, domain.NotificationStateChanged, "new", "in_progress")
	})

	t.Run("the acting assignee is not told about their own move", func(t *testing.T) {
		h := newNotifyHarness(t)
		actor := domain.User{ID: h.assignee.ID, Name: h.assignee.Name, Role: domain.RoleAgent}
		if _, err := h.svc.Transition(context.Background(), actor, h.ticket.ID, domain.StateInProgress, ""); err != nil {
			t.Fatalf("Transition: %v", err)
		}
		h.requireOne(t, h.requester.ID, domain.NotificationStateChanged, "new", "in_progress")
		if got := h.notified(h.assignee.ID); len(got) != 0 {
			t.Fatalf("assignee was told about their own action: %+v", got)
		}
	})

	t.Run("a reopen is a plain state change", func(t *testing.T) {
		h := newNotifyHarness(t)
		h.ticket.State = domain.StateResolved
		if err := h.tickets.Update(context.Background(), &h.ticket); err != nil {
			t.Fatalf("seed resolved: %v", err)
		}
		if _, err := h.svc.Transition(context.Background(), h.admin, h.ticket.ID, domain.StateInProgress, ""); err != nil {
			t.Fatalf("Transition (reopen): %v", err)
		}
		h.requireOne(t, h.requester.ID, domain.NotificationStateChanged, "resolved", "in_progress")
		h.requireOne(t, h.assignee.ID, domain.NotificationStateChanged, "resolved", "in_progress")
	})
}

// TestNotificationOnResolveAsksTheRequesterToConfirm covers the S10
// "resolved, awaiting confirmation" row: the requester is asked to confirm
// (not told about a bare state change), the assignee sees the state change.
func TestNotificationOnResolveAsksTheRequesterToConfirm(t *testing.T) {
	h := newNotifyHarness(t)
	if _, err := h.svc.Transition(context.Background(), h.admin, h.ticket.ID, domain.StateResolved, ""); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	h.requireOne(t, h.requester.ID, domain.NotificationResolvedAwaitingConfirmation, "new", "resolved")
	h.requireOne(t, h.assignee.ID, domain.NotificationStateChanged, "new", "resolved")
}

// TestNotificationOnResolutionConfirmation covers the S10 rows for the
// requester's yes: the assignee learns the resolution was confirmed. The
// requester is the actor, so they are told about neither.
func TestNotificationOnResolutionConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name string
		act  func(h *notifyHarness, actor domain.User) error
		kind domain.NotificationKind
		from string
		to   string
	}{
		{"confirm", func(h *notifyHarness, actor domain.User) error {
			_, err := h.svc.ConfirmResolution(context.Background(), actor, h.ticket.ID)
			return err
		}, domain.NotificationResolutionConfirmed, "resolved", "closed"},
		{"reject", func(h *notifyHarness, actor domain.User) error {
			_, err := h.svc.RejectResolution(context.Background(), actor, h.ticket.ID)
			return err
		}, domain.NotificationResolutionRejected, "resolved", "in_progress"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newNotifyHarness(t)
			h.ticket.State = domain.StateResolved
			if err := h.tickets.Update(context.Background(), &h.ticket); err != nil {
				t.Fatalf("seed resolved: %v", err)
			}
			actor := domain.User{ID: h.requester.ID, Name: h.requester.Name, Role: h.requester.Role}
			if err := tc.act(h, actor); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			h.requireOne(t, h.assignee.ID, tc.kind, tc.from, tc.to)
			if got := h.notified(h.requester.ID); len(got) != 0 {
				t.Fatalf("requester was told about their own %s: %+v", tc.name, got)
			}
		})
	}
}

// TestNotificationOnPriorityButNotTitle covers the two edit rows of S10 and
// the maintainer's decision (L4): priority edits notify, title edits do not.
func TestNotificationOnPriorityButNotTitle(t *testing.T) {
	t.Run("priority change notifies requester and assignee", func(t *testing.T) {
		h := newNotifyHarness(t)
		priority := domain.PriorityHigh
		if _, err := h.svc.Update(context.Background(), h.admin, h.ticket.ID, domain.TicketUpdate{Priority: &priority}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		h.requireOne(t, h.requester.ID, domain.NotificationPriorityChanged, "medium", "high")
		h.requireOne(t, h.assignee.ID, domain.NotificationPriorityChanged, "medium", "high")
	})

	t.Run("title change notifies nobody", func(t *testing.T) {
		h := newNotifyHarness(t)
		title := "VPN drops every hour"
		if _, err := h.svc.Update(context.Background(), h.admin, h.ticket.ID, domain.TicketUpdate{Title: &title}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if len(h.tx.notifications) != 0 {
			t.Fatalf("title edit recorded %+v, want nobody", h.tx.notifications)
		}
	})
}

// TestNotificationNeverAddressesTheActor covers the S10 rule at its hardest:
// the actor is BOTH the requester and the assignee, so every candidate is the
// actor and the change notifies nobody. The unit of work still receives the
// mutation (the change persists); the domain's self-recipient guard would
// reject the row, so the policy must drop it first.
func TestNotificationNeverAddressesTheActor(t *testing.T) {
	h := newNotifyHarness(t)
	solo := h.users.seedRole("Sol", "sol@example.com", domain.RoleAdmin, true)
	h.ticket.RequesterUserID = ptr(solo.ID)
	h.ticket.UserID = ptr(solo.ID)
	if err := h.tickets.Update(context.Background(), &h.ticket); err != nil {
		t.Fatalf("seed solo ticket: %v", err)
	}
	actor := domain.User{ID: solo.ID, Name: solo.Name, Role: domain.RoleAdmin}
	if _, err := h.svc.Transition(context.Background(), actor, h.ticket.ID, domain.StateInProgress, ""); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	if len(h.tx.notifications) != 0 {
		t.Fatalf("recorded %+v, want nobody — the actor is every candidate", h.tx.notifications)
	}
}

// TestNotificationDropsRecipientsWhoCannotOpenTheTicket covers the second
// half of the S10 rule: a deactivated person, or a requester whose role no
// longer reaches the ticket, is never told about a change they cannot see.
func TestNotificationDropsRecipientsWhoCannotOpenTheTicket(t *testing.T) {
	t.Run("deactivated requester", func(t *testing.T) {
		h := newNotifyHarness(t)
		h.users.users[h.requester.ID].Active = false
		if _, err := h.svc.Transition(context.Background(), h.admin, h.ticket.ID, domain.StateInProgress, ""); err != nil {
			t.Fatalf("Transition: %v", err)
		}
		if got := h.notified(h.requester.ID); len(got) != 0 {
			t.Fatalf("deactivated requester was notified: %+v", got)
		}
		h.requireOne(t, h.assignee.ID, domain.NotificationStateChanged, "new", "in_progress")
	})

	t.Run("requester promoted to agent without the assignment", func(t *testing.T) {
		h := newNotifyHarness(t)
		// An agent only reads tickets assigned to them (read scope), so this
		// requester can no longer open their own ticket.
		h.users.users[h.requester.ID].Role = domain.RoleAgent
		if _, err := h.svc.Transition(context.Background(), h.admin, h.ticket.ID, domain.StateInProgress, ""); err != nil {
			t.Fatalf("Transition: %v", err)
		}
		if got := h.notified(h.requester.ID); len(got) != 0 {
			t.Fatalf("requester without read access was notified: %+v", got)
		}
		h.requireOne(t, h.assignee.ID, domain.NotificationStateChanged, "new", "in_progress")
	})

	t.Run("legacy ticket without a requester user", func(t *testing.T) {
		h := newNotifyHarness(t)
		h.ticket.RequesterUserID = nil
		if err := h.tickets.Update(context.Background(), &h.ticket); err != nil {
			t.Fatalf("seed legacy ticket: %v", err)
		}
		if _, err := h.svc.Transition(context.Background(), h.admin, h.ticket.ID, domain.StateInProgress, ""); err != nil {
			t.Fatalf("Transition: %v", err)
		}
		if len(h.tx.notifications) != 1 {
			t.Fatalf("recorded %+v, want only the assignee's row", h.tx.notifications)
		}
		h.requireOne(t, h.assignee.ID, domain.NotificationStateChanged, "new", "in_progress")
	})
}

// TestTicketCreationNotifiesNobody pins the S10 decision that creating a
// ticket — and a new claimable ticket in a desk — tells nobody: the queue
// shows it.
func TestTicketCreationNotifiesNobody(t *testing.T) {
	h := newNotifyHarness(t)
	actor := domain.User{ID: h.requester.ID, Name: h.requester.Name, Role: domain.RoleUser}
	if _, err := h.svc.Create(context.Background(), actor, application.CreateTicketInput{
		Title: "new request", CategoryID: h.ticket.CategoryID, Priority: domain.PriorityLow,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(h.tx.notifications) != 0 {
		t.Fatalf("creation recorded %+v, want nobody", h.tx.notifications)
	}
}

// TestNotificationActorsAreAlsoAuditActors pins the referential rule the
// notification schema imposes (parent advisory on slice B2): notifications.
// actor_user_id references users WITHOUT a cascade, so a person who appeared
// ONLY as a notification actor could never be deleted (UserStore.Delete answers
// ErrReferenced). Every actor this policy stamps is therefore also the actor of
// the change's audit event — for every kind the ticket paths emit.
func TestNotificationActorsAreAlsoAuditActors(t *testing.T) {
	resolved := func(h *notifyHarness) {
		h.ticket.State = domain.StateResolved
		if err := h.tickets.Update(context.Background(), &h.ticket); err != nil {
			t.Fatalf("seed resolved: %v", err)
		}
	}
	cases := []struct {
		name string
		prep func(h *notifyHarness)
		act  func(h *notifyHarness) error
		kind domain.NotificationKind
	}{
		{"assigned", nil, func(h *notifyHarness) error {
			other := h.users.seedRole("Bea", "bea-actors@example.com", domain.RoleAgent, true)
			_, err := h.svc.Assign(context.Background(), h.admin, h.ticket.ID, &other.ID, "handing over")
			return err
		}, domain.NotificationAssigned},
		{"state_changed", nil, func(h *notifyHarness) error {
			_, err := h.svc.Transition(context.Background(), h.admin, h.ticket.ID, domain.StateInProgress, "")
			return err
		}, domain.NotificationStateChanged},
		{"resolved_awaiting_confirmation", nil, func(h *notifyHarness) error {
			_, err := h.svc.Transition(context.Background(), h.admin, h.ticket.ID, domain.StateResolved, "")
			return err
		}, domain.NotificationResolvedAwaitingConfirmation},
		{"resolution_confirmed", resolved, func(h *notifyHarness) error {
			actor := domain.User{ID: h.requester.ID, Name: h.requester.Name, Role: h.requester.Role}
			_, err := h.svc.ConfirmResolution(context.Background(), actor, h.ticket.ID)
			return err
		}, domain.NotificationResolutionConfirmed},
		{"resolution_rejected", resolved, func(h *notifyHarness) error {
			actor := domain.User{ID: h.requester.ID, Name: h.requester.Name, Role: h.requester.Role}
			_, err := h.svc.RejectResolution(context.Background(), actor, h.ticket.ID)
			return err
		}, domain.NotificationResolutionRejected},
		{"priority_changed", nil, func(h *notifyHarness) error {
			priority := domain.PriorityHigh
			_, err := h.svc.Update(context.Background(), h.admin, h.ticket.ID, domain.TicketUpdate{Priority: &priority})
			return err
		}, domain.NotificationPriorityChanged},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newNotifyHarness(t)
			if tc.prep != nil {
				tc.prep(h)
			}
			if err := tc.act(h); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			emitted := 0
			for _, n := range h.tx.notifications {
				if n.Kind != tc.kind {
					continue
				}
				emitted++
				if n.ActorUserID == nil {
					continue
				}
				found := false
				for _, ev := range h.audits.events[n.TicketID] {
					if ev.ActorUserID != nil && *ev.ActorUserID == *n.ActorUserID {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("%s notification actor %d is not an audit actor of ticket %d — that user would be undeletable",
						tc.kind, *n.ActorUserID, n.TicketID)
				}
			}
			if emitted == 0 {
				t.Fatalf("%s emitted no %s notification to check", tc.name, tc.kind)
			}
		})
	}
}

// TestNotificationNeverAddressesAServiceUser (issue #488, S3): a service user
// named by the ticket, as requester or assignee, is never notified; the person
// next to it still is.
func TestNotificationNeverAddressesAServiceUser(t *testing.T) {
	h := newNotifyHarness(t)
	h.users.users[h.requester.ID].Kind = domain.UserKindService
	if _, err := h.svc.Transition(context.Background(), h.admin, h.ticket.ID, domain.StateInProgress, ""); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	if got := h.notified(h.requester.ID); len(got) != 0 {
		t.Fatalf("service requester was notified: %+v", got)
	}
	h.requireOne(t, h.assignee.ID, domain.NotificationStateChanged, "new", "in_progress")

	h.users.users[h.assignee.ID].Kind = domain.UserKindService
	if _, err := h.svc.Transition(context.Background(), h.admin, h.ticket.ID, domain.StateResolved, ""); err != nil {
		t.Fatalf("Transition to resolved: %v", err)
	}
	if got := h.notified(h.assignee.ID); len(got) != 1 {
		t.Fatalf("service assignee rows = %+v, want only the one from before it became a service user", got)
	}
}
