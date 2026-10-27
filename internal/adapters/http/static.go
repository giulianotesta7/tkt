package httpadapter

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/giulianotesta7/tkt/internal/i18n"
	"github.com/giulianotesta7/tkt/web/templates"
)

// staticAsset is one embedded file served at one explicit route. Each route
// names one file so arbitrary template content is never exposed.
type staticAsset struct {
	route       string
	name        string
	contentType string
	// localized scripts carry the active language's js.* catalogue as a
	// prologue, so their body depends on a setting and is never cached.
	localized bool
}

// staticAssets is the single list of embedded assets: RegisterStatic mounts it
// and the asset template helper versions it, so a route cannot be served
// without a versioned URL or linked without being served.
var staticAssets = []staticAsset{
	{route: "/static/htmx.min.js", name: "static/htmx.min.js", contentType: "text/javascript; charset=utf-8"},
	{route: "/static/InterVariable.woff2", name: "static/InterVariable.woff2", contentType: "font/woff2"},
	{route: "/static/users.css", name: "static/users.css", contentType: "text/css; charset=utf-8"},
	{route: "/static/ticket_metrics.css", name: "static/ticket_metrics.css", contentType: "text/css; charset=utf-8"},
	// The settings section stylesheet is loaded only on settings pages (the
	// SettingsAssets flag), so the rest of the interface never downloads it.
	{route: "/static/settings.css", name: "static/settings.css", contentType: "text/css; charset=utf-8"},
	// The dirty-tracking script renders no copy of its own, so it needs no
	// catalogue prologue and can be cached like the other copy-free assets.
	{route: "/static/settings.js", name: "static/settings.js", contentType: "text/javascript; charset=utf-8"},
	{route: "/static/users.js", name: "static/users.js", localized: true},
	{route: "/static/tickets.js", name: "static/tickets.js", localized: true},
	{route: "/static/ticket_form.js", name: "static/ticket_form.js", localized: true},
	// The ticket-creation behaviour (issue #391) enhances two native selects into
	// the detail's listbox and filters the service catalog's cards. Its own copy
	// arrives as translated data attributes from the server, so it needs no
	// catalogue prologue and can be cached like the other copy-free assets.
	{route: "/static/ticket_create.js", name: "static/ticket_create.js", contentType: "text/javascript; charset=utf-8"},
	// The written SLA durations (issue #395) take their copy from translated
	// data attributes, so the script needs no catalogue prologue.
	{route: "/static/sla_duration.js", name: "static/sla_duration.js", contentType: "text/javascript; charset=utf-8"},
	{route: "/static/workflow.js", name: "static/workflow.js", localized: true},
	{route: "/static/categories.js", name: "static/categories.js", localized: true},
	{route: "/static/save-feedback.js", name: "static/save-feedback.js", localized: true},
	{route: "/static/ticket_metrics.js", name: "static/ticket_metrics.js", localized: true},
	{route: "/static/sla_countdown.js", name: "static/sla_countdown.js", localized: true},
	// The notification drawer moves focus; it renders no copy, so it needs no
	// catalogue prologue and can be cached like the other copy-free assets.
	{route: "/static/notifications.js", name: "static/notifications.js", contentType: "text/javascript; charset=utf-8"},
	// The ticket detail's own behaviour (title guard, property pickers, hold and
	// reason dialogs, phone fold — issue #386) renders no copy of its own, so it
	// needs no catalogue prologue either.
	{route: "/static/ticket_detail.js", name: "static/ticket_detail.js", contentType: "text/javascript; charset=utf-8"},
	// The saved-reply manager's behaviour (placeholder chips and the delete
	// confirmation) renders no copy of its own either: every string it shows
	// comes from the page, so it needs no catalogue prologue.
	{route: "/static/saved_replies.js", name: "static/saved_replies.js", contentType: "text/javascript; charset=utf-8"},
	{route: "/static/shell.js", name: "static/shell.js", contentType: "text/javascript; charset=utf-8"},
	{route: "/static/queue.js", name: "static/queue.js", contentType: "text/javascript; charset=utf-8"},
}

// Cache rules for a copy-free asset (issue #436). Pages link every asset at
// its content-versioned URL, so the current version can be cached for a year:
// an upgrade that changes the bytes changes the URL, and the browser fetches
// the new one on the next page load. A bare or stale URL must revalidate
// against the ETag instead, so it never pins old bytes.
const (
	staticCacheVersioned  = "public, max-age=31536000, immutable"
	staticCacheRevalidate = "no-cache"
)

// staticVersions maps each route to the version of the bytes it serves: the
// first 16 hex digits of their SHA-256, stable across builds of the same
// content.
type staticVersions map[string]string

func newStaticVersions(fsys fs.FS, assets []staticAsset) (staticVersions, error) {
	versions := make(staticVersions, len(assets))
	for _, a := range assets {
		data, err := fs.ReadFile(fsys, a.name)
		if err != nil {
			return nil, fmt.Errorf("version static %s: %w", a.route, err)
		}
		sum := sha256.Sum256(data)
		versions[a.route] = hex.EncodeToString(sum[:])[:16]
	}
	return versions, nil
}

// url answers the versioned URL of a registered route.
func (v staticVersions) url(route string) (string, error) {
	version, ok := v[route]
	if !ok {
		return "", fmt.Errorf("static asset %q is not registered", route)
	}
	return route + "?v=" + version, nil
}

// embeddedStaticVersions versions the embedded tree once. The tree is fixed at
// build time, so a failure is a build defect and panics on first use.
var embeddedStaticVersions = sync.OnceValue(func() staticVersions {
	v, err := newStaticVersions(templates.FS, staticAssets)
	if err != nil {
		panic(err)
	}
	return v
})

// staticAssetURL is the asset template helper: {{asset "/static/x.js"}} renders
// the versioned URL, and an unregistered route fails the render.
func staticAssetURL(route string) (string, error) {
	return embeddedStaticVersions().url(route)
}

// RegisterStatic mounts the explicit embedded assets used by the application.
//
// language resolves the instance interface language for the scripts that render
// catalogue-backed copy (issue #335, S8). A nil source answers the default
// locale, which keeps a router built without settings (a unit test, a fixture)
// serving working assets.
func RegisterStatic(mux *http.ServeMux, language InterfaceLanguageSource) {
	versions := embeddedStaticVersions()
	for _, a := range staticAssets {
		if a.localized {
			registerLocalizedStatic(mux, a.route, a.name, language)
			continue
		}
		registerEmbeddedStatic(mux, a, versions[a.route])
	}
}

func registerEmbeddedStatic(mux *http.ServeMux, a staticAsset, version string) {
	etag := `"` + version + `"`
	mux.HandleFunc("GET "+a.route, func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(templates.FS, a.name)
		if err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		cacheControl := staticCacheRevalidate
		if r.URL.Query().Get("v") == version {
			cacheControl = staticCacheVersioned
		}
		w.Header().Set("Content-Type", a.contentType)
		w.Header().Set("Cache-Control", cacheControl)
		w.Header().Set("ETag", etag)
		// ServeContent answers If-None-Match with 304 against the ETag set above;
		// the zero modtime keeps it from emitting a Last-Modified validator.
		http.ServeContent(w, r, a.name, time.Time{}, bytes.NewReader(data))
	})
}

// localizedStaticPath reports whether a path serves a localized script, whose
// body depends on the requester's language.
func localizedStaticPath(path string) bool {
	for _, a := range staticAssets {
		if a.route == path {
			return a.localized
		}
	}
	return false
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
		if userFromContext(r.Context()) != nil {
			w.Header().Set("Cache-Control", "private, no-store")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, i18n.Script(requestInterfaceLanguage(r.Context(), language, failures)))
		if _, err := io.Copy(w, f); err != nil {
			log.Printf("serve static %s: %v", name, err)
		}
	})
}
