package httpadapter

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/giulianotesta7/tkt/web/templates"
)

// Issue #436: every embedded asset is linked at a content-versioned URL, so an
// upgrade that changes an asset changes the URL the browser is told to fetch,
// while an unchanged asset keeps its URL and its long-lived cache entry.

// mustAssetURL answers the versioned URL a page links for route.
func mustAssetURL(t testing.TB, route string) string {
	t.Helper()
	u, err := staticAssetURL(route)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// TestStaticVersionFollowsContent proves the version is derived from the asset
// bytes: changing the content changes the URL, and equal content keeps it.
func TestStaticVersionFollowsContent(t *testing.T) {
	before := fstest.MapFS{"static/app.js": {Data: []byte("console.log(1)")}}
	after := fstest.MapFS{"static/app.js": {Data: []byte("console.log(2)")}}
	assets := []staticAsset{{route: "/static/app.js", name: "static/app.js"}}

	old, err := newStaticVersions(before, assets)
	if err != nil {
		t.Fatalf("versions before: %v", err)
	}
	upgraded, err := newStaticVersions(after, assets)
	if err != nil {
		t.Fatalf("versions after: %v", err)
	}
	same, err := newStaticVersions(fstest.MapFS{"static/app.js": {Data: []byte("console.log(1)")}}, assets)
	if err != nil {
		t.Fatalf("versions same: %v", err)
	}

	oldURL, _ := old.url("/static/app.js")
	newURL, _ := upgraded.url("/static/app.js")
	sameURL, _ := same.url("/static/app.js")
	if !regexp.MustCompile(`^/static/app\.js\?v=[0-9a-f]{16}$`).MatchString(oldURL) {
		t.Fatalf("versioned URL = %q, want /static/app.js?v=<16 hex>", oldURL)
	}
	if oldURL == newURL {
		t.Errorf("changed content kept the URL %q: the browser would not fetch the upgrade", oldURL)
	}
	if oldURL != sameURL {
		t.Errorf("unchanged content moved the URL from %q to %q: the cache entry would be lost", oldURL, sameURL)
	}
	if _, err := old.url("/static/missing.js"); err == nil {
		t.Error("an unregistered route must be an error, not an unversioned URL")
	}
	if _, err := newStaticVersions(fstest.MapFS{}, assets); err == nil {
		t.Error("an asset missing from the tree must fail when the versions are built")
	}
}

// TestStaticTemplatesLinkOnlyVersionedAssets proves no template links an
// embedded asset by its bare path: every reference goes through the asset
// helper, so the rule covers every asset, the localized scripts included.
func TestStaticTemplatesLinkOnlyVersionedAssets(t *testing.T) {
	bare := regexp.MustCompile(`(?:src=|href=|url\()["']?/static/`)
	err := fs.WalkDir(templates.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".html") {
			return err
		}
		data, err := fs.ReadFile(templates.FS, path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			if bare.MatchString(line) {
				t.Errorf("%s:%d links a bare /static/ path; use {{asset \"/static/...\"}}", path, i+1)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk templates: %v", err)
	}
}

// TestStaticEveryRouteIsVersioned proves every registered route resolves to a
// versioned URL and serves the bytes it was versioned from.
func TestStaticEveryRouteIsVersioned(t *testing.T) {
	h := newHarness(t)
	if len(staticAssets) == 0 {
		t.Fatal("no static assets registered")
	}
	for _, a := range staticAssets {
		u, err := staticAssetURL(a.route)
		if err != nil {
			t.Errorf("%s: %v", a.route, err)
			continue
		}
		if !strings.HasPrefix(u, a.route+"?v=") {
			t.Errorf("%s: URL = %q, want a ?v= version", a.route, u)
			continue
		}
		rec := h.get(t, u, false)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", u, rec.Code)
		}
	}
}

// TestStaticRenderedPagesUseVersionedURLs proves a real page carries versioned
// URLs for the shell assets, the font and a localized script alike.
func TestStaticRenderedPagesUseVersionedURLs(t *testing.T) {
	h := newHarness(t)
	rec := h.get(t, "/users", false)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /users = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, route := range []string{"/static/htmx.min.js", "/static/shell.js", "/static/users.js", "/static/users.css", "/static/InterVariable.woff2"} {
		want, err := staticAssetURL(route)
		if err != nil {
			t.Fatalf("%s: %v", route, err)
		}
		if !strings.Contains(body, `"`+want+`"`) {
			t.Errorf("page does not link %s", want)
		}
	}
}

// TestStaticCacheHeadersFollowTheVersion pins the cache contract of a
// copy-free asset: the current version is cached for a year as immutable, while
// a bare or stale URL must revalidate, and a matching validator answers 304.
func TestStaticCacheHeadersFollowTheVersion(t *testing.T) {
	h := newHarness(t)
	current, err := staticAssetURL("/static/ticket_detail.js")
	if err != nil {
		t.Fatal(err)
	}

	rec := h.get(t, current, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", current, rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("current version Cache-Control = %q, want public, max-age=31536000, immutable", got)
	}
	etag := rec.Header().Get("ETag")
	if !regexp.MustCompile(`^"[0-9a-f]{16}"$`).MatchString(etag) {
		t.Errorf("ETag = %q, want the quoted content version", etag)
	}
	if !strings.HasSuffix(current, "?v="+strings.Trim(etag, `"`)) {
		t.Errorf("ETag %s does not match the URL version %s", etag, current)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/javascript; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	full := rec.Body.String()

	for _, u := range []string{"/static/ticket_detail.js", "/static/ticket_detail.js?v=0000000000000000"} {
		rec := h.get(t, u, false)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", u, rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("GET %s Cache-Control = %q, want no-cache so the browser revalidates", u, got)
		}
		if got := rec.Header().Get("ETag"); got != etag {
			t.Errorf("GET %s ETag = %q, want %q", u, got, etag)
		}
		if rec.Body.String() != full {
			t.Errorf("GET %s served different bytes than the versioned URL", u)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/static/ticket_detail.js", nil)
	req.Header.Set("If-None-Match", etag)
	cond := httptest.NewRecorder()
	h.mux.ServeHTTP(cond, req)
	if cond.Code != http.StatusNotModified || cond.Body.Len() != 0 {
		t.Errorf("revalidation = %d with %d bytes, want 304 and no body", cond.Code, cond.Body.Len())
	}

	req = httptest.NewRequest(http.MethodGet, "/static/ticket_detail.js", nil)
	req.Header.Set("If-None-Match", `"0000000000000000"`)
	stale := httptest.NewRecorder()
	h.mux.ServeHTTP(stale, req)
	if stale.Code != http.StatusOK || stale.Body.String() != full {
		t.Errorf("stale validator = %d, want 200 with the current bytes", stale.Code)
	}
}

// TestStaticLocalizedScriptsStayUncached proves the version never turns a
// localized script cacheable: its body depends on the language, so it keeps
// its no-cache rule even at the current versioned URL.
func TestStaticLocalizedScriptsStayUncached(t *testing.T) {
	h := newHarness(t)
	u, err := staticAssetURL("/static/tickets.js")
	if err != nil {
		t.Fatal(err)
	}
	rec := h.get(t, u, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", u, rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}
	if got := rec.Header().Get("ETag"); got != "" {
		t.Errorf("ETag = %q, want none on a language-dependent body", got)
	}
}
