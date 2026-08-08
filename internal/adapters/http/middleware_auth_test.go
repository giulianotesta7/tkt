package httpadapter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/adapters/sqlite"
	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// openTestStore opens a real modernc SQLite database in a temp dir, applies
// ALL migrations, and registers cleanup. File-backed (not shared-cache
// memory) because the http package cannot reach the sqlite package-private
// test helpers; the real driver + real pragmas (FK on, immediate tx) are the
// same code path production runs.
func openTestStore(t *testing.T) *sqlite.Store {
	t.Helper()
	s, err := sqlite.Open(t.TempDir() + "/app.db")
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate test store: %v", err)
	}
	return s
}

// testClock is the injected clock for every service under test (D7): fixed
// instants keep fixtures and goldens deterministic.
type testClock struct{ now time.Time }

func (c testClock) Now() time.Time { return c.now }

var fixedNow = time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)

// seedUser inserts an active user directly through the store port.
func seedUser(t *testing.T, s *sqlite.Store, name, email string) *domain.User {
	t.Helper()
	u := &domain.User{Name: name, Email: email, PasswordHash: "not-a-real-hash", Active: true, CreatedAt: fixedNow}
	if err := s.UserStore().Create(context.Background(), u); err != nil {
		t.Fatalf("seed user %q: %v", email, err)
	}
	return u
}

// seedSession inserts a session expiring in +24h (valid by wall clock).
func seedSession(t *testing.T, s *sqlite.Store, userID int64) *domain.Session {
	t.Helper()
	sess := &domain.Session{ID: "tok-" + randomSuffix(), UserID: userID, ExpiresAt: time.Now().Add(application.SessionTTL)}
	if err := s.SessionStore().Create(context.Background(), sess); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return sess
}

func randomSuffix() string {
	return time.Now().Format("150405.000000000")
}

// doRequest runs one request through the middleware-wrapped mux.
func doRequest(mux *http.ServeMux, mw *SessionMiddleware, method, target string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	mw.Wrap(mux).ServeHTTP(rec, req)
	return rec
}

func wantRedirect(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantLocation string) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Errorf("status = %d, want %d", rec.Code, wantStatus)
	}
	if loc := rec.Header().Get("Location"); loc != wantLocation {
		t.Errorf("Location = %q, want %q", loc, wantLocation)
	}
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
