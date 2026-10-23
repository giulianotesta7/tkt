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

// This file pins the requester's three-section list (issue #411, S3/S4/S5/S7):
// the on-hold tickets waiting for the requester's reply, the open ones, and the
// resolved-and-closed ones folded at the end. Each section has its own page
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
func requesterSections(t *testing.T, body string) (waiting, open, closed string) {
	t.Helper()
	openAt := strings.Index(body, `id="open-tickets-title"`)
	if openAt < 0 {
		t.Fatalf("requester list must render the open section: %s", body)
	}
	waitingAt := strings.Index(body, `id="waiting-tickets-title"`)
	closedAt := strings.Index(body, `id="closed-tickets-title"`)
	if waitingAt >= 0 && waitingAt > openAt {
		t.Fatalf("the waiting section must render before the open section")
	}
	if closedAt >= 0 && closedAt < openAt {
		t.Fatalf("the closed section must render after the open section")
	}
	if waitingAt >= 0 {
		waiting = body[waitingAt:openAt]
	}
	if closedAt >= 0 {
		open, closed = body[openAt:closedAt], body[closedAt:]
		return waiting, open, closed
	}
	return waiting, body[openAt:], ""
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

// TestTicketsIndexUserSplitsWaitingOpenAndClosed proves the requester list
// splits into the three sections in order (S3, S4), that the header counts the
// open work (S5), that the waiting row carries the single primary Reply action
// into the ticket's reply box (S3), and that a cancelled ticket keeps its
// current placement — the requester list never showed it, so it is in no
// section (S4, L3).
func TestTicketsIndexUserSplitsWaitingOpenAndClosed(t *testing.T) {
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
	otherClosed := createTicketFor(t, h, other, "Other owner closed request")
	h.seedTransition(t, otherClosed.ID, domain.StateResolved, "")
	closeViaRequester(t, h, other, otherClosed.ID)

	for _, hx := range []bool{false, true} {
		body := ticketsIndexAs(t, h, sess.ID, "", hx)
		waiting, openSection, closedSection := requesterSections(t, body)

		// S5: the header counts the open work: one waiting plus one open.
		mustContain(t, body, "2 open tickets")
		// S4: every section names itself and its own count.
		mustContain(t, waiting, "Waiting for your reply · 1")
		mustContain(t, openSection, "Open · 1")
		mustContain(t, closedSection, "Closed · 2")

		// S3: the waiting row reuses the hold copy and offers one Reply action
		// that opens the ticket at its reply box.
		mustContain(t, waiting,
			"This ticket is waiting for your reply",
			held.Title,
			`href="/tickets/`+strconv.FormatInt(held.ID, 10)+`#reply"`,
			">Reply<",
		)
		// S2: the open row is a title, one-line description, number/updated
		// meta and the requester state badge; the duplicated link is gone.
		mustContain(t, openSection, open.Title, `class="req"`, `class="req-desc"`, `class="req-meta"`, "Received")
		mustContain(t, closedSection, resolved.Title, closed.Title, "Resolved", "Closed")
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
			{name: "waiting ticket", title: held.Title, in: waiting, out: []string{openSection, closedSection}},
			{name: "open ticket", title: open.Title, in: openSection, out: []string{waiting, closedSection}},
			{name: "resolved ticket", title: resolved.Title, in: closedSection, out: []string{waiting, openSection}},
			{name: "closed ticket", title: closed.Title, in: closedSection, out: []string{waiting, openSection}},
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
		// never listed it, so no section carries it. S7: another user's held
		// and closed tickets stay out of every section.
		for _, absent := range []string{cancelled.Title, otherHeld.Title, otherClosed.Title, "Other owner held request"} {
			if strings.Contains(body, absent) {
				t.Errorf("hx=%t requester list must exclude %q: %s", hx, absent, body)
			}
		}
	}

	// The same page read as the other requester proves the split is per actor:
	// their held ticket is waiting for THEM, and none of Ula's ids appear.
	otherBody := ticketsIndexAs(t, h, otherSess.ID, "", false)
	otherWaiting, _, _ := requesterSections(t, otherBody)
	mustContain(t, otherWaiting, otherHeld.Title, "Waiting for your reply · 1")
	for _, absent := range []string{held.Title, open.Title, resolved.Title, closed.Title} {
		if strings.Contains(otherBody, absent) {
			t.Errorf("another requester must not see %q: %s", absent, otherBody)
		}
	}
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
	waiting, openSection, closedSection := requesterSections(t, body)
	mustContain(t, body, "22 open tickets")
	mustContain(t, waiting, "Waiting for your reply · 11", "waiting_page=2")
	mustContain(t, openSection, "Open · 11", "page=2")
	mustContain(t, closedSection, "Closed · 22", "closed_page=2")

	// Paging the closed section leaves the open and waiting sections on page
	// one: their page boundaries are separate queries.
	body = ticketsIndexAs(t, h, sess.ID, "?q=probe&closed_page=2", false)
	waiting, openSection, closedSection = requesterSections(t, body)
	mustContain(t, openSection, "Open · 11", "Open probe 10")
	mustContain(t, waiting, "Waiting for your reply · 11", "Held probe 10")
	if strings.Contains(closedSection, "Closed probe 10") {
		t.Errorf("closed page 2 must not repeat the closed rows of page 1: %s", closedSection)
	}
	for _, absent := range []string{"Open probe 00", "Held probe 00", "Closed probe 00"} {
		if strings.Contains(body, absent) {
			t.Errorf("closed page 2 must not move the open or waiting sections off page 1 (%s): %s", absent, body)
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

	// Paging the waiting section leaves both list sections alone.
	body = ticketsIndexAs(t, h, sess.ID, "?q=probe&waiting_page=2", false)
	waiting, openSection, _ = requesterSections(t, body)
	mustContain(t, waiting, "Held probe 00")
	mustContain(t, openSection, "Open · 11", "Open probe 10")
	if strings.Contains(openSection, "Open probe 00") {
		t.Errorf("paging the waiting section must not page the open one: %s", openSection)
	}
}

// TestTicketsIndexUserSearchReadsEverySection proves the search filter composes
// with all three sections (S5): a match in the waiting, the open and the closed
// set renders in its own section, a non-matching ticket disappears from every
// section, and the header count follows the filtered open work.
func TestTicketsIndexUserSearchReadsEverySection(t *testing.T) {
	h := newHarness(t)
	user := seedUserRole(t, h.store, "Sara", "sara@example.com", domain.RoleUser)
	sess := seedSession(t, h.store, user.ID)

	held := createTicketFor(t, h, user, "VPN screenshot needed")
	holdViaService(t, h, held.ID, "we need the screenshot")
	open := createTicketFor(t, h, user, "VPN client logs")
	resolved := createTicketFor(t, h, user, "VPN resolved case")
	h.seedTransition(t, resolved.ID, domain.StateResolved, "")
	unrelated := createTicketFor(t, h, user, "Printer jam on floor three")
	unrelatedClosed := createTicketFor(t, h, user, "Old printer case")
	h.seedTransition(t, unrelatedClosed.ID, domain.StateResolved, "")

	for _, hx := range []bool{false, true} {
		body := ticketsIndexAs(t, h, sess.ID, "?q=VPN", hx)
		waiting, openSection, closedSection := requesterSections(t, body)
		mustContain(t, waiting, held.Title, "Waiting for your reply · 1")
		mustContain(t, openSection, open.Title, "Open · 1")
		mustContain(t, closedSection, resolved.Title, "Closed · 1")
		if !strings.Contains(body, "2 open tickets") {
			t.Errorf("hx=%t the header must count the filtered open work: %s", hx, body)
		}
		for _, absent := range []string{unrelated.Title, unrelatedClosed.Title} {
			if strings.Contains(body, absent) {
				t.Errorf("hx=%t search must drop the non-matching %q: %s", hx, absent, body)
			}
		}
	}

	// No match anywhere: the empty state keeps the requester's own copy and
	// offers the primary New ticket action, and no empty section survives.
	body := ticketsIndexAs(t, h, sess.ID, "?q=zzzabsent", false)
	mustContain(t, body, "No requests match your search", `href="/tickets/new"`, "0 open tickets")
	for _, title := range []string{held.Title, open.Title, resolved.Title} {
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
	for _, absent := range []string{"Waiting for your reply", `id="closed-tickets-title"`} {
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
	resolved := createTicketFor(t, h, user, "Closed timestamp request")
	h.seedTransition(t, resolved.ID, domain.StateResolved, "")

	waiting, openSection, closedSection := requesterSections(t, ticketsIndexAs(t, h, sess.ID, "", false))
	for _, section := range []string{waiting, openSection, closedSection} {
		mustContain(t, section, `<time class="card-timestamp" tabindex="0" datetime="`, `aria-label="`, `data-full-date="`)
	}
}
