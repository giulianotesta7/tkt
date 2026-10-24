package application_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func requesterExportIDs(t *testing.T, svc *application.SearchService, actor domain.User, q application.TicketQuery) []int64 {
	t.Helper()
	var ids []int64
	if err := svc.Export(context.Background(), actor, q, func(tk domain.Ticket) error {
		ids = append(ids, tk.ID)
		return nil
	}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	return ids
}

func requesterSearchIDs(t *testing.T, svc *application.SearchService, actor domain.User, q application.TicketQuery) []int64 {
	t.Helper()
	res, err := svc.Search(context.Background(), actor, q, 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	ids := []int64{}
	for _, tk := range res.Tickets {
		ids = append(ids, tk.ID)
	}
	return ids
}

// A caller that names no section still reads active work only, with its own
// on-hold filter, through Search and Export alike; each requester section
// exports exactly its own tickets; and the export section is the union of the
// three, which no caller filter can narrow (issue #425). Cancelled work stays
// out of every requester read.
func TestRequesterExportSectionsAndTheNoSectionRead(t *testing.T) {
	svc, tickets, clock := newSearchService()
	alice := domain.User{ID: 1, Name: "Alice", Role: domain.RoleUser}
	open := seedSectionTicket(tickets, clock, "alice open", alice.ID, domain.StateNew, false)
	held := seedSectionTicket(tickets, clock, "alice held", alice.ID, domain.StateInProgress, true)
	resolved := seedSectionTicket(tickets, clock, "alice resolved", alice.ID, domain.StateResolved, false)
	closed := seedSectionTicket(tickets, clock, "alice closed", alice.ID, domain.StateClosed, false)
	cancelled := seedSectionTicket(tickets, clock, "alice cancelled", alice.ID, domain.StateCancelled, false)

	active := []int64{held.ID, open.ID}
	for _, tc := range []struct {
		name string
		q    application.TicketQuery
		want []int64
	}{
		{"plain", application.TicketQuery{}, active},
		{"caller on-hold honored", application.TicketQuery{OnHold: true}, []int64{held.ID}},
		{"crafted closed state", application.TicketQuery{State: ptr(domain.StateClosed)}, active},
		{"crafted closed states", application.TicketQuery{States: []domain.State{domain.StateClosed}}, active},
		{"crafted needs-action", application.TicketQuery{RequesterNeedsAction: true}, active},
		{"crafted not-on-hold", application.TicketQuery{NotOnHold: true}, active},
	} {
		for name, got := range map[string][]int64{
			"Search": requesterSearchIDs(t, svc, alice, tc.q),
			"Export": requesterExportIDs(t, svc, alice, tc.q),
		} {
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("%s: %s = %v, want %v", tc.name, name, got, tc.want)
			}
			for _, id := range got {
				if id == resolved.ID || id == closed.ID || id == cancelled.ID {
					t.Fatalf("%s: history ticket %d leaked into the no-section read", tc.name, id)
				}
			}
		}
	}

	union := []int64{closed.ID, resolved.ID, held.ID, open.ID}
	exportSection := application.TicketQuery{Section: application.TicketSectionRequesterExport}
	if got := requesterExportIDs(t, svc, alice, exportSection); fmt.Sprint(got) != fmt.Sprint(union) {
		t.Fatalf("export section = %v, want the union of the three sections %v", got, union)
	}
	category := int64(99)
	for name, q := range map[string]application.TicketQuery{
		"priority":   {Section: application.TicketSectionRequesterExport, Priority: ptr(domain.PriorityCritical)},
		"category":   {Section: application.TicketSectionRequesterExport, CategoryID: &category},
		"unassigned": {Section: application.TicketSectionRequesterExport, Unassigned: true},
		"assignee":   {Section: application.TicketSectionRequesterExport, UserID: &alice.ID},
	} {
		if got := requesterExportIDs(t, svc, alice, q); fmt.Sprint(got) != fmt.Sprint(union) {
			t.Fatalf("a caller %s filter narrowed the export section to %v, want %v", name, got, union)
		}
	}
	for section, want := range map[application.TicketSection][]int64{
		application.TicketSectionRequesterAction: {resolved.ID, held.ID},
		application.TicketSectionRequesterOpen:   {open.ID},
		application.TicketSectionRequesterClosed: {closed.ID},
	} {
		if got := requesterExportIDs(t, svc, alice, application.TicketQuery{Section: section}); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("section %d export = %v, want %v", section, got, want)
		}
	}
	bob := domain.User{ID: 2, Name: "Bob", Role: domain.RoleUser}
	if got := requesterExportIDs(t, svc, bob, exportSection); len(got) != 0 {
		t.Fatalf("another requester's export = %v, want none", got)
	}
}
