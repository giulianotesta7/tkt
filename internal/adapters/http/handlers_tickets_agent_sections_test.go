package httpadapter

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// The agent "My work" screen keeps two stacked sections but splits the
// personal queue by lifecycle (issue #410, S4): open work (new, in progress)
// stays in "Assigned to me", and the agent's resolved and closed tickets move
// into a collapsed disclosure at the end of that section with its OWN page
// cursor. These tests drive the public HTTP surface only: the section content
// order, the counts, the cursors and the search that spans all sections.

// agentHeading is the exact section heading markup: the section's own label
// followed by its muted count, which keeps the accessible name
// "Assigned to me · 1" while the count reads as secondary.
func agentHeading(label string, count int) string {
	return `>` + label + ` <span class="agent-queue-count">· ` + strconv.Itoa(count) + `</span></h2>`
}

// agentSectionsBody renders GET /tickets for the given agent session and query.
func agentSectionsBody(t *testing.T, h *harness, sessionID, query string) string {
	t.Helper()
	rec := doRequest(h.mux, h.mw, http.MethodGet, "/tickets"+query, map[string]string{
		"Cookie": sessionCookie + "=" + sessionID,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /tickets%s status = %d, want 200: %s", query, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// between returns the substring of body that starts at from and ends at to.
// A missing marker fails the test: a silent empty slice would make every
// negative assertion below pass.
func between(t *testing.T, body, from, to string) string {
	t.Helper()
	start := strings.Index(body, from)
	if start < 0 {
		t.Fatalf("body is missing %q", from)
	}
	rest := body[start:]
	end := strings.Index(rest, to)
	if end < 0 {
		t.Fatalf("body is missing %q after %q", to, from)
	}
	return rest[:end]
}

func TestTicketsIndexAgentSplitsOpenFromDoneSections(t *testing.T) {
	h := newHarness(t)
	agent := seedUserRole(t, h.store, "Ava", "ava-sections@example.com", domain.RoleAgent)
	sess := seedSession(t, h.store, agent.ID)

	desk, err := h.desks.Create(t.Context(), *h.admin, "Sections desk")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}
	if err := h.desks.AddMember(t.Context(), *h.admin, desk.ID, agent.ID); err != nil {
		t.Fatalf("add desk member: %v", err)
	}
	category, err := h.categories.Create(t.Context(), "Agent sections")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	h.publishWorkflow(t, category.ID, domain.WorkflowDefinition{{
		Type: domain.StepAssignToDesk,
		AssignToDesk: &domain.AssignToDeskStep{
			DeskID: desk.ID, Strategy: domain.StrategyClaim,
		},
	}})

	openNew := h.seedTicket(t, "Open ticket new", nil)
	h.assignTicket(t, openNew.ID, agent.ID)
	openInProgress := h.seedTicket(t, "Open ticket in progress", nil)
	h.assignTicket(t, openInProgress.ID, agent.ID)
	h.seedTransition(t, openInProgress.ID, domain.StateInProgress, "")
	resolved := h.seedTicket(t, "Finished ticket resolved", nil)
	h.assignTicket(t, resolved.ID, agent.ID)
	h.seedTransition(t, resolved.ID, domain.StateResolved, "")
	closed := h.seedTicket(t, "Finished ticket closed", nil)
	h.assignTicket(t, closed.ID, agent.ID)
	h.makeLegacy(t, closed.ID)
	h.seedTransition(t, closed.ID, domain.StateResolved, "")
	h.seedTransition(t, closed.ID, domain.StateClosed, "")

	if _, err := h.tickets.Create(t.Context(), *h.admin, application.CreateTicketInput{
		Title: "Claimable ticket", CategoryID: category.ID, Priority: domain.PriorityMedium,
	}); err != nil {
		t.Fatalf("create claimable ticket: %v", err)
	}
	// Out of scope for this agent: another agent's personal ticket and a
	// ticket on a desk this agent does not belong to stay invisible (S8).
	otherAgent := seedUserRole(t, h.store, "Otto", "otto-sections@example.com", domain.RoleAgent)
	foreign := h.seedTicket(t, "Other agent ticket", nil)
	h.assignTicket(t, foreign.ID, otherAgent.ID)
	otherDesk, err := h.desks.Create(t.Context(), *h.admin, "Hidden desk")
	if err != nil {
		t.Fatalf("create hidden desk: %v", err)
	}
	hiddenCategory, err := h.categories.Create(t.Context(), "Hidden desk category")
	if err != nil {
		t.Fatalf("create hidden category: %v", err)
	}
	h.publishWorkflow(t, hiddenCategory.ID, domain.WorkflowDefinition{{
		Type: domain.StepAssignToDesk,
		AssignToDesk: &domain.AssignToDeskStep{
			DeskID: otherDesk.ID, Strategy: domain.StrategyClaim,
		},
	}})
	if _, err := h.tickets.Create(t.Context(), *h.admin, application.CreateTicketInput{
		Title: "Hidden desk ticket", CategoryID: hiddenCategory.ID, Priority: domain.PriorityMedium,
	}); err != nil {
		t.Fatalf("create hidden desk ticket: %v", err)
	}

	body := agentSectionsBody(t, h, sess.ID, "")

	// S6: the page summary carries the open and available counts.
	if want := `class="page-subtitle agent-summary">2 open · 1 available<`; !strings.Contains(body, want) {
		t.Errorf("page summary must contain %q, got: %s", want, body)
	}
	// Two stacked sections with their counts (S2, S4).
	for _, want := range []string{
		`>Assigned to me <span class="agent-queue-count">· 2</span></h2>`,
		`>Available to claim <span class="agent-queue-count">· 1</span></h2>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("section heading must contain %q, got: %s", want, body)
		}
	}

	openSection := between(t, body, `id="assigned-tickets-title"`, "<details")
	for _, want := range []string{"Open ticket new", "Open ticket in progress"} {
		if !strings.Contains(openSection, want) {
			t.Errorf("open section must list %q, got: %s", want, openSection)
		}
	}
	for _, gone := range []string{"Finished ticket resolved", "Finished ticket closed"} {
		if strings.Contains(openSection, gone) {
			t.Errorf("open section must not list %q, got: %s", gone, openSection)
		}
	}

	// S4: finished work is collapsed at the end of the assigned section, with
	// its own count.
	doneSection := between(t, body, "<details", `id="claimable-tickets-title"`)
	if !strings.HasPrefix(doneSection, `<details class="agent-fold">`) {
		t.Errorf("done disclosure must render collapsed, got: %s", doneSection)
	}
	// The agent has never opened these tickets and the administrator arranged
	// them, so every row carries the last-visit mark (issue #415).
	if want := `>Resolved and closed <span class="agent-fold-count">· 2</span><span class="agent-fold-changed">`; !strings.Contains(doneSection, want) {
		t.Errorf("done disclosure summary must contain %q, got: %s", want, doneSection)
	}
	for _, want := range []string{"Finished ticket resolved", "Finished ticket closed"} {
		if !strings.Contains(doneSection, want) {
			t.Errorf("done section must list %q, got: %s", want, doneSection)
		}
	}
	if strings.Contains(doneSection, "Open ticket new") {
		t.Errorf("done section must not list open work, got: %s", doneSection)
	}

	// The row is clickable through the triage queue's row behavior, and the
	// title keeps its own link (S2).
	if want := `<tr class="queue-row" data-href="/tickets/` + strconv.FormatInt(openNew.ID, 10) + `">`; !strings.Contains(body, want) {
		t.Errorf("open row must carry its navigation href, missing %q", want)
	}
	if want := `<a class="queue-title queue-title-changed" href="/tickets/` + strconv.FormatInt(openNew.ID, 10) + `">Open ticket new</a>`; !strings.Contains(body, want) {
		t.Errorf("row title must stay a link to its ticket, missing %q", want)
	}

	// S8: scope is unchanged.
	for _, gone := range []string{"Other agent ticket", "Hidden desk ticket"} {
		if strings.Contains(body, gone) {
			t.Errorf("agent queue must not leak %q", gone)
		}
	}
}

// doneTitles returns which of the seeded finished titles a rendered section
// shows, in document order.
func doneTitles(section string, seeded []string) []string {
	var found []string
	for _, title := range seeded {
		if strings.Contains(section, title) {
			found = append(found, title)
		}
	}
	return found
}

func TestTicketsIndexAgentDoneSectionKeepsItsOwnCursor(t *testing.T) {
	h := newHarness(t)
	agent := seedUserRole(t, h.store, "Ava", "ava-done-page@example.com", domain.RoleAgent)
	sess := seedSession(t, h.store, agent.ID)

	open := h.seedTicket(t, "Paging open anchor", nil)
	h.assignTicket(t, open.ID, agent.ID)
	var finished []string
	for i := 0; i < application.PageSize+1; i++ {
		title := "Paging done " + strconv.Itoa(i)
		done := h.seedTicket(t, title, nil)
		h.assignTicket(t, done.ID, agent.ID)
		h.seedTransition(t, done.ID, domain.StateResolved, "")
		finished = append(finished, title)
	}

	first := agentSectionsBody(t, h, sess.ID, "?q=paging")
	doneSection := between(t, first, "<details", `id="claimable-tickets-title"`)
	if !strings.HasPrefix(doneSection, `<details class="agent-fold">`) {
		t.Errorf("first done page must stay collapsed, got: %s", doneSection)
	}
	if want := "Page 1 of 2 · 11 tickets"; !strings.Contains(doneSection, want) {
		t.Errorf("done section must carry its own pagination %q, got: %s", want, doneSection)
	}
	assignedSection := between(t, first, `id="assigned-tickets-title"`, "<details")
	if strings.Contains(assignedSection, "Page 1 of 2") {
		t.Errorf("the done cursor must not paginate the open list, got: %s", assignedSection)
	}
	if !strings.Contains(assignedSection, "Paging open anchor") {
		t.Errorf("open work stays on its own first page, got: %s", assignedSection)
	}

	second := agentSectionsBody(t, h, sess.ID, "?q=paging&done_page=2")
	openDone := between(t, second, "<details", `id="claimable-tickets-title"`)
	if !strings.HasPrefix(openDone, `<details class="agent-fold" open>`) {
		t.Errorf("a done page beyond the first must render the disclosure open, got: %s", openDone)
	}
	if want := "Page 2 of 2 · 11 tickets"; !strings.Contains(openDone, want) {
		t.Errorf("opened done section must paginate itself, missing %q in %s", want, openDone)
	}
	// The two done pages partition the eleven finished tickets: page 1 holds a
	// full page, page 2 holds the rest, and no row appears on both.
	pageOne := doneTitles(doneSection, finished)
	pageTwo := doneTitles(openDone, finished)
	if len(pageOne) != application.PageSize || len(pageTwo) != 1 {
		t.Fatalf("done page sizes = (%d, %d), want (%d, 1)", len(pageOne), len(pageTwo), application.PageSize)
	}
	for _, title := range pageTwo {
		for _, seen := range pageOne {
			if title == seen {
				t.Errorf("done row %q repeated across pages", title)
			}
		}
	}
	if !strings.Contains(between(t, second, `id="assigned-tickets-title"`, "<details"), "Paging open anchor") {
		t.Error("changing the done cursor must not move the open list")
	}
}

func TestTicketsIndexAgentSearchSpansOpenDoneAndClaimable(t *testing.T) {
	h := newHarness(t)
	agent := seedUserRole(t, h.store, "Ava", "ava-span@example.com", domain.RoleAgent)
	sess := seedSession(t, h.store, agent.ID)

	openMatch := h.seedTicket(t, "Printer open case", nil)
	h.assignTicket(t, openMatch.ID, agent.ID)
	doneMatch := h.seedTicket(t, "Printer finished case", nil)
	h.assignTicket(t, doneMatch.ID, agent.ID)
	h.seedTransition(t, doneMatch.ID, domain.StateResolved, "")
	openMiss := h.seedTicket(t, "Laptop open case", nil)
	h.assignTicket(t, openMiss.ID, agent.ID)

	body := agentSectionsBody(t, h, sess.ID, "?q=printer")

	if want := `class="page-subtitle agent-summary">1 open · 0 available<`; !strings.Contains(body, want) {
		t.Errorf("search summary must contain %q, got: %s", want, body)
	}
	if want := `>Assigned to me <span class="agent-queue-count">· 1</span></h2>`; !strings.Contains(body, want) {
		t.Errorf("open section must count the search matches, missing %q", want)
	}
	if want := `>Resolved and closed <span class="agent-fold-count">· 1</span><span class="agent-fold-changed">`; !strings.Contains(body, want) {
		t.Errorf("done section must count the search matches, missing %q", want)
	}
	openSection := between(t, body, `id="assigned-tickets-title"`, "<details")
	if !strings.Contains(openSection, "Printer open case") || strings.Contains(openSection, "Laptop open case") {
		t.Errorf("search must filter the open section, got: %s", openSection)
	}
	doneSection := between(t, body, "<details", `id="claimable-tickets-title"`)
	if !strings.Contains(doneSection, "Printer finished case") {
		t.Errorf("search must reach the done section, got: %s", doneSection)
	}
	claimableSection := body[strings.Index(body, `id="claimable-tickets-title"`):]
	if want := "No tickets match your search."; !strings.Contains(claimableSection, want) {
		t.Errorf("search must keep filtering the claimable section, missing %q", want)
	}
}

func TestAgentListHrefCarriesEverySectionCursor(t *testing.T) {
	if got, want := agentListHref(filterState{Q: "printer"}, 1, 1, 1), "/tickets?q=printer"; got != want {
		t.Errorf("first pages = %q, want %q", got, want)
	}
	if got, want := agentListHref(filterState{Q: "printer"}, 2, 4, 3), "/tickets?assigned_page=2&claimable_page=3&done_page=4&q=printer"; got != want {
		t.Errorf("all cursors = %q, want %q", got, want)
	}
	if got, want := agentListHref(filterState{}, 1, 2, 1), "/tickets?done_page=2"; got != want {
		t.Errorf("done cursor alone = %q, want %q", got, want)
	}
}

// A ticket assigned to the agent and then cancelled is finished work too: it
// joins the collapsed disclosure with its own Cancelled badge instead of
// disappearing from the screen, as it was listed before the split.
func TestTicketsIndexAgentListsCancelledTicketsInTheDoneSection(t *testing.T) {
	h := newHarness(t)
	agent := seedUserRole(t, h.store, "Cid", "cid-sections@example.com", domain.RoleAgent)
	sess := seedSession(t, h.store, agent.ID)

	open := h.seedTicket(t, "Still open ticket", nil)
	h.assignTicket(t, open.ID, agent.ID)
	cancelled := h.seedTicket(t, "Cancelled assigned ticket", nil)
	h.assignTicket(t, cancelled.ID, agent.ID)
	h.seedTransition(t, cancelled.ID, domain.StateCancelled, "")

	body := agentSectionsBody(t, h, sess.ID, "")

	openSection := between(t, body, `id="assigned-tickets-title"`, "<details")
	if strings.Contains(openSection, "Cancelled assigned ticket") {
		t.Errorf("open section must not list the cancelled ticket: %s", openSection)
	}
	if want := agentHeading("Assigned to me", 1); !strings.Contains(body, want) {
		t.Errorf("open section heading must contain %q, got: %s", want, body)
	}
	done := between(t, body, "<details", "</details>")
	for _, want := range []string{"Cancelled assigned ticket", `<span class="agent-fold-count">· 1</span>`, ">Cancelled<"} {
		if !strings.Contains(done, want) {
			t.Errorf("done section must contain %q, got: %s", want, done)
		}
	}
}

// Every agent row keeps its timestamp reachable from the keyboard and named
// for screen readers: the relative reading is visible, and the exact instant
// is the focusable element's accessible name and tooltip, as on the cards the
// tables replaced.
func TestTicketsIndexAgentTimestampsStayFocusableWithTheFullDate(t *testing.T) {
	h := newHarness(t)
	agent := seedUserRole(t, h.store, "Tia", "tia-sections@example.com", domain.RoleAgent)
	sess := seedSession(t, h.store, agent.ID)
	ticket := h.seedTicket(t, "Timestamp ticket", nil)
	h.assignTicket(t, ticket.ID, agent.ID)

	body := agentSectionsBody(t, h, sess.ID, "")
	row := between(t, body, `id="assigned-tickets-title"`, "</table>")
	for _, want := range []string{`<time class="card-timestamp" tabindex="0" datetime="`, `aria-label="`, `data-full-date="`} {
		if !strings.Contains(row, want) {
			t.Errorf("assigned row timestamp must contain %q, got: %s", want, row)
		}
	}
}
