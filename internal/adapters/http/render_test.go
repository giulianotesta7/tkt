package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
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
