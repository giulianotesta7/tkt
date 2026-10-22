package httpadapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// The instance identity in the shell (issue #383, S5): the session middleware
// reads the organization ONCE per request and stamps it, pageData carries the
// brand, the sidebar renders the logo or the monogram with the name and the
// help desk name, the document title ends with the help desk name, and
// GET /brand/logo serves the stored PNG to anyone — a login page needs it
// before there is a session.

// brandPNG is a valid PNG the brand route serves byte for byte.
var brandPNG = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)

func setOrganization(t *testing.T, h *harness, org domain.Organization) {
	t.Helper()
	if err := h.store.OrganizationStore().SetOrganization(context.Background(), org); err != nil {
		t.Fatalf("store organization: %v", err)
	}
}

func TestBrandLogoIsPublicAndAnswersTheStoredPNG(t *testing.T) {
	h := newHarness(t)
	setOrganization(t, h, domain.Organization{
		Name: "Acme Support", HelpDesk: "Service desk", Logo: brandPNG, LogoMIME: domain.OrganizationLogoContentType,
	})

	// Anonymous: the login page renders the logo before any session exists.
	req := httptest.NewRequest(http.MethodGet, "/brand/logo", nil)
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("anonymous GET /brand/logo = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=86400" {
		t.Errorf("Cache-Control = %q, want public, max-age=86400", got)
	}
	if got := rec.Body.Bytes(); string(got) != string(brandPNG) {
		t.Errorf("body = %d bytes, want the %d stored bytes", len(got), len(brandPNG))
	}
}

func TestBrandLogoIs404WhenNoLogoIsStored(t *testing.T) {
	h := newHarness(t)

	rec := h.get(t, "/brand/logo", false)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /brand/logo with no logo = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); strings.Contains(body, "PNG") {
		t.Errorf("404 body must carry no logo bytes, got %q", body)
	}
}

// TestBrandReachesTheShellFromTheMiddleware proves the stamp: the page the
// middleware serves carries the stored identity — logo, name, help desk name —
// without the handler reading the organization itself.
func TestBrandReachesTheShellFromTheMiddleware(t *testing.T) {
	h := newHarness(t)
	setOrganization(t, h, domain.Organization{
		Name: "Acme Support", HelpDesk: "Service desk", Logo: brandPNG, LogoMIME: domain.OrganizationLogoContentType,
	})

	rec := h.get(t, "/tickets", false)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /tickets = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`class="brand-mark" href="/tickets" aria-label="Acme Support"><img src="/brand/logo?v=`,
		`class="brand-name">Acme Support<`,
		`class="brand-product">Service desk<`,
		`alt=""`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("shell must contain %q, got: %s", want, body)
		}
	}
	// The logo replaces the monogram mark: a logo and a monogram in one brand
	// would be two marks for one concept.
	if strings.Contains(body, `<span class="brand-mark">AS</span>`) {
		t.Errorf("shell must render the logo instead of the monogram, got: %s", body)
	}
}

// TestBrandFallsBackToTheMonogram proves the documented default identity: an
// instance that never configured itself renders "tkt" with its monogram and
// no image request at all.
func TestBrandFallsBackToTheMonogram(t *testing.T) {
	h := newHarness(t)

	rec := h.get(t, "/tickets", false)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /tickets = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`class="brand-mark" href="/tickets" aria-label="tkt">T</a>`,
		`class="brand-name">tkt<`,
		`class="brand-product">Help desk<`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("default shell must contain %q, got: %s", want, body)
		}
	}
	if strings.Contains(body, "/brand/logo") {
		t.Errorf("an instance with no logo must not request one, got: %s", body)
	}
}

// TestBrandLogoVersionBustsTheCache proves the URL the shell emits changes with
// the bytes: the logo answers a day-long cache, so a replaced logo must not be
// served from it.
func TestBrandLogoVersionBustsTheCache(t *testing.T) {
	h := newHarness(t)
	setOrganization(t, h, domain.Organization{Name: "Acme", HelpDesk: "Help desk", Logo: brandPNG, LogoMIME: domain.OrganizationLogoContentType})
	first := brandLogoHref(t, h)

	replaced := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 80)...)
	setOrganization(t, h, domain.Organization{Name: "Acme", HelpDesk: "Help desk", Logo: replaced, LogoMIME: domain.OrganizationLogoContentType})
	second := brandLogoHref(t, h)

	if first == "" || second == "" {
		t.Fatalf("logo hrefs = (%q, %q), want both rendered", first, second)
	}
	if first == second {
		t.Errorf("logo href %q did not change with the replaced logo, so the cached image wins", first)
	}
}

// brandLogoHref returns the logo URL the shell emitted.
func brandLogoHref(t *testing.T, h *harness) string {
	t.Helper()
	body := h.get(t, "/tickets", false).Body.String()
	start := strings.Index(body, `src="/brand/logo?v=`)
	if start < 0 {
		return ""
	}
	rest := body[start+len(`href="`):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// TestDocumentTitleEndsWithTheHelpDeskName proves S5's title contract: a
// screen title is "<screen> · <help desk>", and the ticket title keeps its own
// shape with the same suffix, so a browser tab says which help desk it is.
func TestDocumentTitleEndsWithTheHelpDeskName(t *testing.T) {
	h := newHarness(t)
	setOrganization(t, h, domain.Organization{Name: "Acme Support", HelpDesk: "Service desk"})

	rec := h.get(t, "/tickets", false)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /tickets = %d, want 200", rec.Code)
	}
	if got := documentTitle(t, rec.Body.String()); got != "Tickets · Service desk" {
		t.Errorf("<title> = %q, want %q", got, "Tickets · Service desk")
	}
}
