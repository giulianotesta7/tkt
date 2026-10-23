package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestTicketStoreFiltersOwnedTicketsByMultipleStates(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	actorID := seedUser(t, s, "Requester", "requester@example.test", true)
	categoryID := seedCategory(t, s, "Support")

	for _, ticket := range []domain.Ticket{
		{Number: 101, Title: "New", CategoryID: categoryID, RequesterUserID: &actorID, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: testClock, UpdatedAt: testClock},
		{Number: 102, Title: "In progress", CategoryID: categoryID, RequesterUserID: &actorID, Priority: domain.PriorityMedium, State: domain.StateInProgress, CreatedAt: testClock.Add(time.Minute), UpdatedAt: testClock.Add(time.Minute)},
		{Number: 103, Title: "Resolved", CategoryID: categoryID, RequesterUserID: &actorID, Priority: domain.PriorityMedium, State: domain.StateResolved, CreatedAt: testClock.Add(2 * time.Minute), UpdatedAt: testClock.Add(2 * time.Minute)},
	} {
		seedTicket(t, s, ticket)
	}

	q := application.TicketQuery{
		Scope:   application.ScopeOwned,
		ActorID: actorID,
		States:  []domain.State{domain.StateNew, domain.StateInProgress},
	}
	got, err := s.TicketStore().List(ctx, q, application.Page{Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("List returned %d tickets, want 2", len(got))
	}
	for _, ticket := range got {
		if ticket.State == domain.StateResolved {
			t.Errorf("List returned resolved ticket %d", ticket.Number)
		}
	}

	count, err := s.TicketStore().Count(ctx, q)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 2 {
		t.Errorf("Count = %d, want 2", count)
	}
}

// TestTicketStoreRequesterNeedsActionKeepsTheOwnedScope pins the store predicate
// the requester's "Needs your action" section runs (issue #411, S8): the OR of
// (active work on hold) and (resolved awaiting confirmation) must be
// parenthesized so it ANDs with the requester own-ticket scope clause. A
// requester therefore never reads another requester's held or resolved ticket,
// while the same predicate under ScopeAll proves both halves are selected.
func TestTicketStoreRequesterNeedsActionKeepsTheOwnedScope(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	alice := seedUser(t, s, "Alice", "alice@example.com", true)
	bob := seedUser(t, s, "Bob", "bob@example.com", true)
	categoryID := seedCategory(t, s, "Support")

	for _, ticket := range []domain.Ticket{
		{Number: 101, Title: "Alice open", CategoryID: categoryID, RequesterUserID: &alice, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: testClock, UpdatedAt: testClock},
		{Number: 102, Title: "Alice held", CategoryID: categoryID, RequesterUserID: &alice, Priority: domain.PriorityMedium, State: domain.StateInProgress, CreatedAt: testClock.Add(time.Minute), UpdatedAt: testClock.Add(time.Minute)},
		{Number: 103, Title: "Alice resolved", CategoryID: categoryID, RequesterUserID: &alice, Priority: domain.PriorityMedium, State: domain.StateResolved, CreatedAt: testClock.Add(2 * time.Minute), UpdatedAt: testClock.Add(2 * time.Minute)},
		{Number: 104, Title: "Alice closed", CategoryID: categoryID, RequesterUserID: &alice, Priority: domain.PriorityMedium, State: domain.StateClosed, CreatedAt: testClock.Add(3 * time.Minute), UpdatedAt: testClock.Add(3 * time.Minute)},
		{Number: 105, Title: "Alice cancelled", CategoryID: categoryID, RequesterUserID: &alice, Priority: domain.PriorityMedium, State: domain.StateCancelled, CreatedAt: testClock.Add(4 * time.Minute), UpdatedAt: testClock.Add(4 * time.Minute)},
		{Number: 106, Title: "Bob held", CategoryID: categoryID, RequesterUserID: &bob, Priority: domain.PriorityMedium, State: domain.StateInProgress, CreatedAt: testClock.Add(5 * time.Minute), UpdatedAt: testClock.Add(5 * time.Minute)},
		{Number: 107, Title: "Bob resolved", CategoryID: categoryID, RequesterUserID: &bob, Priority: domain.PriorityMedium, State: domain.StateResolved, CreatedAt: testClock.Add(6 * time.Minute), UpdatedAt: testClock.Add(6 * time.Minute)},
		{Number: 108, Title: "Bob open", CategoryID: categoryID, RequesterUserID: &bob, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: testClock.Add(7 * time.Minute), UpdatedAt: testClock.Add(7 * time.Minute)},
	} {
		seedTicket(t, s, ticket)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE tickets SET on_hold_since = ?, on_hold_reason = 'waiting for the requester' WHERE number IN (102, 106)`, testClock.Add(8*time.Minute).UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("set holds: %v", err)
	}

	numbers := func(tickets []domain.Ticket) []int64 {
		out := make([]int64, 0, len(tickets))
		for _, ticket := range tickets {
			out = append(out, int64(ticket.Number))
		}
		return out
	}
	equal := func(got []int64, want ...int64) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	// One requester: the held and the resolved tickets, newest first, and
	// nothing of Bob's — the OR must not escape the own-ticket scope.
	owned := application.TicketQuery{Scope: application.ScopeOwned, ActorID: alice, RequesterNeedsAction: true}
	got, err := s.TicketStore().List(ctx, owned, application.Page{Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !equal(numbers(got), 103, 102) {
		t.Fatalf("owned Needs-your-action List = %v, want [103 102]", numbers(got))
	}
	for _, ticket := range got {
		if ticket.RequesterUserID == nil || *ticket.RequesterUserID != alice {
			t.Fatalf("retrieved ticket %d escaped the requester's scope", ticket.Number)
		}
	}
	count, err := s.TicketStore().Count(ctx, owned)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != len(got) {
		t.Fatalf("owned Needs-your-action Count = %d, want %d (a section header must match its rows)", count, len(got))
	}

	// The same predicate without the scope selects both requester's halves, so
	// the own-ticket restriction above comes from the scope clause and the
	// filtered predicate is genuinely the OR.
	all, err := s.TicketStore().List(ctx, application.TicketQuery{Scope: application.ScopeAll, RequesterNeedsAction: true}, application.Page{Limit: 10})
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if !equal(numbers(all), 107, 106, 103, 102) {
		t.Fatalf("unscoped Needs-your-action List = %v, want [107 106 103 102]", numbers(all))
	}
}
