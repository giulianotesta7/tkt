package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// TestTicketStoreUnassignedAndOnHoldViews covers the two queue-view clauses
// issue #382 adds to the shared WHERE builder: Unassigned keeps only tickets
// with no assignee, OnHold only tickets whose on_hold_since is set, and both
// compose with the actor scope so List and Count agree.
func TestTicketStoreUnassignedAndOnHoldViews(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	adminID := seedUser(t, s, "Admin", "admin@example.test", true)
	agentID := seedUser(t, s, "Agent", "agent@example.test", true)
	categoryID := seedCategory(t, s, "Support")

	for _, ticket := range []domain.Ticket{
		{Number: 201, Title: "Unassigned one", CategoryID: categoryID, RequesterUserID: &adminID, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: testClock, UpdatedAt: testClock},
		{Number: 202, Title: "Assigned one", CategoryID: categoryID, RequesterUserID: &adminID, UserID: &agentID, Priority: domain.PriorityMedium, State: domain.StateInProgress, CreatedAt: testClock.Add(time.Minute), UpdatedAt: testClock.Add(time.Minute)},
		{Number: 203, Title: "Unassigned two", CategoryID: categoryID, RequesterUserID: &adminID, Priority: domain.PriorityLow, State: domain.StateNew, CreatedAt: testClock.Add(2 * time.Minute), UpdatedAt: testClock.Add(2 * time.Minute)},
	} {
		seedTicket(t, s, ticket)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE tickets SET on_hold_since = ?, on_hold_reason = 'waiting' WHERE number = 202`, testClock.Add(3*time.Minute).UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("put 202 on hold: %v", err)
	}

	for _, tc := range []struct {
		name  string
		query application.TicketQuery
		want  []int
	}{
		{name: "unassigned", query: application.TicketQuery{Scope: application.ScopeAll, Unassigned: true}, want: []int{203, 201}},
		{name: "on hold", query: application.TicketQuery{Scope: application.ScopeAll, OnHold: true}, want: []int{202}},
		{name: "unassigned composes with priority", query: application.TicketQuery{Scope: application.ScopeAll, Unassigned: true, Priority: priorityPtr(domain.PriorityLow)}, want: []int{203}},
		{name: "on hold respects the assigned scope", query: application.TicketQuery{Scope: application.ScopeAssigned, ActorID: adminID, OnHold: true}, want: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.TicketStore().List(ctx, tc.query, application.Page{Limit: 10})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			numbers := make([]int, len(got))
			for i, tk := range got {
				numbers[i] = tk.Number
			}
			if len(numbers) != len(tc.want) {
				t.Fatalf("List = %v, want %v", numbers, tc.want)
			}
			for i := range tc.want {
				if numbers[i] != tc.want[i] {
					t.Errorf("List = %v, want %v", numbers, tc.want)
				}
			}
			count, err := s.TicketStore().Count(ctx, tc.query)
			if err != nil {
				t.Fatalf("Count: %v", err)
			}
			if count != len(tc.want) {
				t.Errorf("Count = %d, want %d", count, len(tc.want))
			}
		})
	}
}

func priorityPtr(p domain.Priority) *domain.Priority { return &p }
