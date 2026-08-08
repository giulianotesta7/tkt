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
// tickets_fts MATCH ?)`; TicketQuery.Text is the D4-tokenized expression
// (application.BuildTextQuery) and binds as-is — quoted tokens never
// produce FTS syntax errors.

func mustSearch(t *testing.T, s *Store, q application.TicketQuery) []domain.Ticket {
	t.Helper()
	got, err := s.SearchStore().Search(context.Background(), q, application.Page{Offset: 0, Limit: 10})
	if err != nil {
		t.Fatalf("Search(%q): %v", q.Text, err)
	}
	return got
}

func seedSearchTicket(t *testing.T, s *Store, number int, title, description string, state domain.State, priority domain.Priority, catID int64) int64 {
	t.Helper()
	return seedTicket(t, s, domain.Ticket{Number: number, Title: title, Description: description,
		CategoryID: catID, Priority: priority, State: state,
		CreatedAt: testClock, UpdatedAt: testClock}).ID
}

func TestFTS5SearchAcrossFields(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "Bugs")
	ctx := context.Background()

	// Ticket A matches in the description; ticket B matches only in a
	// comment (comment-timeline indexed via trg_comments_ai).
	seedSearchTicket(t, s, 1, "Network", "timeout on connect", domain.StateNew, domain.PriorityLow, cat)
	b := seedSearchTicket(t, s, 2, "Printer", "spooler setup", domain.StateNew, domain.PriorityLow, cat)
	if err := s.CommentStore().Add(ctx, &domain.Comment{TicketID: b, Author: "Ana",
		Body: "network timeout observed", CreatedAt: testClock}); err != nil {
		t.Fatal(err)
	}

	got := mustSearch(t, s, application.TicketQuery{Text: application.BuildTextQuery("timeout")})
	if len(got) != 2 {
		t.Fatalf("matches = %d, want 2 (description + comment), got %+v", len(got), got)
	}
	ids := map[int64]bool{}
	for _, tk := range got {
		ids[tk.ID] = true
	}
	if !ids[1] || !ids[2] {
		t.Errorf("matches = %v, want tickets 1 and 2 (cross-field)", ids)
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
	if got := mustSearch(t, s, application.TicketQuery{Text: application.BuildTextQuery("Old")}); len(got) != 1 {
		t.Fatalf("before edit: 'Old' matches = %d, want 1", len(got))
	}

	tk.Title = "New"
	tk.UpdatedAt = testClock.Add(2 * time.Hour)
	if err := s.TicketStore().Update(ctx, tk); err != nil {
		t.Fatal(err)
	}

	if got := mustSearch(t, s, application.TicketQuery{Text: application.BuildTextQuery("Old")}); len(got) != 0 {
		t.Errorf("after edit: 'Old' still matches %d tickets — superseded content must not remain searchable", len(got))
	}
	if got := mustSearch(t, s, application.TicketQuery{Text: application.BuildTextQuery("New")}); len(got) != 1 {
		t.Errorf("after edit: 'New' matches = %d, want 1", len(got))
	}
}

func TestFTS5SearchSpecialCharsNeverError(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "Bugs")
	seedSearchTicket(t, s, 1, "Network", "timeout on connect", domain.StateNew, domain.PriorityLow, cat)

	// Raw user input carrying FTS5 syntax (threat matrix: "FTS syntax
	// chars in q → 200/empty, no 500"). The D4-tokenized expression binds
	// as-is; quoted tokens must never error.
	rawInputs := []string{
		`"`, `(`, `*`, `:`, `a OR b`, `say "hi"`, `'`, `-`, `!`, `network (timeout`,
	}
	for _, raw := range rawInputs {
		q := application.TicketQuery{Text: application.BuildTextQuery(raw)}
		if _, err := s.SearchStore().Search(context.Background(), q, application.Page{Offset: 0, Limit: 10}); err != nil {
			t.Errorf("Search(%q → %q): %v", raw, q.Text, err)
		}
		if _, err := s.SearchStore().SearchCount(context.Background(), q); err != nil {
			t.Errorf("SearchCount(%q → %q): %v", raw, q.Text, err)
		}
	}

	// Sanity: a normal token still matches alongside the specials.
	got := mustSearch(t, s, application.TicketQuery{Text: application.BuildTextQuery("network (timeout")})
	if len(got) != 1 {
		t.Errorf("'network (timeout' matches = %d, want 1 (specials degrade, tokens still match)", len(got))
	}
}

func TestFTS5SearchComposesWithFilters(t *testing.T) {
	s := newTestDB(t)
	bugs := seedCategory(t, s, "Bugs")
	support := seedCategory(t, s, "Support")
	ctx := context.Background()

	seedSearchTicket(t, s, 1, "A", "timeout in bugs resolved", domain.StateResolved, domain.PriorityHigh, bugs)
	seedSearchTicket(t, s, 2, "B", "timeout in bugs new", domain.StateNew, domain.PriorityHigh, bugs)
	seedSearchTicket(t, s, 3, "C", "timeout in support resolved", domain.StateResolved, domain.PriorityHigh, support)
	seedSearchTicket(t, s, 4, "D", "printer in support", domain.StateNew, domain.PriorityLow, support)

	text := application.BuildTextQuery("timeout")

	// Text AND state: A and C.
	got := mustSearch(t, s, application.TicketQuery{Text: text, State: ptr(domain.StateResolved)})
	if len(got) != 2 {
		t.Errorf("timeout+resolved = %d, want 2", len(got))
	}
	// Text AND state AND category: only A.
	got = mustSearch(t, s, application.TicketQuery{
		Text: text, State: ptr(domain.StateResolved), CategoryID: ptr(bugs)})
	if len(got) != 1 || got[0].Number != 1 {
		t.Errorf("timeout+resolved+bugs = %+v, want [1]", got)
	}
	// No text: plain filter list (SearchStore handles empty text).
	got = mustSearch(t, s, application.TicketQuery{State: ptr(domain.StateNew)})
	if len(got) != 2 {
		t.Errorf("state=new = %d, want 2 (no text clause)", len(got))
	}

	n, err := s.SearchStore().SearchCount(ctx, application.TicketQuery{Text: text, State: ptr(domain.StateResolved)})
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

	seedSearchTicket(t, s, 1, "A", "timeout resolved", domain.StateResolved, domain.PriorityHigh, cat)
	seedSearchTicket(t, s, 2, "B", "timeout new", domain.StateNew, domain.PriorityHigh, cat)
	seedSearchTicket(t, s, 3, "C", "printer", domain.StateNew, domain.PriorityLow, cat)

	// Chips must reflect the TEXT-filtered result set (the shared filter
	// builder carries the text clause into the chip queries).
	byState, err := s.TicketStore().CountsByState(ctx, application.TicketQuery{Text: application.BuildTextQuery("timeout")})
	if err != nil {
		t.Fatal(err)
	}
	if byState[domain.StateResolved] != 1 || byState[domain.StateNew] != 1 || len(byState) != 2 {
		t.Errorf("chips by state under text=timeout: %v, want {resolved:1, new:1}", byState)
	}
	byPriority, err := s.TicketStore().CountsByPriority(ctx, application.TicketQuery{
		Text: application.BuildTextQuery("timeout"), State: ptr(domain.StateNew)})
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

	seedSearchTicket(t, s, 1, "Network", "timeout", domain.StateNew, domain.PriorityLow, cat)
	seedSearchTicket(t, s, 2, "Printer", "spooler", domain.StateNew, domain.PriorityLow, cat)

	got, err := s.TicketStore().List(ctx, application.TicketQuery{Text: application.BuildTextQuery("timeout")},
		application.Page{Offset: 0, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Number != 1 {
		t.Errorf("List with text = %+v, want [1]", got)
	}
}
