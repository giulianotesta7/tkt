package httpadapter

import (
	"io"
	"log"
	"net/http"

	"github.com/giulianotesta7/tkt/web/templates"
)

// RegisterStatic mounts the vendored static assets (web/static, embedded via
// the templates bridge package). Only htmx.min.js ships today; the route is
// explicit and read-only so the app stays dependency-free at runtime and
// never serves user content. Composition roots call this next to the other
// Register methods (design D9 route table; task 5.4).
func RegisterStatic(mux *http.ServeMux) {
	mux.HandleFunc("GET /static/htmx.min.js", func(w http.ResponseWriter, r *http.Request) {
		f, err := templates.FS.Open("static/htmx.min.js")
		if err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		// Vendored and version-pinned: safe to cache aggressively.
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.WriteHeader(http.StatusOK)
		if _, err := io.Copy(w, f); err != nil {
			log.Printf("serve static htmx: %v", err)
		}
	})
}
