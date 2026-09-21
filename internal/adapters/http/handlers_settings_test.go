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

// Instance appearance, SLA, and working-calendar settings
// (handlers_settings.go): GET /settings is admin/root-only and shows the
// current color, the SLA panel, and the working-calendar panel;
// POST /settings/appearance persists a valid color, POST /settings/sla
// persists the SLA panel, POST /settings/calendar persists the working
// calendar, and each rejects an invalid submission with an inline error
// that re-renders EVERY panel; the shell CSS follows the stored value.

func TestSettingsIndexRequiresAdmin(t *testing.T) {
	h := newHarness(t)
	user, err := h.users.Create(t.Context(), *h.admin, application.CreateUserInput{Name: "User", Email: "user@tkt.test", Password: "secret"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	session := h.loginCookie(t, user.Email, "secret")
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

func TestSettingsIndexShowsCurrentColor(t *testing.T) {
	h := newHarness(t)

	rec := h.get(t, "/settings", false)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`href="/settings"`, // rail link present for the admin shell
		`>Settings</h1>`,   // page title
		`Appearance`,       // panel heading
		`name="internal_comment_bg" value="#E8EEFF"`,
		`--internal-comment-bg:#E8EEFF;`, // shell CSS carries the seeded default
		// SLA panel: seeded warning percent, the feature seeded OFF, and the
		// seeded default matrix decomposed into the grid's h/m/s units
		// (critical 1800s/14400s, low 28800s/259200s).
		`min="1" max="99" value="80"`,
		`name="sla_enabled" value="1"> Enable SLA targets`,
		`name="first_response_m_critical" value="30"`,
		`name="resolve_h_critical" value="4"`,
		`name="first_response_h_low" value="8"`,
		`name="resolve_h_low" value="72"`,
		// Calendar panel: the seeded default calendar (migration 0016) —
		// Monday-Friday checked, Saturday and Sunday unchecked, a 09:00-18:00
		// window, and the UTC zone.
		`<h2>Working calendar</h2>`,
		`name="sla_calendar_days" value="1" checked`,
		`name="sla_calendar_days" value="2" checked`,
		`name="sla_calendar_days" value="3" checked`,
		`name="sla_calendar_days" value="4" checked`,
		`name="sla_calendar_days" value="5" checked`,
		`name="sla_calendar_days" value="6"> Saturday`,
		`name="sla_calendar_days" value="7"> Sunday`,
		`id="sla_calendar_start" name="sla_calendar_start" value="09:00"`,
		`id="sla_calendar_end" name="sla_calendar_end" value="18:00"`,
		`id="sla_calendar_timezone" name="sla_calendar_timezone" value="UTC"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("settings page must contain %q, got: %s", want, body)
		}
	}
	for _, absent := range []string{
		`name="sla_calendar_days" value="6" checked`,
		`name="sla_calendar_days" value="7" checked`,
	} {
		if strings.Contains(body, absent) {
			t.Errorf("settings page must not contain %q (Saturday and Sunday seed unchecked), got: %s", absent, body)
		}
	}
	if strings.Contains(body, `name="sla_enabled" value="1" checked`) {
		t.Errorf("sla_enabled seeds OFF, the panel must render it unchecked, got: %s", body)
	}
}

func TestSettingsUpdatePersistsAndRedirects(t *testing.T) {
	h := newHarness(t)

	rec := h.postForm(t, "/settings/appearance", url.Values{"internal_comment_bg": {"#EFE9FB"}}, false)

	wantRedirect(t, rec, http.StatusSeeOther, "/settings")

	got, err := h.store.SettingsStore().GetInternalCommentBg(t.Context())
	if err != nil {
		t.Fatalf("read stored color: %v", err)
	}
	if got != "#EFE9FB" {
		t.Errorf("stored bg = %q, want %q", got, "#EFE9FB")
	}

	// The following GET renders the new color as selected AND in the CSS.
	rec = h.get(t, "/settings", false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`value="#EFE9FB" checked`,
		`--internal-comment-bg:#EFE9FB;`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("settings page after update must contain %q, got: %s", want, body)
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
	if got := rec.Header().Get("X-Save-Feedback"); got != "" {
		t.Errorf("rejected update must not emit save feedback, got %q", got)
	}

	got, err := h.store.SettingsStore().GetInternalCommentBg(t.Context())
	if err != nil {
		t.Fatalf("read stored color: %v", err)
	}
	if got != "#E8EEFF" {
		t.Errorf("bg = %q after rejected update, want %q", got, "#E8EEFF")
	}
}

// TestSettingsUpdateDeniedForNonAdmin proves the POST is gated on the same
// capability as the page (server-side check, not markup hiding).
func TestSettingsUpdateDeniedForNonAdmin(t *testing.T) {
	h := newHarness(t)
	user, err := h.users.Create(t.Context(), *h.admin, application.CreateUserInput{Name: "User", Email: "user@tkt.test", Password: "secret"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	session := h.loginCookie(t, user.Email, "secret")
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
	if got != "#E8EEFF" {
		t.Errorf("bg = %q after denied update, want %q", got, "#E8EEFF")
	}
}

// TestSettingsRailLinkCapabilityIsolation proves the Settings rail link is
// driven by CanManageSettings and the Users link by CanManageUsers, in both
// directions. The two role sets coincide today (admin and root grant both),
// so the presentation flags are the only place this split is observable — and
// conflating them is exactly the coupling the split removes.
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
			// The title attribute is unique to the rail anchor; a bare href also
			// matches the in-page "All" filter link and "Back to users".
			if got := strings.Contains(body, `href="/users" title="Users"`); got != tc.wantUsersLink {
				t.Errorf("users rail link present = %t, want %t", got, tc.wantUsersLink)
			}
			if got := strings.Contains(body, `href="/settings" title="Settings"`); got != tc.wantSettingsLink {
				t.Errorf("settings rail link present = %t, want %t", got, tc.wantSettingsLink)
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
// three SLA settings groups and redirects 303 back to /settings, and that
// the following GET renders the new values.
func TestSettingsSLAPersistsAndRedirects(t *testing.T) {
	h := newHarness(t)

	rec := h.postForm(t, "/settings/sla", slaPanelForm("90"), false)

	wantRedirect(t, rec, http.StatusSeeOther, "/settings")

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
	body := h.get(t, "/settings", false).Body.String()
	for _, want := range []string{
		`value="90"`,
		`name="first_response_h_critical" value="1"`,
		`name="resolve_h_critical" value="4"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("settings page after SLA save must contain %q, got: %s", want, body)
		}
	}
}

// TestSettingsSLAPersistsEnabled proves the checkbox enables the feature.
func TestSettingsSLAPersistsEnabled(t *testing.T) {
	h := newHarness(t)

	form := slaPanelForm("80")
	form.Set("sla_enabled", "1")
	rec := h.postForm(t, "/settings/sla", form, false)

	wantRedirect(t, rec, http.StatusSeeOther, "/settings")

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
	user, err := h.users.Create(t.Context(), *h.admin, application.CreateUserInput{Name: "User", Email: "user@tkt.test", Password: "secret"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	session := h.loginCookie(t, user.Email, "secret")
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
	user, err := h.users.Create(t.Context(), *h.admin, application.CreateUserInput{Name: "User", Email: "user@tkt.test", Password: "secret"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	session := h.loginCookie(t, user.Email, "secret")
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
// re-renders 422 with the inline error banner and leaves the store exactly
// as it was — the targets and the enabled flag submitted alongside it are
// not persisted either.
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
// persists all four sla_calendar_* keys and redirects 303 back to
// /settings, and that the following GET renders the persisted values.
func TestSettingsCalendarPersistsAndRedirects(t *testing.T) {
	h := newHarness(t)

	form := url.Values{
		"sla_calendar_days":     {"1", "3", "6"},
		"sla_calendar_start":    {"08:30"},
		"sla_calendar_end":      {"17:45"},
		"sla_calendar_timezone": {"America/Argentina/Buenos_Aires"},
	}
	rec := h.postForm(t, "/settings/calendar", form, false)

	wantRedirect(t, rec, http.StatusSeeOther, "/settings")

	// The unknown submitted days dropped out and the survivors kept the
	// canonical ISO order; the window and the zone round-trip.
	assertCalendarEqual(t, readStoredCalendar(t, h), domain.SLACalendar{
		WorkingDays: []time.Weekday{time.Monday, time.Wednesday, time.Saturday},
		StartMinute: 8*60 + 30,
		EndMinute:   17*60 + 45,
		Location:    mustLoadLocation(t, "America/Argentina/Buenos_Aires"),
	})

	// The following GET renders the persisted values.
	body := h.get(t, "/settings", false).Body.String()
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
			t.Errorf("settings page after calendar save must contain %q, got: %s", want, body)
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

// TestSettingsRejectionsPopulateSiblingPanels proves every settings
// rejection re-renders EVERY panel: a rejected post on one panel must not
// blank the two sibling panels.
func TestSettingsRejectionsPopulateSiblingPanels(t *testing.T) {
	// The seeded storage values each sibling panel must keep showing.
	slaSeed := []string{
		`min="1" max="99" value="80"`,
		`name="first_response_m_critical" value="30"`,
		`name="resolve_h_critical" value="4"`,
	}
	calendarSeed := []string{
		`name="sla_calendar_days" value="1" checked`,
		`name="sla_calendar_days" value="6"> Saturday`,
		`id="sla_calendar_start" name="sla_calendar_start" value="09:00"`,
		`id="sla_calendar_end" name="sla_calendar_end" value="18:00"`,
		`id="sla_calendar_timezone" name="sla_calendar_timezone" value="UTC"`,
	}

	t.Run("rejected appearance keeps SLA and calendar panels", func(t *testing.T) {
		h := newHarness(t)
		rec := h.postForm(t, "/settings/appearance", url.Values{"internal_comment_bg": {"#123456"}}, false)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", rec.Code)
		}
		for _, panel := range [][]string{slaSeed, calendarSeed} {
			for _, want := range panel {
				if !strings.Contains(rec.Body.String(), want) {
					t.Errorf("rejected appearance must keep rendering %q, got: %s", want, rec.Body.String())
				}
			}
		}
	})

	t.Run("rejected SLA keeps the calendar panel", func(t *testing.T) {
		h := newHarness(t)
		rec := h.postForm(t, "/settings/sla", slaPanelForm("150"), false)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", rec.Code)
		}
		for _, want := range calendarSeed {
			if !strings.Contains(rec.Body.String(), want) {
				t.Errorf("rejected SLA must keep rendering %q, got: %s", want, rec.Body.String())
			}
		}
	})

	t.Run("rejected calendar keeps the SLA panel", func(t *testing.T) {
		h := newHarness(t)
		form := url.Values{
			"sla_calendar_days":     {"1"},
			"sla_calendar_start":    {"18:00"},
			"sla_calendar_end":      {"09:00"},
			"sla_calendar_timezone": {"UTC"},
		}
		rec := h.postForm(t, "/settings/calendar", form, false)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", rec.Code)
		}
		for _, want := range slaSeed {
			if !strings.Contains(rec.Body.String(), want) {
				t.Errorf("rejected calendar must keep rendering %q, got: %s", want, rec.Body.String())
			}
		}
	})
}
