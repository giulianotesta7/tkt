package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUsersStaticAssetsAreExplicitAndConditionallyLoaded(t *testing.T) {
	h := newHarness(t)
	for _, tc := range []struct{ path, kind, marker string }{{"/static/users.css", "text/css; charset=utf-8", ".users-drawer"}, {"/static/users.js", "text/javascript; charset=utf-8", "history.back"}} {
		rec := h.get(t, tc.path, false)
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != tc.kind || rec.Header().Get("Cache-Control") != "no-cache" || !strings.Contains(rec.Body.String(), tc.marker) {
			t.Fatalf("asset %s response = %d/%q", tc.path, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
	mux := http.NewServeMux()
	RegisterStatic(mux, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/users.html", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("arbitrary static path = %d, want 404", rec.Code)
	}
	for _, path := range []string{"/users", "/users/new"} {
		body := h.get(t, path, false).Body.String()
		if !strings.Contains(body, "/static/users.css") || !strings.Contains(body, "/static/users.js") {
			t.Errorf("%s omits Users assets", path)
		}
	}
	if body := h.get(t, "/tickets", false).Body.String(); !strings.Contains(body, "/static/users.css") || strings.Contains(body, "/static/users.js") {
		t.Error("tickets must load the shared page-foundation stylesheet without Users JavaScript")
	}
	if body := h.get(t, "/login", false).Body.String(); strings.Contains(body, "/static/users.css") || strings.Contains(body, "/static/users.js") {
		t.Error("unrelated auth page loads Users assets")
	}
}

// The Users visual refactor (issue #394, S2/S3/S4) is a stylesheet contract as
// much as a markup one: the list recovered the page width, so its table scrolls
// inside its own container instead of widening the page, and the drawer keeps
// one column whose lifecycle section and sticky actions the user always sees.
func TestUsersStylesheetFullWidthListAndDrawerSections(t *testing.T) {
	h := newHarness(t)
	css := h.get(t, "/static/users.css", false).Body.String()
	for _, tc := range []struct {
		name         string
		selectors    []string
		declarations []string
	}{
		{"Users-only header spacing", []string{".users-root :where(#users-background) .users-header"}, []string{"margin-bottom:20px"}},
		{"lifecycle section", []string{".users-root #users-drawer-host .users-lifecycle"}, []string{"border-top:1px solid var(--line)"}},
		{"drawer actions", []string{".users-root #users-drawer-host .users-drawer-footer"}, []string{"position:sticky", "bottom:0"}},
	} {
		assertCSSRuleContains(t, css, tc.name, tc.selectors, tc.declarations)
	}
	// The rows pan inside the panel at phone width, which is what keeps the page
	// itself from scrolling sideways (issue #394, S4).
	if !strings.Contains(css, ".users-root .users-list{min-width:0;max-width:100%;overflow:auto;position:relative}") {
		t.Error("the Users list must scroll the table inside its own panel at phone width")
	}
	// A full-width drawer at phone width, never wider than the viewport.
	if !strings.Contains(css, "width:min(440px,100vw)") {
		t.Error("the Users drawer must cap at 440px and stay inside a 390px viewport")
	}
	if strings.Contains(css, ".users-access-card") {
		t.Error("users.css still styles the Access levels card that option A removed")
	}
}
