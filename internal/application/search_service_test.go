package application_test

import (
	"context"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func newSearchService() (*application.SearchService, *fakeTicketStore, *fakeClock) {
	clock := fixedClock()
	tickets := newFakeTicketStore()
	return application.NewSearchService(tickets, &fakeSearchStore{tickets: tickets}), tickets, clock
}

// seedSearchTicket inserts a ticket at the next clock instant (newest first
// ordering).
func seedSearchTicket(store *fakeTicketStore, clock *fakeClock, title, desc string, state domain.State, priority domain.Priority, catID int64, userID *int64) domain.Ticket {
	clock.Advance(timeMinute)
	return store.seed(domain.Ticket{
		Title: title, Description: desc, CategoryID: catID, UserID: userID,
		Priority: priority, State: state, CreatedAt: clock.now, UpdatedAt: clock.now,
	})
}

// seedSearchTicketOwned inserts a ticket created by requesterID (user-role
// ownership scope, ticket-access spec).
func seedSearchTicketOwned(store *fakeTicketStore, clock *fakeClock, title string, requesterID int64) domain.Ticket {
	clock.Advance(timeMinute)
	return store.seed(domain.Ticket{
		Title: title, Description: "x", CategoryID: 1, RequesterUserID: ptr(requesterID),
		Priority: domain.PriorityLow, State: domain.StateNew, CreatedAt: clock.now, UpdatedAt: clock.now,
	})
}

// adminActor is the all-scope actor used by existing search tests that
// exercise filters and pagination, not scope.
func adminActor() domain.User {
	return domain.User{ID: 99, Name: "Admin", Email: "admin@example.com", Role: domain.RoleAdmin}
}

func TestBuildTextQuery(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"quotes only", `"`, ""},
		{"single token", "timeout", `"timeout"`},
		{"two tokens", "network timeout", `"network" AND "timeout"`},
		{"embedded quotes", `say "hi"`, `"say" AND """hi"""`},
		{"fts operators", `"a OR b`, `"""a" AND "OR" AND "b"`},
		{"special chars", `( * :`, `"(" AND "*" AND ":"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := application.BuildTextQuery(tc.in); got != tc.want {
				t.Fatalf("BuildTextQuery(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestBuildTitleQuery(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		numbers []int64
	}{
		{"empty", "", "", nil},
		{"single token", "timeout", `title : "timeout"`, nil},
		{"two tokens", "network timeout", `title : "network" AND title : "timeout"`, nil},
		{"bare number", "3", `title : "3"`, []int64{3}},
		{"ticket id", "TKT-2", `title : "TKT-2"`, []int64{2}},
		{"title and id", "monitor 3", `title : "monitor" AND title : "3"`, []int64{3}},
		{"embedded quotes", `say "hi"`, `title : "say" AND title : """hi"""`, nil},
		{"fts operators", `"a OR b`, `title : """a" AND title : "OR" AND title : "b"`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, numbers := application.BuildTitleQuery(tc.in)
			if got != tc.want {
				t.Fatalf("BuildTitleQuery(%q) text = %q, want %q", tc.in, got, tc.want)
			}
			if len(numbers) != len(tc.numbers) {
				t.Fatalf("BuildTitleQuery(%q) numbers = %v, want %v", tc.in, numbers, tc.numbers)
			}
			for i := range numbers {
				if numbers[i] != tc.numbers[i] {
					t.Fatalf("BuildTitleQuery(%q) numbers = %v, want %v", tc.in, numbers, tc.numbers)
				}
			}
		})
	}
}

// TestSearchFiltersComposeWithAND covers the 4-filter composition scenario:
// state resolved + priority high + category Bugs + user 2 matches only the
// ticket satisfying all four conditions.
func TestSearchFiltersComposeWithAND(t *testing.T) {
	svc, tickets, clock := newSearchService()
	catBugs, catSupport := int64(1), int64(2)
	user1, user2 := ptr(int64(1)), ptr(int64(2))

	seedSearchTicket(tickets, clock, "A", "resolved high bugs u2", domain.StateResolved, domain.PriorityHigh, catBugs, user2)
	seedSearchTicket(tickets, clock, "B", "resolved high bugs u1", domain.StateResolved, domain.PriorityHigh, catBugs, user1)
	seedSearchTicket(tickets, clock, "C", "resolved medium bugs u2", domain.StateResolved, domain.PriorityMedium, catBugs, user2)
	seedSearchTicket(tickets, clock, "D", "new high bugs u2", domain.StateNew, domain.PriorityHigh, catBugs, user2)
	seedSearchTicket(tickets, clock, "E", "resolved high support u2", domain.StateResolved, domain.PriorityHigh, catSupport, user2)

	q := application.TicketQuery{Scope: application.ScopeAll,
		State:      ptr(domain.StateResolved),
		Priority:   ptr(domain.PriorityHigh),
		CategoryID: &catBugs,
		UserID:     user2,
	}
	result, err := svc.Search(context.Background(), adminActor(), q, 1)
	if err != nil {
		t.Fatalf("Search: unexpected error: %v", err)
	}
	if result.Total != 1 {
		t.Fatalf("Search: exactly one ticket must match all four filters, got %d", result.Total)
	}
	if len(result.Tickets) != 1 || result.Tickets[0].Title != "A" {
		t.Fatalf("Search: the AND match must be ticket A, got %+v", result.Tickets)
	}
}

// TestSearchEmptyFilterReturnsAll covers the empty-filter-set rule.
func TestSearchEmptyFilterReturnsAll(t *testing.T) {
	svc, tickets, clock := newSearchService()
	for i := 0; i < 4; i++ {
		seedSearchTicket(tickets, clock, "T", "x", domain.StateNew, domain.PriorityLow, 1, nil)
	}

	result, err := svc.Search(context.Background(), adminActor(), application.TicketQuery{Scope: application.ScopeAll}, 1)
	if err != nil {
		t.Fatalf("Search: unexpected error: %v", err)
	}
	if result.Total != 4 || len(result.Tickets) != 4 {
		t.Fatalf("Search: empty filter set must return all tickets (4), got total=%d len=%d", result.Total, len(result.Tickets))
	}
}

// TestSearchTextAndComposition: multi-token queries match only tickets
// whose TITLE contains every token (quoted-AND semantics, D4; description
// text is not searchable).
func TestSearchTextAndComposition(t *testing.T) {
	svc, tickets, clock := newSearchService()
	seedSearchTicket(tickets, clock, "Network timeout", "timeout on connect", domain.StateNew, domain.PriorityLow, 1, nil)
	seedSearchTicket(tickets, clock, "Printer spool", "network timeout on spool", domain.StateNew, domain.PriorityLow, 1, nil)
	seedSearchTicket(tickets, clock, "Both network timeout", "again", domain.StateNew, domain.PriorityLow, 1, nil)

	result, err := svc.Search(context.Background(), adminActor(), application.TicketQuery{Scope: application.ScopeAll, Text: "network timeout"}, 1)
	if err != nil {
		t.Fatalf("Search: unexpected error: %v", err)
	}
	if result.Total != 2 {
		t.Fatalf("Search: only tickets whose TITLE has BOTH tokens must match, got %d", result.Total)
	}
	for _, tt := range result.Tickets {
		if tt.Title != "Network timeout" && tt.Title != "Both network timeout" {
			t.Fatalf("Search: unexpected match %q", tt.Title)
		}
	}
}

// TestSearchByNumber: the text filter matches exact ticket numbers (TKT-N)
// OR titles — "TKT-2" resolves to ticket number 2 even when no title hits.
func TestSearchByNumber(t *testing.T) {
	svc, tickets, clock := newSearchService()
	seedSearchTicket(tickets, clock, "Network", "x", domain.StateNew, domain.PriorityLow, 1, nil)
	seedSearchTicket(tickets, clock, "Printer", "x", domain.StateNew, domain.PriorityLow, 1, nil)

	result, err := svc.Search(context.Background(), adminActor(), application.TicketQuery{Scope: application.ScopeAll, Text: "TKT-2"}, 1)
	if err != nil {
		t.Fatalf("Search: unexpected error: %v", err)
	}
	if result.Total != 1 || len(result.Tickets) != 1 || result.Tickets[0].Title != "Printer" {
		t.Fatalf("Search(TKT-2) = %+v, want [Printer]", result)
	}
}

// TestSearchStablePagination covers 25 tickets -> pages 10/10/5 with no
// overlap (D2, ticket-search spec).
func TestSearchStablePagination(t *testing.T) {
	svc, tickets, clock := newSearchService()
	const total = 25
	for i := 0; i < total; i++ {
		seedSearchTicket(tickets, clock, "T", "x", domain.StateNew, domain.PriorityLow, 1, nil)
	}

	seen := map[int64]bool{}
	pages := []int{10, 10, 5}
	for page, wantLen := range pages {
		result, err := svc.Search(context.Background(), adminActor(), application.TicketQuery{Scope: application.ScopeAll}, page+1)
		if err != nil {
			t.Fatalf("Search page %d: unexpected error: %v", page+1, err)
		}
		if result.Total != total {
			t.Fatalf("Search page %d: total must be %d, got %d", page+1, total, result.Total)
		}
		if len(result.Tickets) != wantLen {
			t.Fatalf("Search page %d: %d tickets expected, got %d", page+1, wantLen, len(result.Tickets))
		}
		for _, tt := range result.Tickets {
			if seen[tt.ID] {
				t.Fatalf("Search: page %d overlaps a previous page (ticket %d seen twice)", page+1, tt.ID)
			}
			seen[tt.ID] = true
		}
	}
	if len(seen) != total {
		t.Fatalf("Search: all %d tickets must be covered across pages, got %d", total, len(seen))
	}
}

// TestSearchUserScopeOwnOnly proves the empty-filter set returns only the
// user's own tickets (requester = self), never another user's (ticket-search
// spec: empty filters respect actor scope).
func TestSearchUserScopeOwnOnly(t *testing.T) {
	svc, tickets, clock := newSearchService()
	actorA := domain.User{ID: 1, Name: "A", Role: domain.RoleUser}
	actorB := domain.User{ID: 2, Name: "B", Role: domain.RoleUser}

	own := seedSearchTicketOwned(tickets, clock, "A's ticket", actorA.ID)
	seedSearchTicketOwned(tickets, clock, "B's ticket", actorB.ID)

	result, err := svc.Search(context.Background(), actorA, application.TicketQuery{}, 1)
	if err != nil {
		t.Fatalf("Search: unexpected error: %v", err)
	}
	if result.Total != 1 || len(result.Tickets) != 1 || result.Tickets[0].ID != own.ID {
		t.Fatalf("user A must see only their own ticket, got total=%d tickets=%+v", result.Total, result.Tickets)
	}
}

// TestSearchAgentScopeAssignedOnly proves the agent's search is scoped to
// assignment: unassigned tickets and other agents' tickets never match,
// even with no filters (ticket-search spec: agent search scoped to
// assignment; empty filter set never returns out-of-scope tickets).
func TestSearchAgentScopeAssignedOnly(t *testing.T) {
	svc, tickets, clock := newSearchService()
	agentX := domain.User{ID: 3, Name: "X", Role: domain.RoleAgent}
	agentY := domain.User{ID: 4, Name: "Y", Role: domain.RoleAgent}

	assigned := seedSearchTicket(tickets, clock, "X's", "x", domain.StateNew, domain.PriorityLow, 1, ptr(agentX.ID))
	seedSearchTicket(tickets, clock, "Y's", "x", domain.StateNew, domain.PriorityLow, 1, ptr(agentY.ID))
	seedSearchTicket(tickets, clock, "unassigned", "x", domain.StateNew, domain.PriorityLow, 1, nil)

	result, err := svc.Search(context.Background(), agentX, application.TicketQuery{}, 1)
	if err != nil {
		t.Fatalf("Search: unexpected error: %v", err)
	}
	if result.Total != 1 || len(result.Tickets) != 1 || result.Tickets[0].ID != assigned.ID {
		t.Fatalf("agent X must see only their assigned ticket, got total=%d tickets=%+v", result.Total, result.Tickets)
	}
}

// TestSearchAdminScopeFullQueue proves admin/root empty-filter searches
// return EVERY ticket — assigned, unassigned, requester-owned (ticket-access
// spec: admin SHALL access the full queue).
func TestSearchAdminScopeFullQueue(t *testing.T) {
	svc, tickets, clock := newSearchService()
	admin := domain.User{ID: 99, Name: "Admin", Role: domain.RoleAdmin}

	seedSearchTicketOwned(tickets, clock, "owned", 1)
	seedSearchTicket(tickets, clock, "assigned", "x", domain.StateNew, domain.PriorityLow, 1, ptr(int64(3)))
	seedSearchTicket(tickets, clock, "unassigned", "x", domain.StateNew, domain.PriorityLow, 1, nil)

	result, err := svc.Search(context.Background(), admin, application.TicketQuery{}, 1)
	if err != nil {
		t.Fatalf("Search: unexpected error: %v", err)
	}
	if result.Total != 3 || len(result.Tickets) != 3 {
		t.Fatalf("admin must see the full queue (3 tickets), got total=%d len=%d", result.Total, len(result.Tickets))
	}
}

// TestSearchUnknownRoleDeniesAll proves an actor with an unknown/empty role
// sees nothing — the fail-closed ScopeNone path (policy.go contract).
func TestSearchUnknownRoleDeniesAll(t *testing.T) {
	svc, tickets, clock := newSearchService()
	seedSearchTicket(tickets, clock, "t", "x", domain.StateNew, domain.PriorityLow, 1, nil)

	result, err := svc.Search(context.Background(), domain.User{Name: "Ghost"}, application.TicketQuery{}, 1)
	if err != nil {
		t.Fatalf("Search: unexpected error: %v", err)
	}
	if result.Total != 0 || len(result.Tickets) != 0 {
		t.Fatalf("unknown role must see nothing, got total=%d", result.Total)
	}
}

func TestSearchPageZeroDefaultsToOne(t *testing.T) {
	svc, tickets, clock := newSearchService()
	for i := 0; i < 12; i++ {
		seedSearchTicket(tickets, clock, "T", "x", domain.StateNew, domain.PriorityLow, 1, nil)
	}

	result, err := svc.Search(context.Background(), adminActor(), application.TicketQuery{Scope: application.ScopeAll}, 0)
	if err != nil {
		t.Fatalf("Search: unexpected error: %v", err)
	}
	if len(result.Tickets) != 10 {
		t.Fatalf("Search: page 0 must behave as page 1 (10 tickets), got %d", len(result.Tickets))
	}
}
