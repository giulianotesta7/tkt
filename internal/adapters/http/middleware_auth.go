package httpadapter

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
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

// SessionMiddleware enforces the session, bootstrap, and CSRF gates (D14,
// D16, D17). It wraps the whole mux:
//
//   - POST requests with a cross-site Origin header are rejected 403 before
//     any handler runs (D17 — login/logout CSRF included).
//   - /healthz is always allowed (D12).
//   - /login*: an authenticated visitor is sent to /tickets; otherwise the
//     handler runs.
//   - /setup*: available ONLY while the users table is empty (D16);
//     otherwise anonymous visitors go to /login and authed users to
//     /tickets.
//   - Everything else requires a valid session: missing/expired token →
//     303 /login (or 303 /setup when the table is empty); a deactivated
//     user's session is destroyed on sight (D14 — deactivation kills
//     sessions, next request is logged out).
type SessionMiddleware struct {
	sessions application.SessionStore
	users    application.UserStore
}

// NewSessionMiddleware wires the middleware against the session and user
// ports.
func NewSessionMiddleware(sessions application.SessionStore, users application.UserStore) *SessionMiddleware {
	return &SessionMiddleware{sessions: sessions, users: users}
}

// Wrap returns the mux-wrapping handler.
func (m *SessionMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !originAllowed(r) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		path := r.URL.Path
		if path == "/healthz" || strings.HasPrefix(path, "/healthz/") {
			next.ServeHTTP(w, r)
			return
		}

		session, _ := m.resolveSession(r)

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
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}
			if count > 0 {
				if session != nil {
					redirect(w, r, "/tickets")
				} else {
					redirect(w, r, "/login")
				}
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		if session == nil {
			count, err := m.users.Count(r.Context())
			if err != nil {
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}
			if count == 0 {
				redirect(w, r, "/setup")
				return
			}
			redirect(w, r, "/login")
			return
		}

		user, err := m.users.GetByID(r.Context(), session.UserID)
		if err != nil || !user.Active {
			if err == nil {
				// D14: deactivation kills the user's sessions — destroy the
				// row so the next request is logged out for good.
				_ = m.sessions.Delete(r.Context(), session.ID)
			}
			redirect(w, r, "/login")
			return
		}

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKeyUser{}, user)))
	})
}

// resolveSession reads the cookie and resolves it to a valid session; a
// missing, expired, or forged token yields nil (the middleware treats those
// identically: unauthenticated).
func (m *SessionMiddleware) resolveSession(r *http.Request) (*domain.Session, error) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil, nil
	}
	s, err := m.sessions.GetByID(r.Context(), c.Value)
	if err != nil {
		return nil, nil // expired/forged → unauthenticated
	}
	return s, nil
}

// originAllowed implements the D17 Origin gate: unsafe methods (POST) with a
// present Origin header must carry the request's own authority. Browsers
// send Origin on every POST; SameSite=Strict already blocks the cookie from
// cross-site sends, this is the belt-and-suspenders for forged forms. A
// malformed Origin is rejected; absent Origin (curl, non-browser clients) is
// allowed.
func originAllowed(r *http.Request) bool {
	if r.Method != http.MethodPost {
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

// redirect issues a 303 See Other (HTMX follows it natively).
func redirect(w http.ResponseWriter, r *http.Request, to string) {
	http.Redirect(w, r, to, http.StatusSeeOther)
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
