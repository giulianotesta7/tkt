package httpadapter

import (
	"net/http"
	"strconv"
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
	}, {Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Continue"}}})
	claimable, err := h.tickets.Create(t.Context(), *h.admin, application.CreateTicketInput{
		Title: "Claim me now", CategoryID: cat.ID, Priority: domain.PriorityHigh,
	})
	if err != nil {
		t.Fatalf("create claimable ticket: %v", err)
	}

	rec := doRequest(h.mux, h.mw, http.MethodGet, "/tickets", map[string]string{
		"Cookie": sessionCookie + "=" + sess.ID,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	// The triage queue's visual language (issue #410, S2): one table per
	// section, rows that open their ticket, the state/hold/SLA facts beside
	// the priority, and a live count summary in the page subtitle.
	for _, want := range []string{
		`<h1 class="page-title">My work</h1>`,
		`class="page-subtitle agent-summary">1 open · 1 available<`,
		agentHeading("Assigned to me", 1),
		agentHeading("Available to claim", 1),
		`id="role-ticket-search"`,
		`hx-get="/tickets"`,
		`table class="queue agent-table"`,
		`<tr class="queue-row" data-href="/tickets/` + strconv.FormatInt(mine.ID, 10) + `">`,
		`class="queue-title" href="/tickets/` + strconv.FormatInt(mine.ID, 10) + `">Assigned to Ava</a>`,
		"Claim me now",
		`>Current task:</span> handle<`,
		"No desk assigned",
		`class="queue-requester">Admin</span>`,
		`class="badge new"`,
		`class="ticket-priority-value"`,
		`class="cell-muted queue-updated" data-label="Updated"`,
		`data-label="Desk">Claim desk</td>`,
		`class="cell-muted queue-updated" data-label="Created"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("agent view must contain %q, got: %s", want, body)
		}
	}
	for _, absent := range []string{
		`name="state"`, `name="priority"`, `name="category_id"`, `name="user_id"`,
		"Open ticket", "View ticket", "Requester:", "Desk:",
	} {
		if strings.Contains(body, absent) {
			t.Errorf("agent view must not render %q, got: %s", absent, body)
		}
	}
	if strings.Contains(body, "Resolved and closed") {
		t.Error("an agent with no finished work must not render the done disclosure")
	}

	// Slice 10 visible claim control: the claimable row renders the claim
	// form on the existing workflow completion route; the assigned row has
	// none, and View stays before Claim in DOM/tab order.
	completion := "/tickets/" + strconv.FormatInt(claimable.ID, 10) + "/workflow/steps/1/complete"
	wantForm := `<form class="agent-claim-form" method="post" action="` + completion + `" hx-post="` + completion + `" hx-target="#agent-ticket-list" hx-swap="outerHTML">`
	if !strings.Contains(body, wantForm) || !strings.Contains(body, ">Claim ticket</button>") {
		t.Errorf("claimable row must render the claim control with endpoint %s, got: %s", completion, body)
	}
	// S5: Claim is a secondary small control, never the primary accent action.
	if !strings.Contains(body, `<button class="btn small" type="submit">Claim ticket</button>`) {
		t.Errorf("claim control must be the secondary small button, got: %s", body)
	}
	if strings.Contains(between(t, body, `id="agent-ticket-list"`, `class="tabbar"`), "btn primary") {
		t.Errorf("no agent queue control may be a primary action, got: %s", body)
	}
	if n := strings.Count(body, `class="agent-claim-form"`); n != 1 {
		t.Errorf("exactly one claim form expected (assigned rows carry none), got %d", n)
	}
	assignedSection := body[strings.Index(body, `id="assigned-tickets-title"`):strings.Index(body, `id="claimable-tickets-title"`)]
	if strings.Contains(assignedSection, `class="agent-claim-form"`) || strings.Contains(assignedSection, "Claim ticket") {
		t.Error("assigned row must not render a claim control")
	}
	titleIndex := strings.Index(body, `class="queue-title" href="/tickets/`+strconv.FormatInt(claimable.ID, 10)+`"`)
	claimIndex := strings.Index(body, ">Claim ticket</button>")
	if titleIndex < 0 || claimIndex < 0 || titleIndex > claimIndex {
		t.Error("the row title must precede the Claim control in DOM/tab order")
	}

	claimHeaders := map[string]string{
		"Cookie": sessionCookie + "=" + sess.ID, "HX-Request": "true", "HX-Target": "agent-ticket-list",
		"HX-Current-URL": "/tickets?q=Claim&assigned_page=2&claimable_page=2",
	}
	claimPath := "/tickets/" + strconv.FormatInt(claimable.ID, 10) + "/workflow/steps/1/complete"
	rec = doRequest(h.mux, h.mw, http.MethodPost, claimPath, claimHeaders)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), agentHeading("Assigned to me", 1)) ||
		!strings.Contains(rec.Body.String(), agentHeading("Available to claim", 0)) || strings.Contains(rec.Body.String(), `id="ticket-detail"`) {
		t.Fatalf("list claim = %d, body: %s", rec.Code, rec.Body.String())
	}
	rec = doRequest(h.mux, h.mw, http.MethodPost, claimPath, claimHeaders)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "This ticket is no longer available to claim.") ||
		rec.Header().Get("HX-Retarget") != "#agent-ticket-list" || rec.Header().Get("HX-Reswap") != "outerHTML" {
		t.Fatalf("stale list claim = %d, headers: %#v, body: %s", rec.Code, rec.Header(), rec.Body.String())
	}
}
