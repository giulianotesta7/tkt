package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// seedResolvedTicket arranges a stored resolved ticket (bypassing the
// service) for the manual-closure gate tests. requesterID optionally pins a
// requester (nil keeps the ticket requester-NULL); assigneeID optionally
// assigns an agent so the agent scoped read admits the ticket.
func seedResolvedTicket(t *testing.T, h *ticketHarness, requesterID, assigneeID *int64) domain.Ticket {
	t.Helper()
	cat := h.categories.seed("Bugs")
	ticket := seededTicket(h.tickets, cat.ID, domain.StateResolved)
	ticket.RequesterUserID = requesterID
	ticket.UserID = assigneeID
	if err := h.tickets.Update(context.Background(), &ticket); err != nil {
		t.Fatalf("seed resolved ticket: %v", err)
	}
	return ticket
}

// TestManualClosureRequiresConfirmation pins the manual-closure gate on
// Transition (state-machine delta "Agent cannot close a requester-owned
// resolved ticket"; confirmation-closure delta "Agent closes a requester-less
// resolved ticket"): a manual resolved -> closed transition on a ticket that
// HAS a requester is rejected for every actor with no state change and no
// audit row, while a requester-NULL ticket closes manually and the event is
// attributed as a manual agent closure.
func TestManualClosureRequiresConfirmation(t *testing.T) {
	t.Run("assigned agent denied on requester-owned", func(t *testing.T) {
		h := newTicketHarness()
		requester := h.users.seedRole("Bob", "bob@example.com", domain.RoleUser, true)
		agent := h.users.seedRole("Ana", "ana@example.com", domain.RoleAgent, true)
		ticket := seedResolvedTicket(t, h, &requester.ID, &agent.ID)
		actor := domain.User{ID: agent.ID, Name: agent.Name, Email: agent.Email, Role: domain.RoleAgent}
		h.clock.Advance(timeMinute)

		_, err := h.svc.Transition(context.Background(), actor, ticket.ID, domain.StateClosed, "")
		var ferr *domain.ForbiddenError
		if !errors.As(err, &ferr) || ferr.Message != application.ErrMsgClosureRequiresConfirmation {
			t.Fatalf("Transition: requester-owned manual closure must be denied with %q, got %v",
				application.ErrMsgClosureRequiresConfirmation, err)
		}
		stored, _ := h.tickets.GetByID(context.Background(), ticket.ID, application.TicketQuery{Scope: application.ScopeAll})
		if stored.State != domain.StateResolved {
			t.Fatalf("Transition: denied closure must keep the ticket resolved, got %q", stored.State)
		}
		if len(h.audits.events) != 0 {
			t.Fatal("Transition: denied closure must not be audited")
		}
	})

	t.Run("admin denied on requester-owned", func(t *testing.T) {
		h := newTicketHarness()
		requester := h.users.seedRole("Bob", "bob@example.com", domain.RoleUser, true)
		ticket := seedResolvedTicket(t, h, &requester.ID, nil)
		actor := domain.User{ID: 9, Name: "Ada", Role: domain.RoleAdmin}
		h.clock.Advance(timeMinute)

		_, err := h.svc.Transition(context.Background(), actor, ticket.ID, domain.StateClosed, "")
		var ferr *domain.ForbiddenError
		if !errors.As(err, &ferr) || ferr.Message != application.ErrMsgClosureRequiresConfirmation {
			t.Fatalf("Transition: requester-owned manual closure must be denied with %q, got %v",
				application.ErrMsgClosureRequiresConfirmation, err)
		}
		stored, _ := h.tickets.GetByID(context.Background(), ticket.ID, application.TicketQuery{Scope: application.ScopeAll})
		if stored.State != domain.StateResolved {
			t.Fatalf("Transition: denied closure must keep the ticket resolved, got %q", stored.State)
		}
		if len(h.audits.events) != 0 {
			t.Fatal("Transition: denied closure must not be audited")
		}
	})

	t.Run("requester-NULL closes manually via assigned agent", func(t *testing.T) {
		h := newTicketHarness()
		agent := h.users.seedRole("Ana", "ana@example.com", domain.RoleAgent, true)
		ticket := seedResolvedTicket(t, h, nil, &agent.ID)
		actor := domain.User{ID: agent.ID, Name: agent.Name, Email: agent.Email, Role: domain.RoleAgent}
		h.clock.Advance(timeMinute)

		updated, err := h.svc.Transition(context.Background(), actor, ticket.ID, domain.StateClosed, "")
		if err != nil {
			t.Fatalf("Transition: requester-NULL manual closure must succeed, got %v", err)
		}
		if updated.State != domain.StateClosed {
			t.Fatalf("Transition: manually closed ticket must be closed, got %q", updated.State)
		}
		if updated.ClosedAt == nil || !updated.ClosedAt.Equal(h.clock.now) {
			t.Fatalf("Transition: closure must stamp closed_at from the clock, got %v", updated.ClosedAt)
		}
		events, _ := h.audits.ListByTicket(context.Background(), ticket.ID)
		if len(events) != 1 {
			t.Fatalf("Transition: closure must be audited exactly once, got %d events", len(events))
		}
		if events[0].ClosureVia == nil || *events[0].ClosureVia != domain.ClosureViaManualAgent {
			t.Fatalf("Transition: closure event must record closure_via %q, got %v",
				domain.ClosureViaManualAgent, events[0].ClosureVia)
		}
	})
}
