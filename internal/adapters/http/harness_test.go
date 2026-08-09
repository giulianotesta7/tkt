package httpadapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// testClock is the injected clock for every service under test (D7).
// The instant is real-relative (the sqlite session store enforces the 24h
// TTL against wall clock, so sessions minted by the services must expire in
// the future); deterministic golden output comes from literal fixture
// instants, never from service-minted times.
type testClock struct{ now time.Time }

func (c testClock) Now() time.Time { return c.now }

var fixedNow = time.Now()

// seedUser inserts an active user directly through the store port (no
// password usable for login — use userService.Create for that).
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
	sess := &domain.Session{ID: "tok-" + time.Now().Format("150405.000000000"), UserID: userID, ExpiresAt: time.Now().Add(application.SessionTTL)}
	if err := s.SessionStore().Create(context.Background(), sess); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return sess
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

// wantRedirect asserts a redirect status and Location.
func wantRedirect(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantLocation string) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Errorf("status = %d, want %d", rec.Code, wantStatus)
	}
	if loc := rec.Header().Get("Location"); loc != wantLocation {
		t.Errorf("Location = %q, want %q", loc, wantLocation)
	}
}

// authHarness wires the services available before the ticket stores: the
// auth + user use cases against the real sqlite store, the real renderer,
// and the middleware-wrapped mux with the auth routes registered.
type authHarness struct {
	store    *sqlite.Store
	users    *application.UserService
	auth     *application.AuthService
	renderer *Renderer
	mux      *http.ServeMux
	mw       *SessionMiddleware
}

func newAuthHarness(t *testing.T) *authHarness {
	t.Helper()
	s := openTestStore(t)
	clock := testClock{now: fixedNow}
	usersSvc := application.NewUserService(s.UserStore(), clock)
	authSvc := application.NewAuthService(s.UserStore(), s.SessionStore(), clock)
	renderer := NewRenderer()

	mux := http.NewServeMux()
	NewAuthHandlers(authSvc, usersSvc, renderer).Register(mux)
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore())
	return &authHarness{store: s, users: usersSvc, auth: authSvc, renderer: renderer, mux: mux, mw: mw}
}

// createUser registers a user with a real bcrypt password (login-ready).
func (h *authHarness) createUser(t *testing.T, name, email, password string) *domain.User {
	t.Helper()
	u, err := h.users.Create(context.Background(), application.CreateUserInput{Name: name, Email: email, Password: password})
	if err != nil {
		t.Fatalf("create user %q: %v", email, err)
	}
	return u
}

// postForm builds a form-encoded POST request against the harness.
func (h *authHarness) postForm(t *testing.T, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)
	return rec
}

// postFormAs is postForm with a session cookie attached.
func (h *authHarness) postFormAs(t *testing.T, path string, form url.Values, sessionID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", sessionCookie+"="+sessionID)
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)
	return rec
}
