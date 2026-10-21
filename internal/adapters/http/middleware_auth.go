package httpadapter

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/internal/i18n"
)

// sessionCookie is the session cookie name (design "HTTP Layer"):
// tkt_session=<opaque 32-byte hex token>; HttpOnly, Secure behind TLS,
// SameSite=Strict (D14, D17), Path=/, no Expires (session cookie; the 24h
// TTL is enforced server-side via sessions.expires_at).
const sessionCookie = "tkt_session"

// ctxKeyUser carries the resolved session user into handlers (design
// "HTTP Layer": the logged-in user context flows into handlers as a request
// context value).
type ctxKeyUser struct{}

// userFromContext returns the session user stamped by the middleware, or nil
// when the request is unauthenticated (exempt routes).
func userFromContext(ctx context.Context) *domain.User {
	u, _ := ctx.Value(ctxKeyUser{}).(*domain.User)
	return u
}

// ctxKeyInternalCommentBg carries the instance appearance color read once
// per authenticated request (appearance-settings spec: every shell render
// styles internal comments with the configured background).
type ctxKeyInternalCommentBg struct{}

// internalCommentBgFrom returns the appearance color stamped by the
// middleware, or "" when absent (auth pages, handlers used in isolation).
func internalCommentBgFrom(ctx context.Context) string {
	s, _ := ctx.Value(ctxKeyInternalCommentBg{}).(string)
	return s
}

// ctxKeyInterfaceLanguage carries the instance interface language read once per
// request (issue #335, S8/S15). Stamping it here is what lets the renderer, the
// timeline narrative, and every error message resolve the SAME language without
// a second settings read per concern. It is stamped before the login/setup
// branches because the auth shells are catalogue-rendered too.
type ctxKeyInterfaceLanguage struct{}

// interfaceLanguageFrom returns the interface language stamped by the
// middleware and whether one was stamped at all.
func interfaceLanguageFrom(ctx context.Context) (string, bool) {
	language, ok := ctx.Value(ctxKeyInterfaceLanguage{}).(string)
	return language, ok
}

// ctxKeySidebarCollapsed carries the session user's sidebar state read once per
// authenticated request (issue #377, S2). Stamping it here is what lets the
// shell render the stored state on the first paint — no flash of the wrong
// width, and no preference read from pageDataFrom or any template.
type ctxKeySidebarCollapsed struct{}

// sidebarCollapsedFrom returns the sidebar state stamped by the middleware, or
// false (expanded) when nothing was stamped: an auth page, a handler used in
// isolation, or a request whose preference read failed all render the expanded
// shell, which is the documented default.
func sidebarCollapsedFrom(ctx context.Context) bool {
	collapsed, _ := ctx.Value(ctxKeySidebarCollapsed{}).(bool)
	return collapsed
}

const (
	// interfaceLanguageReadTimeout bounds ONE settings read (R4-001): the
	// instance language is a single indexed row, so a read that has not
	// answered within this window is a failing store, and the request must
	// proceed in the default language rather than hold the page open.
	interfaceLanguageReadTimeout = 2 * time.Second
	// interfaceLanguageEveryNthFailure re-arms the deduplicated failure log
	// (R4-002): a persistent outage records its first line, then one line per
	// this many failures, so an operator still sees that it is ongoing without
	// a line per request. Both per-request read failure logs below share it.
	interfaceLanguageEveryNthFailure = 100
)

// languageReadLog deduplicates the interface-language read failure (R4-002):
// the first failure logs, the next success re-arms the counter, and a long
// outage logs once every interfaceLanguageEveryNthFailure attempts.
type languageReadLog struct {
	mu      sync.Mutex
	failing bool
	count   int
}

func (l *languageReadLog) failed(err error) {
	l.mu.Lock()
	l.count++
	// Decide under the lock, log outside it: a slow log write must not hold
	// every concurrent request behind the mutex.
	report := !l.failing || l.count%interfaceLanguageEveryNthFailure == 0
	l.failing = true
	l.mu.Unlock()
	if report {
		log.Printf("httpadapter: resolve interface language: %v (falling back to %q)", err, i18n.Default)
	}
}

func (l *languageReadLog) recovered() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failing = false
	l.count = 0
}

// sidebarReadLog deduplicates the sidebar-state read failure (issue #377, S2)
// with the same policy languageReadLog applies to the interface-language read
// (R4-002): the first failure logs, the next success re-arms the counter, and a
// long outage logs once every interfaceLanguageEveryNthFailure attempts. It is
// a separate type rather than a reuse of languageReadLog because the fallback
// is not a locale — the request proceeds with the expanded shell — so the two
// must never share a message or a counter.
type sidebarReadLog struct {
	mu      sync.Mutex
	failing bool
	count   int
}

func (l *sidebarReadLog) failed(err error) {
	l.mu.Lock()
	l.count++
	// Decide under the lock, log outside it: a slow log write must not hold
	// every concurrent request behind the mutex.
	report := !l.failing || l.count%interfaceLanguageEveryNthFailure == 0
	l.failing = true
	l.mu.Unlock()
	if report {
		log.Printf("httpadapter: read sidebar preference: %v (rendering the expanded sidebar)", err)
	}
}

func (l *sidebarReadLog) recovered() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failing = false
	l.count = 0
}

// readInterfaceLanguage resolves the instance interface language through one
// bounded settings read, degrading to the default language on any failure. It
// is the single read path shared by the request middleware and a renderer built
// without one, so both bound the read and both log a failure the same way.
func readInterfaceLanguage(ctx context.Context, src InterfaceLanguageSource, failures *languageReadLog) string {
	if src == nil {
		return i18n.Default
	}
	readCtx, cancel := context.WithTimeout(ctx, interfaceLanguageReadTimeout)
	defer cancel()
	language, err := src.GetInterfaceLanguage(readCtx)
	if err != nil {
		failures.failed(err)
		return i18n.Default
	}
	failures.recovered()
	return i18n.Normalize(language)
}

// requestInterfaceLanguage resolves the language of ONE request: the value the
// middleware stamped, or a bounded settings read when no middleware ran (the
// renderer and the static handlers used in isolation).
func requestInterfaceLanguage(ctx context.Context, src InterfaceLanguageSource, failures *languageReadLog) string {
	if language, ok := interfaceLanguageFrom(ctx); ok {
		return i18n.Normalize(language)
	}
	return readInterfaceLanguage(ctx, src, failures)
}

// SessionMiddleware enforces the session, bootstrap, and CSRF gates (D14,
// D16, D17). It wraps the whole mux:
//
//   - Requests with an unsafe method (anything outside the safe allow-list
//     GET, HEAD, OPTIONS, TRACE) and a cross-site Origin header are rejected
//     403 before any handler runs (D17 — login/logout CSRF included).
//   - /healthz is always allowed (D12).
//   - /login*: an authenticated visitor is sent to /tickets; otherwise the
//     handler runs.
//   - /setup*: available ONLY while the users table is empty (D16);
//     otherwise anonymous visitors go to /login and authed users to
//     /tickets.
//   - Everything else requires a valid session: missing/expired token →
//     303 /login (or 303 /setup when the table is empty); a deactivated
//     user's session is destroyed on sight (D14 — deactivation kills
//     sessions, next request is logged out). An HTMX request refused here is
//     answered with HX-Redirect instead of a 3xx (issue #347): see
//     refuseUnauthenticated.
type SessionMiddleware struct {
	sessions application.SessionStore
	users    application.UserStore
	settings application.SettingsStore
	// sidebar is the OPTIONAL per-user sidebar-state reader (issue #377, S2).
	// A middleware built without it stamps the expanded default, so every
	// caller that does not care about the shell keeps working unchanged.
	sidebar application.SidebarPreferencesStore
	// languageFailures deduplicates the interface-language read failure
	// (R4-002) across requests.
	languageFailures languageReadLog
	// sidebarFailures deduplicates the sidebar-state read failure the same way
	// (issue #377, S2).
	sidebarFailures sidebarReadLog
}

// NewSessionMiddleware wires the middleware against the session, user, and
// instance settings ports.
func NewSessionMiddleware(sessions application.SessionStore, users application.UserStore, settings application.SettingsStore) *SessionMiddleware {
	return &SessionMiddleware{sessions: sessions, users: users, settings: settings}
}

// WithSidebarPreferences opts the middleware into stamping each authenticated
// request with the user's stored sidebar state (issue #377, S2). It is opt-in
// so the middleware keeps one constructor and a caller without a preferences
// store (a focused test, a future surface) is never left with a nil read.
func (m *SessionMiddleware) WithSidebarPreferences(store application.SidebarPreferencesStore) *SessionMiddleware {
	m.sidebar = store
	return m
}

// Wrap returns the mux-wrapping handler.
func (m *SessionMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !originAllowed(r) {
			httpError(w, r, "error.forbidden", http.StatusForbidden)
			return
		}

		path := r.URL.Path
		if path == "/healthz" || strings.HasPrefix(path, "/healthz/") {
			next.ServeHTTP(w, r)
			return
		}

		// Issue #335: the instance interface language is read ONCE here and
		// stamped on the request, so the renderer, the timeline narrative, the
		// error mapper, and the catalogue-backed scripts all resolve one value
		// without extra settings reads. It is stamped before the static and
		// auth branches because the scripts and the auth shells are
		// catalogue-rendered too.
		r = r.WithContext(context.WithValue(r.Context(), ctxKeyInterfaceLanguage{},
			readInterfaceLanguage(r.Context(), m.settings, &m.languageFailures)))

		// Vendored static assets are public: the shell references them on
		// every page and htmx must load before the session check matters.
		if strings.HasPrefix(path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}

		session, err := m.resolveSession(r)
		if err != nil {
			// Operational failure (store unavailable): a recoverable 500,
			// never a misleading login redirect.
			httpError(w, r, "error.internal_server", http.StatusInternalServerError)
			return
		}

		if path == "/login" || strings.HasPrefix(path, "/login/") {
			if session != nil {
				redirect(w, r, "/tickets")
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		if path == "/setup" || strings.HasPrefix(path, "/setup/") {
			count, err := m.users.Count(r.Context())
			if err != nil {
				httpError(w, r, "error.internal_server", http.StatusInternalServerError)
				return
			}
			if count > 0 {
				if session != nil {
					redirect(w, r, "/tickets")
				} else {
					refuseUnauthenticated(w, r, "/login")
				}
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		if session == nil {
			count, err := m.users.Count(r.Context())
			if err != nil {
				httpError(w, r, "error.internal_server", http.StatusInternalServerError)
				return
			}
			if count == 0 {
				refuseUnauthenticated(w, r, "/setup")
				return
			}
			refuseUnauthenticated(w, r, "/login")
			return
		}

		user, err := m.users.GetByID(r.Context(), session.UserID)
		if err != nil {
			if !errors.Is(err, domain.ErrNotFound) {
				// Operational failure: a recoverable 500, not a login redirect.
				httpError(w, r, "error.internal_server", http.StatusInternalServerError)
				return
			}
			// The user row vanished (deleted out from under the session):
			// destroy the session and treat the request as logged out.
			_ = m.sessions.Delete(r.Context(), session.ID)
			refuseUnauthenticated(w, r, "/login")
			return
		}
		if !user.Active {
			// D14: deactivation kills the user's sessions — destroy the
			// row so the next request is logged out for good.
			_ = m.sessions.Delete(r.Context(), session.ID)
			refuseUnauthenticated(w, r, "/login")
			return
		}

		bg, err := m.settings.GetInternalCommentBg(r.Context())
		if err != nil {
			// Operational failure (settings store unavailable): a
			// recoverable 500, consistent with the session/user reads.
			httpError(w, r, "error.internal_server", http.StatusInternalServerError)
			return
		}
		// Issue #377: the user's sidebar state is read ONCE here and stamped on
		// the request, so every page render answers the stored state without a
		// second preferences read. A failing read is NOT fatal — the request
		// proceeds with the expanded shell (the documented default) and the
		// failure is logged under the dedup policy, because losing the
		// collapse state must never cost a page.
		sidebar := false
		if m.sidebar != nil {
			collapsed, err := m.sidebar.SidebarCollapsed(r.Context(), user.ID)
			if err != nil {
				m.sidebarFailures.failed(err)
			} else {
				m.sidebarFailures.recovered()
				sidebar = collapsed
			}
		}
		ctx := context.WithValue(r.Context(), ctxKeyUser{}, user)
		ctx = context.WithValue(ctx, ctxKeyInternalCommentBg{}, bg)
		ctx = context.WithValue(ctx, ctxKeySidebarCollapsed{}, sidebar)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// resolveSession reads the cookie and resolves it to a valid session. A
// missing, expired, or forged token yields (nil, nil): the middleware treats
// those identically as unauthenticated. An operational store failure is
// returned as an error so callers answer 500 instead of a login redirect.
func (m *SessionMiddleware) resolveSession(r *http.Request) (*domain.Session, error) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil, nil
	}
	s, err := m.sessions.GetByID(r.Context(), c.Value)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, nil // expired/forged → unauthenticated
		}
		return nil, err // operational failure → caller decides the status
	}
	return s, nil
}

// originAllowed implements the D17 Origin gate: every unsafe method (anything
// outside the safe allow-list GET, HEAD, OPTIONS, TRACE) with a present
// Origin header must carry the request's own authority. Browsers send Origin
// on every state-changing request; SameSite=Strict already blocks the cookie
// from cross-site sends, this is the belt-and-suspenders for forged forms. A
// malformed Origin is rejected; absent Origin (curl, non-browser clients) is
// allowed: rejecting it would break scripts without adding protection,
// because it is a browser that attaches the victim's cookie.
func originAllowed(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// redirect issues a 303 See Other for an ordinary navigation or form post. It
// must NOT answer an HTMX request the session gate refuses: htmx's XHR follows
// the redirect transparently and swaps the destination document into the
// requesting target, which is exactly the defect refuseUnauthenticated exists
// to prevent (issue #347).
func redirect(w http.ResponseWriter, r *http.Request, to string) {
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// htmxRequest reports whether htmx issued the request. Every htmx XHR carries
// HX-Request, which is the header the rest of this adapter reads (it is set on
// boosted and history-restore requests too; htmx adds HX-Boosted and
// HX-History-Restore-Request alongside it rather than instead of it).
func htmxRequest(r *http.Request) bool {
	return r.Header.Get("HX-Request") != ""
}

// refuseUnauthenticated answers a request the session gate refuses because it
// has no usable session, sending it to to (/login, or /setup on a fresh
// instance).
//
// An ordinary navigation or form post keeps the 303 See Other it has always
// answered. An HTMX request gets HX-Redirect plus an empty 204 instead, so htmx
// performs a full navigation to to and never swaps anything into the requesting
// target: without it, a drawer, a list region, or the ticket list receives the
// whole login document (issue #347).
//
// The 204 is deliberate on htmx 2.0.4 (web/templates/static/htmx.min.js): the
// HX-Redirect branch of the response handler runs BEFORE its responseHandling
// table, so a 204 does not skip the header, and the default table marks 204 as
// swap:false, so even a client that ignored the header has nothing to inject.
//
// Cache-Control: no-store belongs to the HTMX answer only. 204 is one of the
// status codes a cache may store heuristically, so without it a shared or
// intermediary cache could keep this refusal and replay it to a user whose
// session is perfectly valid, throwing them at the login page of a working
// session. The ordinary 303 stays byte-identical to what it has always
// answered: 303 is not heuristically cacheable, and #347 changes nothing about
// the non-HTMX path.
func refuseUnauthenticated(w http.ResponseWriter, r *http.Request, to string) {
	if htmxRequest(r) {
		w.Header().Set("HX-Redirect", to)
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	redirect(w, r, to)
}

// sessionCookieValue builds the session cookie. Secure is enabled behind TLS
// (production) and off on plain HTTP (local dev), per design "HTTP Layer":
// "Secure (production behind TLS; dev flag documented)".
func sessionCookieValue(s *domain.Session, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookie,
		Value:    s.ID,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	}
}
