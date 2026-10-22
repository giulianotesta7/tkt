package httpadapter

import (
	"io"
	"log"
	"net/http"

	"github.com/giulianotesta7/tkt/internal/i18n"
	"github.com/giulianotesta7/tkt/web/templates"
)

// RegisterStatic mounts the explicit embedded assets used by the application.
// Each route names one file so arbitrary template content is never exposed.
//
// language resolves the instance interface language for the scripts that render
// catalogue-backed copy (issue #335, S8). A nil source answers the default
// locale, which keeps a router built without settings (a unit test, a fixture)
// serving working assets.
func RegisterStatic(mux *http.ServeMux, language InterfaceLanguageSource) {
	registerEmbeddedStatic(mux, "/static/htmx.min.js", "static/htmx.min.js", "text/javascript; charset=utf-8", "public, max-age=86400")
	registerEmbeddedStatic(mux, "/static/InterVariable.woff2", "static/InterVariable.woff2", "font/woff2", "public, max-age=86400")
	registerEmbeddedStatic(mux, "/static/users.css", "static/users.css", "text/css; charset=utf-8", "no-cache")
	registerEmbeddedStatic(mux, "/static/ticket_metrics.css", "static/ticket_metrics.css", "text/css; charset=utf-8", "no-cache")
	// The settings section stylesheet is loaded only on settings pages (the
	// SettingsAssets flag), so the rest of the interface never downloads it.
	registerEmbeddedStatic(mux, "/static/settings.css", "static/settings.css", "text/css; charset=utf-8", "no-cache")
	// The dirty-tracking script renders no copy of its own, so it needs no
	// catalogue prologue and can be cached like the other copy-free assets.
	registerEmbeddedStatic(mux, "/static/settings.js", "static/settings.js", "text/javascript; charset=utf-8", "public, max-age=86400")
	registerLocalizedStatic(mux, "/static/users.js", "static/users.js", language)
	registerLocalizedStatic(mux, "/static/tickets.js", "static/tickets.js", language)
	registerLocalizedStatic(mux, "/static/ticket_form.js", "static/ticket_form.js", language)
	registerLocalizedStatic(mux, "/static/workflow.js", "static/workflow.js", language)
	registerLocalizedStatic(mux, "/static/categories.js", "static/categories.js", language)
	registerLocalizedStatic(mux, "/static/save-feedback.js", "static/save-feedback.js", language)
	registerLocalizedStatic(mux, "/static/ticket_metrics.js", "static/ticket_metrics.js", language)
	registerLocalizedStatic(mux, "/static/sla_countdown.js", "static/sla_countdown.js", language)
	// The notification drawer moves focus; it renders no copy, so it needs no
	// catalogue prologue and can be cached like the other copy-free assets.
	registerEmbeddedStatic(mux, "/static/notifications.js", "static/notifications.js", "text/javascript; charset=utf-8", "public, max-age=86400")
	registerEmbeddedStatic(mux, "/static/shell.js", "static/shell.js", "text/javascript; charset=utf-8", "public, max-age=86400")
	registerEmbeddedStatic(mux, "/static/queue.js", "static/queue.js", "text/javascript; charset=utf-8", "public, max-age=86400")
}

func registerEmbeddedStatic(mux *http.ServeMux, route, name, contentType, cacheControl string) {
	mux.HandleFunc("GET "+route, func(w http.ResponseWriter, r *http.Request) {
		f, err := templates.FS.Open(name)
		if err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", cacheControl)
		w.WriteHeader(http.StatusOK)
		if _, err := io.Copy(w, f); err != nil {
			log.Printf("serve static %s: %v", name, err)
		}
	})
}

// registerLocalizedStatic serves one script with the active instance language's
// js.* catalogue prepended as the window.tktI18n prologue. The server therefore
// owns every user-facing string a script renders: the script keeps no English
// literal, and switching the instance language switches its copy too.
func registerLocalizedStatic(mux *http.ServeMux, route, name string, language InterfaceLanguageSource) {
	failures := &languageReadLog{}
	mux.HandleFunc("GET "+route, func(w http.ResponseWriter, r *http.Request) {
		f, err := templates.FS.Open(name)
		if err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		defer f.Close()
		// no-cache, because the body now depends on the instance setting: a
		// cached script would keep rendering the language it was fetched in.
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, i18n.Script(requestInterfaceLanguage(r.Context(), language, failures)))
		if _, err := io.Copy(w, f); err != nil {
			log.Printf("serve static %s: %v", name, err)
		}
	})
}
