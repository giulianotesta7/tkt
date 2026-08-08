package httpadapter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// failingSessionStore answers every session lookup with an operational error.
type failingSessionStore struct{ application.SessionStore }

func (failingSessionStore) GetByID(context.Context, string) (*domain.Session, error) {
	return nil, errors.New("session store unavailable")
}

// failingUserStore answers every user lookup with an operational error.
type failingUserStore struct{ application.UserStore }

func (failingUserStore) GetByID(context.Context, int64) (*domain.User, error) {
	return nil, errors.New("user store unavailable")
}

// TestMiddlewareNoCookieRedirectsToLogin proves the unauthenticated request
// is redirected 303 to /login (session-protected routes spec).
func TestMiddlewareNoCookieRedirectsToLogin(t *testing.T) {
	s := openTestStore(t)
	seedUser(t, s, "Ana", "ana@example.com")
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tickets", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("protected")) })

	rec := doRequest(mux, mw, http.MethodGet, "/tickets", nil)

	wantRedirect(t, rec, http.StatusSeeOther, "/login")
}

// TestMiddlewareExpiredAndForgedTokenRedirectToLogin proves expired or
// forged tokens are treated as unauthenticated (expired-session spec).
func TestMiddlewareExpiredAndForgedTokenRedirectToLogin(t *testing.T) {
	s := openTestStore(t)
	user := seedUser(t, s, "Ana", "ana@example.com")
	// An expired session row (wall-clock past) — GetByID must treat it as
	// not found (and purge it lazily).
	expired := &domain.Session{ID: "expired-token", UserID: user.ID, ExpiresAt: time.Now().Add(-time.Hour)}
	if err := s.SessionStore().Create(context.Background(), expired); err != nil {
		t.Fatalf("seed expired session: %v", err)
	}
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tickets", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("protected")) })

	for _, tt := range []struct {
		name   string
		cookie string
	}{
		{"expired", "tkt_session=expired-token"},
		{"forged", "tkt_session=never-issued"},
		{"garbage", "tkt_session="},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := doRequest(mux, mw, http.MethodGet, "/tickets", map[string]string{"Cookie": tt.cookie})
			wantRedirect(t, rec, http.StatusSeeOther, "/login")
		})
	}
}

// TestMiddlewareEmptyUsersRedirectsToSetup proves the D16 bootstrap gate:
// with an empty users table every protected route redirects 303 to /setup.
func TestMiddlewareEmptyUsersRedirectsToSetup(t *testing.T) {
	s := openTestStore(t)
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tickets", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("protected")) })
	mux.HandleFunc("GET /users", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("users")) })

	for _, target := range []string{"/tickets", "/users"} {
		rec := doRequest(mux, mw, http.MethodGet, target, nil)
		wantRedirect(t, rec, http.StatusSeeOther, "/setup")
	}
}

// TestMiddlewareSetupExemptWithEmptyUsers proves /setup stays reachable when
// the users table is empty (first-user bootstrap flow).
func TestMiddlewareSetupExemptWithEmptyUsers(t *testing.T) {
	s := openTestStore(t)
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /setup", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("setup-form")) })

	rec := doRequest(mux, mw, http.MethodGet, "/setup", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (setup must be reachable)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "setup-form") {
		t.Errorf("body = %q, want the setup stub to run", rec.Body.String())
	}
}

// TestMiddlewareSetupUnavailableWithUsers proves the bootstrap flow is not
// available once at least one user exists (bootstrap-unavailable spec):
// anonymous visitors are directed to login, authed users to tickets.
func TestMiddlewareSetupUnavailableWithUsers(t *testing.T) {
	s := openTestStore(t)
	user := seedUser(t, s, "Ana", "ana@example.com")
	session := seedSession(t, s, user.ID)
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /setup", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("setup-form")) })

	t.Run("anonymous to login", func(t *testing.T) {
		rec := doRequest(mux, mw, http.MethodGet, "/setup", nil)
		wantRedirect(t, rec, http.StatusSeeOther, "/login")
	})
	t.Run("authed to tickets", func(t *testing.T) {
		rec := doRequest(mux, mw, http.MethodGet, "/setup", map[string]string{"Cookie": "tkt_session=" + session.ID})
		wantRedirect(t, rec, http.StatusSeeOther, "/tickets")
	})
}

// TestMiddlewareValidSessionReachesHandler proves a valid cookie resolves the
// session and the user flows into the handler via request context.
func TestMiddlewareValidSessionReachesHandler(t *testing.T) {
	s := openTestStore(t)
	user := seedUser(t, s, "Ana", "ana@example.com")
	session := seedSession(t, s, user.ID)
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tickets", func(w http.ResponseWriter, r *http.Request) {
		u := userFromContext(r.Context())
		if u == nil {
			http.Error(w, "no user in context", http.StatusInternalServerError)
			return
		}
		w.Write([]byte("ok:" + u.Name + ":" + u.Email))
	})

	rec := doRequest(mux, mw, http.MethodGet, "/tickets", map[string]string{"Cookie": "tkt_session=" + session.ID})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "ok:Ana:ana@example.com" {
		t.Errorf("body = %q, want the session user in context", got)
	}
}

// TestMiddlewareDeactivatedUserSessionKilled proves D14: a deactivated
// user's session is rejected AND the session row is destroyed, so the next
// request is logged out.
func TestMiddlewareDeactivatedUserSessionKilled(t *testing.T) {
	s := openTestStore(t)
	user := seedUser(t, s, "Ana", "ana@example.com")
	session := seedSession(t, s, user.ID)
	// Deactivate through the store (the deactivation handler path is 5.6).
	user.Active = false
	if err := s.UserStore().Update(context.Background(), user); err != nil {
		t.Fatalf("deactivate user: %v", err)
	}
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tickets", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("protected")) })

	rec := doRequest(mux, mw, http.MethodGet, "/tickets", map[string]string{"Cookie": "tkt_session=" + session.ID})

	wantRedirect(t, rec, http.StatusSeeOther, "/login")
	_, err := s.SessionStore().GetByID(context.Background(), session.ID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("session must be deleted after deactivation, GetByID err = %v (want ErrNotFound)", err)
	}
}

// TestMiddlewareAuthedUserOnLoginRedirectsToTickets proves an authenticated
// user hitting /login is sent to /tickets instead of the login form.
func TestMiddlewareAuthedUserOnLoginRedirectsToTickets(t *testing.T) {
	s := openTestStore(t)
	user := seedUser(t, s, "Ana", "ana@example.com")
	session := seedSession(t, s, user.ID)
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("login-form")) })

	rec := doRequest(mux, mw, http.MethodGet, "/login", map[string]string{"Cookie": "tkt_session=" + session.ID})
	wantRedirect(t, rec, http.StatusSeeOther, "/tickets")

	// Without a session the login form is served.
	anon := doRequest(mux, mw, http.MethodGet, "/login", nil)
	if anon.Code != http.StatusOK || !strings.Contains(anon.Body.String(), "login-form") {
		t.Errorf("anonymous /login must reach the handler, got %d %q", anon.Code, anon.Body.String())
	}
}

// TestMiddlewareHealthzExempt proves /healthz needs no session.
func TestMiddlewareHealthzExempt(t *testing.T) {
	s := openTestStore(t)
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })

	rec := doRequest(mux, mw, http.MethodGet, "/healthz", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Errorf("healthz = %d %q, want 200 ok", rec.Code, rec.Body.String())
	}
}

// TestMiddlewareCrossSiteOriginPOSTRejected proves D17: a POST carrying a
// cross-site Origin header is rejected with 403 before any handler runs —
// even on exempt routes (login CSRF).
func TestMiddlewareCrossSiteOriginPOSTRejected(t *testing.T) {
	s := openTestStore(t)
	seedUser(t, s, "Ana", "ana@example.com")
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore())
	mux := http.NewServeMux()
	mux.HandleFunc("POST /tickets", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("created")) })
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("login")) })

	for _, target := range []string{"/tickets", "/login"} {
		t.Run(target, func(t *testing.T) {
			rec := doRequest(mux, mw, http.MethodPost, target, map[string]string{"Origin": "https://evil.example"})
			if rec.Code != http.StatusForbidden {
				t.Errorf("cross-site POST %s status = %d, want 403", target, rec.Code)
			}
		})
	}
	t.Run("malformed origin", func(t *testing.T) {
		rec := doRequest(mux, mw, http.MethodPost, "/tickets", map[string]string{"Origin": ":::not-a-url:::"})
		if rec.Code != http.StatusForbidden {
			t.Errorf("malformed Origin status = %d, want 403", rec.Code)
		}
	})
}

// TestMiddlewareSameOriginPOSTAllowed proves same-origin and header-less
// POSTs pass the D17 gate.
func TestMiddlewareSameOriginPOSTAllowed(t *testing.T) {
	s := openTestStore(t)
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore())
	mux := http.NewServeMux()
	mux.HandleFunc("POST /tickets", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("created")) })

	t.Run("same origin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "http://tkt.test/tickets", nil)
		req.Header.Set("Origin", "http://tkt.test")
		rec := httptest.NewRecorder()
		mw.Wrap(mux).ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther && rec.Code != http.StatusOK {
			t.Errorf("same-origin POST status = %d, want the request to proceed", rec.Code)
		}
	})
	t.Run("no origin header", func(t *testing.T) {
		rec := doRequest(mux, mw, http.MethodPost, "/tickets", nil)
		if rec.Code == http.StatusForbidden {
			t.Errorf("POST without Origin must not be rejected, got 403")
		}
	})
}

// TestMiddlewareGetWithOriginIgnored proves the Origin gate only applies to
// unsafe methods.
func TestMiddlewareGetWithOriginIgnored(t *testing.T) {
	s := openTestStore(t)
	seedUser(t, s, "Ana", "ana@example.com")
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("login-form")) })

	rec := doRequest(mux, mw, http.MethodGet, "/login", map[string]string{"Origin": "https://evil.example"})
	if rec.Code != http.StatusOK {
		t.Errorf("GET with cross-site Origin must be ignored, got %d", rec.Code)
	}
}

// TestMiddlewareSessionStoreFailure500 proves an operational session-store
// failure answers 500 — never a misleading login redirect for a valid user.
func TestMiddlewareSessionStoreFailure500(t *testing.T) {
	s := openTestStore(t)
	mw := NewSessionMiddleware(failingSessionStore{}, s.UserStore())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tickets", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("protected")) })

	rec := doRequest(mux, mw, http.MethodGet, "/tickets", map[string]string{"Cookie": "tkt_session=any"})

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("session-store failure status = %d, want 500", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("session-store failure must not redirect, got Location %q", loc)
	}
}

// TestMiddlewareUserStoreFailure500 proves an operational user-store failure
// answers 500 for an authenticated request — never a login redirect.
func TestMiddlewareUserStoreFailure500(t *testing.T) {
	s := openTestStore(t)
	user := seedUser(t, s, "Ana", "ana@example.com")
	session := seedSession(t, s, user.ID)
	mw := NewSessionMiddleware(s.SessionStore(), failingUserStore{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tickets", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("protected")) })

	rec := doRequest(mux, mw, http.MethodGet, "/tickets", map[string]string{"Cookie": "tkt_session=" + session.ID})

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("user-store failure status = %d, want 500", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("user-store failure must not redirect, got Location %q", loc)
	}
}
