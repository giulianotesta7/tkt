package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Task 4.5: FTS5 contentless index (0002) + search store. The text clause
// is the design's `t.id IN (SELECT rowid FROM tickets_fts WHERE
// tickets_fts MATCH ?)`; TicketQuery.Text is the D4-tokenized, title-scoped
// expression and TicketQuery.Numbers the extracted ticket IDs
// (application.BuildTitleQuery). The search box scope is ID or title only:
// description and comment bodies are never searchable.

func mustSearch(t *testing.T, s *Store, q application.TicketQuery) []domain.Ticket {
	t.Helper()
	got, err := s.SearchStore().Search(context.Background(), q, application.Page{Offset: 0, Limit: 10})
	if err != nil {
		t.Fatalf("Search(%q): %v", q.Text, err)
	}
	return got
}

// textQuery builds the store-side query for a raw search-box input.
func textQuery(raw string) application.TicketQuery {
	q := application.TicketQuery{Scope: application.ScopeAll}
	q.Text, q.Numbers = application.BuildTitleQuery(raw)
	return q
}

func seedSearchTicket(t *testing.T, s *Store, number int, title, description string, state domain.State, priority domain.Priority, catID int64) int64 {
	t.Helper()
	return seedTicket(t, s, domain.Ticket{Number: number, Title: title, Description: description,
		CategoryID: catID, Priority: priority, State: state,
		CreatedAt: testClock, UpdatedAt: testClock}).ID
}

func TestFTS5SearchMatchesTitleOnly(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "Bugs")
	ctx := context.Background()

	// Ticket A matches in the TITLE; ticket B matches only in a comment
	// and ticket C only in its description — none of those must surface.
	seedSearchTicket(t, s, 1, "Network timeout", "timeout on connect", domain.StateNew, domain.PriorityLow, cat)
	b := seedSearchTicket(t, s, 2, "Printer", "spooler setup", domain.StateNew, domain.PriorityLow, cat)
	seedSearchTicket(t, s, 3, "Monitor", "timeout on display", domain.StateNew, domain.PriorityLow, cat)
	if err := s.CommentStore().Add(ctx, &domain.Comment{TicketID: b, Author: "Ana",
		Body: "network timeout observed", CreatedAt: testClock}); err != nil {
		t.Fatal(err)
	}

	got := mustSearch(t, s, textQuery("timeout"))
	if len(got) != 1 || got[0].Number != 1 {
		t.Fatalf("matches = %+v, want [1] (title only; description/comment must not match)", got)
	}
}

func TestFTS5SearchByNumber(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "Bugs")

	seedSearchTicket(t, s, 1, "Network", "timeout on connect", domain.StateNew, domain.PriorityLow, cat)
	seedSearchTicket(t, s, 2, "Printer", "spooler setup", domain.StateNew, domain.PriorityLow, cat)
	seedSearchTicket(t, s, 3, "Monitor", "display setup", domain.StateNew, domain.PriorityLow, cat)

	// "3" and "TKT-3" both resolve to ticket number 3.
	got := mustSearch(t, s, textQuery("3"))
	if len(got) != 1 || got[0].Number != 3 {
		t.Errorf(`"3" matches = %+v, want [3]`, got)
	}
	got = mustSearch(t, s, textQuery("TKT-2"))
	if len(got) != 1 || got[0].Number != 2 {
		t.Errorf(`"TKT-2" matches = %+v, want [2]`, got)
	}
	// "monitor 3": title OR number — the title hit alone is enough.
	got = mustSearch(t, s, textQuery("monitor 3"))
	if len(got) != 1 || got[0].Number != 3 {
		t.Errorf(`"monitor 3" matches = %+v, want [3]`, got)
	}
	// A missing number matches nothing.
	if got := mustSearch(t, s, textQuery("999")); len(got) != 0 {
		t.Errorf(`"999" matches = %+v, want none`, got)
	}
}

func TestFTS5SearchReflectsEdits(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "Bugs")
	ctx := context.Background()

	tk := &domain.Ticket{Title: "Old", Description: "obsolete text", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateNew,
		CreatedAt: testClock, UpdatedAt: testClock}
	if err := s.TicketStore().Create(ctx, tk); err != nil {
		t.Fatal(err)
	}

	// Acceptance: edit "Old" → "New": search "Old" must be empty, "New"
	// must hit (trg_tickets_au keeps the index consistent with the edit).
	if got := mustSearch(t, s, textQuery("Old")); len(got) != 1 {
		t.Fatalf("before edit: 'Old' matches = %d, want 1", len(got))
	}

	tk.Title = "New"
	tk.UpdatedAt = testClock.Add(2 * time.Hour)
	if err := s.TicketStore().Update(ctx, tk); err != nil {
		t.Fatal(err)
	}

	if got := mustSearch(t, s, textQuery("Old")); len(got) != 0 {
		t.Errorf("after edit: 'Old' still matches %d tickets — superseded content must not remain searchable", len(got))
	}
	if got := mustSearch(t, s, textQuery("New")); len(got) != 1 {
		t.Errorf("after edit: 'New' matches = %d, want 1", len(got))
	}
}

func TestFTS5SearchSpecialCharsNeverError(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "Bugs")
	seedSearchTicket(t, s, 1, "Network timeout", "timeout on connect", domain.StateNew, domain.PriorityLow, cat)

	// Raw user input carrying FTS5 syntax (threat matrix: "FTS syntax
	// chars in q → 200/empty, no 500"). The D4-tokenized, title-scoped
	// expression binds as-is; quoted tokens must never error.
	rawInputs := []string{
		`"`, `(`, `*`, `:`, `a OR b`, `say "hi"`, `'`, `-`, `!`, `network (timeout`,
	}
	for _, raw := range rawInputs {
		q := textQuery(raw)
		if _, err := s.SearchStore().Search(context.Background(), q, application.Page{Offset: 0, Limit: 10}); err != nil {
			t.Errorf("Search(%q → %q): %v", raw, q.Text, err)
		}
		if _, err := s.SearchStore().SearchCount(context.Background(), q); err != nil {
			t.Errorf("SearchCount(%q → %q): %v", raw, q.Text, err)
		}
	}

	// Sanity: a normal token still matches alongside the specials.
	got := mustSearch(t, s, textQuery("network (timeout"))
	if len(got) != 1 {
		t.Errorf("'network (timeout' matches = %d, want 1 (specials degrade, tokens still match)", len(got))
	}
}

// TestFTS5SearchScopedToAssignment proves the actor scope is applied to
// search BEFORE the text filter: agent X's search never returns Y's
// assigned tickets even when the title matches, and the count agrees
// (ticket-search spec: agent search is scoped to assignment).
func TestFTS5SearchScopedToAssignment(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "Bugs")
	x := seedUser(t, s, "X", "x@example.com", true)
	y := seedUser(t, s, "Y", "y@example.com", true)
	ctx := context.Background()

	seedTicket(t, s, domain.Ticket{Number: 1, Title: "shared term", CategoryID: cat, UserID: ptr(x),
		Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: testClock, UpdatedAt: testClock})
	seedTicket(t, s, domain.Ticket{Number: 2, Title: "shared term", CategoryID: cat, UserID: ptr(y),
		Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: testClock, UpdatedAt: testClock})

	q := textQuery("shared term")
	q.Scope = application.ScopeAssigned
	q.ActorID = x
	got := mustSearch(t, s, q)
	if len(got) != 1 || got[0].Number != 1 {
		t.Fatalf("agent X's search = %+v, want [1] only (Y's ticket must not match)", got)
	}
	n, err := s.SearchStore().SearchCount(ctx, q)
	if err != nil {
		t.Fatalf("search count: %v", err)
	}
	if n != 1 {
		t.Fatalf("agent X's search count = %d, want 1", n)
	}
}

func TestFTS5SearchComposesWithFilters(t *testing.T) {
	s := newTestDB(t)
	bugs := seedCategory(t, s, "Bugs")
	support := seedCategory(t, s, "Support")
	ctx := context.Background()

	seedSearchTicket(t, s, 1, "Timeout A", "ignored description", domain.StateResolved, domain.PriorityHigh, bugs)
	seedSearchTicket(t, s, 2, "Timeout B", "ignored description", domain.StateNew, domain.PriorityHigh, bugs)
	seedSearchTicket(t, s, 3, "Timeout C", "ignored description", domain.StateResolved, domain.PriorityHigh, support)
	seedSearchTicket(t, s, 4, "Printer D", "ignored description", domain.StateNew, domain.PriorityLow, support)

	text := textQuery("timeout")

	// Text AND state: A and C.
	got := mustSearch(t, s, application.TicketQuery{Scope: application.ScopeAll, Text: text.Text, Numbers: text.Numbers, State: ptr(domain.StateResolved)})
	if len(got) != 2 {
		t.Errorf("timeout+resolved = %d, want 2", len(got))
	}
	// Text AND state AND category: only A.
	got = mustSearch(t, s, application.TicketQuery{
		Scope: application.ScopeAll,
		Text:  text.Text, Numbers: text.Numbers, State: ptr(domain.StateResolved), CategoryID: ptr(bugs)})
	if len(got) != 1 || got[0].Number != 1 {
		t.Errorf("timeout+resolved+bugs = %+v, want [1]", got)
	}
	// No text: plain filter list (SearchStore handles empty text).
	got = mustSearch(t, s, application.TicketQuery{Scope: application.ScopeAll, State: ptr(domain.StateNew)})
	if len(got) != 2 {
		t.Errorf("state=new = %d, want 2 (no text clause)", len(got))
	}

	n, err := s.SearchStore().SearchCount(ctx, application.TicketQuery{Scope: application.ScopeAll, Text: text.Text, Numbers: text.Numbers, State: ptr(domain.StateResolved)})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("SearchCount = %d, want 2", n)
	}
}

func TestFTS5ChipsReflectTextFilter(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "Bugs")
	ctx := context.Background()

	seedSearchTicket(t, s, 1, "Timeout resolved", "ignored", domain.StateResolved, domain.PriorityHigh, cat)
	seedSearchTicket(t, s, 2, "Timeout new", "ignored", domain.StateNew, domain.PriorityHigh, cat)
	seedSearchTicket(t, s, 3, "Printer", "ignored", domain.StateNew, domain.PriorityLow, cat)

	// Chips must reflect the TEXT-filtered result set (the shared filter
	// builder carries the text clause into the chip queries).
	text := textQuery("timeout")
	byState, err := s.TicketStore().CountsByState(ctx, application.TicketQuery{Scope: application.ScopeAll, Text: text.Text, Numbers: text.Numbers})
	if err != nil {
		t.Fatal(err)
	}
	if byState[domain.StateResolved] != 1 || byState[domain.StateNew] != 1 || len(byState) != 2 {
		t.Errorf("chips by state under text=timeout: %v, want {resolved:1, new:1}", byState)
	}
	byPriority, err := s.TicketStore().CountsByPriority(ctx, application.TicketQuery{
		Scope: application.ScopeAll,
		Text:  text.Text, Numbers: text.Numbers, State: ptr(domain.StateNew)})
	if err != nil {
		t.Fatal(err)
	}
	if byPriority[domain.PriorityHigh] != 1 || len(byPriority) != 1 {
		t.Errorf("chips by priority under text+state: %v, want {high:1}", byPriority)
	}
}

func TestFTS5TicketListSupportsText(t *testing.T) {
	// The shared builder means the plain List path also honors text (used
	// by chips); assert it directly for the contract.
	s := newTestDB(t)
	cat := seedCategory(t, s, "Bugs")
	ctx := context.Background()

	seedSearchTicket(t, s, 1, "Network timeout", "ignored", domain.StateNew, domain.PriorityLow, cat)
	seedSearchTicket(t, s, 2, "Printer", "ignored", domain.StateNew, domain.PriorityLow, cat)

	text := textQuery("timeout")
	got, err := s.TicketStore().List(ctx, application.TicketQuery{Scope: application.ScopeAll, Text: text.Text, Numbers: text.Numbers},
		application.Page{Offset: 0, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Number != 1 {
		t.Errorf("List with text = %+v, want [1]", got)
	}
}
