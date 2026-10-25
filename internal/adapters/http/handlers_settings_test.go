package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Settings sections (issue #383, S1/S2): GET /settings redirects to the first
// section, GET /settings/{section} renders exactly that section, an unknown
// section is a 404, and every existing POST keeps its path, validation and
// capability while redirecting back to the section it belongs to. The tests
// below also pin the pieces of the layout a user meets: the sub-navigation with
// its aria-current mark, and the sticky save bar that names its section.

// sectionGet is an authenticated GET of one settings section, fataling on a
// non-200 so the assertions below can read the body directly.
func sectionGet(t *testing.T, h *harness, path string) string {
	t.Helper()
	rec := h.get(t, path, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200: %s", path, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func TestSettingsIndexRequiresAdmin(t *testing.T) {
	h := newHarness(t)
	user, err := h.users.Create(t.Context(), *h.admin, application.CreateUserInput{Name: "User", Email: "user@tkt.test", Password: "s3cret-pass-12"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	session := h.loginCookie(t, user.Email, "s3cret-pass-12")
	if session == "" {
		t.Fatal("user login must succeed")
	}

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.Header.Set("Cookie", sessionCookie+"="+session)
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("non-admin GET /settings = %d, want 403", rec.Code)
	}
}

// TestSettingsIndexRedirectsToTheFirstSection proves S1's entry point: the
// surface has no page of its own, so the bare URL answers a 303 to the
// Organization section.
func TestSettingsIndexRedirectsToTheFirstSection(t *testing.T) {
	h := newHarness(t)

	rec := h.get(t, "/settings", false)

	wantRedirect(t, rec, http.StatusSeeOther, "/settings/organization")
}

// TestSettingsSectionsRenderOneSectionEach proves S2's core promise: each URL
// renders exactly its own section, so a longer page never hides the control the
// administrator opened.
func TestSettingsSectionsRenderOneSectionEach(t *testing.T) {
	h := newHarness(t)

	cases := []struct {
		path   string
		title  string
		own    []string
		absent []string
	}{
		{
			path:  "/settings/organization",
			title: "<h2 class=\"settings-section-title\">Organization</h2>",
			own: []string{
				`action="/settings/organization"`,
				`id="org_name" name="name"`,
				`id="org_help_desk" name="help_desk"`,
				`id="org_support_email" name="support_email"`,
				`type="file" id="org_logo" name="logo" accept="image/png"`,
				`Save organization`,
			},
			absent: []string{`action="/settings/language"`, `action="/settings/sla"`, `name="internal_comment_bg"`, `action="/settings/calendar"`},
		},
		{
			path:  "/settings/language",
			title: "<h2 class=\"settings-section-title\">Interface language</h2>",
			own: []string{
				`action="/settings/language"`,
				`<select id="interface_language" name="interface_language">`,
				`Save language`,
			},
			absent: []string{`action="/settings/organization"`, `name="internal_comment_bg"`, `action="/settings/calendar"`},
		},
		{
			path:  "/settings/tickets",
			title: "<h2 class=\"settings-section-title\">Tickets &amp; SLA</h2>",
			own: []string{
				`action="/settings/appearance"`,
				`name="internal_comment_bg" value="#E8EEFF"`,
				`action="/settings/sla"`,
				`min="1" max="99" value="80"`,
				`name="first_response_duration_critical" value="30m"`,
				`Save appearance`,
				`Save SLA settings`,
			},
			absent: []string{`action="/settings/organization"`, `action="/settings/calendar"`, `action="/settings/language"`},
		},
		{
			path:  "/settings/calendar",
			title: "<h2 class=\"settings-section-title\">Working calendar</h2>",
			own: []string{
				`action="/settings/calendar"`,
				`name="sla_calendar_days" value="1" checked`,
				`name="sla_calendar_days" value="6"> Saturday`,
				`id="sla_calendar_start" name="sla_calendar_start" value="09:00"`,
				`id="sla_calendar_end" name="sla_calendar_end" value="18:00"`,
				`id="sla_calendar_timezone" name="sla_calendar_timezone" value="UTC"`,
				`Save working calendar`,
			},
			absent: []string{`action="/settings/sla"`, `name="internal_comment_bg"`, `action="/settings/organization"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			body := sectionGet(t, h, tc.path)
			if !strings.Contains(body, tc.title) {
				t.Errorf("GET %s must render %q, got: %s", tc.path, tc.title, body)
			}
			for _, want := range tc.own {
				if !strings.Contains(body, want) {
					t.Errorf("GET %s must contain %q, got: %s", tc.path, want, body)
				}
			}
			for _, unwanted := range tc.absent {
				if strings.Contains(body, unwanted) {
					t.Errorf("GET %s must not render another section's %q, got: %s", tc.path, unwanted, body)
				}
			}
			// Every section page is still the Settings surface: the rail link is
			// active and the shell keeps the internal-comment background.
			if !strings.Contains(body, `href="/settings" data-tip="Settings" aria-current="page"`) {
				t.Errorf("GET %s must keep the Settings rail link active", tc.path)
			}
		})
	}
}

// TestSettingsUnknownSectionIs404 proves S1: an unknown segment is not a
// section, so it answers 404 instead of a page with an empty content column.
func TestSettingsUnknownSectionIs404(t *testing.T) {
	h := newHarness(t)

	for _, path := range []string{"/settings/appearance", "/settings/bogus", "/settings/settings"} {
		if rec := h.get(t, path, false); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
	}
}

// TestSettingsSectionsRequireTheirCapability proves each section keeps the gate
// its controls have: every section but Email is CapManageSettings, and Email is
// root-only, so an administrator's direct visit is refused with 403.
func TestSettingsSectionsRequireTheirCapability(t *testing.T) {
	h := newHarness(t)
	admin := seedUserRole(t, h.store, "Section Admin", "section-admin@tkt.test", domain.RoleAdmin)
	session := seedSession(t, h.store, admin.ID)

	for _, section := range []string{"organization", "language", "tickets", "calendar"} {
		if rec := getAs(t, h, "/settings/"+section, session.ID); rec.Code != http.StatusOK {
			t.Errorf("admin GET /settings/%s = %d, want 200", section, rec.Code)
		}
	}
	if rec := getAs(t, h, "/settings/email", session.ID); rec.Code != http.StatusForbidden {
		t.Errorf("admin GET /settings/email = %d, want 403: the Email section is root-only", rec.Code)
	}

	requester := seedUserRole(t, h.store, "Section User", "section-user@tkt.test", domain.RoleUser)
	requesterSession := seedSession(t, h.store, requester.ID)
	for _, section := range []string{"organization", "language", "tickets", "calendar", "email"} {
		if rec := getAs(t, h, "/settings/"+section, requesterSession.ID); rec.Code != http.StatusForbidden {
			t.Errorf("requester GET /settings/%s = %d, want 403", section, rec.Code)
		}
	}
}

// TestSettingsNavMarksTheActiveSectionAndHidesEmail proves the sub-navigation:
// one entry per section in a stable order, aria-current="page" exactly on the
// active one, and no Email entry for an actor who may not open it.
func TestSettingsNavMarksTheActiveSectionAndHidesEmail(t *testing.T) {
	h := newHarness(t)

	_, rootSession := seedRoot(t, h)
	body := getAs(t, h, "/settings/calendar", rootSession.ID).Body.String()
	for _, want := range []string{
		`<nav class="settings-nav" aria-label="Settings sections">`,
		`href="/settings/organization">Organization</a>`,
		`href="/settings/language">Language</a>`,
		`href="/settings/tickets">Tickets &amp; SLA</a>`,
		`href="/settings/calendar" aria-current="page">Working calendar</a>`,
		`href="/settings/email">Email notifications</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the section nav must contain %q, got: %s", want, body)
		}
	}
	if got := strings.Count(body, `aria-current="page"`); got < 1 {
		t.Errorf("the active section must be marked, got %d marks", got)
	}

	// The admin's rail never offers the root-only section.
	admin := seedUserRole(t, h.store, "Nav Admin", "nav-admin@tkt.test", domain.RoleAdmin)
	adminSession := seedSession(t, h.store, admin.ID)
	adminBody := getAs(t, h, "/settings/organization", adminSession.ID).Body.String()
	if strings.Contains(adminBody, `href="/settings/email"`) {
		t.Errorf("an administrator's nav must not offer the root-only Email notifications section, got: %s", adminBody)
	}
}

// TestSettingsPagesLoadTheirOwnAssets proves S2's asset contract: the settings
// stylesheet and the save-bar script load on a settings page, and nowhere else.
func TestSettingsPagesLoadTheirOwnAssets(t *testing.T) {
	h := newHarness(t)

	body := sectionGet(t, h, "/settings/organization")
	for _, want := range []string{
		`<link rel="stylesheet" href="/static/settings.css">`,
		`<script src="/static/settings.js" defer></script>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("a settings page must load %q, got: %s", want, body)
		}
	}
	tickets := h.get(t, "/tickets", false).Body.String()
	if strings.Contains(tickets, "/static/settings.css") {
		t.Errorf("a non-settings page must not download the settings stylesheet")
	}
}

// TestSettingsSaveBarNamesItsSection proves S2's save bar: every settings form
// carries a sticky bar that is hidden until the form is dirty and names the
// section it saves, with a discard control beside it.
func TestSettingsSaveBarNamesItsSection(t *testing.T) {
	h := newHarness(t)

	cases := []struct {
		path  string
		label string
	}{
		{"/settings/organization", "Save organization"},
		{"/settings/language", "Save language"},
		{"/settings/tickets", "Save SLA settings"},
		{"/settings/calendar", "Save working calendar"},
	}
	for _, tc := range cases {
		body := sectionGet(t, h, tc.path)
		if got := strings.Count(body, `class="settings-savebar" data-settings-savebar hidden`); got == 0 {
			t.Errorf("GET %s must render a hidden save bar, got: %s", tc.path, body)
		}
		if !strings.Contains(body, tc.label) {
			t.Errorf("GET %s must name its save action %q, got: %s", tc.path, tc.label, body)
		}
		if !strings.Contains(body, `type="reset">Discard</button>`) {
			t.Errorf("GET %s must offer a discard control beside its save, got: %s", tc.path, body)
		}
	}
	// The tickets section owns four forms (appearance, creation flow, SLA,
	// automatic hold close), so it owes four bars: one per form, each naming its
	// own save.
	tickets := sectionGet(t, h, "/settings/tickets")
	if got := strings.Count(tickets, `data-settings-savebar hidden`); got != 4 {
		t.Errorf("the tickets section renders %d save bars, want one per form (4)", got)
	}
}

// TestSettingsDroppedCopyIsGone proves S2's copy rule: the descriptive
// paragraphs an administrator can infer are gone, while the help that states a
// rule they cannot infer (the SLA warning meaning, the calendar semantics, the
// encryption rule) stays.
func TestSettingsDroppedCopyIsGone(t *testing.T) {
	h := newHarness(t)

	for _, path := range []string{"/settings/organization", "/settings/language", "/settings/tickets", "/settings/calendar"} {
		body := sectionGet(t, h, path)
		for _, unwanted := range []string{
			"Instance configuration",
			"Changes the language of every screen",
			"Background color for internal comments",
			"Off by default",
		} {
			if strings.Contains(body, unwanted) {
				t.Errorf("GET %s must not carry the dropped copy %q", path, unwanted)
			}
		}
	}

	tickets := sectionGet(t, h, "/settings/tickets")
	if !strings.Contains(tickets, "At risk once this share of the target has elapsed.") {
		t.Errorf("the SLA warning meaning is a rule the user cannot infer and must stay")
	}
	calendar := sectionGet(t, h, "/settings/calendar")
	if !strings.Contains(calendar, "When the SLA clock runs.") {
		t.Errorf("the calendar semantics are a rule the user cannot infer and must stay")
	}
}

func TestSettingsUpdatePersistsAndRedirects(t *testing.T) {
	h := newHarness(t)

	rec := h.postForm(t, "/settings/appearance", url.Values{"internal_comment_bg": {"#EFE9FB"}}, false)

	wantRedirect(t, rec, http.StatusSeeOther, "/settings/tickets")

	got, err := h.store.SettingsStore().GetInternalCommentBg(t.Context())
	if err != nil {
		t.Fatalf("read stored color: %v", err)
	}
	if got != "#EFE9FB" {
		t.Errorf("stored bg = %q, want %q", got, "#EFE9FB")
	}

	// The following GET renders the new color as selected AND in the CSS.
	body := sectionGet(t, h, "/settings/tickets")
	for _, want := range []string{
		`value="#EFE9FB" checked`,
		`--internal-comment-bg:#EFE9FB;`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("tickets section after update must contain %q, got: %s", want, body)
		}
	}
}

func TestSettingsUpdateRejectsInvalidColor(t *testing.T) {
	h := newHarness(t)

	rec := h.postForm(t, "/settings/appearance", url.Values{"internal_comment_bg": {"#123456"}}, false)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `error-banner`) {
		t.Errorf("invalid color must render the inline error banner, got: %s", rec.Body.String())
	}
	// The rejection re-renders its OWN section: the appearance control and the
	// SLA panel that share the page are both there.
	for _, want := range []string{`name="internal_comment_bg"`, `name="sla_warning_percent"`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("rejected appearance must keep rendering %q, got: %s", want, rec.Body.String())
		}
	}
	got, err := h.store.SettingsStore().GetInternalCommentBg(t.Context())
	if err != nil {
		t.Fatalf("read stored color: %v", err)
	}
	if got != application.DefaultInternalCommentBg {
		t.Errorf("stored bg = %q after a rejected update, want the default", got)
	}
}

func TestSettingsUpdateDeniedForNonAdmin(t *testing.T) {
	h := newHarness(t)
	user, err := h.users.Create(t.Context(), *h.admin, application.CreateUserInput{Name: "User", Email: "user@tkt.test", Password: "s3cret-pass-12"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	session := h.loginCookie(t, user.Email, "s3cret-pass-12")
	if session == "" {
		t.Fatal("user login must succeed")
	}

	req := httptest.NewRequest(http.MethodPost, "/settings/appearance", strings.NewReader(url.Values{"internal_comment_bg": {"#EFE9FB"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", sessionCookie+"="+session)
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("non-admin POST /settings/appearance = %d, want 403", rec.Code)
	}
	if got := rec.Header().Get("X-Save-Feedback"); got != "" {
		t.Errorf("denied update must not emit save feedback, got %q", got)
	}
	got, err := h.store.SettingsStore().GetInternalCommentBg(t.Context())
	if err != nil {
		t.Fatalf("read stored color: %v", err)
	}
	if got != application.DefaultInternalCommentBg {
		t.Errorf("stored bg = %q after a denied update, want the default", got)
	}
}

// TestSettingsRailLinkCapabilityIsolation proves the rail keeps the Settings
// link behind CanManageSettings and the Users link behind CanManageUsers, in
// both directions. The two role sets coincide today (admin and root grant
// both), so the presentation flags are the only place this split is observable
// — and conflating them is exactly the coupling the split removes.
func TestSettingsRailLinkCapabilityIsolation(t *testing.T) {
	cases := []struct {
		name             string
		manageUsers      bool
		manageSettings   bool
		wantUsersLink    bool
		wantSettingsLink bool
	}{
		{"both granted", true, true, true, true},
		{"users only", true, false, true, false},
		{"settings only", false, true, false, true},
		{"neither granted", false, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := fixtureUsersIndexData()
			data.CanManageUsers = tc.manageUsers
			data.CanManageSettings = tc.manageSettings
			body := renderGolden(t, "users_index", "", data, false)
			// The data-tip attribute is unique to the sidebar anchor; a bare href
			// also matches the in-page "All" filter link and "Back to users".
			if got := strings.Contains(body, `href="/users" data-tip="Users"`); got != tc.wantUsersLink {
				t.Errorf("users sidebar link present = %t, want %t", got, tc.wantUsersLink)
			}
			if got := strings.Contains(body, `href="/settings" data-tip="Settings"`); got != tc.wantSettingsLink {
				t.Errorf("settings sidebar link present = %t, want %t", got, tc.wantSettingsLink)
			}
		})
	}
}

// slaSettingsFixture is the seeded SLA configuration (migration 0013) the
// unchanged-store assertions compare against.
func slaSettingsFixture(t *testing.T, h *harness) (bool, int, []domain.SLAPolicy) {
	t.Helper()
	enabled, err := h.store.SettingsStore().GetSLAEnabled(t.Context())
	if err != nil {
		t.Fatalf("read sla_enabled: %v", err)
	}
	percent, err := h.store.SettingsStore().GetSLAWarningPercent(t.Context())
	if err != nil {
		t.Fatalf("read sla_warning_percent: %v", err)
	}
	defaults, err := h.store.SLAStore().ListDefaults(t.Context())
	if err != nil {
		t.Fatalf("read sla defaults: %v", err)
	}
	return enabled, percent, defaults
}

func assertSLASettingsEqual(t *testing.T, h *harness, wantEnabled bool, wantPercent int, wantDefaults []domain.SLAPolicy) {
	t.Helper()
	gotEnabled, gotPercent, gotDefaults := slaSettingsFixture(t, h)
	if gotEnabled != wantEnabled {
		t.Errorf("sla_enabled = %t, want %t", gotEnabled, wantEnabled)
	}
	if gotPercent != wantPercent {
		t.Errorf("sla_warning_percent = %d, want %d", gotPercent, wantPercent)
	}
	if len(gotDefaults) != len(wantDefaults) {
		t.Fatalf("sla defaults length = %d, want %d", len(gotDefaults), len(wantDefaults))
	}
	for i, want := range wantDefaults {
		if gotDefaults[i] != want {
			t.Errorf("sla defaults[%d] = %+v, want %+v", i, gotDefaults[i], want)
		}
	}
}

// slaPanelForm builds a complete valid SLA panel submission: warning percent
// and the four-priority x two-milestone matrix in the grid's h/m/s fields.
func slaPanelForm(percent string) url.Values {
	targets := map[string][2]string{
		"critical": {"1", "4"},
		"high":     {"2", "8"},
		"medium":   {"4", "24"},
		"low":      {"8", "48"},
	}
	form := url.Values{"sla_warning_percent": {percent}}
	for priority, hours := range targets {
		form.Set("first_response_h_"+priority, hours[0])
		form.Set("first_response_m_"+priority, "0")
		form.Set("first_response_s_"+priority, "0")
		form.Set("resolve_h_"+priority, hours[1])
		form.Set("resolve_m_"+priority, "0")
		form.Set("resolve_s_"+priority, "0")
	}
	return form
}

// TestSettingsSLAPersistsAndRedirects proves a valid panel post persists all
// three SLA settings groups and redirects 303 back to the Tickets section, and
// that the following GET renders the new values.
func TestSettingsSLAPersistsAndRedirects(t *testing.T) {
	h := newHarness(t)

	rec := h.postForm(t, "/settings/sla", slaPanelForm("90"), false)

	wantRedirect(t, rec, http.StatusSeeOther, "/settings/tickets")

	enabled, err := h.store.SettingsStore().GetSLAEnabled(t.Context())
	if err != nil {
		t.Fatalf("read sla_enabled: %v", err)
	}
	if enabled {
		t.Errorf("sla_enabled = true, want false: the submitted form left the checkbox unchecked")
	}
	percent, err := h.store.SettingsStore().GetSLAWarningPercent(t.Context())
	if err != nil {
		t.Fatalf("read sla_warning_percent: %v", err)
	}
	if percent != 90 {
		t.Errorf("sla_warning_percent = %d, want 90", percent)
	}
	defaults, err := h.store.SLAStore().ListDefaults(t.Context())
	if err != nil {
		t.Fatalf("read sla defaults: %v", err)
	}
	want := []domain.SLAPolicy{
		{Priority: domain.PriorityCritical, FirstResponseSeconds: 3600, ResolveSeconds: 14400},
		{Priority: domain.PriorityHigh, FirstResponseSeconds: 7200, ResolveSeconds: 28800},
		{Priority: domain.PriorityMedium, FirstResponseSeconds: 14400, ResolveSeconds: 86400},
		{Priority: domain.PriorityLow, FirstResponseSeconds: 28800, ResolveSeconds: 172800},
	}
	if len(defaults) != len(want) {
		t.Fatalf("sla defaults length = %d, want %d: %+v", len(defaults), len(want), defaults)
	}
	for i := range want {
		if defaults[i] != want[i] {
			t.Errorf("sla defaults[%d] = %+v, want %+v", i, defaults[i], want[i])
		}
	}

	// The following GET renders the persisted values: enabled unchecked,
	// 90 percent, critical first response 1h.
	body := sectionGet(t, h, "/settings/tickets")
	for _, want := range []string{
		`value="90"`,
		`name="first_response_duration_critical" value="1h"`,
		`name="resolve_duration_critical" value="4h"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("tickets section after SLA save must contain %q, got: %s", want, body)
		}
	}
}

// TestSettingsSLAPersistsEnabled proves the checkbox enables the feature.
func TestSettingsSLAPersistsEnabled(t *testing.T) {
	h := newHarness(t)

	form := slaPanelForm("80")
	form.Set("sla_enabled", "1")
	rec := h.postForm(t, "/settings/sla", form, false)

	wantRedirect(t, rec, http.StatusSeeOther, "/settings/tickets")

	enabled, err := h.store.SettingsStore().GetSLAEnabled(t.Context())
	if err != nil {
		t.Fatalf("read sla_enabled: %v", err)
	}
	if !enabled {
		t.Errorf("sla_enabled = false, want true")
	}
}

// TestSettingsSLADeniedForNonAdmin proves the POST is gated on the same
// capability as the page, and a denied post writes nothing.
func TestSettingsSLADeniedForNonAdmin(t *testing.T) {
	h := newHarness(t)
	user, err := h.users.Create(t.Context(), *h.admin, application.CreateUserInput{Name: "User", Email: "user@tkt.test", Password: "s3cret-pass-12"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	session := h.loginCookie(t, user.Email, "s3cret-pass-12")
	if session == "" {
		t.Fatal("user login must succeed")
	}

	req := httptest.NewRequest(http.MethodPost, "/settings/sla", strings.NewReader(slaPanelForm("90").Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", sessionCookie+"="+session)
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("non-admin POST /settings/sla = %d, want 403", rec.Code)
	}
	if got := rec.Header().Get("X-Save-Feedback"); got != "" {
		t.Errorf("denied update must not emit save feedback, got %q", got)
	}
	wantEnabled, wantPercent, wantDefaults := slaSettingsFixture(t, h)
	assertSLASettingsEqual(t, h, wantEnabled, wantPercent, wantDefaults)
}

// TestSettingsCalendarDeniedForNonAdmin proves POST /settings/calendar is
// gated on the same capability as the page, and a denied post writes nothing.
func TestSettingsCalendarDeniedForNonAdmin(t *testing.T) {
	h := newHarness(t)
	user, err := h.users.Create(t.Context(), *h.admin, application.CreateUserInput{Name: "User", Email: "user@tkt.test", Password: "s3cret-pass-12"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	session := h.loginCookie(t, user.Email, "s3cret-pass-12")
	if session == "" {
		t.Fatal("user login must succeed")
	}

	body := url.Values{
		"sla_calendar_days":     {"1"},
		"sla_calendar_start":    {"08:00"},
		"sla_calendar_end":      {"20:00"},
		"sla_calendar_timezone": {"UTC"},
	}.Encode()
	req := httptest.NewRequest(http.MethodPost, "/settings/calendar", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", sessionCookie+"="+session)
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("non-admin POST /settings/calendar = %d, want 403", rec.Code)
	}
	if got := rec.Header().Get("X-Save-Feedback"); got != "" {
		t.Errorf("denied update must not emit save feedback, got %q", got)
	}
	assertCalendarEqual(t, readStoredCalendar(t, h), domain.DefaultSLACalendar())
}

// TestSettingsSLARejectsInvalidPercent proves a percent outside 1..99
// re-renders the Tickets section 422 with the inline error banner and leaves the
// store exactly as it was — the targets and the enabled flag submitted alongside
// it are not persisted either.
func TestSettingsSLARejectsInvalidPercent(t *testing.T) {
	h := newHarness(t)
	wantEnabled, wantPercent, wantDefaults := slaSettingsFixture(t, h)

	rec := h.postForm(t, "/settings/sla", slaPanelForm("150"), false)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `error-banner`) {
		t.Errorf("invalid percent must render the inline error banner, got: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `name="internal_comment_bg"`) {
		t.Errorf("the rejection must re-render the whole Tickets section, got: %s", rec.Body.String())
	}
	if got := rec.Header().Get("X-Save-Feedback"); got != "" {
		t.Errorf("rejected update must not emit save feedback, got %q", got)
	}
	assertSLASettingsEqual(t, h, wantEnabled, wantPercent, wantDefaults)
}

// TestSettingsSLARejectsBelowFloorTarget proves a below-60-second target
// re-renders 422 with the inline error banner and leaves the store exactly
// as it was — including the warning percent submitted alongside it.
func TestSettingsSLARejectsBelowFloorTarget(t *testing.T) {
	h := newHarness(t)
	wantEnabled, wantPercent, wantDefaults := slaSettingsFixture(t, h)

	form := slaPanelForm("50")
	// A below-60-second resolve total for critical: the hours and minutes
	// fields must drop too, or the h*3600 term keeps the total above the
	// floor.
	form.Set("resolve_h_critical", "0")
	form.Set("resolve_m_critical", "0")
	form.Set("resolve_s_critical", "30")
	rec := h.postForm(t, "/settings/sla", form, false)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `error-banner`) {
		t.Errorf("below-floor target must render the inline error banner, got: %s", rec.Body.String())
	}
	if got := rec.Header().Get("X-Save-Feedback"); got != "" {
		t.Errorf("rejected update must not emit save feedback, got %q", got)
	}
	assertSLASettingsEqual(t, h, wantEnabled, wantPercent, wantDefaults)
}

// readStoredCalendar reads the instance working calendar through the
// settings store port — never raw SQL.
func readStoredCalendar(t *testing.T, h *harness) domain.SLACalendar {
	t.Helper()
	cal, err := h.store.SettingsStore().GetSLACalendar(t.Context())
	if err != nil {
		t.Fatalf("read stored calendar: %v", err)
	}
	return cal
}

// assertCalendarEqual compares a stored calendar field by field (the slice
// order is the canonical ISO order the store documents).
func assertCalendarEqual(t *testing.T, got, want domain.SLACalendar) {
	t.Helper()
	if len(got.WorkingDays) != len(want.WorkingDays) {
		t.Fatalf("working days = %v, want %v", got.WorkingDays, want.WorkingDays)
	}
	for i := range want.WorkingDays {
		if got.WorkingDays[i] != want.WorkingDays[i] {
			t.Errorf("working days = %v, want %v", got.WorkingDays, want.WorkingDays)
		}
	}
	if got.StartMinute != want.StartMinute {
		t.Errorf("start minute = %d, want %d", got.StartMinute, want.StartMinute)
	}
	if got.EndMinute != want.EndMinute {
		t.Errorf("end minute = %d, want %d", got.EndMinute, want.EndMinute)
	}
	if got.Location == nil || want.Location == nil {
		if got.Location != want.Location {
			t.Fatalf("location = %v, want %v", got.Location, want.Location)
		}
		return
	}
	if got.Location.String() != want.Location.String() {
		t.Errorf("location = %q, want %q", got.Location.String(), want.Location.String())
	}
}

// TestSettingsCalendarPersistsAndRedirects proves a valid calendar post
// persists all four sla_calendar_* keys and redirects 303 back to the Working
// calendar section, and that the following GET renders the persisted values.
func TestSettingsCalendarPersistsAndRedirects(t *testing.T) {
	h := newHarness(t)

	form := url.Values{
		"sla_calendar_days":     {"1", "3", "6"},
		"sla_calendar_start":    {"08:30"},
		"sla_calendar_end":      {"17:45"},
		"sla_calendar_timezone": {"America/Argentina/Buenos_Aires"},
	}
	rec := h.postForm(t, "/settings/calendar", form, false)

	wantRedirect(t, rec, http.StatusSeeOther, "/settings/calendar")

	// The unknown submitted days dropped out and the survivors kept the
	// canonical ISO order; the window and the zone round-trip.
	assertCalendarEqual(t, readStoredCalendar(t, h), domain.SLACalendar{
		WorkingDays: []time.Weekday{time.Monday, time.Wednesday, time.Saturday},
		StartMinute: 8*60 + 30,
		EndMinute:   17*60 + 45,
		Location:    mustLoadLocation(t, "America/Argentina/Buenos_Aires"),
	})

	// The following GET renders the persisted values.
	body := sectionGet(t, h, "/settings/calendar")
	for _, want := range []string{
		`name="sla_calendar_days" value="1" checked`,
		`name="sla_calendar_days" value="3" checked`,
		`name="sla_calendar_days" value="6" checked`,
		`name="sla_calendar_days" value="2"> Tuesday`,
		`id="sla_calendar_start" name="sla_calendar_start" value="08:30"`,
		`id="sla_calendar_end" name="sla_calendar_end" value="17:45"`,
		`id="sla_calendar_timezone" name="sla_calendar_timezone" value="America/Argentina/Buenos_Aires"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("calendar section after save must contain %q, got: %s", want, body)
		}
	}
}

// mustLoadLocation resolves an IANA name for test expectations; the tzdata
// the handler resolves against is the same database.
func mustLoadLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load location %q: %v", name, err)
	}
	return loc
}

// TestSettingsCalendarRejectsInvalidSubmissions proves the three service
// rejections re-render 422 with the inline error banner, echo the SUBMITTED
// values, and leave the stored calendar exactly as it was.
func TestSettingsCalendarRejectsInvalidSubmissions(t *testing.T) {
	cases := []struct {
		name     string
		form     url.Values
		wantBody []string
	}{
		{
			name: "no working day at all",
			form: url.Values{
				"sla_calendar_start":    {"09:00"},
				"sla_calendar_end":      {"18:00"},
				"sla_calendar_timezone": {"UTC"},
			},
			wantBody: []string{
				`id="sla_calendar_start" name="sla_calendar_start" value="09:00"`,
				`id="sla_calendar_end" name="sla_calendar_end" value="18:00"`,
				`id="sla_calendar_timezone" name="sla_calendar_timezone" value="UTC"`,
			},
		},
		{
			name: "start not before end",
			form: url.Values{
				"sla_calendar_days":     {"1", "2", "3", "4", "5"},
				"sla_calendar_start":    {"18:00"},
				"sla_calendar_end":      {"09:00"},
				"sla_calendar_timezone": {"UTC"},
			},
			wantBody: []string{
				`id="sla_calendar_start" name="sla_calendar_start" value="18:00"`,
				`id="sla_calendar_end" name="sla_calendar_end" value="09:00"`,
				`name="sla_calendar_days" value="1" checked`,
			},
		},
		{
			name: "unknown timezone",
			form: url.Values{
				"sla_calendar_days":     {"1"},
				"sla_calendar_start":    {"09:00"},
				"sla_calendar_end":      {"18:00"},
				"sla_calendar_timezone": {"Mars/Olympus"},
			},
			wantBody: []string{
				`name="sla_calendar_days" value="1" checked`,
				`id="sla_calendar_start" name="sla_calendar_start" value="09:00"`,
				`id="sla_calendar_timezone" name="sla_calendar_timezone" value="Mars/Olympus"`,
			},
		},
		{
			name: "non-portable timezone",
			form: url.Values{
				"sla_calendar_days":     {"1"},
				"sla_calendar_start":    {"09:00"},
				"sla_calendar_end":      {"18:00"},
				"sla_calendar_timezone": {"Local"},
			},
			wantBody: []string{
				`name="sla_calendar_days" value="1" checked`,
				`id="sla_calendar_timezone" name="sla_calendar_timezone" value="Local"`,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)

			rec := h.postForm(t, "/settings/calendar", tc.form, false)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want 422", rec.Code)
			}
			body := rec.Body.String()
			if !strings.Contains(body, `error-banner`) {
				t.Errorf("invalid calendar must render the inline error banner, got: %s", body)
			}
			if got := rec.Header().Get("X-Save-Feedback"); got != "" {
				t.Errorf("rejected update must not emit save feedback, got %q", got)
			}
			for _, want := range tc.wantBody {
				if !strings.Contains(body, want) {
					t.Errorf("rejected calendar must echo %q, got: %s", want, body)
				}
			}
			// Nothing persisted: the stored calendar is still the seeded
			// default, and no submitted day was checked into it.
			assertCalendarEqual(t, readStoredCalendar(t, h), domain.DefaultSLACalendar())
		})
	}
}

// TestSettingsCalendarRejectsUnparsableClock proves an unparsable clock
// value is rejected and NEVER coerced to 00:00 — midnight is a legal
// window start, so coercion would silently persist a wrong calendar.
func TestSettingsCalendarRejectsUnparsableClock(t *testing.T) {
	h := newHarness(t)

	form := url.Values{
		"sla_calendar_days":     {"1", "2", "3", "4", "5"},
		"sla_calendar_start":    {""},
		"sla_calendar_end":      {"18:00"},
		"sla_calendar_timezone": {"UTC"},
	}
	rec := h.postForm(t, "/settings/calendar", form, false)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "the working calendar needs a start and end time in HH:MM form") {
		t.Errorf("unparseable clock must render the HH:MM error message, got: %s", body)
	}
	// Unchanged storage proves the blank start was not coerced to 00:00:
	// the seeded default starts at 09:00 (540).
	assertCalendarEqual(t, readStoredCalendar(t, h), domain.DefaultSLACalendar())
}

// --- Interface language section (issue #335, S8/S9/S14; issue #383, S1) ---
//
// The Language section offers the instance-wide interface language: root/admin
// only, one option per shipped language, persisted through the settings store,
// and the page re-renders in the language just saved. A rejected code changes
// nothing and its message reads in the language the instance is currently
// rendering. The descriptive paragraph the old page carried is gone (S2): the
// choice is instance-wide by being a section of the instance settings.

// TestSettingsLanguageSectionShowsTheStoredLanguage proves the section exists
// with the stored language marked and no descriptive paragraph.
func TestSettingsLanguageSectionShowsTheStoredLanguage(t *testing.T) {
	h := newHarness(t)

	body := sectionGet(t, h, "/settings/language")
	for _, want := range []string{
		`action="/settings/language"`,
		`<h2 class="settings-section-title">Interface language</h2>`,
		`<select id="interface_language" name="interface_language">`,
		`<option value="en" selected>English</option>`,
		`<option value="es">Español</option>`,
		`Save language`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the language section must contain %q, got: %s", want, body)
		}
	}
	if got := strings.Count(body, `value="es" selected`); got != 0 {
		t.Errorf("Spanish must not be marked before it is chosen, got %d marked options", got)
	}
}

// TestSettingsUpdateLanguagePersistsAndRendersTheNewLanguage proves both halves
// of the save: the code reaches the store exactly as submitted (S9), and the
// page the administrator lands on renders in that language (S8) — a new request,
// so the whole shell follows the instance setting, not just the panel.
func TestSettingsUpdateLanguagePersistsAndRendersTheNewLanguage(t *testing.T) {
	h := newHarness(t)

	rec := h.postForm(t, "/settings/language", url.Values{"interface_language": {"es"}}, false)
	wantRedirect(t, rec, http.StatusSeeOther, "/settings/language")

	// The save toast the redirect carries must already read the language just
	// saved: the page that renders it is Spanish, and S8 allows no leftover
	// string in the previous language.
	feedback := h.getWithSaveFeedback(t, "/settings/language", rec)
	if !strings.Contains(feedback, `data-feedback-message="Guardado"`) {
		t.Errorf("the save toast must read the saved language, got: %.400s", feedback)
	}
	if strings.Contains(feedback, `data-feedback-message="Saved"`) {
		t.Errorf("the save toast must not read the previous language, got: %.400s", feedback)
	}

	got, err := h.store.SettingsStore().GetInterfaceLanguage(t.Context())
	if err != nil {
		t.Fatalf("read stored language: %v", err)
	}
	if got != application.InterfaceLanguageSpanish {
		t.Fatalf("stored language = %q, want %q", got, application.InterfaceLanguageSpanish)
	}

	body := sectionGet(t, h, "/settings/language")
	for _, want := range []string{
		`<html lang="es">`,
		`<h1 class="command-title">Configuración</h1>`,
		`<h2 class="settings-section-title">Idioma de la interfaz</h2>`,
		`<option value="es" selected>Español</option>`,
		`Guardar idioma`,
		`href="/settings/tickets">Tickets y SLA</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the language section in Spanish must contain %q, got: %s", want, body)
		}
	}
	for _, unwanted := range []string{`<h2 class="settings-section-title">Interface language</h2>`, `>Save language<`} {
		if strings.Contains(body, unwanted) {
			t.Errorf("the language section in Spanish must not render the English %q", unwanted)
		}
	}
}

// TestSettingsUpdateLanguageRejectsUnshippedCode proves a refused code is a 422
// that writes nothing, emits no save feedback, and reports the rejection in the
// language the instance is currently rendering — English here.
func TestSettingsUpdateLanguageRejectsUnshippedCode(t *testing.T) {
	h := newHarness(t)

	rec := h.postForm(t, "/settings/language", url.Values{"interface_language": {"fr"}}, false)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Save-Feedback"); got != "" {
		t.Errorf("rejected update must not emit save feedback, got %q", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `class="error-banner"`) {
		t.Errorf("rejected language must render the inline error banner, got: %s", body)
	}
	if !strings.Contains(body, "invalid interface language") {
		t.Errorf("rejection must read the current (English) language, got: %s", body)
	}
	if !strings.Contains(body, `value="en" selected`) {
		t.Errorf("the stored language must stay marked after a rejection, got: %s", body)
	}

	got, err := h.store.SettingsStore().GetInterfaceLanguage(t.Context())
	if err != nil {
		t.Fatalf("read stored language: %v", err)
	}
	if got != application.DefaultInterfaceLanguage {
		t.Errorf("stored language = %q after rejected update, want %q", got, application.DefaultInterfaceLanguage)
	}
}

// TestSettingsUpdateLanguageRejectionSpeaksTheCurrentLanguage proves the
// validation error follows the instance language too (S8): on a Spanish
// instance the refusal reads Spanish, and the stored value is untouched.
func TestSettingsUpdateLanguageRejectionSpeaksTheCurrentLanguage(t *testing.T) {
	h := newHarness(t)
	if err := h.store.SettingsStore().SetInterfaceLanguage(t.Context(), application.InterfaceLanguageSpanish); err != nil {
		t.Fatalf("seed Spanish instance: %v", err)
	}

	rec := h.postForm(t, "/settings/language", url.Values{"interface_language": {"en-US"}}, false)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<html lang="es">`) {
		t.Errorf("the rejection must re-render in the instance language, got: %s", body)
	}
	if !strings.Contains(body, "idioma de la interfaz no válido") {
		t.Errorf("the rejection must read Spanish on a Spanish instance, got: %s", body)
	}
	if strings.Contains(body, "invalid interface language") {
		t.Errorf("the rejection must not render the English message, got: %s", body)
	}

	got, err := h.store.SettingsStore().GetInterfaceLanguage(t.Context())
	if err != nil {
		t.Fatalf("read stored language: %v", err)
	}
	if got != application.InterfaceLanguageSpanish {
		t.Errorf("stored language = %q after rejected update, want %q", got, application.InterfaceLanguageSpanish)
	}
}

// TestSettingsUpdateLanguageDeniedForNonAdmin proves the new POST is gated on
// CapManageSettings like every other settings route: a user and an agent are
// refused server-side, emit no save feedback, and change nothing.
func TestSettingsUpdateLanguageDeniedForNonAdmin(t *testing.T) {
	for _, role := range []domain.Role{domain.RoleUser, domain.RoleAgent} {
		t.Run(string(role), func(t *testing.T) {
			h := newHarness(t)
			actor := seedUserRole(t, h.store, string(role), string(role)+"-language@tkt.test", role)
			session := seedSession(t, h.store, actor.ID)

			req := httptest.NewRequest(http.MethodPost, "/settings/language", strings.NewReader(url.Values{"interface_language": {"es"}}.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Cookie", sessionCookie+"="+session.ID)
			rec := httptest.NewRecorder()
			h.mw.Wrap(h.mux).ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Errorf("POST /settings/language as %s = %d, want 403", role, rec.Code)
			}
			if got := rec.Header().Get("X-Save-Feedback"); got != "" {
				t.Errorf("denied update must not emit save feedback, got %q", got)
			}
			got, err := h.store.SettingsStore().GetInterfaceLanguage(t.Context())
			if err != nil {
				t.Fatalf("read stored language: %v", err)
			}
			if got != application.DefaultInterfaceLanguage {
				t.Errorf("stored language = %q after denied update, want %q", got, application.DefaultInterfaceLanguage)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Automatic close of held tickets (issue #353, spec S2). The field lives in
// the Tickets & SLA section of Settings (the section the SLA panel owns, issue
// #383), is saved by its own POST (one panel per form, so one write per
// request), stores 0 when left empty — the feature is off — and refuses a
// window outside 0..60 with the catalogued message.
// ---------------------------------------------------------------------------

// holdAutoCloseInput is the exact field markup the tickets section renders, so a
// test can assert one attribute order without matching unrelated numbers.
func holdAutoCloseInput(value string) string {
	return `id="hold_auto_close_working_days" name="hold_auto_close_working_days" class="sla-warning-input" min="0" value="` + value + `"`
}

func storedHoldAutoCloseDays(t *testing.T, h *harness) int {
	t.Helper()
	days, err := h.store.SettingsStore().GetHoldAutoCloseDays(t.Context())
	if err != nil {
		t.Fatalf("read hold_auto_close_working_days: %v", err)
	}
	return days
}

// TestSettingsHoldAutoCloseWindowRendersOffAsEmpty proves the seeded instance
// (off) renders an EMPTY number field: "empty = off" is the panel's contract,
// so a stored 0 must never render a literal 0 the administrator could mistake
// for a one-day window.
func TestSettingsHoldAutoCloseWindowRendersOffAsEmpty(t *testing.T) {
	h := newHarness(t)

	body := sectionGet(t, h, "/settings/tickets")
	if !strings.Contains(body, holdAutoCloseInput("")) {
		t.Errorf("tickets section must render the hold auto-close field empty while off, got: %s", body)
	}
	if !strings.Contains(body, "Close tickets on hold after N working days without a reply") {
		t.Errorf("tickets section must label the field with the spec sentence, got: %s", body)
	}
}

// TestSettingsHoldAutoCloseWindowPersists proves a saved window survives and
// renders back, and that clearing it turns the feature off, without touching
// the SLA panel that shares the section.
func TestSettingsHoldAutoCloseWindowPersists(t *testing.T) {
	h := newHarness(t)
	wantEnabled, wantPercent, wantDefaults := slaSettingsFixture(t, h)

	rec := h.postForm(t, "/settings/hold-auto-close", url.Values{"hold_auto_close_working_days": {"3"}}, false)
	wantRedirect(t, rec, http.StatusSeeOther, "/settings/tickets")
	if got := storedHoldAutoCloseDays(t, h); got != 3 {
		t.Errorf("stored window = %d, want 3", got)
	}
	if body := sectionGet(t, h, "/settings/tickets"); !strings.Contains(body, holdAutoCloseInput("3")) {
		t.Errorf("tickets section must render the saved window, got: %s", body)
	}

	rec = h.postForm(t, "/settings/hold-auto-close", url.Values{"hold_auto_close_working_days": {""}}, false)
	wantRedirect(t, rec, http.StatusSeeOther, "/settings/tickets")
	if got := storedHoldAutoCloseDays(t, h); got != 0 {
		t.Errorf("stored window = %d after clearing, want 0 (off)", got)
	}
	if body := sectionGet(t, h, "/settings/tickets"); !strings.Contains(body, holdAutoCloseInput("")) {
		t.Errorf("tickets section must render the cleared field empty, got: %s", body)
	}
	assertSLASettingsEqual(t, h, wantEnabled, wantPercent, wantDefaults)
}

// TestSettingsHoldAutoCloseWindowRejectsOutOfRange proves every refused value
// re-renders 422 with the catalogued message, echoes what was submitted, and
// stores nothing.
func TestSettingsHoldAutoCloseWindowRejectsOutOfRange(t *testing.T) {
	h := newHarness(t)
	rec := h.postForm(t, "/settings/hold-auto-close", url.Values{"hold_auto_close_working_days": {"3"}}, false)
	wantRedirect(t, rec, http.StatusSeeOther, "/settings/tickets")

	for _, submitted := range []string{"99", "-1", "abc"} {
		t.Run(submitted, func(t *testing.T) {
			rec := h.postForm(t, "/settings/hold-auto-close", url.Values{"hold_auto_close_working_days": {submitted}}, false)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if !strings.Contains(body, `error-banner`) {
				t.Errorf("rejected window must render the inline error banner, got: %s", body)
			}
			if !strings.Contains(body, "the hold auto-close window must be between 0 and 60 working days") {
				t.Errorf("rejected window must render the catalogued message, got: %s", body)
			}
			// The re-render echoes the SUBMITTED value and the panels this POST
			// does not own, so one bad number costs the administrator nothing.
			if !strings.Contains(body, holdAutoCloseInput(submitted)) {
				t.Errorf("re-render must echo the submitted %q, got: %s", submitted, body)
			}
			if !strings.Contains(body, `<h2 class="settings-section-title">Tickets &amp; SLA</h2>`) {
				t.Errorf("re-render must be the tickets section, got: %s", body)
			}
			if !strings.Contains(body, `name="sla_warning_percent"`) {
				t.Errorf("re-render must keep the SLA panel, got: %s", body)
			}
			if got := rec.Header().Get("X-Save-Feedback"); got != "" {
				t.Errorf("rejected window must not emit save feedback, got %q", got)
			}
			if got := storedHoldAutoCloseDays(t, h); got != 3 {
				t.Errorf("stored window = %d after a rejected %q, want the previous 3", got, submitted)
			}
		})
	}
}

// TestSettingsHoldAutoCloseDeniedForNonAdmin proves the route is gated on the
// same capability as the rest of the SLA section, and a denied post writes
// nothing.
func TestSettingsHoldAutoCloseDeniedForNonAdmin(t *testing.T) {
	h := newHarness(t)
	user, err := h.users.Create(t.Context(), *h.admin, application.CreateUserInput{Name: "User", Email: "user@tkt.test", Password: "s3cret-pass-12"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	session := h.loginCookie(t, user.Email, "s3cret-pass-12")
	if session == "" {
		t.Fatal("user login must succeed")
	}

	req := httptest.NewRequest(http.MethodPost, "/settings/hold-auto-close", strings.NewReader(url.Values{"hold_auto_close_working_days": {"5"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", sessionCookie+"="+session)
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("non-admin POST /settings/hold-auto-close = %d, want 403", rec.Code)
	}
	if got := rec.Header().Get("X-Save-Feedback"); got != "" {
		t.Errorf("denied update must not emit save feedback, got %q", got)
	}
	if got := storedHoldAutoCloseDays(t, h); got != 0 {
		t.Errorf("stored window = %d after a denied post, want 0", got)
	}
}
