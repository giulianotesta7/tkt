package application_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestAPITicketScopeAll(t *testing.T) {
	for _, tc := range []struct {
		name   string
		kind   domain.UserKind
		role   domain.Role
		active bool
		flag   bool
		all    bool
	}{
		{"agent service with scope", domain.UserKindService, domain.RoleAgent, true, true, true},
		{"agent service without scope", domain.UserKindService, domain.RoleAgent, true, false, false},
		{"person unchanged", domain.UserKindPerson, domain.RoleAgent, true, true, false},
		{"user service unchanged", domain.UserKindService, domain.RoleUser, true, true, false},
		{"inactive service unchanged", domain.UserKindService, domain.RoleAgent, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newTicketHarness()
			cat := h.categories.seed("Bugs")
			owner := h.users.seedRole("Owner", "owner@example.com", domain.RoleUser, true)
			actor := h.users.seedRole("Bot", "bot@example.com", tc.role, tc.active)
			actor.Kind = tc.kind
			if err := h.users.Update(t.Context(), &actor); err != nil {
				t.Fatal(err)
			}
			ticket, err := h.svc.Create(t.Context(), owner, validCreateInput(cat.ID))
			if err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			if tc.flag {
				ctx = application.WithTicketScopeAll(ctx)
			}
			search := application.NewSearchService(h.tickets, &fakeSearchStore{tickets: h.tickets})
			result, err := search.Search(ctx, actor, application.TicketQuery{}, 1)
			if err != nil || (len(result.Tickets) == 1) != tc.all {
				t.Fatalf("search = %+v, %v", result, err)
			}
			_, err = h.svc.GetByID(ctx, actor, ticket.ID)
			if tc.all && err != nil || !tc.all && !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("detail = %v", err)
			}
			commentsTx := newFakeCommentUnitOfWork(h.comments)
			comments := application.NewCommentService(h.tickets, h.comments, h.users, commentsTx, h.clock)
			_, err = comments.Add(ctx, actor, ticket.ID, "Public reply", "public")
			if tc.all && err != nil || !tc.all && !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("comment = %v", err)
			}
			// User-role transitions have their own capability rejection. Never
			// let the scope flag bypass that role rule.
			_, err = h.svc.Transition(ctx, actor, ticket.ID, domain.StateInProgress, "")
			if tc.all && err != nil || !tc.all && err == nil {
				t.Fatalf("transition = %v", err)
			}
			if tc.all {
				// tickets:all widens the read, not the agent-only assignment
				// restriction or the prohibition on service assignees.
				target := h.users.seedRole("Agent", "agent@example.com", domain.RoleAgent, true)
				if _, err := h.svc.Assign(ctx, actor, ticket.ID, &target.ID, ""); err == nil {
					t.Fatal("agent assigned another person an unassigned ticket")
				}
				if _, err := h.svc.Assign(ctx, actor, ticket.ID, &actor.ID, ""); err == nil {
					t.Fatal("service user became an assignee")
				}
				if _, err := h.svc.ConfirmResolution(ctx, actor, ticket.ID); err == nil {
					t.Fatal("scope bypassed requester identity")
				}
			}
			stored, err := h.svc.GetByID(t.Context(), owner, ticket.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.all {
				if stored.Ticket.State != domain.StateInProgress || len(stored.Comments) != 1 || len(stored.AuditEvents) != 2 {
					t.Fatalf("effects = %+v", stored)
				}
			} else if stored.Ticket.State != domain.StateNew || len(stored.Comments) != 0 || len(stored.AuditEvents) != 1 {
				t.Fatalf("rejected operations changed data: %+v", stored)
			}
		})
	}
}

func TestAPITicketScopeAllNotificationRecipientsUnchanged(t *testing.T) {
	// Run the same ordinary person's change with and without an API flag.
	// Candidate access belongs to the recipient, never to the request context.
	run := func(ctx context.Context) []domain.Notification {
		h := newTicketHarness()
		cat := h.categories.seed("Bugs")
		owner := h.users.seedRole("Owner", "owner@example.com", domain.RoleUser, true)
		admin := h.users.seedRole("Admin", "admin@example.com", domain.RoleAdmin, true)
		outsider := h.users.seedRole("Outsider", "outsider@example.com", domain.RoleAgent, true)
		ticket, err := h.svc.Create(ctx, owner, validCreateInput(cat.ID))
		if err != nil {
			t.Fatal(err)
		}
		followers := newFakeFollowerStore(h.users)
		followers.seedFollow(ticket.ID, outsider.ID)
		follow := application.NewFollowService(h.tickets, h.users, followers, h.clock)
		h.svc.WithFollowers(follow)
		if _, err := h.svc.Transition(ctx, admin, ticket.ID, domain.StateInProgress, ""); err != nil {
			t.Fatal(err)
		}
		for _, n := range h.tx.notifications {
			if n.UserID == outsider.ID {
				t.Fatal("out-of-scope follower notified")
			}
		}
		return h.tx.notifications
	}
	plain := run(t.Context())
	flagged := run(application.WithTicketScopeAll(t.Context()))
	if len(plain) == 0 || !reflect.DeepEqual(plain, flagged) {
		t.Fatalf("notifications differ: plain=%+v flagged=%+v", plain, flagged)
	}
}
