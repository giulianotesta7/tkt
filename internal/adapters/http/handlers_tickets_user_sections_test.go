package httpadapter

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// This file pins the requester's three-section list (issue #411, S3/S4/S5/S7/
// S8): the tickets needing the requester's action (on-hold work waiting for
// their reply, plus the resolved ones awaiting their confirmation), the open
// ones, and the closed ones folded at the end. Each section has its own page
// cursor, the header counts the open work, and the search reaches every
// section while the actor scope stays exactly their own tickets.

// closeViaRequester closes a resolved requester-owned ticket through the audited
// confirmation use case: a requester's ticket leaves the workflow only when the
// requester confirms the resolution, so a closed fixture must take that path.
func closeViaRequester(t *testing.T, h *harness, requester *domain.User, id int64) {
	t.Helper()
	if _, err := h.tickets.ConfirmResolution(context.Background(), *requester, id); err != nil {
		t.Fatalf("confirm resolution of ticket %d: %v", id, err)
	}
}

// requesterSections slices the rendered requester list into its three sections
// through the section landmarks the template publishes. A missing landmark is
// a contract failure, not an empty section: the closed section announces itself
// with a disclosure summary, and the open section is always rendered.
func requesterSections(t *testing.T, body string) (action, open, closed string) {
	t.Helper()
	openAt := strings.Index(body, `id="open-tickets-title"`)
	if openAt < 0 {
		t.Fatalf("requester list must render the open section: %s", body)
	}
	actionAt := strings.Index(body, `id="action-tickets-title"`)
	closedAt := strings.Index(body, `id="closed-tickets-title"`)
	if actionAt >= 0 && actionAt > openAt {
		t.Fatalf("the needs-your-action section must render before the open section")
	}
	if closedAt >= 0 && closedAt < openAt {
		t.Fatalf("the closed section must render after the open section")
	}
	if actionAt >= 0 {
		action = body[actionAt:openAt]
	}
	if closedAt >= 0 {
		open, closed = body[openAt:closedAt], body[closedAt:]
		return action, open, closed
	}
	return action, body[openAt:], ""
}

// ticketsIndexAs reads /tickets as the given session, optionally as an HTMX
// fragment, and returns the body after asserting the 200 the list contract
// promises.
func ticketsIndexAs(t *testing.T, h *harness, sessionID, query string, hx bool) string {
	t.Helper()
	headers := map[string]string{"Cookie": sessionCookie + "=" + sessionID}
	if hx {
		headers["HX-Request"] = "true"
	}
	rec := doRequest(h.mux, h.mw, http.MethodGet, "/tickets"+query, headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /tickets%s hx=%t status = %d, want 200: %s", query, hx, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// TestTicketsIndexUserSplitsNeedsYourActionOpenAndClosed proves the requester
// list splits into the three sections in order (S3, S4, S8), that the combined
// Needs-your-action section holds BOTH the on-hold work and the resolved tickets
// awaiting confirmation with their own action, that the folded Closed section
// holds only closed tickets, that the header counts all open work (S5), and that
// a cancelled ticket keeps its current placement — the requester list never
// showed it, so it is in no section (S4, L3).
func TestTicketsIndexUserSplitsNeedsYourActionOpenAndClosed(t *testing.T) {
	h := newHarness(t)
	user := seedUserRole(t, h.store, "Ula", "ula@example.com", domain.RoleUser)
	other := seedUserRole(t, h.store, "Omar", "omar@example.com", domain.RoleUser)
	sess := seedSession(t, h.store, user.ID)
	otherSess := seedSession(t, h.store, other.ID)

	held := createTicketFor(t, h, user, "Waiting on the VPN screenshot")
	holdViaService(t, h, held.ID, "send us a photo of the connector")
	open := createTicketFor(t, h, user, "Open request title")
	resolved := createTicketFor(t, h, user, "Resolved request title")
	h.seedTransition(t, resolved.ID, domain.StateResolved, "")
	closed := createTicketFor(t, h, user, "Closed request title")
	h.seedTransition(t, closed.ID, domain.StateResolved, "")
	closeViaRequester(t, h, user, closed.ID)
	cancelled := createTicketFor(t, h, user, "Cancelled request title")
	h.seedTransition(t, cancelled.ID, domain.StateCancelled, "")
	otherHeld := createTicketFor(t, h, other, "Other owner held request")
	holdViaService(t, h, otherHeld.ID, "not your ticket")
	otherResolved := createTicketFor(t, h, other, "Other owner resolved request")
	h.seedTransition(t, otherResolved.ID, domain.StateResolved, "")
	otherClosed := createTicketFor(t, h, other, "Other owner closed request")
	h.seedTransition(t, otherClosed.ID, domain.StateResolved, "")
	closeViaRequester(t, h, other, otherClosed.ID)

	for _, hx := range []bool{false, true} {
		body := ticketsIndexAs(t, h, sess.ID, "", hx)
		action, openSection, closedSection := requesterSections(t, body)

		// S5/S8: the header counts every row the Needs-your-action section
		// carries plus the open work: one held, one resolved, one open.
		mustContain(t, body, "3 open tickets")
		// S4/S8: every section names itself and its own count.
		mustContain(t, action, "Needs your action · 2")
		mustContain(t, openSection, "Open · 1")
		mustContain(t, closedSection, "Closed · 1")

		// S8: the held row reuses the hold copy and offers one Reply action
		// that opens the ticket at its reply box.
		mustContain(t, action,
			"This ticket is waiting for your reply",
			held.Title,
			`href="/tickets/`+strconv.FormatInt(held.ID, 10)+`#reply"`,
			">Reply<",
		)
		// S8: the resolved row names the question that still awaits the
		// requester and offers one primary Review action into the existing
		// confirmation panel.
		mustContain(t, action,
			"Is your issue solved?",
			resolved.Title,
			`href="/tickets/`+strconv.FormatInt(resolved.ID, 10)+`#resolution"`,
			">Review<",
		)
		// The list's existing order rule holds inside the combined section:
		// newest first by creation (created_at DESC, id DESC), so the resolved
		// ticket seeded after the held one opens the section.
		if resolvedAt, heldAt := strings.Index(action, resolved.Title), strings.Index(action, held.Title); resolvedAt < 0 || heldAt < 0 || resolvedAt > heldAt {
			t.Errorf("hx=%t the Needs-your-action rows must stay newest first (resolved=%d held=%d): %s", hx, resolvedAt, heldAt, body)
		}
		// S2: the open row is a title, one-line description, number/updated
		// meta and the requester state badge; the duplicated link is gone.
		mustContain(t, openSection, open.Title, `class="req"`, `class="req-desc"`, `class="req-meta"`, "Received")
		mustContain(t, closedSection, closed.Title, "Closed")
		if strings.Contains(closedSection, resolved.Title) {
			t.Errorf("hx=%t a resolved ticket awaiting confirmation must not sit in the closed fold: %s", hx, closedSection)
		}
		if strings.Contains(body, "View request") {
			t.Errorf("hx=%t the duplicated View request link must be gone: %s", hx, body)
		}

		// Section membership: one ticket appears in exactly one section.
		for _, tc := range []struct {
			name   string
			title  string
			in     string
			out    []string
			absent string
		}{
			{name: "held ticket", title: held.Title, in: action, out: []string{openSection, closedSection}},
			{name: "resolved ticket", title: resolved.Title, in: action, out: []string{openSection, closedSection}},
			{name: "open ticket", title: open.Title, in: openSection, out: []string{action, closedSection}},
			{name: "closed ticket", title: closed.Title, in: closedSection, out: []string{action, openSection}},
		} {
			if !strings.Contains(tc.in, tc.title) {
				t.Errorf("hx=%t %s %q must render in its own section", hx, tc.name, tc.title)
			}
			for _, otherSection := range tc.out {
				if strings.Contains(otherSection, tc.title) {
					t.Errorf("hx=%t %s %q must appear in exactly one section: %s", hx, tc.name, tc.title, body)
				}
			}
		}

		// A cancelled ticket keeps its current placement: the requester list
		// never listed it, so no section carries it. S7: another user's held,
		// resolved and closed tickets stay out of every section.
		for _, absent := range []string{cancelled.Title, otherHeld.Title, otherResolved.Title, otherClosed.Title} {
			if strings.Contains(body, absent) {
				t.Errorf("hx=%t requester list must exclude %q: %s", hx, absent, body)
			}
		}
	}

	// The same page read as the other requester proves the split is per actor:
	// their held and resolved tickets are what awaits THEM, and none of Ula's
	// ids appear.
	otherBody := ticketsIndexAs(t, h, otherSess.ID, "", false)
	otherAction, _, _ := requesterSections(t, otherBody)
	mustContain(t, otherAction, otherHeld.Title, otherResolved.Title, "Needs your action · 2")
	for _, absent := range []string{held.Title, open.Title, resolved.Title, closed.Title} {
		if strings.Contains(otherBody, absent) {
			t.Errorf("another requester must not see %q: %s", absent, otherBody)
		}
	}
}

// TestTicketsIndexUserReviewOpensTheConfirmationPanel pins the resolved half of
// the Needs-your-action section (issue #411, S8): the Review link lands on the
// ticket detail at the EXISTING confirmation panel anchor, so the requester
// reads the resolution before deciding, and the closed fold never carries it.
func TestTicketsIndexUserReviewOpensTheConfirmationPanel(t *testing.T) {
	h := newHarness(t)
	user := seedUserRole(t, h.store, "Rita", "rita@example.com", domain.RoleUser)
	sess := seedSession(t, h.store, user.ID)
	resolved := createTicketFor(t, h, user, "Resolved needs confirmation")
	h.seedTransition(t, resolved.ID, domain.StateResolved, "")

	body := ticketsIndexAs(t, h, sess.ID, "", false)
	action, _, closedSection := requesterSections(t, body)
	mustContain(t, action, "Needs your action · 1", "Is your issue solved?",
		`href="/tickets/`+strconv.FormatInt(resolved.ID, 10)+`#resolution"`, ">Review<")
	if closedSection != "" {
		t.Errorf("the closed fold must hold only closed tickets, got: %s", closedSection)
	}

	detail := getDetailAs(t, h, "/tickets/"+strconv.FormatInt(resolved.ID, 10), sess.ID, false).Body.String()
	mustContain(t, detail, `id="resolution"`, "Is your issue solved?")
}

// TestTicketsIndexUserSectionsPageIndependently proves every section carries
// its own page cursor (S4): paging one section never moves another, and each
// section's next link preserves the other cursors and the search term.
func TestTicketsIndexUserSectionsPageIndependently(t *testing.T) {
	h := newHarness(t)
	user := seedUserRole(t, h.store, "Pia", "pia@example.com", domain.RoleUser)
	sess := seedSession(t, h.store, user.ID)

	var heldIDs, resolvedIDs, closedIDs []int64
	for index := 0; index < 11; index++ {
		createTicketFor(t, h, user, fmt.Sprintf("Open probe %02d", index))
		heldIDs = append(heldIDs, createTicketFor(t, h, user, fmt.Sprintf("Held probe %02d", index)).ID)
		resolvedIDs = append(resolvedIDs, createTicketFor(t, h, user, fmt.Sprintf("Resolved probe %02d", index)).ID)
		closedIDs = append(closedIDs, createTicketFor(t, h, user, fmt.Sprintf("Closed probe %02d", index)).ID)
	}
	for _, id := range heldIDs {
		holdViaService(t, h, id, "waiting for the requester")
	}
	for _, id := range append(append([]int64{}, resolvedIDs...), closedIDs...) {
		h.seedTransition(t, id, domain.StateResolved, "")
	}
	for _, id := range closedIDs {
		closeViaRequester(t, h, user, id)
	}

	// Page one: each section links to its own second page, and the open
	// section's next link keeps no closed cursor because none was chosen yet.
	body := ticketsIndexAs(t, h, sess.ID, "?q=probe", false)
	action, openSection, closedSection := requesterSections(t, body)
	mustContain(t, body, "33 open tickets")
	mustContain(t, action, "Needs your action · 22", "action_page=2")
	mustContain(t, openSection, "Open · 11", "page=2")
	mustContain(t, closedSection, "Closed · 11", "closed_page=2")

	// Paging the closed section leaves the action and open sections on page one:
	// their page boundaries are separate queries.
	body = ticketsIndexAs(t, h, sess.ID, "?q=probe&closed_page=2", false)
	action, openSection, closedSection = requesterSections(t, body)
	mustContain(t, openSection, "Open · 11", "Open probe 10")
	mustContain(t, action, "Needs your action · 22", "Resolved probe 10", "Held probe 10")
	if strings.Contains(closedSection, "Closed probe 10") {
		t.Errorf("closed page 2 must not repeat the closed rows of page 1: %s", closedSection)
	}
	for _, absent := range []string{"Open probe 00", "Held probe 00", "Resolved probe 00"} {
		if strings.Contains(body, absent) {
			t.Errorf("closed page 2 must not move the action or open sections off page 1 (%s): %s", absent, body)
		}
	}

	// Paging the open section leaves the closed section on its own page one and
	// keeps its next link pointing at the closed second page.
	body = ticketsIndexAs(t, h, sess.ID, "?q=probe&page=2", false)
	_, openSection, closedSection = requesterSections(t, body)
	mustContain(t, openSection, "Open probe 00")
	mustContain(t, closedSection, "Closed probe 10", "closed_page=2")
	if strings.Contains(closedSection, "Closed probe 00") {
		t.Errorf("paging the open section must not page the closed one: %s", closedSection)
	}

	// Paging the action section leaves both list sections alone.
	body = ticketsIndexAs(t, h, sess.ID, "?q=probe&action_page=2", false)
	action, openSection, _ = requesterSections(t, body)
	mustContain(t, action, "Resolved probe 05", "Held probe 01")
	mustContain(t, openSection, "Open · 11", "Open probe 10")
	for _, absent := range []string{"Resolved probe 10", "Held probe 10", "Open probe 00"} {
		if strings.Contains(body, absent) {
			t.Errorf("action page 2 must not repeat the action rows of page 1 or page the open section (%s): %s", absent, body)
		}
	}
}

// TestTicketsIndexUserSearchReadsEverySection proves the search filter composes
// with all three sections (S5): a match in the action (held and resolved), the
// open and the closed set renders in its own section, a non-matching ticket
// disappears from every section, and the header count follows the filtered open
// work.
func TestTicketsIndexUserSearchReadsEverySection(t *testing.T) {
	h := newHarness(t)
	user := seedUserRole(t, h.store, "Sara", "sara@example.com", domain.RoleUser)
	sess := seedSession(t, h.store, user.ID)

	held := createTicketFor(t, h, user, "VPN screenshot needed")
	holdViaService(t, h, held.ID, "we need the screenshot")
	open := createTicketFor(t, h, user, "VPN client logs")
	resolved := createTicketFor(t, h, user, "VPN resolved case")
	h.seedTransition(t, resolved.ID, domain.StateResolved, "")
	closed := createTicketFor(t, h, user, "VPN closed case")
	h.seedTransition(t, closed.ID, domain.StateResolved, "")
	closeViaRequester(t, h, user, closed.ID)
	unrelated := createTicketFor(t, h, user, "Printer jam on floor three")
	unrelatedClosed := createTicketFor(t, h, user, "Old printer case")
	h.seedTransition(t, unrelatedClosed.ID, domain.StateResolved, "")
	otherResolved := createTicketFor(t, h, user, "Printer resolved case")
	h.seedTransition(t, otherResolved.ID, domain.StateResolved, "")

	for _, hx := range []bool{false, true} {
		body := ticketsIndexAs(t, h, sess.ID, "?q=VPN", hx)
		action, openSection, closedSection := requesterSections(t, body)
		mustContain(t, action, held.Title, resolved.Title, "Needs your action · 2")
		mustContain(t, openSection, open.Title, "Open · 1")
		mustContain(t, closedSection, closed.Title, "Closed · 1")
		if !strings.Contains(body, "3 open tickets") {
			t.Errorf("hx=%t the header must count the filtered open work: %s", hx, body)
		}
		for _, absent := range []string{unrelated.Title, unrelatedClosed.Title, otherResolved.Title} {
			if strings.Contains(body, absent) {
				t.Errorf("hx=%t search must drop the non-matching %q: %s", hx, absent, body)
			}
		}
	}

	// No match anywhere: the empty state keeps the requester's own copy and
	// offers the primary New ticket action, and no empty section survives.
	body := ticketsIndexAs(t, h, sess.ID, "?q=zzzabsent", false)
	mustContain(t, body, "No requests match your search", `href="/tickets/new"`, "0 open tickets")
	for _, title := range []string{held.Title, open.Title, resolved.Title, closed.Title} {
		if strings.Contains(body, title) {
			t.Errorf("a search with no match must render no row %q: %s", title, body)
		}
	}

	// A requester with no tickets at all reads the empty list with the same
	// action, and no section renders a header for nothing.
	empty := seedUserRole(t, h.store, "Nora", "nora@example.com", domain.RoleUser)
	emptySess := seedSession(t, h.store, empty.ID)
	body = ticketsIndexAs(t, h, emptySess.ID, "", false)
	mustContain(t, body, "No requests yet", `href="/tickets/new"`, "0 open tickets")
	for _, absent := range []string{"Needs your action", `id="closed-tickets-title"`} {
		if strings.Contains(body, absent) {
			t.Errorf("an empty requester list must not render %q: %s", absent, body)
		}
	}
}

// Every requester row keeps its timestamp reachable from the keyboard and
// named for screen readers: the relative reading is visible, and the exact
// instant is the focusable element's accessible name and tooltip, as on the
// cards the list replaced. It holds in all three sections.
func TestTicketsIndexUserTimestampsStayFocusableWithTheFullDate(t *testing.T) {
	h := newHarness(t)
	user := seedUserRole(t, h.store, "Tess", "tess@example.com", domain.RoleUser)
	sess := seedSession(t, h.store, user.ID)

	held := createTicketFor(t, h, user, "Waiting timestamp request")
	holdViaService(t, h, held.ID, "send the log")
	createTicketFor(t, h, user, "Open timestamp request")
	resolved := createTicketFor(t, h, user, "Resolved timestamp request")
	h.seedTransition(t, resolved.ID, domain.StateResolved, "")
	closed := createTicketFor(t, h, user, "Closed timestamp request")
	h.seedTransition(t, closed.ID, domain.StateResolved, "")
	closeViaRequester(t, h, user, closed.ID)

	action, openSection, closedSection := requesterSections(t, ticketsIndexAs(t, h, sess.ID, "", false))
	for _, section := range []string{action, openSection, closedSection} {
		mustContain(t, section, `<time class="card-timestamp" tabindex="0" datetime="`, `aria-label="`, `data-full-date="`)
	}
}
