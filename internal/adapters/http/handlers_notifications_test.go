package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/internal/i18n"
)

// The notification center's HTTP surface (issue #344, slice B4; specs S13-S16).
// Every assertion goes through the real routes, the real services, and the real
// file-backed database, and reads the PUBLIC interface — the count endpoint, the
// panel, the read routes — never the notifications table.

// notificationRequestAs runs one request as the session's user. The harness
// helpers all speak as the seeded admin; a notification is addressed to ONE
// recipient, so the tests have to act as that recipient.
func notificationRequestAs(t *testing.T, h *harness, sessionID, method, target string, form string) *httptest.ResponseRecorder {
	t.Helper()
	var body *strings.Reader
	if form == "" {
		body = strings.NewReader("")
	} else {
		body = strings.NewReader(form)
	}
	req := httptest.NewRequest(method, target, body)
	req.Header.Set("Cookie", sessionCookie+"="+sessionID)
	if form != "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)
	return rec
}

// notificationCountAs reads the bell's count fragment as one user.
func notificationCountAs(t *testing.T, h *harness, sessionID string) string {
	t.Helper()
	rec := notificationRequestAs(t, h, sessionID, http.MethodGet, "/notifications/count", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /notifications/count = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// assignedNotificationFixture assigns a ticket to a fresh agent and returns
// that agent plus a live session for them: the one arrangement that produces an
// "assigned" notification through the real ticket service.
func assignedNotificationFixture(t *testing.T, h *harness) (agentUserID int64, sessionID string) {
	t.Helper()
	agent := h.createUser(t, "Notify Agent", "notify-agent@tkt.test", "Secret123!42")
	ticket := h.seedTicket(t, "Assign me", nil)
	h.assignTicket(t, ticket.ID, agent.ID)
	return agent.ID, seedSession(t, h.store, agent.ID).ID
}

// TestNotificationsCountRendersTheUnreadBadge is S13's markup half: the bell's
// own endpoint answers a fragment carrying the visible count and an accessible
// name that speaks it ("Notifications, 1 unread"), for every signed-in role.
func TestNotificationsCountRendersTheUnreadBadge(t *testing.T) {
	h := newHarness(t)
	_, session := assignedNotificationFixture(t, h)

	body := notificationCountAs(t, h, session)

	for _, want := range []string{
		`href="/notifications"`,
		`aria-label="Notifications, 1 unread"`,
		`<span class="bell-badge" aria-hidden="true">1</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("count fragment missing %q: %s", want, body)
		}
	}
	// The count endpoint answers a fragment, never a full document.
	if strings.Contains(body, "<!DOCTYPE html>") || strings.Contains(body, `class="sidebar"`) {
		t.Errorf("the count endpoint rendered a document instead of a fragment: %s", body)
	}
}

// TestNotificationsCountHidesTheBadgeAtZero covers the rest of S13's markup
// rule: nothing unread means no badge at all, and the accessible name is the
// plain one.
func TestNotificationsCountHidesTheBadgeAtZero(t *testing.T) {
	h := newHarness(t)

	body := notificationCountAs(t, h, h.adminSession.ID)

	if strings.Contains(body, "bell-badge") {
		t.Errorf("a user with nothing unread still renders a badge: %s", body)
	}
	if !strings.Contains(body, `aria-label="Notifications"`) {
		t.Errorf("the zero-count bell lost its accessible name: %s", body)
	}
	// The actor is never notified about their own action, so the admin who did
	// the assigning has an empty center (S10).
	if strings.Contains(body, "unread") {
		t.Errorf("the actor sees an unread count for their own action: %s", body)
	}
}

// TestNotificationsCountIsScopedToOneRecipient proves the count is the
// recipient's own: another user's unread rows never reach it.
func TestNotificationsCountIsScopedToOneRecipient(t *testing.T) {
	h := newHarness(t)
	_, recipientSession := assignedNotificationFixture(t, h)
	bystander := h.createUser(t, "Bystander", "bystander@tkt.test", "Secret123!42")
	bystanderSession := seedSession(t, h.store, bystander.ID).ID

	if body := notificationCountAs(t, h, recipientSession); !strings.Contains(body, "1 unread") {
		t.Fatalf("the recipient's count = %s, want 1 unread", body)
	}
	if body := notificationCountAs(t, h, bystanderSession); strings.Contains(body, "unread") {
		t.Errorf("a bystander sees someone else's unread count: %s", body)
	}
}

// TestNotificationsCountRequiresASession proves the count endpoint is not a
// public read: an anonymous request never learns a count.
func TestNotificationsCountRequiresASession(t *testing.T) {
	h := newHarness(t)
	assignedNotificationFixture(t, h)

	req := httptest.NewRequest(http.MethodGet, "/notifications/count", nil)
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("anonymous count request = %d %q, want a 303 to /login", rec.Code, rec.Header().Get("Location"))
	}
}

func TestBaseRailLoadsTheBellCountFromItsOwnEndpoint(t *testing.T) {
	h := newHarness(t)

	body := h.get(t, "/tickets", false).Body.String()

	for _, want := range []string{
		`id="notification-bell"`,
		`hx-get="/notifications/count"`,
		`hx-trigger="load, every 45s [document.visibilityState === 'visible'], notifications:changed from:body"`,
		`hx-target="this"`,
		`hx-swap="innerHTML"`,
		`href="/notifications"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("top bar bell missing %q", want)
		}
	}
	// Opening the panel is an HTMX link to the panel fragment; both live on the
	// same anchor so the accessible name and the click behavior cannot drift.
	if !strings.Contains(body, `hx-get="/notifications"`) {
		t.Errorf("the bell does not open the panel through its own route: %s", body)
	}
}

// --- The panel (S14) ---------------------------------------------------------

// notificationPanelAs reads the panel as one user.
func notificationPanelAs(t *testing.T, h *harness, sessionID string, hx bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/notifications", nil)
	req.Header.Set("Cookie", sessionCookie+"="+sessionID)
	if hx {
		req.Header.Set("HX-Request", "true")
	}
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)
	return rec
}

// TestNotificationsPanelVariesOnHXRequest pins the caching contract of the ONE
// URL that serves two representations (S13/S14): GET /notifications answers the
// full page or the drawer fragment depending on HX-Request, so anything caching
// it has to key on that header — the same contract the users and categories
// routes already publish. The two representations themselves are pinned by the
// panel tests below; this one only adds the header and proves neither form was
// traded for the other.
func TestNotificationsPanelVariesOnHXRequest(t *testing.T) {
	h := newHarness(t)
	_, session := assignedNotificationFixture(t, h)

	for _, tc := range []struct {
		name     string
		hx       bool
		document bool
	}{
		{name: "drawer fragment", hx: true, document: false},
		{name: "full page", hx: false, document: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := notificationPanelAs(t, h, session, tc.hx)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET /notifications = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Vary"); got != "HX-Request" {
				t.Errorf("Vary = %q, want HX-Request", got)
			}
			if got := strings.Contains(rec.Body.String(), "<!DOCTYPE"); got != tc.document {
				t.Errorf("response carries a full document = %v, want %v", got, tc.document)
			}
		})
	}
}

// notificationRowIDs returns the ids the panel offers to open, in rendered
// order, so a test can assert the newest-first order without reading storage.
func notificationRowIDs(t *testing.T, body string) []string {
	t.Helper()
	var ids []string
	for _, chunk := range strings.Split(body, `action="/notifications/`)[1:] {
		id, rest, ok := strings.Cut(chunk, "/read")
		if !ok || rest == "" {
			continue
		}
		if _, err := strconv.ParseInt(id, 10, 64); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

// TestNotificationsPanelListsTheNewestFirst is S14's list: every row names the
// ticket, what happened, who did it and when, unread rows are marked, and the
// newest row comes first.
func TestNotificationsPanelListsTheNewestFirst(t *testing.T) {
	h := newHarness(t)
	agent := h.createUser(t, "Panel Agent", "panel-agent@tkt.test", "Secret123!42")
	first := h.seedTicket(t, "First assignment", nil)
	h.assignTicket(t, first.ID, agent.ID)
	second := h.seedTicket(t, "Second assignment", nil)
	h.assignTicket(t, second.ID, agent.ID)
	session := seedSession(t, h.store, agent.ID).ID

	rec := notificationPanelAs(t, h, session, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /notifications = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`<h1 class="command-title">Notifications</h1>`,
		"TKT-1 · First assignment",
		"TKT-2 · Second assignment",
		"Assigned to you",
		"Panel Agent",
		"Admin",
		`class="notification-unread"`,
		"Mark all as read",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("panel missing %q: %.2000s", want, body)
		}
	}
	if ids := notificationRowIDs(t, body); len(ids) != 2 || ids[0] == ids[1] {
		t.Errorf("panel row ids = %v, want two distinct rows", ids)
	} else if strings.Index(body, "TKT-2") > strings.Index(body, "TKT-1") {
		t.Errorf("the newest notification is not first: %.2000s", body)
	}
}

// TestNotificationsPanelFragmentIsTheBellDrawer proves the same route answers
// the drawer fragment the bell swaps in: a dialog with its own heading, never a
// second document.
func TestNotificationsPanelFragmentIsTheBellDrawer(t *testing.T) {
	h := newHarness(t)
	_, session := assignedNotificationFixture(t, h)

	rec := notificationPanelAs(t, h, session, true)

	if rec.Code != http.StatusOK {
		t.Fatalf("HX GET /notifications = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`role="dialog"`,
		`aria-modal="true"`,
		`aria-labelledby="notifications-panel-title"`,
		`id="notifications-panel-title"`,
		`data-notifications-close`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("panel fragment missing %q: %.2000s", want, body)
		}
	}
	if strings.Contains(body, "<!DOCTYPE html>") || strings.Contains(body, `class="sidebar"`) {
		t.Errorf("the drawer fragment rendered a whole document: %.2000s", body)
	}
	// Opening the panel refreshes the bell without a page render: the count keeps
	// coming from its own endpoint (S13).
	if got := rec.Header().Get("HX-Trigger-After-Swap"); got != "notifications:changed" {
		t.Errorf("panel fragment trigger = %q, want the bell refresh event", got)
	}
}

// TestNotificationsPanelGroupsByDay is S14's grouping: today, yesterday, and the
// date of anything older, in the instance language.
func TestNotificationsPanelGroupsByDay(t *testing.T) {
	h := newHarness(t)
	agent := h.createUser(t, "Day Agent", "day-agent@tkt.test", "Secret123!42")
	session := seedSession(t, h.store, agent.ID).ID
	older := h.seedTicket(t, "Older assignment", nil)
	h.assignTicket(t, older.ID, agent.ID)
	yesterday := h.seedTicket(t, "Yesterday assignment", nil)
	h.assignTicket(t, yesterday.ID, agent.ID)
	today := h.seedTicket(t, "Today assignment", nil)
	h.assignTicket(t, today.ID, agent.ID)
	raw := h.rawDB(t)
	if _, err := raw.Exec(`UPDATE notifications SET created_at = ? WHERE ticket_id = ?`, fixedNow.UTC().AddDate(0, 0, -3).Format(time.RFC3339), older.ID); err != nil {
		t.Fatalf("age notification: %v", err)
	}
	if _, err := raw.Exec(`UPDATE notifications SET created_at = ? WHERE ticket_id = ?`, fixedNow.UTC().AddDate(0, 0, -1).Format(time.RFC3339), yesterday.ID); err != nil {
		t.Fatalf("age notification: %v", err)
	}

	body := notificationPanelAs(t, h, session, false).Body.String()

	for _, want := range []string{
		`>Today</h3>`,
		`>Yesterday</h3>`,
		formatMonthDayYear(i18n.New(i18n.Default), fixedNow.UTC().AddDate(0, 0, -3)),
	} {
		if !strings.Contains(body, want) {
			t.Errorf("panel missing the day group %q: %.2000s", want, body)
		}
	}
	todayAt := strings.Index(body, ">Today</h3>")
	yesterdayAt := strings.Index(body, ">Yesterday</h3>")
	dateAt := strings.Index(body, formatMonthDayYear(i18n.New(i18n.Default), fixedNow.UTC().AddDate(0, 0, -3)))
	if !(todayAt < yesterdayAt && yesterdayAt < dateAt) {
		t.Errorf("day groups out of order (today %d, yesterday %d, date %d)", todayAt, yesterdayAt, dateAt)
	}
}

// TestNotificationsPanelEmptyState covers the copy a fresh account reads.
func TestNotificationsPanelEmptyState(t *testing.T) {
	h := newHarness(t)

	body := notificationPanelAs(t, h, h.adminSession.ID, false).Body.String()

	for _, want := range []string{"No notifications yet", "When something happens on a ticket you can see, it shows up here."} {
		if !strings.Contains(body, want) {
			t.Errorf("panel empty state missing %q: %.1200s", want, body)
		}
	}
	if strings.Contains(body, "Mark all as read") {
		t.Errorf("an empty panel offers to mark nothing read: %.1200s", body)
	}
}

// TestNotificationsMarkOneReadOpensTheTicket is S14's click: a POST marks the
// row read and answers a redirect to the ticket, never a GET that writes.
func TestNotificationsMarkOneReadOpensTheTicket(t *testing.T) {
	h := newHarness(t)
	agent := h.createUser(t, "Click Agent", "click-agent@tkt.test", "Secret123!42")
	ticket := h.seedTicket(t, "Clickable assignment", nil)
	h.assignTicket(t, ticket.ID, agent.ID)
	session := seedSession(t, h.store, agent.ID).ID
	id := notificationRowIDs(t, notificationPanelAs(t, h, session, false).Body.String())[0]

	// Reading is a write and never a GET: the read route only answers POST, so a
	// GET on it leaves the badge exactly as it was.
	notificationRequestAs(t, h, session, http.MethodGet, "/notifications/"+id+"/read", "")
	if body := notificationCountAs(t, h, session); !strings.Contains(body, "1 unread") {
		t.Errorf("a GET cleared the notification: %s", body)
	}
	rec := notificationRequestAs(t, h, session, http.MethodPost, "/notifications/"+id+"/read", "ticket_id="+strconv.FormatInt(ticket.ID, 10))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/tickets/"+strconv.FormatInt(ticket.ID, 10) {
		t.Fatalf("POST read = %d %q, want a 303 to the ticket", rec.Code, rec.Header().Get("Location"))
	}
	if body := notificationCountAs(t, h, session); strings.Contains(body, "unread") {
		t.Errorf("the badge survived the read: %s", body)
	}
	if strings.Contains(notificationPanelAs(t, h, session, false).Body.String(), `class="notification-unread"`) {
		t.Errorf("the row is still marked unread after being read")
	}
	// The redirect target is a real page for this reader.
	detail := notificationRequestAs(t, h, session, http.MethodGet, "/tickets/"+strconv.FormatInt(ticket.ID, 10), "")
	if detail.Code != http.StatusOK {
		t.Errorf("the ticket the notification opened = %d, want 200", detail.Code)
	}
}

// TestNotificationsMarkAllReadClearsTheBadge is S14's "Mark all as read".
func TestNotificationsMarkAllReadClearsTheBadge(t *testing.T) {
	h := newHarness(t)
	agent := h.createUser(t, "Bulk Agent", "bulk-agent@tkt.test", "Secret123!42")
	for _, title := range []string{"Bulk one", "Bulk two"} {
		ticket := h.seedTicket(t, title, nil)
		h.assignTicket(t, ticket.ID, agent.ID)
	}
	session := seedSession(t, h.store, agent.ID).ID
	if body := notificationCountAs(t, h, session); !strings.Contains(body, "2 unread") {
		t.Fatalf("count before mark-all = %s, want 2 unread", body)
	}

	rec := notificationRequestAs(t, h, session, http.MethodPost, "/notifications/read-all", "")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/notifications" {
		t.Fatalf("POST read-all = %d %q, want a 303 to the panel", rec.Code, rec.Header().Get("Location"))
	}
	if body := notificationCountAs(t, h, session); strings.Contains(body, "unread") {
		t.Errorf("the badge survived mark-all: %s", body)
	}
	if strings.Contains(notificationPanelAs(t, h, session, false).Body.String(), `class="notification-unread"`) {
		t.Errorf("rows are still marked unread after mark-all")
	}
}

// TestNotificationsWritesAreScopedToOneRecipient is the write half of S16: an id
// that is not yours is indistinguishable from a missing one, and nothing moves.
func TestNotificationsWritesAreScopedToOneRecipient(t *testing.T) {
	h := newHarness(t)
	agent := h.createUser(t, "Owner Agent", "owner-agent@tkt.test", "Secret123!42")
	ticket := h.seedTicket(t, "Owned assignment", nil)
	h.assignTicket(t, ticket.ID, agent.ID)
	ownerSession := seedSession(t, h.store, agent.ID).ID
	bystander := h.createUser(t, "Other Agent", "other-agent@tkt.test", "Secret123!42")
	bystanderSession := seedSession(t, h.store, bystander.ID).ID
	id := notificationRowIDs(t, notificationPanelAs(t, h, ownerSession, false).Body.String())[0]
	// The panel lists the reader's own rows only (the store scopes every read).
	if body := notificationPanelAs(t, h, bystanderSession, false).Body.String(); strings.Contains(body, "Owned assignment") {
		t.Errorf("a bystander's panel shows someone else's notification: %.1200s", body)
	}

	rec := notificationRequestAs(t, h, bystanderSession, http.MethodPost, "/notifications/"+id+"/read", "ticket_id="+strconv.FormatInt(ticket.ID, 10))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("reading someone else's notification = %d, want 404", rec.Code)
	}
	if body := notificationCountAs(t, h, ownerSession); !strings.Contains(body, "1 unread") {
		t.Errorf("another user's read changed the owner's badge: %s", body)
	}
	// Mark-all is scoped too: the bystander's own (empty) center changes nothing
	// for the owner.
	notificationRequestAs(t, h, bystanderSession, http.MethodPost, "/notifications/read-all", "")
	if body := notificationCountAs(t, h, ownerSession); !strings.Contains(body, "1 unread") {
		t.Errorf("another user's mark-all changed the owner's badge: %s", body)
	}
}

// TestNotificationForAnUnreachableTicketStaysReadable is S16's opening rule: a
// notification for a ticket the reader can no longer open still marks read, and
// the ticket itself answers the ordinary access-denied path.
func TestNotificationForAnUnreachableTicketStaysReadable(t *testing.T) {
	h := newHarness(t)
	reader := h.createUser(t, "Moved Agent", "moved-agent@tkt.test", "Secret123!42")
	ticket := h.seedTicket(t, "Reassigned away", nil)
	h.assignTicket(t, ticket.ID, reader.ID)
	session := seedSession(t, h.store, reader.ID).ID
	id := notificationRowIDs(t, notificationPanelAs(t, h, session, false).Body.String())[0]
	// The ticket is handed to someone else: the first agent's read scope no longer
	// covers it (an agent sees their own assignments and their desk's claimable
	// tickets; this one is assigned elsewhere).
	other := h.createUser(t, "New Owner", "new-owner@tkt.test", "Secret123!42")
	if _, err := h.tickets.Assign(t.Context(), *h.admin, ticket.ID, &other.ID, "handing over"); err != nil {
		t.Fatalf("reassign the ticket: %v", err)
	}

	panel := notificationPanelAs(t, h, session, false).Body.String()
	if !strings.Contains(panel, "Ticket no longer available") {
		t.Errorf("a row for an unreachable ticket must say so: %.2000s", panel)
	}
	if strings.Contains(panel, "Reassigned away") {
		t.Errorf("the panel repeated the title of a ticket the reader can no longer open: %.2000s", panel)
	}
	detail := notificationRequestAs(t, h, session, http.MethodGet, "/tickets/"+strconv.FormatInt(ticket.ID, 10), "")
	if detail.Code != http.StatusNotFound {
		t.Fatalf("the detail of an out-of-scope ticket = %d, want 404", detail.Code)
	}

	rec := notificationRequestAs(t, h, session, http.MethodPost, "/notifications/"+id+"/read", "ticket_id="+strconv.FormatInt(ticket.ID, 10))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("reading a notification for an unreachable ticket = %d, want a 303", rec.Code)
	}
	if body := notificationCountAs(t, h, session); strings.Contains(body, "unread") {
		t.Errorf("the row came back unread after a failed open: %s", body)
	}
}

// TestRequesterNeverSeesAnInternalCommentNotification is S16's visibility rule
// asserted at the HTTP level: the requester's panel holds the public comment's
// row and never the internal one, and the mention of an internal comment appears
// nowhere in it.
func TestRequesterNeverSeesAnInternalCommentNotification(t *testing.T) {
	h := newHarness(t)
	requester := seedUserRole(t, h.store, "Requester", "requester-notify@tkt.test", domain.RoleUser)
	ticket := seedUserOwnedTicket(t, h, requester, "Visibility case")
	requesterSession := seedSession(t, h.store, requester.ID).ID
	h.postForm(t, "/tickets/"+strconv.FormatInt(ticket.ID, 10)+"/comments", url.Values{"body": {"public answer"}, "visibility": {"public"}}, false)
	h.postForm(t, "/tickets/"+strconv.FormatInt(ticket.ID, 10)+"/comments", url.Values{"body": {"internal note"}, "visibility": {"internal"}}, false)

	body := notificationPanelAs(t, h, requesterSession, false).Body.String()

	if !strings.Contains(body, "New comment") {
		t.Errorf("the requester never learned about the public comment: %.2000s", body)
	}
	for _, leaked := range []string{"New internal comment", "internal note", "comment_internal"} {
		if strings.Contains(body, leaked) {
			t.Errorf("the requester's panel revealed the internal comment (%q): %.2000s", leaked, body)
		}
	}
	if count := notificationCountAs(t, h, requesterSession); !strings.Contains(count, "1 unread") {
		t.Errorf("the requester's badge = %s, want exactly the public comment", count)
	}
}

// TestNotificationsPanelSpeaksTheInstanceLanguage is S15 end to end: the copy,
// the state and priority labels inside the sentence, the unread marker, and the
// bell's accessible name all follow the instance language.
func TestNotificationsPanelSpeaksTheInstanceLanguage(t *testing.T) {
	h := newHarness(t)
	if err := h.settings.SetInterfaceLanguage(t.Context(), *h.admin, application.InterfaceLanguageSpanish); err != nil {
		t.Fatalf("set instance language: %v", err)
	}
	agent := h.createUser(t, "Agente Español", "agente-es@tkt.test", "Secret123!42")
	ticket := h.seedTicket(t, "Asignación en español", nil)
	h.assignTicket(t, ticket.ID, agent.ID)
	h.seedTransition(t, ticket.ID, domain.StateInProgress, "")
	session := seedSession(t, h.store, agent.ID).ID

	body := notificationPanelAs(t, h, session, false).Body.String()
	for _, want := range []string{
		"Se te asignó el ticket",
		"El estado cambió de Nuevo a En curso",
		"Sin leer",
		"Marcar todo como leído",
		"Notificaciones",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Spanish panel missing %q: %.2000s", want, body)
		}
	}
	if count := notificationCountAs(t, h, session); !strings.Contains(count, `aria-label="Notificaciones, 2 sin leer"`) {
		t.Errorf("Spanish bell accessible name = %s", count)
	}
}

// TestNotificationDrawerScriptIsServed proves the shell's script tag resolves.
// An asset the static register does not name answers the catch-all redirect with
// an HTML body, which the browser then tries to parse as JavaScript — the exact
// page error this route prevents.
func TestNotificationDrawerScriptIsServed(t *testing.T) {
	h := newHarness(t)

	rec := h.get(t, "/static/notifications.js", false)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /static/notifications.js = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("Content-Type = %q, want JavaScript", ct)
	}
	if !strings.Contains(rec.Body.String(), "notification-panel-host") {
		t.Errorf("the served script is not the drawer's: %.400s", rec.Body.String())
	}
}

// TestNotificationsHTMXWithoutASessionRedirectsInsteadOfSwappingLoginMarkup is
// the polling half of the bell's session contract (S13/S14). The bell polls
// GET /notifications/count with htmx and the panel opens with htmx too, so a
// request whose session is gone must NOT be answered with the 303 the session
// middleware gives an ordinary navigation: htmx follows that redirect inside
// the swap and pours the whole login document into the bell. The notification
// routes therefore answer such a request with HX-Redirect to the login page, so
// htmx navigates the way a normal click would, and with no body to swap. An
// ordinary navigation to the same URL keeps the 303 it has always answered.
func TestNotificationsHTMXWithoutASessionRedirectsInsteadOfSwappingLoginMarkup(t *testing.T) {
	h := newHarness(t)
	assignedNotificationFixture(t, h)

	noSession := map[string]string{}
	unknownSession := map[string]string{sessionCookie: "0123456789abcdef0123456789abcdef"}
	for _, tc := range []struct {
		name    string
		cookies map[string]string
	}{
		{name: "no session cookie", cookies: noSession},
		{name: "unknown session token", cookies: unknownSession},
	} {
		for _, target := range []string{"/notifications/count", "/notifications"} {
			t.Run(tc.name+" HTMX "+target, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodGet, target, nil)
				for k, v := range tc.cookies {
					req.Header.Set("Cookie", k+"="+v)
				}
				req.Header.Set("HX-Request", "true")
				rec := httptest.NewRecorder()
				h.mw.Wrap(h.mux).ServeHTTP(rec, req)

				if got := rec.Header().Get("HX-Redirect"); got != "/login" {
					t.Errorf("HX-Redirect = %q, want /login (status %d)", got, rec.Code)
				}
				if rec.Code != http.StatusNoContent {
					t.Errorf("status = %d, want 204: htmx has nothing to swap", rec.Code)
				}
				if body := rec.Body.String(); body != "" {
					t.Errorf("a redirect answer carries a body to swap into the bell: %.400s", body)
				}
			})
		}

		// An ordinary navigation is untouched: it still lands on the login page
		// through the 303 the middleware has always answered.
		t.Run(tc.name+" ordinary /notifications", func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/notifications", nil)
			for k, v := range tc.cookies {
				req.Header.Set("Cookie", k+"="+v)
			}
			rec := httptest.NewRecorder()
			h.mw.Wrap(h.mux).ServeHTTP(rec, req)

			if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
				t.Fatalf("ordinary /notifications = %d %q, want a 303 to /login", rec.Code, rec.Header().Get("Location"))
			}
			if got := rec.Header().Get("HX-Redirect"); got != "" {
				t.Errorf("ordinary navigation returned HX-Redirect %q", got)
			}
		})
	}
}

// TestBaseRailLoadsTheBellCountFromItsOwnEndpoint is S13's wiring half: the
// rail renders the bell on every page and polls the count fragment from its own
// endpoint instead of computing it in the page data.
