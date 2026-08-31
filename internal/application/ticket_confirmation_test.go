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
	// A resolved ticket carries resolved_at (stamped by the transition that
	// produced the state); the direct store seed must mirror that invariant.
	resolvedAt := ticket.UpdatedAt
	ticket.ResolvedAt = &resolvedAt
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

// seedPinnedResolvedTicket arranges a resolved ticket pinned to a workflow
// version (the state a workflow-pinned ticket is left in after its terminal
// resolve step): the pin is stamped on the seeded ticket and persisted via the
// direct store update, mirroring what the workflow create path writes.
func seedPinnedResolvedTicket(t *testing.T, h *ticketHarness, requesterID, assigneeID *int64, versionID int64) domain.Ticket {
	t.Helper()
	ticket := seedResolvedTicket(t, h, requesterID, assigneeID)
	ticket.WorkflowVersionID = &versionID
	if err := h.tickets.Update(context.Background(), &ticket); err != nil {
		t.Fatalf("seed pinned resolved ticket: %v", err)
	}
	return ticket
}

// TestRejectResolution pins the requester-rejection path (confirmation-closure
// and workflow-execution deltas): the requester returns their resolved ticket
// to in_progress; a workflow-pinned ticket is DETACHED (WorkflowVersionID
// nil-ed) so it continues as a manual ticket, while an already-manual ticket is
// a plain reopen. Rejection is a manual transition, not a workflow operation:
// exactly one requester-attributed audit event, never a "workflow" actor. The
// agent reopen of a resolved ticket keeps the pin (state-machine delta: agent
// reopen MUST NOT detach).
func TestRejectResolution(t *testing.T) {
	t.Run("pinned reject detaches the workflow and reopens manually", func(t *testing.T) {
		h := newTicketHarness()
		requester := h.users.seedRole("Bob", "bob@example.com", domain.RoleUser, true)
		const versionID int64 = 7
		ticket := seedPinnedResolvedTicket(t, h, &requester.ID, nil, versionID)
		actor := domain.User{ID: requester.ID, Name: requester.Name, Email: requester.Email, Role: domain.RoleUser}
		h.clock.Advance(timeMinute)

		updated, err := h.svc.RejectResolution(context.Background(), actor, ticket.ID)
		if err != nil {
			t.Fatalf("RejectResolution: requester rejection must succeed, got %v", err)
		}
		if updated.State != domain.StateInProgress {
			t.Fatalf("RejectResolution: rejected ticket must be in_progress, got %q", updated.State)
		}
		if updated.WorkflowVersionID != nil {
			t.Fatalf("RejectResolution: rejection must detach the workflow pin, got %v", *updated.WorkflowVersionID)
		}
		if updated.ResolvedAt != nil {
			t.Fatalf("RejectResolution: rejection must clear resolved_at, got %v", updated.ResolvedAt)
		}
		if updated.ClosedAt != nil {
			t.Fatalf("RejectResolution: rejection must not stamp closed_at, got %v", updated.ClosedAt)
		}
		// The persisted row mirrors the returned aggregate: detached, in_progress.
		stored, _ := h.tickets.GetByID(context.Background(), ticket.ID, application.TicketQuery{Scope: application.ScopeAll})
		if stored.State != domain.StateInProgress || stored.WorkflowVersionID != nil {
			t.Fatalf("RejectResolution: stored row must be a detached in_progress ticket, got state=%q pin=%v", stored.State, stored.WorkflowVersionID)
		}
		// Rejection is a manual transition, not a workflow operation: exactly one
		// requester-attributed transition event — never a "workflow" actor.
		events, _ := h.audits.ListByTicket(context.Background(), ticket.ID)
		if len(events) != 1 {
			t.Fatalf("RejectResolution: exactly one audit event expected, got %d", len(events))
		}
		ev := events[0]
		if ev.ActorUserID == nil || *ev.ActorUserID != requester.ID || ev.Actor == "workflow" {
			t.Fatalf("RejectResolution: event must be attributed to the requester %q, got actor=%q id=%v", requester.Name, ev.Actor, ev.ActorUserID)
		}
		if ev.FromValue == nil || *ev.FromValue != string(domain.StateResolved) || ev.ToValue == nil || *ev.ToValue != string(domain.StateInProgress) {
			t.Fatalf("RejectResolution: event must record resolved -> in_progress, got %v -> %v", ev.FromValue, ev.ToValue)
		}
	})

	t.Run("manual ticket reject is a plain reopen", func(t *testing.T) {
		h := newTicketHarness()
		requester := h.users.seedRole("Bob", "bob@example.com", domain.RoleUser, true)
		ticket := seedResolvedTicket(t, h, &requester.ID, nil) // WorkflowVersionID already nil
		actor := domain.User{ID: requester.ID, Name: requester.Name, Email: requester.Email, Role: domain.RoleUser}
		h.clock.Advance(timeMinute)

		updated, err := h.svc.RejectResolution(context.Background(), actor, ticket.ID)
		if err != nil {
			t.Fatalf("RejectResolution: manual-ticket rejection must succeed, got %v", err)
		}
		if updated.State != domain.StateInProgress || updated.WorkflowVersionID != nil {
			t.Fatalf("RejectResolution: manual ticket must return to in_progress with no pin, got state=%q pin=%v", updated.State, updated.WorkflowVersionID)
		}
		if updated.ResolvedAt != nil {
			t.Fatalf("RejectResolution: manual reopen must clear resolved_at, got %v", updated.ResolvedAt)
		}
	})

	t.Run("agent reopen keeps the workflow pin", func(t *testing.T) {
		h := newTicketHarness()
		requester := h.users.seedRole("Bob", "bob@example.com", domain.RoleUser, true)
		agent := h.users.seedRole("Ana", "ana@example.com", domain.RoleAgent, true)
		const versionID int64 = 7
		ticket := seedPinnedResolvedTicket(t, h, &requester.ID, &agent.ID, versionID)
		actor := domain.User{ID: agent.ID, Name: agent.Name, Email: agent.Email, Role: domain.RoleAgent}
		h.clock.Advance(timeMinute)

		updated, err := h.svc.Transition(context.Background(), actor, ticket.ID, domain.StateInProgress, "")
		if err != nil {
			t.Fatalf("Transition: agent reopen from resolved must succeed without a reason, got %v", err)
		}
		if updated.State != domain.StateInProgress {
			t.Fatalf("Transition: reopened ticket must be in_progress, got %q", updated.State)
		}
		if updated.WorkflowVersionID == nil || *updated.WorkflowVersionID != versionID {
			t.Fatalf("Transition: agent reopen must NOT detach the workflow pin %d, got %v", versionID, updated.WorkflowVersionID)
		}
	})
}

// TestConfirmResolution pins the requester-confirmation closure path
// (confirmation-closure delta): only the ticket's requester, only while the
// ticket is resolved. Confirmation stamps closed_at, keeps resolved_at, and
// attributes the audit event as requester_confirmation under the requester's
// identity. Non-requesters get Forbidden (readable roles) or NotFound
// (out-of-scope role users); non-resolved states fail at the state machine
// with no write.
func TestConfirmResolution(t *testing.T) {
	t.Run("requester confirms own resolved ticket", func(t *testing.T) {
		h := newTicketHarness()
		requester := h.users.seedRole("Bob", "bob@example.com", domain.RoleUser, true)
		ticket := seedResolvedTicket(t, h, &requester.ID, nil)
		actor := domain.User{ID: requester.ID, Name: requester.Name, Email: requester.Email, Role: domain.RoleUser}
		h.clock.Advance(timeMinute)

		updated, err := h.svc.ConfirmResolution(context.Background(), actor, ticket.ID)
		if err != nil {
			t.Fatalf("ConfirmResolution: requester confirmation must succeed, got %v", err)
		}
		if updated.State != domain.StateClosed {
			t.Fatalf("ConfirmResolution: confirmed ticket must be closed, got %q", updated.State)
		}
		if updated.ClosedAt == nil || !updated.ClosedAt.Equal(h.clock.now) {
			t.Fatalf("ConfirmResolution: confirmation must stamp closed_at, got %v", updated.ClosedAt)
		}
		if updated.ResolvedAt == nil {
			t.Fatal("ConfirmResolution: resolved_at must remain set after confirmation")
		}
		events, _ := h.audits.ListByTicket(context.Background(), ticket.ID)
		if len(events) != 1 {
			t.Fatalf("ConfirmResolution: confirmation must be audited exactly once, got %d events", len(events))
		}
		ev := events[0]
		if ev.ClosureVia == nil || *ev.ClosureVia != domain.ClosureViaRequesterConfirmation {
			t.Fatalf("ConfirmResolution: event must record closure_via %q, got %v",
				domain.ClosureViaRequesterConfirmation, ev.ClosureVia)
		}
		if ev.ActorUserID == nil || *ev.ActorUserID != requester.ID {
			t.Fatalf("ConfirmResolution: event actor must be the requester %d, got %v", requester.ID, ev.ActorUserID)
		}
	})

	t.Run("confirm on non-resolved states is a state-machine error with no write", func(t *testing.T) {
		for _, state := range []domain.State{domain.StateNew, domain.StateInProgress, domain.StateClosed} {
			t.Run(string(state), func(t *testing.T) {
				h := newTicketHarness()
				requester := h.users.seedRole("Bob", "bob@example.com", domain.RoleUser, true)
				cat := h.categories.seed("Bugs")
				ticket := seededTicket(h.tickets, cat.ID, state)
				ticket.RequesterUserID = &requester.ID
				if state == domain.StateClosed {
					ticket.ClosedAt = &h.clock.now
				}
				if err := h.tickets.Update(context.Background(), &ticket); err != nil {
					t.Fatalf("seed ticket: %v", err)
				}
				actor := domain.User{ID: requester.ID, Name: requester.Name, Email: requester.Email, Role: domain.RoleUser}

				_, err := h.svc.ConfirmResolution(context.Background(), actor, ticket.ID)
				var verr *domain.InvalidTransitionError
				if !errors.As(err, &verr) {
					t.Fatalf("ConfirmResolution on %s: want state-machine rejection, got %v", state, err)
				}
				if len(h.audits.events) != 0 {
					t.Fatalf("ConfirmResolution on %s: rejected confirmation must not be audited", state)
				}
			})
		}
	})

	t.Run("non-requester staff denied", func(t *testing.T) {
		for _, role := range []domain.Role{domain.RoleAgent, domain.RoleAdmin, domain.RoleRoot} {
			t.Run(string(role), func(t *testing.T) {
				h := newTicketHarness()
				requester := h.users.seedRole("Bob", "bob@example.com", domain.RoleUser, true)
				staff := h.users.seedRole("Ana", "ana@example.com", role, true)
				ticket := seedResolvedTicket(t, h, &requester.ID, &staff.ID)
				actor := domain.User{ID: staff.ID, Name: staff.Name, Email: staff.Email, Role: role}

				_, err := h.svc.ConfirmResolution(context.Background(), actor, ticket.ID)
				var ferr *domain.ForbiddenError
				if !errors.As(err, &ferr) || ferr.Message != application.ErrMsgNotTicketRequester {
					t.Fatalf("ConfirmResolution: non-requester %s must be denied with %q, got %v",
						role, application.ErrMsgNotTicketRequester, err)
				}
				stored, _ := h.tickets.GetByID(context.Background(), ticket.ID, application.TicketQuery{Scope: application.ScopeAll})
				if stored.State != domain.StateResolved {
					t.Fatalf("ConfirmResolution: denied confirmation must keep the ticket resolved, got %q", stored.State)
				}
			})
		}
	})

	t.Run("another role-user cannot confirm someone else's ticket", func(t *testing.T) {
		h := newTicketHarness()
		requester := h.users.seedRole("Bob", "bob@example.com", domain.RoleUser, true)
		ticket := seedResolvedTicket(t, h, &requester.ID, nil)
		other := domain.User{ID: 42, Name: "Mallory", Role: domain.RoleUser}

		_, err := h.svc.ConfirmResolution(context.Background(), other, ticket.ID)
		var nerr *domain.NotFoundError
		if !errors.As(err, &nerr) {
			t.Fatalf("ConfirmResolution: out-of-scope role user must get NotFound, got %v", err)
		}
	})
}
