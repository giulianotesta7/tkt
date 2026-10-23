package httpadapter

// Durable regression tests for slice B4 of the notification center (issue #344,
// specs S13-S16). They were derived from the feature document rather than from
// the writer's own tests: each one drives the real routes as real roles and
// reads only the public surface (count endpoint, panel, read routes, ticket
// detail) plus the notifications table for the state invariants a request must
// not move. They are kept verbatim apart from their names because they cover
// what the slice's own tests assume: a row outlives the reader's access without
// repeating it (S16), a read cannot be steered or forged (S14, D17), the count
// has exactly one source (S13), and the copy follows the instance language
// (S15).

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func notificationRequestWithHeaders(t *testing.T, h *harness, sessionID, method, target, form string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(form))
	req.Header.Set("Cookie", sessionCookie+"="+sessionID)
	if form != "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)
	return rec
}

func notificationPanelRowCount(t *testing.T, body string) int {
	t.Helper()
	return strings.Count(body, `action="/notifications/`) - strings.Count(body, `action="/notifications/read-all"`)
}

func notificationStateDigest(t *testing.T, h *harness) string {
	t.Helper()
	var db *sql.DB = h.rawDB(t)
	rows, err := db.Query(`SELECT id, user_id, ticket_id, kind, read_at FROM notifications ORDER BY id`)
	if err != nil {
		t.Fatalf("notification state query: %v", err)
	}
	defer rows.Close()
	sum := sha256.New()
	for rows.Next() {
		var id, uid, tid int64
		var kind string
		var read sql.NullString
		if err := rows.Scan(&id, &uid, &tid, &kind, &read); err != nil {
			t.Fatalf("notification state scan: %v", err)
		}
		fmt.Fprintf(sum, "%d|%d|%d|%s|%s\n", id, uid, tid, kind, read.String)
	}
	return hex.EncodeToString(sum.Sum(nil))
}

func isTicketPath(loc string) bool {
	if !strings.HasPrefix(loc, "/tickets/") {
		return false
	}
	_, err := strconv.ParseInt(strings.TrimPrefix(loc, "/tickets/"), 10, 64)
	return err == nil
}

func unreadNotificationRows(t *testing.T, h *harness) int64 {
	t.Helper()
	return scanOneInt(t, h.rawDB(t), `SELECT COUNT(*) FROM notifications WHERE read_at IS NULL`)
}

// S16 / S10: an agent reassigned away must not see the ticket's title or number
// anywhere the panel surfaces, while the row itself stays readable.
func TestNotificationsPanelDropsTicketFactsAfterReassignment(t *testing.T) {
	h := newHarness(t)
	reader := h.createUser(t, "Verifier Reader", "verifier-reader@tkt.test", "Secret123!42")
	ticket := h.seedTicket(t, "Secret payroll outage", nil)
	h.assignTicket(t, ticket.ID, reader.ID)
	session := seedSession(t, h.store, reader.ID).ID
	if body := notificationRequestWithHeaders(t, h, session, http.MethodGet, "/notifications", "", nil).Body.String(); !strings.Contains(body, "Secret payroll outage") {
		t.Fatalf("precondition: reader cannot see their own row title")
	}
	other := h.createUser(t, "Verifier New Owner", "verifier-owner@tkt.test", "Secret123!42")
	if _, err := h.tickets.Assign(t.Context(), *h.admin, ticket.ID, &other.ID, "hand over"); err != nil {
		t.Fatalf("reassign: %v", err)
	}

	for _, target := range []string{"/notifications", "/notifications/count"} {
		for _, hx := range []bool{false, true} {
			hdr := map[string]string{}
			if hx {
				hdr["HX-Request"] = "true"
			}
			body := notificationRequestWithHeaders(t, h, session, http.MethodGet, target, "", hdr).Body.String()
			for _, leak := range []string{"Secret payroll outage", "TKT-1"} {
				if strings.Contains(body, leak) {
					t.Errorf("GET %s (hx=%v) leaks %q: %.600s", target, hx, leak, body)
				}
			}
			if target == "/notifications" && hx && !strings.Contains(body, "Ticket no longer available") {
				t.Errorf("fragment does not explain the unreachable ticket: %.600s", body)
			}
		}
	}
	// Opening the row lands on the ordinary access-denied path, never on facts.
	detail := notificationRequestWithHeaders(t, h, session, http.MethodGet, "/tickets/"+strconv.FormatInt(ticket.ID, 10), "", nil)
	if detail.Code != http.StatusNotFound {
		t.Fatalf("detail of an out-of-scope ticket = %d, want 404", detail.Code)
	}
	if strings.Contains(detail.Body.String(), "Secret payroll outage") {
		t.Errorf("the 404 body leaked the title")
	}
}

// S16 / S10: an agent demoted to the user role loses read scope on a ticket
// assigned to them; the panel must stop repeating its facts.
func TestNotificationsPanelDropsTicketFactsAfterDemotion(t *testing.T) {
	h := newHarness(t)
	agent := h.createUser(t, "Verifier Demoted", "verifier-demoted@tkt.test", "Secret123!42")
	ticket := h.seedTicket(t, "Confidential merger plan", nil)
	h.assignTicket(t, ticket.ID, agent.ID)
	session := seedSession(t, h.store, agent.ID).ID

	rec := h.postForm(t, "/users/"+strconv.FormatInt(agent.ID, 10)+"/edit", map[string][]string{
		"name":   {"Verifier Demoted"},
		"email":  {"verifier-demoted@tkt.test"},
		"role":   {"user"},
		"active": {"true"},
	}, false)
	if rec.Code != http.StatusSeeOther && rec.Code != http.StatusOK {
		t.Fatalf("demote via HTTP = %d: %s", rec.Code, rec.Body.String())
	}
	if detail := notificationRequestWithHeaders(t, h, session, http.MethodGet, "/tickets/"+strconv.FormatInt(ticket.ID, 10), "", nil); detail.Code != http.StatusNotFound {
		t.Fatalf("demoted reader can still open the ticket: %d", detail.Code)
	}
	body := notificationRequestWithHeaders(t, h, session, http.MethodGet, "/notifications", "", nil).Body.String()
	for _, leak := range []string{"Confidential merger plan", "TKT-1"} {
		if strings.Contains(body, leak) {
			t.Errorf("demoted reader's panel leaks %q: %.800s", leak, body)
		}
	}
}

// S16: a requester never learns an internal comment exists.
func TestNotificationsRequesterPanelHidesInternalComments(t *testing.T) {
	h := newHarness(t)
	requester := seedUserRole(t, h.store, "Verifier Requester", "verifier-requester@tkt.test", domain.RoleUser)
	ticket := seedUserOwnedTicket(t, h, requester, "Printer on fire")
	agent := h.createUser(t, "Verifier Commenter", "verifier-commenter@tkt.test", "Secret123!42")
	h.assignTicket(t, ticket.ID, agent.ID)
	requesterSession := seedSession(t, h.store, requester.ID).ID
	agentSession := seedSession(t, h.store, agent.ID).ID

	if rec := notificationRequestWithHeaders(t, h, agentSession, http.MethodPost, "/tickets/"+strconv.FormatInt(ticket.ID, 10)+"/comments", "body=internal+secret+words&visibility=internal", nil); rec.Code >= 400 {
		t.Fatalf("internal comment as agent = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := notificationRequestWithHeaders(t, h, agentSession, http.MethodPost, "/tickets/"+strconv.FormatInt(ticket.ID, 10)+"/comments", "body=public+answer&visibility=public", nil); rec.Code >= 400 {
		t.Fatalf("public comment as agent = %d: %s", rec.Code, rec.Body.String())
	}
	for _, target := range []string{"/notifications", "/notifications/count"} {
		body := notificationRequestWithHeaders(t, h, requesterSession, http.MethodGet, target, "", nil).Body.String()
		for _, leak := range []string{"New internal comment", "Nuevo comentario interno", "internal secret words", "internal+secret+words", "comment_internal", "Reassigned away"} {
			if strings.Contains(body, leak) {
				t.Errorf("GET %s reveals the internal comment (%q): %.800s", target, leak, body)
			}
		}
	}
	count := notificationRequestWithHeaders(t, h, requesterSession, http.MethodGet, "/notifications/count", "", nil).Body.String()
	if !strings.Contains(count, "1 unread") {
		t.Errorf("requester's badge = %.300s, want exactly the public comment", count)
	}
}

// S14 / cross-user: another user's id answers 404 and changes nothing.
func TestNotificationsForeignReadIs404AndChangesNothing(t *testing.T) {
	h := newHarness(t)
	owner := h.createUser(t, "Verifier Owner", "verifier-owner2@tkt.test", "Secret123!42")
	ticket := h.seedTicket(t, "Owned by the recipient", nil)
	h.assignTicket(t, ticket.ID, owner.ID)
	ownerSession := seedSession(t, h.store, owner.ID).ID
	bystander := h.createUser(t, "Verifier Bystander", "verifier-bystander@tkt.test", "Secret123!42")
	bystanderSession := seedSession(t, h.store, bystander.ID).ID

	body := notificationRequestWithHeaders(t, h, ownerSession, http.MethodGet, "/notifications", "", nil).Body.String()
	ids := notificationRowIDs(t, body)
	if len(ids) != 1 {
		t.Fatalf("owner rows = %v, want 1", ids)
	}
	before := notificationStateDigest(t, h)
	for _, form := range []string{"", "ticket_id=" + strconv.FormatInt(ticket.ID, 10)} {
		rec := notificationRequestWithHeaders(t, h, bystanderSession, http.MethodPost, "/notifications/"+ids[0]+"/read", form, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("foreign read (form %q) = %d, want 404: %.300s", form, rec.Code, rec.Body.String())
		}
	}
	if after := notificationStateDigest(t, h); after != before {
		t.Errorf("a rejected cross-user read mutated notifications: %s -> %s", before, after)
	}
	// A nonexistent id is the same answer.
	if rec := notificationRequestWithHeaders(t, h, ownerSession, http.MethodPost, "/notifications/999999/read", "ticket_id=1", nil); rec.Code != http.StatusNotFound {
		t.Errorf("missing id = %d, want 404", rec.Code)
	}
	// read-all is scoped: the bystander's own empty center leaves the owner alone.
	if rec := notificationRequestWithHeaders(t, h, bystanderSession, http.MethodPost, "/notifications/read-all", "", nil); rec.Code != http.StatusSeeOther {
		t.Errorf("read-all as bystander = %d", rec.Code)
	}
	if after := notificationStateDigest(t, h); after != before {
		t.Errorf("a bystander's read-all mutated the owner's rows: %s -> %s", before, after)
	}
	if n := unreadNotificationRows(t, h); n != 1 {
		t.Errorf("unread rows = %d, want 1", n)
	}
}

// S13/S14: the mark-read redirect cannot be steered off-site and refuses
// non-ticket targets.
func TestNotificationsMarkReadCannotRedirectOffSite(t *testing.T) {
	h := newHarness(t)
	owner := h.createUser(t, "Verifier Redirect", "verifier-redirect@tkt.test", "Secret123!42")
	ticket := h.seedTicket(t, "Redirect target", nil)
	h.assignTicket(t, ticket.ID, owner.ID)
	session := seedSession(t, h.store, owner.ID).ID
	id := notificationRowIDs(t, notificationRequestWithHeaders(t, h, session, http.MethodGet, "/notifications", "", nil).Body.String())[0]

	for _, raw := range []string{
		"", "abc", "0", "-7", "//evil.example", "https://evil.example/x",
		"javascript:alert(1)", "/\\evil.example", "1%0d%0aLocation:%20//evil.example",
		"999999", "1/../2",
	} {
		rec := notificationRequestWithHeaders(t, h, session, http.MethodPost, "/notifications/"+id+"/read", "ticket_id="+raw, nil)
		if rec.Code != http.StatusSeeOther {
			t.Errorf("ticket_id=%q = %d, want 303", raw, rec.Code)
			continue
		}
		loc := rec.Header().Get("Location")
		if loc != "/notifications" && !isTicketPath(loc) {
			t.Errorf("ticket_id=%q produced Location %q", raw, loc)
		}
		if strings.Contains(loc, "evil") || strings.Contains(loc, "//") {
			t.Errorf("ticket_id=%q steered the redirect off-site: %q", raw, loc)
		}
	}
}

// D17 / S14: the Origin gate covers both new POSTs.
func TestNotificationsReadWritesRejectCrossSiteOrigin(t *testing.T) {
	h := newHarness(t)
	owner := h.createUser(t, "Verifier Origin", "verifier-origin@tkt.test", "Secret123!42")
	ticket := h.seedTicket(t, "Origin target", nil)
	h.assignTicket(t, ticket.ID, owner.ID)
	session := seedSession(t, h.store, owner.ID).ID
	id := notificationRowIDs(t, notificationRequestWithHeaders(t, h, session, http.MethodGet, "/notifications", "", nil).Body.String())[0]
	before := notificationStateDigest(t, h)

	for _, target := range []string{"/notifications/" + id + "/read", "/notifications/read-all"} {
		rec := notificationRequestWithHeaders(t, h, session, http.MethodPost, target, "ticket_id="+strconv.FormatInt(ticket.ID, 10), map[string]string{"Origin": "https://evil.example"})
		if rec.Code != http.StatusForbidden {
			t.Errorf("cross-site POST %s = %d, want 403", target, rec.Code)
		}
	}
	if after := notificationStateDigest(t, h); after != before {
		t.Errorf("a cross-site POST marked rows read: %s -> %s", before, after)
	}
	// Same-origin POSTs still work.
	rec := notificationRequestWithHeaders(t, h, session, http.MethodPost, "/notifications/"+id+"/read", "ticket_id="+strconv.FormatInt(ticket.ID, 10), map[string]string{"Origin": "http://example.com"})
	if rec.Code != http.StatusSeeOther {
		t.Errorf("same-origin POST = %d, want 303", rec.Code)
	}
	if n := unreadNotificationRows(t, h); n != 0 {
		t.Errorf("unread rows after the same-origin read = %d, want 0", n)
	}
}

// S14: the panel shows the newest 20, newest first, and "mark all as read"
// clears a badge larger than the window.
func TestNotificationsPanelShowsTheNewestTwentyRows(t *testing.T) {
	h := newHarness(t)
	agent := h.createUser(t, "Verifier Window", "verifier-window@tkt.test", "Secret123!42")
	session := seedSession(t, h.store, agent.ID).ID
	for i := 1; i <= 25; i++ {
		ticket := h.seedTicket(t, fmt.Sprintf("Window ticket %02d", i), nil)
		h.assignTicket(t, ticket.ID, agent.ID)
	}
	body := notificationRequestWithHeaders(t, h, session, http.MethodGet, "/notifications", "", nil).Body.String()
	if n := notificationPanelRowCount(t, body); n != 20 {
		t.Errorf("panel rows = %d, want 20", n)
	}
	if !strings.Contains(body, "Window ticket 25") {
		t.Errorf("panel does not start with the newest row")
	}
	if strings.Contains(body, "Window ticket 01") {
		t.Errorf("panel shows a row outside the newest-20 window")
	}
	if count := notificationRequestWithHeaders(t, h, session, http.MethodGet, "/notifications/count", "", nil).Body.String(); !strings.Contains(count, "25 unread") {
		t.Errorf("badge = %.200s, want the exact 25", count)
	}
	if rec := notificationRequestWithHeaders(t, h, session, http.MethodPost, "/notifications/read-all", "", nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("read-all = %d", rec.Code)
	}
	if n := unreadNotificationRows(t, h); n != 0 {
		t.Errorf("unread rows after read-all = %d, want 0", n)
	}
	if count := notificationRequestWithHeaders(t, h, session, http.MethodGet, "/notifications/count", "", nil).Body.String(); strings.Contains(count, "rail-badge") || strings.Contains(count, "unread") {
		t.Errorf("zero-state bell still shows a count: %.200s", count)
	}
}

// S13: no ordinary page render carries the count; the endpoint is the only
// source, and its zero state has no badge.
func TestNotificationsNoPageRenderCarriesTheCount(t *testing.T) {
	h := newHarness(t)
	agent := h.createUser(t, "Verifier Page", "verifier-page@tkt.test", "Secret123!42")
	for i := 1; i <= 3; i++ {
		ticket := h.seedTicket(t, fmt.Sprintf("Page ticket %d", i), nil)
		h.assignTicket(t, ticket.ID, agent.ID)
	}
	session := seedSession(t, h.store, agent.ID).ID
	for _, target := range []string{"/tickets", "/notifications"} {
		body := notificationRequestWithHeaders(t, h, session, http.MethodGet, target, "", nil).Body.String()
		if strings.Contains(body, `<span class="bell-badge"`) || strings.Contains(body, " unread\"") {
			t.Errorf("GET %s rendered a badge from the page render", target)
		}
		if !strings.Contains(body, `hx-get="/notifications/count"`) {
			t.Errorf("GET %s lost the bell's own count endpoint", target)
		}
		if !strings.Contains(body, "every 45s [document.visibilityState === 'visible']") {
			t.Errorf("GET %s lost the 45s visible-only poll", target)
		}
	}
	count := notificationRequestWithHeaders(t, h, session, http.MethodGet, "/notifications/count", "", nil).Body.String()
	for _, want := range []string{`aria-label="Notifications, 3 unread"`, `<span class="bell-badge" aria-hidden="true">3</span>`} {
		if !strings.Contains(count, want) {
			t.Errorf("count fragment missing %q: %.400s", want, count)
		}
	}
	if strings.Contains(count, "TKT-") {
		t.Errorf("the count fragment carries ticket facts: %.400s", count)
	}
	if strings.Contains(count, "<!DOCTYPE") {
		t.Errorf("the count endpoint returned a document")
	}
}

// S15: both catalogues carry the new copy and the panel follows the instance
// language at render time (neutral tuteo, "ticket" vocabulary).
func TestNotificationsSpanishPanelAndBell(t *testing.T) {
	h := newHarness(t)
	agent := h.createUser(t, "Verifier Es", "verifier-es@tkt.test", "Secret123!42")
	session := seedSession(t, h.store, agent.ID).ID

	en := notificationRequestWithHeaders(t, h, session, http.MethodGet, "/notifications", "", nil).Body.String()
	if !strings.Contains(en, "No notifications yet") {
		t.Fatalf("English empty state missing: %.400s", en)
	}
	// The notification is CREATED while the instance is English; the Spanish
	// render below proves the copy is resolved at render time (S15), not stored.
	ticket := h.seedTicket(t, "Fallo de la VPN", nil)
	h.assignTicket(t, ticket.ID, agent.ID)
	enRow := notificationRequestWithHeaders(t, h, session, http.MethodGet, "/notifications", "", nil).Body.String()
	if !strings.Contains(enRow, "Assigned to you") {
		t.Fatalf("English row missing: %.800s", enRow)
	}
	if err := h.settings.SetInterfaceLanguage(t.Context(), *h.admin, application.InterfaceLanguageSpanish); err != nil {
		t.Fatalf("set language: %v", err)
	}
	full := notificationRequestWithHeaders(t, h, session, http.MethodGet, "/notifications", "", nil).Body.String()
	for _, want := range []string{"Se te asignó el ticket", "Sin leer", "Marcar todo como leído", ">Hoy</h3>", "<h1 class=\"command-title\">Notificaciones</h1>"} {
		if !strings.Contains(full, want) {
			t.Errorf("Spanish panel missing %q: %.800s", want, full)
		}
	}
	for _, leak := range []string{"Assigned to you", "Mark all as read", "No notifications yet", "Unread<"} {
		if strings.Contains(full, leak) {
			t.Errorf("Spanish panel still renders English %q", leak)
		}
	}
	one := notificationRequestWithHeaders(t, h, session, http.MethodGet, "/notifications/count", "", nil).Body.String()
	if !strings.Contains(one, `aria-label="Notificaciones, 1 sin leer"`) {
		t.Errorf("Spanish counted bell name = %.300s", one)
	}
	// The zero state has its own accessible name and hides the badge.
	if rec := notificationRequestWithHeaders(t, h, session, http.MethodPost, "/notifications/read-all", "", nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("read-all in es = %d", rec.Code)
	}
	zero := notificationRequestWithHeaders(t, h, session, http.MethodGet, "/notifications/count", "", nil).Body.String()
	if !strings.Contains(zero, `aria-label="Notificaciones"`) || strings.Contains(zero, "rail-badge") {
		t.Errorf("Spanish zero bell = %.300s", zero)
	}
	empty := notificationRequestWithHeaders(t, h, h.adminSession.ID, http.MethodGet, "/notifications", "", nil).Body.String()
	for _, want := range []string{"Todavía no hay notificaciones", "Cuando algo pasa en un ticket que puedes ver, aparece aquí."} {
		if !strings.Contains(empty, want) {
			t.Errorf("Spanish empty panel missing %q: %.500s", want, empty)
		}
	}
}

// S16: unassignment (no desk claim in this instance) also drops the reader's
// scope; the panel and the detail must agree, and the title must disappear
// with the access, not with the assignment.
func TestNotificationsPanelDropsTicketFactsAfterUnassignment(t *testing.T) {
	h := newHarness(t)
	reader := h.createUser(t, "Verifier Unassigned", "verifier-unassigned@tkt.test", "Secret123!42")
	ticket := h.seedTicket(t, "Quarterly layoff list", nil)
	h.assignTicket(t, ticket.ID, reader.ID)
	session := seedSession(t, h.store, reader.ID).ID
	if _, err := h.tickets.Assign(t.Context(), *h.admin, ticket.ID, nil, "back to the queue"); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	panel := notificationRequestWithHeaders(t, h, session, http.MethodGet, "/notifications", "", nil).Body.String()
	frag := notificationRequestWithHeaders(t, h, session, http.MethodGet, "/notifications", "", map[string]string{"HX-Request": "true"}).Body.String()
	count := notificationRequestWithHeaders(t, h, session, http.MethodGet, "/notifications/count", "", nil).Body.String()
	for name, body := range map[string]string{"panel": panel, "fragment": frag, "count": count} {
		for _, leak := range []string{"Quarterly layoff list", "TKT-"} {
			if strings.Contains(body, leak) {
				t.Errorf("%s leaks %q after unassignment: %.600s", name, leak, body)
			}
		}
	}
	if detail := notificationRequestWithHeaders(t, h, session, http.MethodGet, "/tickets/"+strconv.FormatInt(ticket.ID, 10), "", nil); detail.Code != http.StatusNotFound {
		t.Errorf("detail after unassignment = %d, want 404 (panel and detail must agree)", detail.Code)
	}
	// The row is still theirs: it stays listed and still marks read.
	id := notificationRowIDs(t, panel)
	if len(id) != 1 {
		t.Fatalf("rows after unassignment = %v, want the row kept", id)
	}
	if rec := notificationRequestWithHeaders(t, h, session, http.MethodPost, "/notifications/"+id[0]+"/read", "ticket_id="+strconv.FormatInt(ticket.ID, 10), nil); rec.Code != http.StatusSeeOther {
		t.Errorf("read of an unreachable-ticket row = %d, want 303", rec.Code)
	}
	if n := unreadNotificationRows(t, h); n != 0 {
		t.Errorf("unread rows = %d, want 0", n)
	}
}
