package httpadapter

// Independent bounded NEW Bell regressions, legacy half (issue #419, T5m.5: S3,
// S11, S12, S13; owner authorization L155).
//
// See independent_bell_checkpoint_test.go for why these are NEW tests: the
// original probe artifacts were lost (L154) and the owner authorized a bounded
// reconstruction (L155). This file is the counterpart probe — the legacy public
// surface the Bell slice had to leave exactly as it was.
//
// A committed test can never read git history, so "before" here is not a
// checkout of the base commit: it is the FROZEN contract the pre-existing ticket
// notification flows owe, arranged through the real services over the real
// file-backed database with a literal injected clock, and asserted through the
// public routes only. The six observations below are recorded once each
// (method, path, status, EVERY header, whole body) as one machine-readable line,
// so a later run of the same frozen fixtures can be diffed against this one; the
// summary line and every assertion are the durable part.
//
// What must not change: the bell count endpoint answers a fragment; the panel
// answers a document or a drawer fragment on the same URL, keyed by HX-Request;
// a ticket row keeps its number, title, actor and unread marker; reading a row
// marks it read and sends the reader to the ticket route it names; and the
// ticket route itself still renders for the recipient.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// independentBellLegacyObservation is ONE recorded public response: the request
// that produced it, its status, every header it answered, and its whole body.
type independentBellLegacyObservation struct {
	Name    string              `json:"name"`
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body"`
}

// independentBellLegacyRecord appends one observation and writes it to the test
// output as a single JSON line. Recording is not an assertion: the assertions
// live at each call site, and this line is the operator's diff artifact for a
// later run.
func independentBellLegacyRecord(t *testing.T, recorded *[]independentBellLegacyObservation, name, method, path string, rec *httptest.ResponseRecorder) {
	t.Helper()
	observation := independentBellLegacyObservation{
		Name:    name,
		Method:  method,
		Path:    path,
		Status:  rec.Code,
		Headers: rec.Header(),
		Body:    rec.Body.String(),
	}
	*recorded = append(*recorded, observation)
	encoded, err := json.Marshal(observation)
	if err != nil {
		t.Fatalf("encode observation %q: %v", name, err)
	}
	fmt.Println("OBS " + string(encoded))
}

// TestIndependentBellLegacyPublic re-proves the six legacy public flows the Bell
// slice had to preserve: bell count, full panel, drawer fragment, reading one
// row, the panel after that read, and the ticket route the row opens.
func TestIndependentBellLegacyPublic(t *testing.T) {
	// A literal instant, so day groups and card times never depend on wall clock.
	oldNow := fixedNow
	fixedNow = time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	defer func() { fixedNow = oldNow }()

	h := newHarness(t)
	// A recipient with exactly one legacy notification: a ticket assigned to it
	// through the audited assign path.
	recipient := h.createUser(t, "Legacy Recipient", "legacy-recipient@tkt.test", "Secret123!42")
	session := seedSession(t, h.store, recipient.ID).ID
	ticket := h.seedTicket(t, "Printer on the third floor", nil)
	h.assignTicket(t, ticket.ID, recipient.ID)
	ticketID := strconv.FormatInt(ticket.ID, 10)

	var recorded []independentBellLegacyObservation

	// (1) The bell's own count endpoint: a fragment with the exact badge.
	count := notificationRequestAs(t, h, session, http.MethodGet, "/notifications/count", "")
	independentBellLegacyRecord(t, &recorded, "bell-count", http.MethodGet, "/notifications/count", count)
	if count.Code != http.StatusOK {
		t.Fatalf("GET /notifications/count = %d, want 200: %s", count.Code, count.Body.String())
	}
	if got := count.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("count Content-Type = %q", got)
	}
	if got := count.Header().Get("Vary"); got != "" {
		t.Errorf("count Vary = %q, want none: the count endpoint serves one representation", got)
	}
	countBody := count.Body.String()
	if strings.Contains(countBody, "<!DOCTYPE") {
		t.Errorf("the count endpoint rendered a document: %.600s", countBody)
	}
	mustContain(t, countBody,
		`aria-label="Notifications, 1 unread"`,
		`<span class="bell-badge" aria-hidden="true">1</span>`,
	)

	// (2) The full panel: a document, one ticket row, unread.
	full := notificationRequestAs(t, h, session, http.MethodGet, "/notifications", "")
	independentBellLegacyRecord(t, &recorded, "panel-full", http.MethodGet, "/notifications", full)
	if full.Code != http.StatusOK {
		t.Fatalf("GET /notifications = %d, want 200: %s", full.Code, full.Body.String())
	}
	if got := full.Header().Get("Vary"); got != "HX-Request" {
		t.Errorf("panel Vary = %q, want HX-Request", got)
	}
	if got := full.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("panel Content-Type = %q", got)
	}
	if got := full.Header().Get("HX-Trigger-After-Swap"); got != "" {
		t.Errorf("a non-HX panel announced %q; the refresh event belongs to the fragment", got)
	}
	fullBody := full.Body.String()
	if !strings.Contains(fullBody, "<!DOCTYPE") {
		t.Errorf("the non-HX panel is not a document: %.600s", fullBody)
	}
	mustContain(t, fullBody,
		`<h1 class="command-title">Notifications</h1>`,
		"TKT-1 · Printer on the third floor",
		"Assigned to you",
		"Admin · ",
		`class="notification-unread"`,
		`action="/notifications/read-all"`,
	)
	for _, offShape := range []string{"is waiting for approval", "Open Users › Pending", "Ticket no longer available"} {
		if strings.Contains(fullBody, offShape) {
			t.Errorf("a ticket row borrowed the account shape %q: %.2000s", offShape, fullBody)
		}
	}

	// (3) The same URL as the drawer fragment: no document, the same row, and the
	// refresh event the bell listens for.
	fragment := notificationPanelAs(t, h, session, true)
	independentBellLegacyRecord(t, &recorded, "panel-fragment", http.MethodGet, "/notifications", fragment)
	if fragment.Code != http.StatusOK {
		t.Fatalf("GET /notifications (HX) = %d, want 200: %s", fragment.Code, fragment.Body.String())
	}
	if got := fragment.Header().Get("Vary"); got != "HX-Request" {
		t.Errorf("fragment Vary = %q, want HX-Request", got)
	}
	if got := fragment.Header().Get("HX-Trigger-After-Swap"); got != "notifications:changed" {
		t.Errorf("fragment HX-Trigger-After-Swap = %q, want notifications:changed", got)
	}
	fragmentBody := fragment.Body.String()
	if strings.Contains(fragmentBody, "<!DOCTYPE") {
		t.Errorf("the HX panel rendered a document: %.600s", fragmentBody)
	}
	mustContain(t, fragmentBody, "TKT-1 · Printer on the third floor", "Assigned to you", `class="notification-unread"`)
	ids := notificationRowIDs(t, fragmentBody)
	if len(ids) != 1 {
		t.Fatalf("fragment row ids = %v, want the one assigned-ticket row", ids)
	}

	// (4) Reading the row: a POST that marks it read and sends the reader to the
	// ticket the row names.
	read := notificationRequestAs(t, h, session, http.MethodPost, "/notifications/"+ids[0]+"/read", url.Values{"ticket_id": {ticketID}}.Encode())
	independentBellLegacyRecord(t, &recorded, "read-one", http.MethodPost, "/notifications/"+ids[0]+"/read", read)
	wantRedirect(t, read, http.StatusSeeOther, "/tickets/"+ticketID)
	if read.Body.Len() != 0 {
		t.Errorf("the read redirect carried a body: %q", read.Body.String())
	}

	// (5) The panel after the read: the row is still the recipient's, now read,
	// and "mark all as read" is no longer offered because nothing is unread.
	afterRead := notificationRequestAs(t, h, session, http.MethodGet, "/notifications", "")
	independentBellLegacyRecord(t, &recorded, "panel-after-read", http.MethodGet, "/notifications", afterRead)
	if afterRead.Code != http.StatusOK {
		t.Fatalf("GET /notifications after reading = %d, want 200: %s", afterRead.Code, afterRead.Body.String())
	}
	if got := afterRead.Header().Get("Vary"); got != "HX-Request" {
		t.Errorf("panel Vary after the read = %q, want HX-Request", got)
	}
	afterReadBody := afterRead.Body.String()
	mustContain(t, afterReadBody, "TKT-1 · Printer on the third floor", "Assigned to you")
	for _, gone := range []string{`class="notification-unread"`, `action="/notifications/read-all"`} {
		if strings.Contains(afterReadBody, gone) {
			t.Errorf("the read row still renders %q: %.2000s", gone, afterReadBody)
		}
	}
	if got := notificationRowIDs(t, afterReadBody); len(got) != 1 || got[0] != ids[0] {
		t.Errorf("row ids after the read = %v, want the same row %v", got, ids)
	}

	// (6) The ticket route the row opened: it still renders for its recipient.
	detail := notificationRequestAs(t, h, session, http.MethodGet, "/tickets/"+ticketID, "")
	independentBellLegacyRecord(t, &recorded, "ticket-detail", http.MethodGet, "/tickets/"+ticketID, detail)
	if detail.Code != http.StatusOK {
		t.Fatalf("GET /tickets/%s = %d, want 200: %s", ticketID, detail.Code, detail.Body.String())
	}
	if got := detail.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("ticket detail Content-Type = %q", got)
	}
	if got := detail.Header().Get("Location"); got != "" {
		t.Errorf("ticket detail answered a Location %q; the recipient still opens it", got)
	}
	// The recipient is an agent who may edit this ticket, so the detail route
	// renders its EDITABLE title block: web/templates/partials/ticket_detail.html
	// (byte-identical at HEAD ce65322 and in this candidate) answers
	// `{{if and .CanEdit (not .Closed)}}` with a visually-hidden h1 plus the title
	// input, and keeps `<h1 class="command-title">` for the read-only branch. The
	// frozen run of this fixture observed exactly that shape, so the assertion
	// below pins the title and the ticket number the recipient actually gets
	// instead of the other branch's markup.
	mustContain(t, detail.Body.String(),
		`<div id="ticket-detail"`,
		`<span class="num">TKT-1</span>`,
		`<h1 class="visually-hidden">Printer on the third floor</h1>`,
		`value="Printer on the third floor" required`,
	)

	// The six observations are the frozen record of this slice's legacy surface.
	if len(recorded) != 6 {
		t.Fatalf("recorded %d observations, want the six frozen ones", len(recorded))
	}
	encoded, err := json.Marshal(recorded)
	if err != nil {
		t.Fatalf("encode the recorded observations: %v", err)
	}
	bodyBytes := 0
	for _, observation := range recorded {
		bodyBytes += len(observation.Body)
	}
	sum := sha256.Sum256(encoded)
	fmt.Printf("OBS-RECORD observations=%d bodies-bytes=%d sha256=%s\n", len(recorded), bodyBytes, hex.EncodeToString(sum[:]))
}
