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
