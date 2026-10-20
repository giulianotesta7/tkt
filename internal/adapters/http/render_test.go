package httpadapter

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/internal/i18n"
	"github.com/giulianotesta7/tkt/web/templates"
)

// fixtureFS is the render-contract fixture template set: a shell root, one
// page defining "content", and two fragments. It exercises the real parse +
// render pipeline (ParseFS, per-page sets, shared fragment set) without
// depending on the production templates that land in later tasks.
func fixtureFS() fstest.MapFS {
	return fstest.MapFS{
		"base.html":                      &fstest.MapFile{Data: []byte(`<!DOCTYPE html><html><head><title>fixture shell</title></head><body data-shell="base">{{template "content" .}}</body></html>`)},
		"pages/fixture.html":             &fstest.MapFile{Data: []byte(`{{define "content"}}<h1>Fixture Page</h1><p>{{.Title}}</p><time datetime="{{formatDatetime .Time}}">{{formatTime .Time}}</time>{{end}}`)},
		"partials/fixture_fragment.html": &fstest.MapFile{Data: []byte(`{{define "fixture_fragment"}}<section data-fragment="1">{{.Title}} @ {{formatTime .Time}}</section>{{end}}`)},
		"partials/fixture_other.html":    &fstest.MapFile{Data: []byte(`{{define "fixture_other"}}<span>other</span>{{end}}`)},
	}
}

type fixtureData struct {
	Title string
	Time  time.Time
}

var fixtureTime = time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)

func mustFixtureRenderer(t *testing.T) *Renderer {
	t.Helper()
	r, err := NewRendererWith(fixtureFS())
	if err != nil {
		t.Fatalf("NewRendererWith(fixtureFS): %v", err)
	}
	return r
}

// TestRenderFullPageWithoutHX proves D6's full-page path: when HX-Request is
// absent the shell root executes and the response carries <html> plus the
// page content.
func TestRenderFullPageWithoutHX(t *testing.T) {
	r := mustFixtureRenderer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/fixture", nil)

	r.Render(rec, req, "fixture", "fixture_fragment", fixtureData{Title: "Login page down", Time: fixtureTime}, http.StatusOK)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<html>") {
		t.Errorf("full page must contain <html>, got: %s", body)
	}
	if !strings.Contains(body, `data-shell="base"`) {
		t.Errorf("full page must render the shell, got: %s", body)
	}
	if !strings.Contains(body, "Fixture Page") {
		t.Errorf("full page must render page content, got: %s", body)
	}
	if !strings.Contains(body, `<time datetime="2026-08-06T10:00:00Z">10:00 · 06-08-2026</time>`) {
		t.Errorf("full page must render semantic RFC3339 with a human UTC label, got: %s", body)
	}
}

func TestHumanizeLabel(t *testing.T) {
	for _, tt := range []struct {
		name  string
		value any
		want  string
	}{
		{name: "state", value: "in_progress", want: "In Progress"},
		{name: "priority", value: domain.PriorityCritical, want: "Critical"},
		{name: "action", value: domain.ActionTransition, want: "Transition"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := humanizeLabel(tt.value); got != tt.want {
				t.Errorf("humanizeLabel(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

// TestFormatCardTime pins the English card timestamp (issue #122): cards show
// a compact exact UTC label ("21:42 · 13 Sep") distinct from the global
// formatTime contract, which remains the accessible full date. The localized
// formatter keeps exactly those bytes for the default locale (S13).
func TestFormatCardTime(t *testing.T) {
	en := i18n.New(i18n.Default)
	utc := time.Date(2025, 9, 13, 21, 42, 7, 0, time.UTC)
	if got := formatClockMonthDay(en, utc); got != "21:42 · 13 Sep" {
		t.Errorf("formatClockMonthDay(en, utc) = %q, want %q", got, "21:42 · 13 Sep")
	}
	// Non-UTC instants convert to UTC before formatting (D7 display contract).
	offset := time.FixedZone("test", -4*60*60)
	if got := formatClockMonthDay(en, time.Date(2025, 9, 13, 17, 42, 0, 0, offset)); got != "21:42 · 13 Sep" {
		t.Errorf("formatClockMonthDay(en, offset) = %q, want %q", got, "21:42 · 13 Sep")
	}
	if got := formatClockMonthDay(en, time.Time{}); got != "" {
		t.Errorf("formatClockMonthDay(en, zero) = %q, want empty", got)
	}
}

// TestCardTimestampContractsPreserved proves the card timestamp reuses the
// existing full-date contracts: an RFC3339 datetime attribute and a full
// accessible date that includes the year.
func TestCardTimestampContractsPreserved(t *testing.T) {
	utc := time.Date(2025, 9, 13, 21, 42, 0, 0, time.UTC)
	if got := formatDatetime(utc); got != "2025-09-13T21:42:00Z" {
		t.Errorf("formatDatetime(utc) = %q, want RFC3339 %q", got, "2025-09-13T21:42:00Z")
	}
	if got := formatDatetime(time.Time{}); got != "" {
		t.Errorf("formatDatetime(zero) = %q, want empty", got)
	}
	if got := formatDisplayTime(utc); got != "21:42 · 13-09-2025" {
		t.Errorf("formatDisplayTime(utc) = %q, want full date %q (must include the year)", got, "21:42 · 13-09-2025")
	}
}

// TestRenderFragmentOnHX proves D6's fragment path: with HX-Request present
// ONLY the named fragment executes — no <html>, no shell, no page content.
func TestRenderFragmentOnHX(t *testing.T) {
	r := mustFixtureRenderer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/fixture", nil)
	req.Header.Set("HX-Request", "true")

	r.Render(rec, req, "fixture", "fixture_fragment", fixtureData{Title: "Login page down", Time: fixtureTime}, http.StatusOK)

	body := rec.Body.String()
	if strings.Contains(body, "<html>") {
		t.Errorf("HX fragment must NOT contain <html>, got: %s", body)
	}
	if strings.Contains(body, "Fixture Page") {
		t.Errorf("HX fragment must not render the page body, got: %s", body)
	}
	if !strings.Contains(body, `data-fragment="1"`) {
		t.Errorf("HX fragment must render the named fragment, got: %s", body)
	}
	if !strings.Contains(body, "Login page down") {
		t.Errorf("HX fragment must render its data, got: %s", body)
	}
}

// TestRenderFragmentEmptyNameOnHX proves the natural-fragment path: an HX
// request with no dedicated fragment executes the page's "content" block
// (login/setup forms), still without the shell.
func TestRenderFragmentEmptyNameOnHX(t *testing.T) {
	r := mustFixtureRenderer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/fixture", nil)
	req.Header.Set("HX-Request", "true")

	r.Render(rec, req, "fixture", "", fixtureData{Title: "Login page down", Time: fixtureTime}, http.StatusOK)

	body := rec.Body.String()
	if strings.Contains(body, "<html>") {
		t.Errorf("HX content fragment must NOT contain <html>, got: %s", body)
	}
	if !strings.Contains(body, "Fixture Page") {
		t.Errorf("HX content fragment must render the page content block, got: %s", body)
	}
}

// TestRenderStatusPassthrough proves the status code travels in the response
// (422 re-renders keep their status on both render paths).
func TestRenderStatusPassthrough(t *testing.T) {
	r := mustFixtureRenderer(t)

	for _, tt := range []struct {
		name string
		hx   bool
	}{
		{"full page", false},
		{"fragment", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/fixture", nil)
			if tt.hx {
				req.Header.Set("HX-Request", "true")
			}
			r.Render(rec, req, "fixture", "fixture_fragment", fixtureData{Title: "x", Time: fixtureTime}, http.StatusUnprocessableEntity)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want 422", rec.Code)
			}
		})
	}
}

// TestRenderUnknownPage proves a render request for a page that does not
// exist degrades to a 500, never a panic.
func TestRenderUnknownPage(t *testing.T) {
	r := mustFixtureRenderer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/nope", nil)

	r.Render(rec, req, "nope", "", fixtureData{Title: "x", Time: fixtureTime}, http.StatusOK)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Internal server error") {
		t.Errorf("body must be the generic 500 text, got: %s", rec.Body.String())
	}
}

// --- Interface language (issue #335, S7/S15) ---

// stubInterfaceLanguage answers a fixed interface language, or a fixed read
// failure, standing in for the instance settings store the composition root
// injects.
type stubInterfaceLanguage struct {
	language string
	err      error
}

func (s stubInterfaceLanguage) GetInterfaceLanguage(context.Context) (string, error) {
	return s.language, s.err
}

// localizedShellFS copies the REAL shell roots (base.html, auth.html) and the
// partials they include from the embedded template tree, plus the login page
// and a trivial fixture page on the rail shell. A locale test therefore asserts
// the production copy of the shells without parsing the whole application tree.
func localizedShellFS(t *testing.T) fstest.MapFS {
	t.Helper()
	fsys := fstest.MapFS{
		"pages/shell.html":       &fstest.MapFile{Data: []byte(`{{define "content"}}<h1>{{t "base.nav.tickets"}}</h1>{{end}}`)},
		"pages/missing_key.html": &fstest.MapFile{Data: []byte(`{{define "content"}}<p>{{t "base.nav.this_key_does_not_exist"}}</p>{{end}}`)},
	}
	for _, name := range []string{
		"base.html",
		"auth.html",
		"pages/login.html",
		"partials/styles.html",
		"partials/save_feedback.html",
	} {
		data, err := fs.ReadFile(templates.FS, name)
		if err != nil {
			t.Fatalf("read embedded template %s: %v", name, err)
		}
		fsys[name] = &fstest.MapFile{Data: data}
	}
	return fsys
}

// TestRenderDeclaresAndRendersTheInstanceLanguage proves S15 and the shell half
// of S8/S13: <html lang> follows the instance setting, and the shell copy comes
// from the catalogue of that language — including the read-failure and
// unknown-value degradations, which must render the default language rather
// than a broken page.
func TestRenderDeclaresAndRendersTheInstanceLanguage(t *testing.T) {
	for _, tc := range []struct {
		name       string
		source     stubInterfaceLanguage
		wantLang   string
		wantCopy   string
		absentCopy string
	}{
		{name: "english", source: stubInterfaceLanguage{language: "en"}, wantLang: "en", wantCopy: "Welcome to tkt", absentCopy: "Te damos la bienvenida a tkt"},
		{name: "spanish", source: stubInterfaceLanguage{language: "es"}, wantLang: "es", wantCopy: "Te damos la bienvenida a tkt", absentCopy: "Welcome to tkt"},
		{name: "unknown value degrades to the default", source: stubInterfaceLanguage{language: "fr"}, wantLang: "en", wantCopy: "Welcome to tkt", absentCopy: "Te damos la bienvenida a tkt"},
		{name: "empty value degrades to the default", source: stubInterfaceLanguage{language: ""}, wantLang: "en", wantCopy: "Welcome to tkt", absentCopy: "Te damos la bienvenida a tkt"},
		{name: "read failure degrades to the default", source: stubInterfaceLanguage{err: errors.New("settings unavailable")}, wantLang: "en", wantCopy: "Welcome to tkt", absentCopy: "Te damos la bienvenida a tkt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := newRendererWithLanguage(localizedShellFS(t), tc.source)
			if err != nil {
				t.Fatalf("newRendererWithLanguage: %v", err)
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/login", nil)

			r.Render(rec, req, "login", "", loginData{}, http.StatusOK)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if !strings.Contains(body, `<html lang="`+tc.wantLang+`">`) {
				t.Errorf("auth shell must declare lang=%q, got: %s", tc.wantLang, firstLine(body))
			}
			if !strings.Contains(body, tc.wantCopy) {
				t.Errorf("auth shell must render %q, got: %s", tc.wantCopy, body)
			}
			if strings.Contains(body, tc.absentCopy) {
				t.Errorf("auth shell must not render %q, got: %s", tc.absentCopy, body)
			}
		})
	}
}

// TestRenderRailShellFollowsTheInstanceLanguage proves the base.html half of
// the same contract: the rail nav, its accessible labels, and the log-out
// control follow the active catalogue, and a key rendered inside a page body
// resolves through the same per-request language.
func TestRenderRailShellFollowsTheInstanceLanguage(t *testing.T) {
	for _, tc := range []struct {
		name         string
		language     string
		wantLang     string
		wantNav      string
		absentNav    string
		wantLogout   string
		absentLogout string
	}{
		{name: "english", language: "en", wantLang: "en", wantNav: "Tickets", absentNav: "Solicitudes", wantLogout: "Log out", absentLogout: "Cerrar sesión"},
		{name: "spanish", language: "es", wantLang: "es", wantNav: "Tickets", absentNav: "Solicitudes", wantLogout: "Cerrar sesión", absentLogout: `title="Log out"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := newRendererWithLanguage(localizedShellFS(t), stubInterfaceLanguage{language: tc.language})
			if err != nil {
				t.Fatalf("newRendererWithLanguage: %v", err)
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/shell", nil)

			r.Render(rec, req, "shell", "", pageData{NavActive: "tickets"}, http.StatusOK)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if !strings.Contains(body, `<html lang="`+tc.wantLang+`">`) {
				t.Errorf("rail shell must declare lang=%q, got: %s", tc.wantLang, firstLine(body))
			}
			for _, want := range []string{`title="` + tc.wantNav + `"`, `aria-label="` + tc.wantNav + `"`, `<h1>` + tc.wantNav + `</h1>`, tc.wantLogout} {
				if !strings.Contains(body, want) {
					t.Errorf("rail shell must render %q, got: %s", want, body)
				}
			}
			if strings.Contains(body, tc.absentNav) {
				t.Errorf("rail shell must not render %q, got: %s", tc.absentNav, body)
			}
			if strings.Contains(body, tc.absentLogout) {
				t.Errorf("rail shell must not render %q, got: %s", tc.absentLogout, body)
			}
		})
	}
}

// TestRenderMissingKeyNeverRendersRaw proves S10 through the real render path: a
// key absent from every catalogue degrades to a readable label, never to the key
// itself.
func TestRenderMissingKeyNeverRendersRaw(t *testing.T) {
	r, err := newRendererWithLanguage(localizedShellFS(t), stubInterfaceLanguage{language: "es"})
	if err != nil {
		t.Fatalf("newRendererWithLanguage: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/missing_key", nil)

	r.Render(rec, req, "missing_key", "", pageData{}, http.StatusOK)

	body := rec.Body.String()
	if strings.Contains(body, "base.nav.this_key_does_not_exist") {
		t.Errorf("a missing key rendered raw: %s", body)
	}
	if !strings.Contains(body, "This key does not exist") {
		t.Errorf("a missing key must degrade to a readable label, got: %s", body)
	}
}

// TestShippedLocalesMatchTheSettingsVocabulary proves the catalogue set and the
// setting's accepted values cannot drift: a language the setting accepts with no
// catalogue would render English while claiming to be Spanish, and a catalogue
// the setting refuses would be unreachable.
func TestShippedLocalesMatchTheSettingsVocabulary(t *testing.T) {
	got := i18n.Supported()
	want := application.AllowedInterfaceLanguages()
	if len(got) != len(want) {
		t.Fatalf("i18n.Supported() = %v, want the settings vocabulary %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("i18n.Supported()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if i18n.Default != application.DefaultInterfaceLanguage {
		t.Errorf("i18n default = %q, want %q", i18n.Default, application.DefaultInterfaceLanguage)
	}
}

// firstLine trims a document to its head line so a failure message stays
// readable.
func firstLine(body string) string {
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		return body[:i]
	}
	return body
}

// TestRenderWithoutLanguageSourceAnswersTheDefaultLocale preserves the
// fixture/harness contract: a renderer built with NewRendererWith has no
// instance settings behind it, so it answers the default locale — the same
// English markup every existing fixture and golden assertion expects.
func TestRenderWithoutLanguageSourceAnswersTheDefaultLocale(t *testing.T) {
	r, err := NewRendererWith(localizedShellFS(t))
	if err != nil {
		t.Fatalf("NewRendererWith: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/shell", nil)

	r.Render(rec, req, "shell", "", pageData{NavActive: "tickets"}, http.StatusOK)

	body := rec.Body.String()
	if !strings.Contains(body, `<html lang="en">`) {
		t.Errorf("a renderer without a language source must declare the default locale, got: %s", firstLine(body))
	}
	if !strings.Contains(body, `title="Tickets"`) {
		t.Errorf("a renderer without a language source must render English copy, got: %s", body)
	}
}
