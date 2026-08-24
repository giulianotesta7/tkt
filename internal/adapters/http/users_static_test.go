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
	RegisterStatic(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/users.html", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("arbitrary static path = %d, want 404", rec.Code)
	}
	for _, path := range []string{"/users/new", "/tickets"} {
		if body := h.get(t, path, false).Body.String(); strings.Contains(body, "/static/users.css") || strings.Contains(body, "/static/users.js") {
			t.Errorf("%s loads Users assets", path)
		}
	}
	body := h.get(t, "/users", false).Body.String()
	if !strings.Contains(body, "/static/users.css") || !strings.Contains(body, "/static/users.js") {
		t.Error("Users page omits scoped assets")
	}
}
