package httpadapter

import (
	"errors"
	"log"
	"net/http"
	"sync"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/i18n"
)

// Company sign-in (issue #419, S11): the public routes that start a provider
// authorization and answer the provider's callback.
//
// They live under /auth/ and never under /login/: that branch sends a browser
// that already holds a session to its tickets, which would swallow a callback
// arriving in the same browser. The two routes are public by design — the
// callback arrives with no TKT session — and they issue one only when the flow
// answered a signed-in outcome.
//
// What this file decides, and what it does not: the flow (application) owns the
// state, nonce and PKCE bookkeeping, the domain and tenant rules, the account
// resolution, and whether an outcome carries a session. Here only the HTTP
// shape lives: the cookie that binds an attempt to the browser that started it,
// the status and page of each outcome, and the rule that a refused or pending
// sign-in sets no session cookie.

// companyStateCookie binds one authorization attempt to the browser that
// started it. Its value is the state the flow generated, it is unreadable by
// script, it travels back only to the callback path of the same provider, and
// it dies ten minutes after it was set — the same window the server-side
// attempt lives in.
const companyStateCookie = "tkt_company_state"

// companyReadLog deduplicates a company sign-in configuration read failure the
// same way languageReadLog and signinReadLog do (R4-002): the first failure
// logs, the next success re-arms the counter, and a long outage logs once every
// interfaceLanguageEveryNthFailure attempts. It is a separate type because its
// fallback is the sign-in page without provider buttons, not a locale, a panel,
// a shell state, or an identity.
type companyReadLog struct {
	mu      sync.Mutex
	failing bool
	count   int
}

func (l *companyReadLog) failed(err error) {
	l.mu.Lock()
	l.count++
	// Decide under the lock, log outside it: a slow log write must not hold
	// every concurrent sign-in behind the mutex.
	report := !l.failing || l.count%interfaceLanguageEveryNthFailure == 0
	l.failing = true
	l.mu.Unlock()
	if report {
		log.Printf("httpadapter: read company sign-in settings: %v (offering no provider)", err)
	}
}

func (l *companyReadLog) recovered() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failing = false
	l.count = 0
}

// companyProviderButton is one button of the sign-in page: the provider whose
// start route it opens, and the provider name the catalogue labels it with.
type companyProviderButton struct {
	// Provider is the closed-list provider name used in the route.
	Provider string
	// LabelKey is the provider name under the auth.company. key prefix, so the
	// template resolves "Continue with Google" from the catalogue.
	LabelKey string
}

// registerCompanySignIn mounts the two public routes of the flow.
func (h *AuthHandlers) registerCompanySignIn(mux *http.ServeMux) {
	mux.HandleFunc("GET "+application.CompanySignInPathPrefix+"{provider}/start", h.companyStart)
	mux.HandleFunc("GET "+application.CompanySignInPathPrefix+"{provider}/callback", h.companyCallback)
}

// applyCompanySignIn stamps the company sign-in state of the sign-in page: the
// buttons the stored configuration makes usable and whether password sign-in is
// off. A configuration that cannot be read offers NO provider button, so a
// broken read can never send someone to a provider the instance may not have
// configured; the password form and its rule keep working, because that rule is
// enforced by the login use case and not by this page.
func (h *AuthHandlers) applyCompanySignIn(r *http.Request, data *loginData) {
	if h.companyConfig == nil {
		return
	}
	settings, err := h.companyConfig.Get(r.Context())
	if err != nil {
		h.companyFailures.failed(err)
		return
	}
	h.companyFailures.recovered()
	data.CompanySignInOff = !settings.PasswordSignInEnabled
	for _, p := range application.CompanyProviders {
		if settings.Configured(p) {
			data.CompanyProviders = append(data.CompanyProviders, companyProviderButton{Provider: string(p), LabelKey: string(p)})
		}
	}
}

// companyProvider answers the provider a route names. A path segment outside the
// closed list is not a sign-in route at all.
func (h *AuthHandlers) companyProvider(w http.ResponseWriter, r *http.Request) (application.CompanyProvider, bool) {
	provider, known := application.ParseCompanyProvider(r.PathValue("provider"))
	if !known || h.companyFlow == nil {
		http.NotFound(w, r)
		return "", false
	}
	return provider, true
}

// companyStart answers GET /auth/{provider}/start: it hands the browser to the
// provider with the state, nonce and PKCE challenge the flow minted, and sets
// the cookie that binds the attempt to this browser.
//
// A provider that is off answers the sign-in page with the neutral "not
// available" sentence rather than an error page: the person arrived from a stale
// button, and the form beside the message is the way out.
func (h *AuthHandlers) companyStart(w http.ResponseWriter, r *http.Request) {
	provider, ok := h.companyProvider(w, r)
	if !ok {
		return
	}
	start, err := h.companyFlow.Begin(r.Context(), provider)
	if err != nil {
		var refusal *application.CompanySignInRefusal
		if errors.As(err, &refusal) {
			h.renderCompanyRefusal(w, r, refusal.Reason, http.StatusForbidden)
			return
		}
		h.companyFailure(w, r, provider, err)
		return
	}
	http.SetCookie(w, companyStateCookieValue(start.State, provider, r.TLS != nil))
	redirect(w, r, start.URL)
}

// companyCallback answers GET /auth/{provider}/callback with exactly one of
// three surfaces: the tickets a signed-in outcome earned, the pending card when
// the account waits for a role, or the sign-in page with a neutral refusal.
//
// The state cookie is spent here whatever the answer is: a callback that is
// replayed, expired, or answered by another browser must not find a usable
// attempt or a usable cookie behind it.
func (h *AuthHandlers) companyCallback(w http.ResponseWriter, r *http.Request) {
	provider, ok := h.companyProvider(w, r)
	if !ok {
		return
	}
	cookieState := ""
	if c, err := r.Cookie(companyStateCookie); err == nil {
		cookieState = c.Value
	}
	clearCompanyStateCookie(w, provider, r.TLS != nil)

	outcome, err := h.companyFlow.Complete(r.Context(), application.CompanySignInCallback{
		Provider:    provider,
		CookieState: cookieState,
		QueryState:  r.URL.Query().Get("state"),
		Code:        r.URL.Query().Get("code"),
	})
	if err != nil {
		h.companyFailure(w, r, provider, err)
		return
	}

	switch outcome.Kind {
	case application.CompanySignInSignedIn:
		http.SetCookie(w, companySessionCookieValue(outcome.Session, r.TLS != nil))
		redirect(w, r, "/tickets")
	case application.CompanySignInPending:
		// No session: a pending account cannot open or see a ticket (S3), so the
		// only thing the callback renders is the card that says so.
		h.renderer.Render(w, r, "login", "", loginData{
			CompanyPending: true,
			Brand:          brandFromContext(r.Context()),
			Panel:          h.panel(r, true),
		}, http.StatusOK)
	default:
		h.renderCompanyRefusal(w, r, outcome.Reason, companyRefusalStatus(outcome.Reason))
	}
}

// renderCompanyRefusal renders the sign-in page with the neutral sentence of one
// refusal reason. The message never repeats the provider's own text, and it
// never says whether an account exists: the same sentences answer a domain, a
// tenant, a deactivated account, and an unusable attempt.
func (h *AuthHandlers) renderCompanyRefusal(w http.ResponseWriter, r *http.Request, reason application.CompanySignInReason, status int) {
	message := i18n.T(requestLocale(r), companyRefusalKey(reason))
	h.renderer.Render(w, r, "login", "", h.loginPageData(r, message, ""), status)
}

// companyFailure answers an operational failure of the flow: a recoverable 500
// with the generic sentence, logged for the operator and never carrying the
// error's own text, which can hold provider or library detail.
func (h *AuthHandlers) companyFailure(w http.ResponseWriter, r *http.Request, provider application.CompanyProvider, err error) {
	log.Printf("httpadapter: company sign-in with %s: %v", provider, err)
	httpError(w, r, "error.internal_server", http.StatusInternalServerError)
}

// companyRefusalKey names the catalogue key of one refusal reason. A reason
// outside the named ones (an ambiguous email, an account already linked to
// another identity) answers the generic sentence: the person cannot fix either
// from the sign-in page.
func companyRefusalKey(reason application.CompanySignInReason) string {
	switch reason {
	case application.CompanySignInOutsideDomain, application.CompanySignInUnverifiedEmail:
		return "auth.company.refused_domain"
	case application.CompanySignInOutsideTenant:
		return "auth.company.refused_tenant"
	case application.CompanySignInDeactivated:
		return "auth.company.refused_deactivated"
	case application.CompanySignInInvalidCallback:
		return "auth.company.refused_attempt"
	case application.CompanySignInProviderOff:
		return "auth.company.refused_unavailable"
	default:
		return "auth.company.refused_generic"
	}
}

// companyRefusalStatus answers the status of one refusal: an attempt that cannot
// be trusted is a malformed request, while the rest are a decision about the
// account, which is what 403 says.
func companyRefusalStatus(reason application.CompanySignInReason) int {
	if reason == application.CompanySignInInvalidCallback {
		return http.StatusBadRequest
	}
	return http.StatusForbidden
}

// companyCallbackPath is the one path the state cookie belongs to: the callback
// of the provider whose attempt it carries.
func companyCallbackPath(p application.CompanyProvider) string {
	return application.CompanySignInPathPrefix + string(p) + "/callback"
}

// companyStateCookieValue builds the state cookie of one attempt. SameSite is
// Lax on purpose: the callback is a top-level navigation back from the
// provider's site, and a Strict cookie would not be sent with it.
func companyStateCookieValue(state string, p application.CompanyProvider, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     companyStateCookie,
		Value:    state,
		Path:     companyCallbackPath(p),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(application.CompanySignInAttemptTTL.Seconds()),
	}
}

// clearCompanyStateCookie spends the state cookie. The path is the callback's,
// so the browser drops exactly the cookie the attempt set.
func clearCompanyStateCookie(w http.ResponseWriter, p application.CompanyProvider, secure bool) {
	c := companyStateCookieValue("", p, secure)
	c.MaxAge = -1
	http.SetCookie(w, c)
}
