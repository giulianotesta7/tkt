package httpadapter

import (
	"io"
	"log"
	"net/http"

	"github.com/giulianotesta7/tkt/web/templates"
)

// RegisterStatic mounts the explicit embedded assets used by the application.
// Each route names one file so arbitrary template content is never exposed.
func RegisterStatic(mux *http.ServeMux) {
	registerEmbeddedStatic(mux, "/static/htmx.min.js", "static/htmx.min.js", "text/javascript; charset=utf-8")
	registerEmbeddedStatic(mux, "/static/users.css", "static/users.css", "text/css; charset=utf-8")
	registerEmbeddedStatic(mux, "/static/users.js", "static/users.js", "text/javascript; charset=utf-8")
}

func registerEmbeddedStatic(mux *http.ServeMux, route, name, contentType string) {
	mux.HandleFunc("GET "+route, func(w http.ResponseWriter, r *http.Request) {
		f, err := templates.FS.Open(name)
		if err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.WriteHeader(http.StatusOK)
		if _, err := io.Copy(w, f); err != nil {
			log.Printf("serve static %s: %v", name, err)
		}
	})
}
