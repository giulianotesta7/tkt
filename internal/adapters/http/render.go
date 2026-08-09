// Package httpadapter implements the presentation layer (design "Package
// Layout"): stdlib net/http handlers over http.ServeMux (D9), HX-aware
// rendering (D6), session middleware (D14, D16, D17), and D5 error mapping.
// It imports the application ports only; persistence stays behind the sqlite
// adapter.
package httpadapter

import (
	"bytes"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/giulianotesta7/tkt/web/templates"
)

// timeLayout is the rendered timestamp form (D7): ISO-8601 UTC TEXT,
// RFC3339 — the same layout the sqlite adapter persists.
const timeLayout = time.RFC3339

// templateFuncs are the presentation helpers shared by every template set.
// The render path never calls time.Now() (D7): formatTime formats the
// already-stamped instants the handlers pass in.
var templateFuncs = template.FuncMap{
	"formatTime": func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format(timeLayout)
	},
	"ticketNumber": func(n int) string {
		return "TKT-" + strconv.Itoa(n)
	},
}

// shellFor maps a page to its shell root. Application pages use the rail
// shell (base.html); the auth pages (login, setup) use the split shell
// (auth.html). A page with no entry defaults to base.html.
func shellFor(page string) string {
	switch page {
	case "login", "setup":
		return "auth.html"
	default:
		return "base.html"
	}
}

// pageSet is a fully parsed page: the shell root to execute and the set
// holding the shell + all partials + the page's own "content" definition.
type pageSet struct {
	shell string
	tmpl  *template.Template
}

// Renderer owns the parsed template sets (D6): one set per page (shell +
// partials + page content) and one shared fragment set for HX swaps.
type Renderer struct {
	pages     map[string]pageSet
	fragments *template.Template
}

// NewRenderer parses the embedded template tree (web/templates). Parse
// errors are fatal: templates are static assets, a broken template is a
// build error.
func NewRenderer() *Renderer {
	r, err := parseRenderer(templates.FS)
	if err != nil {
		panic("httpadapter: parse templates: " + err.Error())
	}
	return r
}

// NewRendererWith parses an arbitrary template tree (test harness: fixture
// sets, golden regeneration).
func NewRendererWith(fsys fs.FS) (*Renderer, error) {
	return parseRenderer(fsys)
}

// parseRenderer builds the per-page sets and the shared fragment set from
// fsys. Layout: shell roots (base.html, auth.html) at the root; pages under
// pages/ (each defines "content"); swap fragments under partials/.
func parseRenderer(fsys fs.FS) (*Renderer, error) {
	fragments, err := template.New("").Funcs(templateFuncs).ParseFS(fsys, "partials/*.html")
	if err != nil {
		return nil, err
	}

	base, err := template.New("base.html").Funcs(templateFuncs).ParseFS(fsys, "base.html")
	if err != nil {
		return nil, err
	}
	shells := map[string]*template.Template{"base.html": base}
	if _, err := fs.Stat(fsys, "auth.html"); err == nil {
		auth, err := template.New("auth.html").Funcs(templateFuncs).ParseFS(fsys, "auth.html")
		if err != nil {
			return nil, err
		}
		shells["auth.html"] = auth
	}

	pageFiles, err := fs.Glob(fsys, "pages/*.html")
	if err != nil {
		return nil, err
	}
	pages := make(map[string]pageSet, len(pageFiles))
	for _, file := range pageFiles {
		name := strings.TrimSuffix(path.Base(file), ".html")
		shell := shells[shellFor(name)]
		if shell == nil {
			return nil, fs.ErrNotExist // shell file missing from fsys
		}
		page, err := shell.Clone()
		if err != nil {
			return nil, err
		}
		if _, err := page.ParseFS(fsys, "partials/*.html", file); err != nil {
			return nil, err
		}
		pages[name] = pageSet{shell: shellFor(name), tmpl: page}
	}
	return &Renderer{pages: pages, fragments: fragments}, nil
}

// Render executes the page (full request) or the fragment (HX-Request) with
// the given status (D6). The response is buffered so template errors cannot
// corrupt a partial write; a render failure degrades to a generic 500.
func (r *Renderer) Render(w http.ResponseWriter, rq *http.Request, page, fragment string, data any, status int) {
	var buf bytes.Buffer
	var err error

	if rq.Header.Get("HX-Request") != "" {
		if fragment != "" {
			err = r.fragments.ExecuteTemplate(&buf, fragment, data)
		} else {
			// Natural fragment: the page's own content block (login/setup forms).
			ps, ok := r.pages[page]
			if !ok {
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}
			err = ps.tmpl.ExecuteTemplate(&buf, "content", data)
		}
	} else {
		ps, ok := r.pages[page]
		if !ok {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		err = ps.tmpl.ExecuteTemplate(&buf, ps.shell, data)
	}
	if err != nil {
		log.Printf("httpadapter: render %s/%s: %v", page, fragment, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}
