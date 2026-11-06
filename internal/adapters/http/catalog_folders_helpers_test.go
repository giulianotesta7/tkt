package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
)

// id formats an id for a URL, which every catalog assertion needs.
func id(value int64) string { return strconv.FormatInt(value, 10) }

// hxGet runs one authenticated HTMX fragment request through the harness mux.
func hxGet(t *testing.T, h *harness, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)
	return rec
}

// seedFolder creates a folder through the real folder service and returns its id.
func seedFolder(t *testing.T, h *harness, parent *int64, name string) int64 {
	t.Helper()
	f, err := application.NewFolderService(h.store.FolderStore(), h.clock).CreateFor(t.Context(), *h.admin, parent, name, "")
	if err != nil {
		t.Fatalf("create folder %q: %v", name, err)
	}
	return f.ID
}
