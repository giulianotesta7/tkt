package httpadapter

import (
	"net/http"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
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

// TestPreferencesPageRendersAlignedSections is S3's composition: one page root,
// one section per preference pairing a label with its control, and exactly one
// form per section writing to its own route with its own explicit save. Nothing
// in the content region speaks HTMX, so changing a select cannot save by itself.
//
// The activity order (issue #438, S1) is the fifth section and follows the same
// rule: its own route, its own explicit submit, and no auto-save.
func TestPreferencesPageRendersAlignedSections(t *testing.T) {
	h := newHarness(t)
	body := h.get(t, "/preferences", false).Body.String()

	if !strings.Contains(body, `class="preferences-screen"`) {
		t.Errorf("the page must render under its own root: %s", body)
	}
	if got := strings.Count(body, `class="preferences-section"`); got != 5 {
		t.Errorf("preferences sections = %d, want five remaining preferences: %s", got, body)
	}
	if got := strings.Count(body, `class="preferences-form"`); got != 5 {
		t.Errorf("preferences forms = %d, want one per section: %s", got, body)
	}
	// Each section keeps its own endpoint and its own submit: saving one
	// preference can never write another one's key.
	for _, section := range []struct{ action, save string }{
		{action: `action="/preferences/language"`, save: "Save language"},
		{action: `action="/preferences/theme"`, save: "Save theme"},
		{action: `action="/preferences/timeline-order"`, save: "Save activity order"},
		{action: `action="/preferences/email"`, save: "Save email preference"},
		{action: `action="/preferences/password"`, save: "Change password"},
	} {
		if got := strings.Count(body, section.action); got != 1 {
			t.Errorf("%s appears %d times, want exactly one form: %s", section.action, got, body)
		}
		if !strings.Contains(body, `type="submit">`+section.save+`</button>`) {
			t.Errorf("the section posting to %s must carry its own explicit %q submit: %s",
				section.action, section.save, body)
		}
	}
	// S3: a select change alone never saves. The content region carries no
	// HTMX attribute at all, so the only way to write is the explicit submit.
	content := contentRegion(t, body)
	if strings.Contains(content, "hx-") || strings.Contains(content, "onchange") {
		t.Errorf("the preferences content must not auto-save on change: %s", content)
	}
}

// TestPreferencesSectionsKeepTheirExistingFields is S3's other half: the existing
// forms keep their endpoints, field names and closed option sets, so a later
// section is an addition and never a change of what the rest writes.
func TestPreferencesSectionsKeepTheirExistingFields(t *testing.T) {
	h := newHarness(t)
	content := contentRegion(t, h.get(t, "/preferences", false).Body.String())

	for _, want := range []string{
		`name="language"`,
		`value="instance"`,
		`name="theme"`,
		`name="timeline_order"`,
		`value="oldest"`,
		`name="email_notifications"`,
		`value="newest"`,
		`value="system"`,
		`value="light"`,
		`value="dark"`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("the preferences page lost %q: %s", want, content)
		}
	}
	// Three selects and one checkbox across the stored preferences: the password
	// section adds none of either kind. The activity order (issue #438, S1) is the
	// third select and follows the same closed-set shape as the other two.
	if got := strings.Count(content, "<select"); got != 3 {
		t.Errorf("select controls = %d, want language, theme, and activity order: %s", got, content)
	}
	if got := strings.Count(content, `type="checkbox"`); got != 1 {
		t.Errorf("checkboxes = %d, want the email switch: %s", got, content)
	}
}

// contentRegion returns the rendered page's <main> content, so an assertion
// about page-specific attributes never trips over the shell's own htmx bell.
func contentRegion(t *testing.T, body string) string {
	t.Helper()
	start := strings.Index(body, `<main`)
	end := strings.Index(body, `</main>`)
	if start < 0 || end < start {
		t.Fatalf("rendered page has no main region: %s", body)
	}
	return body[start:end]
}

// fixturePreferencesPageData freezes the preferences page's default state: the
// stored queue order is the newest-first default, the theme is the system
// default, the activity order is the newest-first default (issue #438, S1), the
// email copy is on, and the password section follows the shipped policy (switch
// ON, the default minimum of 12). Every label still resolves through the
// catalogue, so the snapshot freezes the copy AND the option order.
func fixturePreferencesPageData() preferencesData {
	return preferencesData{
		pageData: pageData{
			NavActive:   "preferences",
			CurrentUser: domain.User{ID: 1, Name: "Ana Torres", Email: "ana@example.com", Active: true, CreatedAt: goldenT0},
		},
		Language:           application.LanguageInstance,
		Current:            application.QueueOrderNewest,
		Orders:             queueOrderOptions(),
		Theme:              application.ThemeSystem,
		Themes:             themeOptions(),
		TimelineOrder:      application.TimelineOrderNewest,
		TimelineOrders:     timelineOrderOptions(),
		EmailNotifications: true,
		Password: passwordSectionData{
			Shown:     application.DefaultPasswordSettings().ChangeEnabled,
			MinLength: application.DefaultPasswordMinLength,
		},
	}
}

// TestGoldenPreferences freezes the redesigned page: the four sections, their
// labels and their own explicit submits.
func TestGoldenPreferences(t *testing.T) {
	goldenFullPage(t, "preferences", renderGolden(t, "preferences", "", fixturePreferencesPageData(), false))
}

// TestRedesignedScreensSpeakSpanish is S4's catalogue half: the two screens are
// composed of existing copy only, so a Spanish instance renders the sections,
// their controls and the feed in neutral tuteo with no English left behind.
func TestRedesignedScreensSpeakSpanish(t *testing.T) {
	preferences := renderLocalized(t, "es", "preferences", "", fixturePreferencesPageData())
	for _, want := range []string{
		"Preferencias",
		"Tema", "Guardar tema",
		"Orden de la actividad", "Orden de la actividad por defecto", "Más nuevo primero", "Más antiguo primero", "Guardar orden de la actividad",
		"Notificaciones por email", "Recibir notificaciones por email", "Guardar preferencia de email",
	} {
		if !strings.Contains(preferences, want) {
			t.Errorf("Spanish preferences must render %q, got: %.800s", want, preferences)
		}
	}
	for _, unwanted := range []string{">Preferences<", ">Default sorting<", ">Save theme<", ">Activity order<", ">Oldest first<"} {
		if strings.Contains(preferences, unwanted) {
			t.Errorf("Spanish preferences must not render the English %q", unwanted)
		}
	}
	// Issue #380: the subtitle repeated the heading, so it is gone.
	if strings.Contains(preferences, "Configuración personal") {
		t.Errorf("Spanish preferences must not render the repeated subtitle, got: %.800s", preferences)
	}

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
