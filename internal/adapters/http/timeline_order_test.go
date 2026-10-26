package httpadapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/internal/i18n"
)

// Ticket activity order (issue #438, S1): ONE per-user preference, set on
// Preferences (POST /preferences/timeline-order) and toggled from the Activity
// header (POST /tickets/{id}/timeline-order). Newest first is the default and
// puts the composer ABOVE Activity; oldest first puts it BELOW, as it always
// was.
//
// The journeys here own the PREFERENCE and the fragment route. The canonical DOM
// order of one feed (entries plus the composer's position) lives in
// handlers_detail_test.go, so neither file repeats the other's whole journey.

// detailRegions slices the rendered detail into the three regions this
// preference reorders: the page before the feed, the feed itself, and the
// composer form. A missing marker fails the test instead of comparing -1.
func detailRegions(t *testing.T, body string) (composerAt, activityAt int) {
	t.Helper()
	composerAt = strings.Index(body, `class="card comment-form"`)
	activityAt = strings.Index(body, `<div class="card detail-activity">`)
	if composerAt < 0 || activityAt < 0 {
		t.Fatalf("the detail must render the composer and the Activity card (composer=%d activity=%d): %.800s", composerAt, activityAt, body)
	}
	return composerAt, activityAt
}

// countPreferenceRows counts the stored rows of one preference key — a fact no
// store port exposes, read through rawDB exactly as the queue-order fixtures do.
func countPreferenceRows(t *testing.T, h *harness, userID int64, key string) int64 {
	t.Helper()
	return scanOneInt(t, h.rawDB(t), `SELECT COUNT(*) FROM user_preferences WHERE user_id = ? AND "key" = ?`, userID, key)
}

// TestTicketTimelineOrderHeaderToggle proves the Activity header control: it
// names the CURRENT order, it announces what submitting does, and it offers the
// OTHER order as the value it will save. Without that, one control would silently
// mean two different things on two tickets.
func TestTicketTimelineOrderHeaderToggle(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Order header", nil)
	path := ticketPath(tkt.ID)

	body := h.get(t, path, false).Body.String()
	for _, want := range []string{
		`<div class="detail-activity-head">`,
		`method="post" action="` + path + `/timeline-order"`,
		`hx-post="` + path + `/timeline-order" hx-target="#ticket-detail" hx-swap="outerHTML"`,
		`name="order" value="` + application.TimelineOrderOldest + `"`,
		`aria-label="` + i18n.New(i18n.Default).Tf("tickets.timeline.order.switch",
			i18n.T(i18n.Default, "preferences.timeline.newest"),
			i18n.T(i18n.Default, "preferences.timeline.oldest")) + `"`,
		`>` + i18n.T(i18n.Default, "preferences.timeline.newest") + `</button>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the Activity header must render %q, got: %.900s", want, body)
		}
	}
	// The control belongs to the Activity header, above the feed it reorders.
	if !appearsBefore(body, `class="detail-activity-head"`, `id="timeline"`) {
		t.Errorf("the order control must sit in the Activity header, got: %.900s", body)
	}

	// After choosing oldest first, the same control names the other order and
	// offers the way back.
	if err := h.store.TimelineOrderPreferences().SetTimelineOrder(context.Background(), h.admin.ID, application.TimelineOrderOldest); err != nil {
		t.Fatalf("seed oldest order: %v", err)
	}
	oldestBody := h.get(t, path, false).Body.String()
	for _, want := range []string{
		`name="order" value="` + application.TimelineOrderNewest + `"`,
		`>` + i18n.T(i18n.Default, "preferences.timeline.oldest") + `</button>`,
	} {
		if !strings.Contains(oldestBody, want) {
			t.Errorf("the toggle must offer the way back, missing %q in: %.900s", want, oldestBody)
		}
	}
}

// TestTicketTimelineOrderToggleSavesAndReorders proves the fragment route: one
// HTMX POST stores the actor's preference, re-renders the SAME #ticket-detail
// fragment (no main-frame navigation), and the composer moves relative to
// Activity. Toggling back restores both, so the control is a real switch and not
// a one-way write.
func TestTicketTimelineOrderToggleSavesAndReorders(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Order toggle", nil)
	path := ticketPath(tkt.ID)

	// The default: newest first, composer above the feed.
	defaultBody := h.get(t, path, false).Body.String()
	composerAt, activityAt := detailRegions(t, defaultBody)
	if composerAt > activityAt {
		t.Fatalf("newest first must put the composer above Activity, got composer=%d activity=%d", composerAt, activityAt)
	}
	if got := storedTimelineOrder(t, h, h.admin.ID); got != "" {
		t.Fatalf("reading the default detail stored %q, want no row", got)
	}

	// Choosing oldest first through the header.
	rec := h.postForm(t, path+"/timeline-order", url.Values{"order": {application.TimelineOrderOldest}}, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTMX POST timeline-order = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<div id="ticket-detail"`) {
		t.Errorf("the response must be the #ticket-detail fragment, got: %.900s", body)
	}
	if strings.Contains(body, "<!DOCTYPE html>") || strings.Contains(body, "<html") {
		t.Errorf("an HTMX response must never carry a whole document, got: %.400s", body)
	}
	if got := storedTimelineOrder(t, h, h.admin.ID); got != application.TimelineOrderOldest {
		t.Errorf("after toggling: stored order = %q, want %q", got, application.TimelineOrderOldest)
	}
	composerAt, activityAt = detailRegions(t, body)
	if composerAt < activityAt {
		t.Errorf("oldest first must put the composer BELOW Activity, got composer=%d activity=%d", composerAt, activityAt)
	}
	if !strings.Contains(body, `name="order" value="`+application.TimelineOrderNewest+`"`) {
		t.Errorf("the swapped toggle must offer the way back, got: %.900s", body)
	}

	// Toggling back: newest first again, composer above the feed.
	rec = h.postForm(t, path+"/timeline-order", url.Values{"order": {application.TimelineOrderNewest}}, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTMX POST timeline-order back = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := storedTimelineOrder(t, h, h.admin.ID); got != application.TimelineOrderNewest {
		t.Errorf("after toggling back: stored order = %q, want %q", got, application.TimelineOrderNewest)
	}
	composerAt, activityAt = detailRegions(t, rec.Body.String())
	if composerAt > activityAt {
		t.Errorf("newest first must put the composer above Activity again, got composer=%d activity=%d", composerAt, activityAt)
	}

	// The native fallback is a plain redirect back to the ticket, so the same
	// control works with JavaScript off.
	native := h.postForm(t, path+"/timeline-order", url.Values{"order": {application.TimelineOrderOldest}}, false)
	if native.Code != http.StatusSeeOther {
		t.Fatalf("native POST timeline-order = %d, want 303: %s", native.Code, native.Body.String())
	}
	if loc := native.Header().Get("Location"); loc != path {
		t.Errorf("native POST Location = %q, want %q", loc, path)
	}
}

// TestTicketTimelineOrderToggleRejectsInvalidValue proves the closed set at the
// fragment route: an out-of-set value answers 422 with the localized refusal,
// stores nothing, and leaves the previously saved preference (and the number of
// stored rows) exactly as they were.
func TestTicketTimelineOrderToggleRejectsInvalidValue(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Order refusal", nil)
	path := ticketPath(tkt.ID)
	ctx := context.Background()
	if err := h.store.TimelineOrderPreferences().SetTimelineOrder(ctx, h.admin.ID, application.TimelineOrderOldest); err != nil {
		t.Fatalf("seed oldest order: %v", err)
	}

	for _, value := range []string{"", "Newest", "newest ", "asc", "priority", "oldest first"} {
		rec := h.postForm(t, path+"/timeline-order", url.Values{"order": {value}}, true)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("HTMX POST order=%q = %d, want 422: %s", value, rec.Code, rec.Body.String())
			continue
		}
		if !strings.Contains(rec.Body.String(), i18n.T(i18n.Default, "error.invalid_timeline_order")) {
			t.Errorf("POST order=%q must carry %q, got: %.900s", value, i18n.T(i18n.Default, "error.invalid_timeline_order"), rec.Body.String())
		}
		if got := storedTimelineOrder(t, h, h.admin.ID); got != application.TimelineOrderOldest {
			t.Errorf("POST order=%q changed the stored order to %q, want %q untouched", value, got, application.TimelineOrderOldest)
		}
		if n := countPreferenceRows(t, h, h.admin.ID, "timeline_order"); n != 1 {
			t.Errorf("POST order=%q left %d timeline_order rows, want exactly 1", value, n)
		}
	}

	// A rejected submission from a user who never chose creates no row at all.
	agent := seedUserRole(t, h.store, "Order refusal agent", "order-refusal@tkt.test", domain.RoleAdmin)
	session := seedSession(t, h.store, agent.ID)
	rec := h.postFormAs(t, path+"/timeline-order", url.Values{"order": {"sideways"}}, session.ID)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("rejected POST as a fresh user = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	if n := countPreferenceRows(t, h, agent.ID, "timeline_order"); n != 0 {
		t.Errorf("a rejected submission stored %d rows for a user who never chose, want 0", n)
	}
}

// A failed ticket read must not commit the reader's personal preference, whether
// the row is absent or already stores a choice. Out-of-scope and missing tickets
// share the same refusal, without imposing a settings capability on the reader.
func TestTicketTimelineOrderUnreadableTicketDoesNotSave(t *testing.T) {
	for _, inaccessible := range []bool{false, true} {
		for _, initial := range []string{"", application.TimelineOrderOldest} {
			name := "missing/" + initial
			if inaccessible {
				name = "inaccessible/" + initial
			}
			t.Run(name, func(t *testing.T) {
				h := newHarness(t)
				actor := h.admin
				path := "/tickets/999999/timeline-order"
				if inaccessible {
					ticket := h.seedTicket(t, "Another person's ticket", nil)
					path = ticketPath(ticket.ID) + "/timeline-order"
					actor = seedUserRole(t, h.store, "Unrelated reader", "unrelated-order@tkt.test", domain.RoleUser)
				}
				if initial != "" {
					if err := h.store.TimelineOrderPreferences().SetTimelineOrder(t.Context(), actor.ID, initial); err != nil {
						t.Fatal(err)
					}
				}
				rowsBefore := countPreferenceRows(t, h, actor.ID, "timeline_order")
				order := application.TimelineOrderOldest
				if initial == order {
					order = application.TimelineOrderNewest
				}
				var rec *httptest.ResponseRecorder
				if inaccessible {
					session := seedSession(t, h.store, actor.ID)
					rec = h.postFormAs(t, path, url.Values{"order": {order}}, session.ID)
				} else {
					rec = h.postForm(t, path, url.Values{"order": {order}}, true)
				}
				if rec.Code != http.StatusNotFound || rec.Body.String() != "ticket not found\n" {
					t.Fatalf("failed read = %d %q, want 404 %q", rec.Code, rec.Body.String(), "ticket not found\n")
				}
				if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
					t.Errorf("Content-Type = %q", got)
				}
				if got := rec.Header().Get("Location"); got != "" {
					t.Errorf("unexpected redirect = %q", got)
				}
				if got := storedTimelineOrder(t, h, actor.ID); got != initial {
					t.Errorf("failed POST changed preference: %q -> %q", initial, got)
				}
				if got := countPreferenceRows(t, h, actor.ID, "timeline_order"); got != rowsBefore {
					t.Errorf("failed POST changed row count: %d -> %d", rowsBefore, got)
				}
			})
		}
	}
}

// TestTicketTimelineOrderIsPerUser proves the preference is the READER's own:
// two people opening the same ticket with different choices get two different
// feeds, and neither write touches the other.
func TestTicketTimelineOrderIsPerUser(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Order per user", nil)
	if _, err := h.comments.Add(context.Background(), *h.admin, tkt.ID, "older-order-token", "public"); err != nil {
		t.Fatalf("seed first comment: %v", err)
	}
	if _, err := h.comments.Add(context.Background(), *h.admin, tkt.ID, "newer-order-token", "public"); err != nil {
		t.Fatalf("seed second comment: %v", err)
	}
	// Two ADMINISTRATORS on purpose: the journey is about two READERS of one
	// ticket seeing two orders, and an admin scope is the one that can open a
	// ticket it neither requested nor is assigned to.
	ana := seedUserRole(t, h.store, "Ana Timeline", "ana-timeline@tkt.test", domain.RoleAdmin)
	bruno := seedUserRole(t, h.store, "Bruno Timeline", "bruno-timeline@tkt.test", domain.RoleAdmin)
	anaSession := seedSession(t, h.store, ana.ID)
	brunoSession := seedSession(t, h.store, bruno.ID)
	path := ticketPath(tkt.ID)

	if rec := h.postFormAs(t, path+"/timeline-order", url.Values{"order": {application.TimelineOrderOldest}}, anaSession.ID); rec.Code != http.StatusSeeOther {
		t.Fatalf("Ana toggle (native) = %d, want 303: %s", rec.Code, rec.Body.String())
	}

	anaBody := getDetailAs(t, h, path, anaSession.ID, false).Body.String()
	brunoBody := getDetailAs(t, h, path, brunoSession.ID, false).Body.String()

	if older, newer := strings.Index(anaBody, "older-order-token"), strings.Index(anaBody, "newer-order-token"); older < 0 || newer < 0 || older > newer {
		t.Errorf("Ana chose oldest first, so the older comment must render above the newer one (older=%d newer=%d)", older, newer)
	}
	if older, newer := strings.Index(brunoBody, "older-order-token"), strings.Index(brunoBody, "newer-order-token"); older < 0 || newer < 0 || newer > older {
		t.Errorf("Bruno never chose, so newest first must render the newer comment above the older one (older=%d newer=%d)", older, newer)
	}
	if got := storedTimelineOrder(t, h, bruno.ID); got != "" {
		t.Errorf("Ana's choice stored %q for Bruno, want no row", got)
	}
}

// TestTicketTimelineOrderOnReadOnlyTicket proves a ticket the reader can no
// longer comment on keeps its feed and its order control while keeping NO
// composer: the preference never invents a composer, and the Activity card is
// still where the feed lives.
func TestTicketTimelineOrderOnReadOnlyTicket(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Order closed", nil)
	if _, err := h.comments.Add(context.Background(), *h.admin, tkt.ID, "closed-order-token", "public"); err != nil {
		t.Fatalf("seed comment: %v", err)
	}
	h.seedTransition(t, tkt.ID, domain.StateInProgress, "")
	h.seedTransition(t, tkt.ID, domain.StateCancelled, "")
	if err := h.store.TimelineOrderPreferences().SetTimelineOrder(context.Background(), h.admin.ID, application.TimelineOrderOldest); err != nil {
		t.Fatalf("seed oldest order: %v", err)
	}

	body := h.get(t, ticketPath(tkt.ID), false).Body.String()
	if strings.Contains(body, `class="card comment-form"`) {
		t.Errorf("a closed ticket must keep no composer, got: %.900s", body)
	}
	if !strings.Contains(body, `class="card detail-activity"`) || !strings.Contains(body, "closed-order-token") {
		t.Errorf("the closed ticket must still render Activity with its entries, got: %.900s", body)
	}
	if !strings.Contains(body, `/timeline-order"`) {
		t.Errorf("the order control belongs to Activity and stays on a read-only ticket, got: %.900s", body)
	}
}

// TestTicketTimelineOrderRouteNeedsAuthentication proves the fragment route sits
// behind the session gate like every other ticket mutation: an anonymous POST is
// bounced to /login and stores nothing.
func TestTicketTimelineOrderRouteNeedsAuthentication(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Order gate", nil)

	rec := doRequest(h.mux, h.mw, http.MethodPost, "/tickets/"+strconv.FormatInt(tkt.ID, 10)+"/timeline-order", nil)
	wantRedirect(t, rec, http.StatusSeeOther, "/login")
	if n := countPreferenceRows(t, h, h.admin.ID, "timeline_order"); n != 0 {
		t.Errorf("an unauthenticated POST stored %d rows, want 0", n)
	}
}

// TestTicketTimelineOrderPreservesComposerContract proves PRESERVE for the block
// that MOVES: whichever side of Activity the composer lands on, it keeps its
// focus anchor, its form action, its draft-preserving fields and its submit
// label. A reordering that quietly rebuilt the composer would be a different
// feature.
func TestTicketTimelineOrderPreservesComposerContract(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Order composer contract", nil)
	path := ticketPath(tkt.ID)
	ctx := context.Background()

	for _, order := range application.AllowedTimelineOrders() {
		if err := h.store.TimelineOrderPreferences().SetTimelineOrder(ctx, h.admin.ID, order); err != nil {
			t.Fatalf("seed %q: %v", order, err)
		}
		body := h.get(t, path, false).Body.String()
		for _, want := range []string{
			`<div class="replies" id="reply">`,
			`<form class="card comment-form" method="post" action="` + path + `/comments" enctype="multipart/form-data">`,
			`<textarea name="body" id="comment-body" hx-preserve="true"`,
			`<input id="comment-internal" type="checkbox" name="internal" value="1" hx-preserve="true">`,
			`<input type="file" id="comment-attachments" name="attachments" multiple`,
			`>Reply</button>`,
			`id="detail-properties"`,
			`class="evidence"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("order %q must keep %q in the composer/evidence regions, got: %.900s", order, want, body)
			}
		}
		// Steps stays above BOTH the composer and Activity, whatever the order.
		stepsAt := strings.Index(body, `id="workflow-pending"`)
		composerAt, activityAt := detailRegions(t, body)
		if stepsAt >= 0 {
			if stepsAt > composerAt || stepsAt > activityAt {
				t.Errorf("order %q moved Steps below the composer (%d) or Activity (%d)", order, composerAt, activityAt)
			}
		}
	}
}
