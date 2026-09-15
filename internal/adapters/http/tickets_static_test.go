package httpadapter

import (
	"net/http"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestTicketsStaticAssetAndAgentGating(t *testing.T) {
	h := newHarness(t)

	agent := seedUserRole(t, h.store, "Agent", "agent@tkt.test", domain.RoleAgent)
	user := seedUserRole(t, h.store, "User", "user@tkt.test", domain.RoleUser)
	root := seedUserRole(t, h.store, "Root", "root@tkt.test", domain.RoleRoot)
	agentSession := seedSession(t, h.store, agent.ID)
	userSession := seedSession(t, h.store, user.ID)
	rootSession := seedSession(t, h.store, root.ID)

	for _, tc := range []struct {
		name      string
		path      string
		sessionID string
		want      bool
	}{
		{name: "agent tickets", path: "/tickets", sessionID: agentSession.ID, want: true},
		{name: "root tickets", path: "/tickets", sessionID: rootSession.ID},
		{name: "admin tickets", path: "/tickets", sessionID: h.adminSession.ID},
		{name: "user tickets", path: "/tickets", sessionID: userSession.ID, want: true},
		{name: "agent non-ticket", path: "/settings", sessionID: agentSession.ID},
		{name: "user non-ticket", path: "/settings", sessionID: userSession.ID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := doRequest(h.mux, h.mw, http.MethodGet, tc.path, map[string]string{"Cookie": sessionCookie + "=" + tc.sessionID})
			got := strings.Contains(rec.Body.String(), "/static/tickets.js")
			if got != tc.want {
				t.Fatalf("%s ticket script = %t, want %t", tc.path, got, tc.want)
			}
		})
	}

	rec := h.get(t, "/static/tickets.js", false)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/javascript; charset=utf-8" || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("tickets asset response = %d/%q/%q", rec.Code, rec.Header().Get("Content-Type"), rec.Header().Get("Cache-Control"))
	}
	js := rec.Body.String()
	for _, marker := range []string{
		`document.body.addEventListener("htmx:beforeSwap"`,
		"xhr.status !== 422",
		`getResponseHeader("HX-Retarget") !== "#agent-ticket-list"`,
		`getResponseHeader("HX-Reswap") !== "outerHTML"`,
		`section[aria-labelledby="claimable-tickets-title"]`,
		"detail.shouldSwap = true",
		"detail.isError = false",
	} {
		if !strings.Contains(js, marker) {
			t.Errorf("tickets.js omits claim guard %q", marker)
		}
	}
	for _, marker := range []string{
		`document.body.addEventListener("htmx:historyRestore"`,
		`getElementById("role-ticket-search")`,
		`searchParams.get("q")`,
	} {
		if !strings.Contains(js, marker) {
			t.Errorf("tickets.js omits history-sync behavior %q", marker)
		}
	}

	position := 1
	data := fixtureListData()
	data.CurrentUser.Role = domain.RoleAgent
	data.AgentView = true
	data.Claimable = ticketListData{Tickets: []agentTicketRow{{
		Ticket:  data.Tickets[0],
		Context: application.AgentTicketRowContext{Position: &position},
	}}}
	body := renderGolden(t, "tickets_index", "", data, false)
	claimForm := `action="/tickets/2/workflow/steps/1/complete" hx-post="/tickets/2/workflow/steps/1/complete" hx-target="#agent-ticket-list" hx-swap="outerHTML"`
	if !strings.Contains(body, claimForm) {
		t.Error("claimable ticket omits the workflow completion form")
	}
	if strings.Index(body, "View ticket</a>") > strings.Index(body, "Claim ticket") {
		t.Error("View ticket must precede Claim ticket in DOM order")
	}
}
