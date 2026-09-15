package httpadapter

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestTicketsIndexAgentEmptiesAndClearSearch(t *testing.T) {
	h := newHarness(t)
	agent := seedUserRole(t, h.store, "Ava", "ava-empty@example.com", domain.RoleAgent)
	sess := seedSession(t, h.store, agent.ID)
	desk, err := h.desks.Create(t.Context(), *h.admin, "Empty desk")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}
	if err := h.desks.AddMember(t.Context(), *h.admin, desk.ID, agent.ID); err != nil {
		t.Fatalf("add desk member: %v", err)
	}

	request := func(query string) string {
		rec := doRequest(h.mux, h.mw, http.MethodGet, "/tickets"+query, map[string]string{
			"Cookie": sessionCookie + "=" + sess.ID,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", query, rec.Code)
		}
		return rec.Body.String()
	}

	trueEmpty := request("")
	for _, want := range []string{"No tickets assigned to you.", "No tickets available in your desks."} {
		if !strings.Contains(trueEmpty, want) {
			t.Errorf("true empty must contain %q", want)
		}
	}
	if strings.Contains(trueEmpty, "Clear search") {
		t.Error("true empty must not offer Clear search")
	}

	searchEmpty := request("?q=zzz-nothing")
	if got := strings.Count(searchEmpty, "No tickets match your search."); got != 2 {
		t.Errorf("search empty copy count = %d, want 2", got)
	}
	if got := strings.Count(searchEmpty, ">Clear search</a>"); got != 2 {
		t.Errorf("clear search action count = %d, want 2", got)
	}
	for _, idx := range []int{
		strings.Index(searchEmpty, ">Clear search</a>"),
		strings.LastIndex(searchEmpty, ">Clear search</a>"),
	} {
		start := strings.LastIndex(searchEmpty[:idx], "<a ")
		if start < 0 {
			t.Fatalf("clear search anchor not found")
		}
		action := searchEmpty[start:idx]
		for _, want := range []string{
			`href="/tickets"`, `hx-get="/tickets"`,
			`hx-target="#tickets-screen"`, `hx-swap="outerHTML"`, `hx-push-url="true"`,
		} {
			if !strings.Contains(action, want) {
				t.Errorf("clear search action must contain %q", want)
			}
		}
	}
}

func TestTicketsIndexAgentSectionPaginationPreservesBothPages(t *testing.T) {
	h := newHarness(t)
	agent := seedUserRole(t, h.store, "Ava", "ava-pages@example.com", domain.RoleAgent)
	sess := seedSession(t, h.store, agent.ID)
	desk, err := h.desks.Create(t.Context(), *h.admin, "Paging desk")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}
	if err := h.desks.AddMember(t.Context(), *h.admin, desk.ID, agent.ID); err != nil {
		t.Fatalf("add desk member: %v", err)
	}
	category, err := h.categories.Create(t.Context(), "Agent paging")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	h.publishWorkflow(t, category.ID, domain.WorkflowDefinition{{
		Type: domain.StepAssignToDesk,
		AssignToDesk: &domain.AssignToDeskStep{
			DeskID: desk.ID, Strategy: domain.StrategyClaim,
		},
	}})

	for i := 0; i < application.PageSize+1; i++ {
		assigned, err := h.tickets.Create(t.Context(), *h.admin, application.CreateTicketInput{
			Title: "Assigned paging " + strconv.Itoa(i), CategoryID: category.ID, Priority: domain.PriorityMedium,
		})
		if err != nil {
			t.Fatalf("create assigned ticket %d: %v", i, err)
		}
		h.assignTicket(t, assigned.ID, agent.ID)
		if _, err := h.tickets.Create(t.Context(), *h.admin, application.CreateTicketInput{
			Title: "Claimable paging " + strconv.Itoa(i), CategoryID: category.ID, Priority: domain.PriorityMedium,
		}); err != nil {
			t.Fatalf("create claimable ticket %d: %v", i, err)
		}
	}

	request := func(query string) string {
		rec := doRequest(h.mux, h.mw, http.MethodGet, "/tickets?"+query, map[string]string{
			"Cookie": sessionCookie + "=" + sess.ID,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /tickets?%s status = %d, want 200", query, rec.Code)
		}
		return rec.Body.String()
	}
	assertPages := func(body string, assignedPage, claimablePage int) {
		t.Helper()
		counts := map[int]int{assignedPage: 1}
		counts[claimablePage]++
		for page, want := range counts {
			got := strings.Count(body, "Page "+strconv.Itoa(page)+" of 2 · 11 tickets")
			if got != want {
				t.Errorf("page %d summary count = %d, want %d", page, got, want)
			}
		}
		for _, heading := range []string{"Assigned to me · 11", "Available to claim · 11"} {
			if !strings.Contains(body, heading) {
				t.Errorf("missing exact section heading %q", heading)
			}
		}
	}

	initial := request("q=paging&assigned_page=2")
	assertPages(initial, 2, 1)
	if want := `href="/tickets?assigned_page=2&amp;claimable_page=2&amp;q=paging"`; !strings.Contains(initial, want) {
		t.Fatalf("claimable next must preserve assigned_page: missing %s", want)
	}
	assertPages(request("q=paging&assigned_page=2&claimable_page=2"), 2, 2)
	assertPages(request("q=paging&assigned_page=1&claimable_page=2"), 1, 2)
	assertPages(request("q=paging&assigned_page=1&claimable_page=1"), 1, 1)
}
