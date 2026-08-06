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

	q := application.TicketQuery{
		State:      ptr(domain.StateResolved),
		Priority:   ptr(domain.PriorityHigh),
		CategoryID: &catBugs,
		UserID:     user2,
	}
	result, err := svc.Search(context.Background(), q, 1)
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

	result, err := svc.Search(context.Background(), application.TicketQuery{}, 1)
	if err != nil {
		t.Fatalf("Search: unexpected error: %v", err)
	}
	if result.Total != 4 || len(result.Tickets) != 4 {
		t.Fatalf("Search: empty filter set must return all tickets (4), got total=%d len=%d", result.Total, len(result.Tickets))
	}
}

// TestSearchTextAndComposition: multi-token queries match only tickets
// containing every token (quoted-AND semantics, D4).
func TestSearchTextAndComposition(t *testing.T) {
	svc, tickets, clock := newSearchService()
	seedSearchTicket(tickets, clock, "Network", "timeout on connect", domain.StateNew, domain.PriorityLow, 1, nil)
	seedSearchTicket(tickets, clock, "Printer", "timeout on spool", domain.StateNew, domain.PriorityLow, 1, nil)
	seedSearchTicket(tickets, clock, "Both", "network timeout again", domain.StateNew, domain.PriorityLow, 1, nil)

	result, err := svc.Search(context.Background(), application.TicketQuery{Text: "network timeout"}, 1)
	if err != nil {
		t.Fatalf("Search: unexpected error: %v", err)
	}
	if result.Total != 2 {
		t.Fatalf("Search: only tickets with BOTH tokens must match, got %d", result.Total)
	}
	for _, tt := range result.Tickets {
		if tt.Title != "Network" && tt.Title != "Both" {
			t.Fatalf("Search: unexpected match %q", tt.Title)
		}
	}
}

// TestSearchFtsSpecialCharsNeverFail: quotes, parens, stars, and colons in q
// degrade to safe queries — never a 500.
func TestSearchFtsSpecialCharsNeverFail(t *testing.T) {
	svc, tickets, clock := newSearchService()
	seedSearchTicket(tickets, clock, "Normal", "plain text", domain.StateNew, domain.PriorityLow, 1, nil)

	for _, q := range []string{`"`, `(`, `*`, `:`, `a OR b`, `"a OR b`, `(x AND y)`, `*:wild`} {
		result, err := svc.Search(context.Background(), application.TicketQuery{Text: q}, 1)
		if err != nil {
			t.Fatalf("Search(%q): FTS special characters must never error, got %v", q, err)
		}
		if result.Total < 0 {
			t.Fatalf("Search(%q): impossible total %d", q, result.Total)
		}
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
		result, err := svc.Search(context.Background(), application.TicketQuery{}, page+1)
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

// TestSearchChipsReflectResultSet covers the summary chips: counts by state
// and priority reflect the filtered result set (including the text filter).
func TestSearchChipsReflectResultSet(t *testing.T) {
	svc, tickets, clock := newSearchService()
	cat := int64(1)
	// Filtered set (category 1): 3 resolved + 2 cancelled.
	for i := 0; i < 3; i++ {
		seedSearchTicket(tickets, clock, "R", "alpha", domain.StateResolved, domain.PriorityHigh, cat, nil)
	}
	for i := 0; i < 2; i++ {
		seedSearchTicket(tickets, clock, "C", "alpha", domain.StateCancelled, domain.PriorityLow, cat, nil)
	}
	// Outside the filter.
	seedSearchTicket(tickets, clock, "X", "alpha", domain.StateNew, domain.PriorityLow, 2, nil)

	q := application.TicketQuery{CategoryID: &cat, Text: "alpha"}
	result, err := svc.Search(context.Background(), q, 1)
	if err != nil {
		t.Fatalf("Search: unexpected error: %v", err)
	}
	if result.ByState[domain.StateResolved] != 3 || result.ByState[domain.StateCancelled] != 2 {
		t.Fatalf("Search: chips must show 3 resolved and 2 cancelled, got %+v", result.ByState)
	}
	if result.ByPriority[domain.PriorityHigh] != 3 || result.ByPriority[domain.PriorityLow] != 2 {
		t.Fatalf("Search: priority chips must show 3 high and 2 low, got %+v", result.ByPriority)
	}
	if _, ok := result.ByState[domain.StateNew]; ok {
		t.Fatalf("Search: chips must reflect the filtered set only, got %+v", result.ByState)
	}
}

func TestSearchPageZeroDefaultsToOne(t *testing.T) {
	svc, tickets, clock := newSearchService()
	for i := 0; i < 12; i++ {
		seedSearchTicket(tickets, clock, "T", "x", domain.StateNew, domain.PriorityLow, 1, nil)
	}

	result, err := svc.Search(context.Background(), application.TicketQuery{}, 0)
	if err != nil {
		t.Fatalf("Search: unexpected error: %v", err)
	}
	if len(result.Tickets) != 10 {
		t.Fatalf("Search: page 0 must behave as page 1 (10 tickets), got %d", len(result.Tickets))
	}
}
