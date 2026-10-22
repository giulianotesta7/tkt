package httpadapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net/http"
	"sync"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// The instance identity (issue #383, S5): the organization name, help desk
// name and logo are read ONCE per request by the session middleware and
// stamped on the request, so the shell, the document titles and the auth pages
// all render one read instead of one query per template. GET /brand/logo
// serves the bytes themselves, publicly: the login page renders the logo
// before any session exists.

// brandData is the identity the shell renders: the name, the help desk name
// beside it, the mark to show when there is no logo, and whether a logo must be
// requested. LogoVersion busts the day-long cache the logo route answers with,
// so a replaced logo is never served from it.
type brandData struct {
	Name        string
	HelpDesk    string
	Monogram    string
	HasLogo     bool
	LogoVersion string
	// SupportEmail is the configured support address, when the instance has
	// one. Only the sign-in shell renders it (S6: a footer line with the
	// address someone can write to); the rail does not.
	SupportEmail string
}

// defaultOrganization is the identity an instance has before anything is
// stored, and the one every read path falls back to. Keeping it in one function
// means the shell, the auth pages and the title helper can never disagree about
// what an unconfigured instance is called.
func defaultOrganization() domain.Organization {
	return domain.Organization{
		Name:     domain.DefaultOrganizationName,
		HelpDesk: domain.DefaultOrganizationHelpDesk,
	}
}

// ctxKeyOrganization carries the instance identity the session middleware read
// once for this request.
type ctxKeyOrganization struct{}

// organizationFrom returns the identity stamped by the middleware. A request
// with no stamp — a handler exercised in isolation, a fixture render — answers
// the documented default instead of an empty brand.
func organizationFrom(ctx context.Context) domain.Organization {
	org, ok := ctx.Value(ctxKeyOrganization{}).(domain.Organization)
	if !ok {
		return defaultOrganization()
	}
	return normalizeBrand(org)
}

// normalizeBrand fills the documented defaults a stored row may leave empty, so
// an empty name can never reach a page as an empty heading.
func normalizeBrand(org domain.Organization) domain.Organization {
	if org.Name == "" {
		org.Name = domain.DefaultOrganizationName
	}
	if org.HelpDesk == "" {
		org.HelpDesk = domain.DefaultOrganizationHelpDesk
	}
	return org
}

// brandFromContext builds the shell payload from the stamped identity.
func brandFromContext(ctx context.Context) brandData { return brandOf(organizationFrom(ctx)) }

// brandOf reduces an organization to the shell's payload.
func brandOf(org domain.Organization) brandData {
	return brandData{
		Name:         org.Name,
		HelpDesk:     org.HelpDesk,
		Monogram:     org.Monogram(),
		HasLogo:      org.HasLogo(),
		LogoVersion:  logoVersion(org.Logo),
		SupportEmail: org.SupportEmail,
	}
}

// logoVersion is the short content hash the shell appends to the logo URL. The
// logo answers a day-long public cache, so the URL is what changes when the
// bytes do; an instance with no logo answers "" and the shell requests nothing.
func logoVersion(logo []byte) string {
	if len(logo) == 0 {
		return ""
	}
	sum := sha256.Sum256(logo)
	return hex.EncodeToString(sum[:6])
}

// organizationReadLog deduplicates the organization read failure the same way
// languageReadLog does (R4-002): the first failure logs, the next success
// re-arms the counter, and a long outage logs once every
// interfaceLanguageEveryNthFailure attempts. It is a separate type because its
// fallback is the default identity, not a locale or a shell state.
type organizationReadLog struct {
	mu      sync.Mutex
	failing bool
	count   int
}

func (l *organizationReadLog) failed(err error) {
	l.mu.Lock()
	l.count++
	report := !l.failing || l.count%interfaceLanguageEveryNthFailure == 0
	l.failing = true
	l.mu.Unlock()
	if report {
		log.Printf("httpadapter: read organization identity: %v (rendering the default brand)", err)
	}
}

func (l *organizationReadLog) recovered() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failing = false
	l.count = 0
}

// BrandHandlers serve the instance logo. It is the one route the shell
// requests before a session exists, so the middleware lets /brand/ through the
// same way it lets /static/ through.
type BrandHandlers struct {
	organization application.OrganizationStore
	// signin is the OPTIONAL sign-in page reader (issue #383, S9/S10). A handler
	// set built without it serves no background image, so a caller that does not
	// care about the sign-in page keeps working unchanged.
	signin application.SigninStore
}

// NewBrandHandlers wires the logo route against the identity port.
func NewBrandHandlers(organization application.OrganizationStore) *BrandHandlers {
	return &BrandHandlers{organization: organization}
}

// WithSignin opts the handler set into serving the sign-in page's background
// image (issue #383, S10). It is opt-in for the same reason the logo route keeps
// one constructor.
func (h *BrandHandlers) WithSignin(signin application.SigninStore) *BrandHandlers {
	h.signin = signin
	return h
}

// Register mounts the two public brand images.
func (h *BrandHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /brand/logo", h.logo)
	mux.HandleFunc("GET /brand/signin-background", h.signinBackground)
}

// logo answers the stored logo. The type is the one stored with the bytes (a
// PNG, proven before it was written) and never a type derived from them at
// serve time; nosniff keeps a browser from reinterpreting the response, and the
// public cache is safe because the shell's URL carries the content hash.
func (h *BrandHandlers) logo(w http.ResponseWriter, r *http.Request) {
	org, err := h.organization.GetOrganization(r.Context())
	if err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	if !org.HasLogo() {
		http.NotFound(w, r)
		return
	}
	contentType := org.LogoMIME
	if contentType == "" {
		contentType = domain.OrganizationLogoContentType
	}
	writeBrandImage(w, contentType, org.Logo)
}

// signinBackground answers the stored sign-in background image (issue #383,
// S10). It mirrors the logo route exactly — the same public path prefix, the
// same headers, and a 404 when the instance holds no image — because both are
// requested by a page that renders before any session exists.
func (h *BrandHandlers) signinBackground(w http.ResponseWriter, r *http.Request) {
	if h.signin == nil {
		http.NotFound(w, r)
		return
	}
	configuration, err := h.signin.GetSignin(r.Context())
	if err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	if !configuration.HasBackground() {
		http.NotFound(w, r)
		return
	}
	contentType := configuration.BackgroundMIME
	if contentType == "" {
		if derived, ok := domain.SigninBackgroundContentType(configuration.Background); ok {
			contentType = derived
		} else {
			contentType = domain.SigninBackgroundPNG
		}
	}
	writeBrandImage(w, contentType, configuration.Background)
}

// writeBrandImage answers one stored image with the headers a public brand image
// carries: the type the row was validated as, nosniff so a browser cannot
// reinterpret the bytes, and a day-long public cache that the shell's
// content-hash URL keeps honest.
func writeBrandImage(w http.ResponseWriter, contentType string, data []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(data); err != nil {
		log.Printf("httpadapter: write brand image: %v", err)
	}
}
