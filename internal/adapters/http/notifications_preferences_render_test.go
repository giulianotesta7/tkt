package httpadapter

import (
	"net/http"
	"strings"
	"testing"
)

// The composition of the two screens issue #396 redesigned (S2/S3). The CSS of
// this change hangs off exactly two page roots, so the roots, the group around
// each control and the placement of the one mutating action are the contract
// these tests own — never a class used for colour.
//
// The bell's drawer shares `notifications_actions`, `notifications_list` and
// `notification_row` with the page. The panel is deliberately OUTSIDE this
// redesign, and its chrome is asserted here as unchanged, so a page-only rule
// added under the wrong scope fails instead of quietly restyling the drawer.

// notificationBodyAs reads one representation of GET /notifications: the full
// page, or the drawer fragment the bell swaps into #notification-panel-host.
func notificationBodyAs(t *testing.T, h *harness, sessionID string, hx bool) string {
	t.Helper()
	rec := notificationPanelAs(t, h, sessionID, hx)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /notifications (HX-Request=%t) = %d, want 200: %s", hx, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// TestNotificationsPageRendersAsAnOpenFeed is S2's reading order: the page is a
// feed grouped by the days the server already computes, its rows keep the
// ticket → event → actor → time hierarchy, unread rows stay marked, and the one
// mutating action ("Mark all as read") belongs to the page header above the
// feed instead of a card that wraps the whole list.
func TestNotificationsPageRendersAsAnOpenFeed(t *testing.T) {
	h := newHarness(t)
	_, session := assignedNotificationFixture(t, h)

	body := notificationBodyAs(t, h, session, false)

	// The page renders under its own root, so every page-only rule can be
	// scoped to it and no drawer rule has to be duplicated.
	if !strings.Contains(body, `class="notifications-screen"`) {
		t.Errorf("the page must render under its own root: %s", body)
	}
	// Separation is a hairline, not a shell: the feed is no longer a card.
	if strings.Contains(body, `class="card notifications-page"`) {
		t.Errorf("the feed must not be wrapped in a card: %s", body)
	}
	// Read-all is the page's header action, and it sits above the feed.
	if !appearsBefore(body, `class="command-header"`, `class="notifications-actions"`) {
		t.Errorf("read-all must render inside the page header: %s", body)
	}
	if !appearsBefore(body, `class="notifications-actions"`, `class="notifications-page"`) {
		t.Errorf("read-all must render above the feed, not inside it: %s", body)
	}
	// S2's hierarchy and its unread distinction survive the redesign.
	for _, want := range []string{
		`class="notifications-groups"`,
		`class="notifications-day"`,
		`class="notification-ident"`,
		`class="notification-what"`,
		`class="notification-meta"`,
		`class="notification-unread"`,
		`action="/notifications/read-all"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lost %q: %s", want, body)
		}
	}
}

// TestNotificationsPageKeepsTheDrawerOutOfThePageScope proves the shared panel
// is untouched by this redesign (L6): the fragment still renders the dialog and
// its own body, and no page-only root leaks into it.
func TestNotificationsPanelKeepsItsDrawerChrome(t *testing.T) {
	h := newHarness(t)
	_, session := assignedNotificationFixture(t, h)

	body := notificationBodyAs(t, h, session, true)

	for _, want := range []string{
		`class="notifications-overlay"`,
		`class="notifications-panel"`,
		`role="dialog"`,
		`aria-labelledby="notifications-panel-title"`,
		`class="notifications-header"`,
		`class="notifications-body"`,
		`class="notification-open"`,
		`action="/notifications/read-all"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the drawer lost %q: %s", want, body)
		}
	}
	for _, unwanted := range []string{`notifications-screen`, `command-header`} {
		if strings.Contains(body, unwanted) {
			t.Errorf("page-only markup %q leaked into the drawer fragment: %s", unwanted, body)
		}
	}
}

// TestRedesignedScreensSpeakSpanish is S4's catalogue half: the redesigned
// feed is composed of existing copy only, so a Spanish instance renders it in
// neutral tuteo with no English left behind.
func TestRedesignedScreensSpeakSpanish(t *testing.T) {
	notifications := renderLocalized(t, "es", "notifications", "", fixtureNotificationsPanelData())
	for _, want := range []string{"Notificaciones", "Hoy", "Ayer", "Sin leer", "Marcar todo como leído"} {
		if !strings.Contains(notifications, want) {
			t.Errorf("Spanish notifications must render %q, got: %.800s", want, notifications)
		}
	}
	for _, unwanted := range []string{">Notifications<", ">Today<", ">Unread<", ">Mark all as read<"} {
		if strings.Contains(notifications, unwanted) {
			t.Errorf("Spanish notifications must not render the English %q", unwanted)
		}
	}
}
