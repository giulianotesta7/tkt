package httpadapter

import (
	"net/http"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestTicketsIndexAgentUsesPersonalAndClaimSections(t *testing.T) {
	h := newHarness(t)
	agent := seedUserRole(t, h.store, "Ava", "ava@example.com", domain.RoleAgent)
	sess := seedSession(t, h.store, agent.ID)
	mine := h.seedTicket(t, "Assigned to Ava", nil)
	h.assignTicket(t, mine.ID, agent.ID)
	desk, err := h.desks.Create(t.Context(), *h.admin, "Claim desk")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}
	if err := h.desks.AddMember(t.Context(), *h.admin, desk.ID, agent.ID); err != nil {
		t.Fatalf("add desk member: %v", err)
	}
	cat, err := h.categories.Create(t.Context(), "Claim rows")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	h.publishWorkflow(t, cat.ID, domain.WorkflowDefinition{{
		Type: domain.StepAssignToDesk,
		AssignToDesk: &domain.AssignToDeskStep{
			DeskID: desk.ID, Strategy: domain.StrategyClaim,
		},
	}})
	if _, err := h.tickets.Create(t.Context(), *h.admin, application.CreateTicketInput{
		Title: "Claim me now", CategoryID: cat.ID, Priority: domain.PriorityHigh,
	}); err != nil {
		t.Fatalf("create claimable ticket: %v", err)
	}

	rec := doRequest(h.mux, h.mw, http.MethodGet, "/tickets", map[string]string{
		"Cookie": sessionCookie + "=" + sess.ID,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`<h1 class="page-title">My work</h1>`,
		`Assigned to me · 1</h2>`,
		`Available to claim · 1</h2>`,
		`id="role-ticket-search"`,
		`hx-get="/tickets"`,
		"Assigned to Ava",
		"Claim me now",
		"Current task: handle",
		"No desk assigned",
		"Requester: Admin",
		`class="badge new"`,
		`class="ticket-priority-value"`,
		"Updated <time",
		"Open ticket",
		"Desk: Claim desk",
		"Created <time",
		"View ticket",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("agent view must contain %q, got: %s", want, body)
		}
	}
	for _, absent := range []string{
		`class="page-subtitle"`, "<table", "<thead",
		`name="state"`, `name="priority"`, `name="category_id"`, `name="user_id"`,
		"Claim ticket", `class="agent-claim-form"`, `hx-target="#agent-ticket-list"`,
		`hx-post="/tickets/`, `action="/tickets/`,
	} {
		if strings.Contains(body, absent) {
			t.Errorf("agent view must not render %q, got: %s", absent, body)
		}
	}
}
