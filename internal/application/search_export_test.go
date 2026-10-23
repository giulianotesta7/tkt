package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// The ticket export walk (issue #409, specs S21-S22): it returns EVERY match,
// not one page, and it is scoped exactly like the list it mirrors.

func exportHarness(t *testing.T, count int) (*application.SearchService, *fakeTicketStore, *fakeUserStore) {
	t.Helper()
	tickets := newFakeTicketStore()
	users := newFakeUserStore()
	for i := 1; i <= count; i++ {
		tickets.seed(domain.Ticket{Title: "ticket", State: domain.StateInProgress, Priority: domain.PriorityMedium})
	}
	return application.NewSearchService(tickets, &fakeSearchStore{tickets: tickets}), tickets, users
}

func collectExport(t *testing.T, s *application.SearchService, actor domain.User) []domain.Ticket {
	t.Helper()
	var got []domain.Ticket
	if err := s.Export(context.Background(), actor, application.TicketQuery{}, func(tk domain.Ticket) error {
		got = append(got, tk)
		return nil
	}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	return got
}

// S21: the list shows ten rows at a time; the export must not stop there.
func TestExportReturnsEveryMatchNotOnePage(t *testing.T) {
	const count = application.PageSize * 7
	svc, _, users := exportHarness(t, count)
	admin := users.seedRole("Ada", "ada@example.com", domain.RoleAdmin, true)

	if got := collectExport(t, svc, admin); len(got) != count {
		t.Fatalf("exported %d tickets, want %d", len(got), count)
	}
}

// S21: a result set larger than one read batch is walked to the end.
func TestExportWalksEveryBatch(t *testing.T) {
	const count = application.ExportBatchSize + 25
	svc, _, users := exportHarness(t, count)
	admin := users.seedRole("Ada", "ada@example.com", domain.RoleAdmin, true)

	if got := collectExport(t, svc, admin); len(got) != count {
		t.Fatalf("exported %d tickets, want %d", len(got), count)
	}
}

// S22: the export is scoped exactly like the list. A requester exports their
// own tickets and nobody else's, through the same preparation Search uses.
func TestExportAppliesTheActorScope(t *testing.T) {
	tickets := newFakeTicketStore()
	users := newFakeUserStore()
	requester := users.seedRole("Carol", "carol@example.com", domain.RoleUser, true)
	other := users.seedRole("Dan", "dan@example.com", domain.RoleUser, true)
	mine := requester.ID
	theirs := other.ID
	tickets.seed(domain.Ticket{Title: "mine", State: domain.StateInProgress, Priority: domain.PriorityMedium, RequesterUserID: &mine})
	tickets.seed(domain.Ticket{Title: "theirs", State: domain.StateInProgress, Priority: domain.PriorityMedium, RequesterUserID: &theirs})
	svc := application.NewSearchService(tickets, &fakeSearchStore{tickets: tickets})

	got := collectExport(t, svc, requester)
	if len(got) != 1 || got[0].Title != "mine" {
		t.Fatalf("exported %d tickets %v, want only the requester's own", len(got), got)
	}
}

// A failing visit stops the walk at once and surfaces unchanged: the handler
// uses it to abandon a response whose connection went away.
func TestExportStopsWhenTheVisitorFails(t *testing.T) {
	svc, _, users := exportHarness(t, 25)
	admin := users.seedRole("Ada", "ada@example.com", domain.RoleAdmin, true)
	boom := errors.New("client is gone")

	seen := 0
	err := svc.Export(context.Background(), admin, application.TicketQuery{}, func(domain.Ticket) error {
		seen++
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Export error = %v, want %v", err, boom)
	}
	if seen != 1 {
		t.Errorf("visited %d tickets, want the walk to stop at the first failure", seen)
	}
}
