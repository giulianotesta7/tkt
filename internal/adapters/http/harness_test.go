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

// seedUserRole inserts an active user with an explicit role directly
// through the store port (test arrange for per-role authorization tests).
func seedUserRole(t *testing.T, s *sqlite.Store, name, email string, role domain.Role) *domain.User {
	t.Helper()
	u := &domain.User{Name: name, Email: email, PasswordHash: "not-a-real-hash", Active: true, Role: role, CreatedAt: fixedNow}
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

// harness is the fully wired test server: every application service over the
// real sqlite store, the real renderer, all registered routes, and the
// middleware-wrapped mux. An admin user with a live session is seeded so
// tests can exercise authenticated routes directly.
type harness struct {
	store        *sqlite.Store
	clock        domain.Clock
	tickets      *application.TicketService
	comments     *application.CommentService
	users        *application.UserService
	auth         *application.AuthService
	categories   *application.CategoryService
	groups       *application.GroupService
	search       *application.SearchService
	renderer     *Renderer
	mux          *http.ServeMux
	mw           *SessionMiddleware
	admin        *domain.User
	adminSession *domain.Session
	bugCategory  *domain.Category
}

func newHarness(t *testing.T) *harness {
	return newHarnessWithAdmin(t, true)
}

// newEmptyHarness wires the same server but seeds NOTHING: no admin user,
// no category — the users table is empty (first-user bootstrap flows).
func newEmptyHarness(t *testing.T) *harness {
	return newHarnessWithAdmin(t, false)
}

func newHarnessWithAdmin(t *testing.T, seedAdmin bool) *harness {
	t.Helper()
	s := openTestStore(t)
	clock := testClock{now: fixedNow}

	usersSvc := application.NewUserService(s.UserStore(), clock)
	authSvc := application.NewAuthService(s.UserStore(), s.SessionStore(), clock)
	catSvc := application.NewCategoryService(s.CategoryStore(), clock)
	groupSvc := application.NewGroupService(s.GroupStore(), s.UserStore(), clock)
	viewBuilder := application.NewViewBuilder(s.TicketStore(), s.UserStore(), s.CategoryStore(), s.CommentStore(), s.AuditStore())
	ticketSvc := application.NewTicketService(s.TicketStore(), s.UserStore(), s.CategoryStore(), s.TicketUnitOfWork(), viewBuilder, clock)
	commentSvc := application.NewCommentService(s.TicketStore(), s.CommentStore(), clock)
	searchSvc := application.NewSearchService(s.TicketStore(), s.SearchStore())
	renderer := NewRenderer()

	mux := http.NewServeMux()
	RegisterStatic(mux)
	NewAuthHandlers(authSvc, usersSvc, renderer).Register(mux)
	NewTicketHandlers(ticketSvc, commentSvc, searchSvc, catSvc, usersSvc, renderer).Register(mux)
	NewUserHandlers(usersSvc, renderer).Register(mux)
	NewCategoryHandlers(catSvc, renderer).Register(mux)
	NewGroupHandlers(groupSvc, renderer).Register(mux)
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore())

	h := &harness{
		store: s, clock: clock,
		tickets: ticketSvc, comments: commentSvc, users: usersSvc, auth: authSvc,
		categories: catSvc, groups: groupSvc, search: searchSvc, renderer: renderer,
		mux: mux, mw: mw,
	}
	if !seedAdmin {
		return h
	}

	// Admin operator + live session for authenticated requests. The harness
	// admin is a REAL admin: assign the role in memory AND persist it, so
	// service calls using the admin as actor exercise the admin capability
	// path (UserService.Create leaves the in-memory role empty — the store
	// default would silently make it an agent).
	admin, err := usersSvc.Create(context.Background(), application.CreateUserInput{Name: "Admin", Email: "admin@tkt.test", Password: "secret"})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	admin.Role = domain.RoleAdmin
	if err := s.UserStore().Update(context.Background(), admin); err != nil {
		t.Fatalf("promote admin role: %v", err)
	}
	sess, err := authSvc.Login(context.Background(), "admin@tkt.test", "secret")
	if err != nil {
		t.Fatalf("admin login: %v", err)
	}
	bugs, err := catSvc.Create(context.Background(), "Bugs")
	if err != nil {
		t.Fatalf("seed category: %v", err)
	}
	h.admin = admin
	h.adminSession = sess
	h.bugCategory = bugs
	return h
}

// get runs an authenticated GET through the middleware-wrapped mux.
func (h *harness) get(t *testing.T, path string, hx bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
	if hx {
		req.Header.Set("HX-Request", "true")
	}
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)
	return rec
}

// postForm runs an authenticated form POST through the middleware-wrapped
// mux (optionally as an HTMX request).
func (h *harness) postForm(t *testing.T, path string, form url.Values, hx bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
	if hx {
		req.Header.Set("HX-Request", "true")
	}
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)
	return rec
}

// postFormAs is postForm with an explicit session cookie (unauthenticated
// flows, logout, other actors).
func (h *harness) postFormAs(t *testing.T, path string, form url.Values, sessionID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", sessionCookie+"="+sessionID)
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)
	return rec
}

// createUser registers a user with a real bcrypt password (login-ready).
func (h *harness) createUser(t *testing.T, name, email, password string) *domain.User {
	t.Helper()
	u, err := h.users.Create(context.Background(), application.CreateUserInput{Name: name, Email: email, Password: password})
	if err != nil {
		t.Fatalf("create user %q: %v", email, err)
	}
	// Most legacy ticket-handler fixtures need an assignable staff member.
	// New managed users default to the user role; test fixtures explicitly
	// promote their synthetic target to agent instead of relying on that old
	// implicit default.
	u.Role = domain.RoleAgent
	if err := h.store.UserStore().Update(context.Background(), u); err != nil {
		t.Fatalf("promote fixture user %q: %v", email, err)
	}
	return u
}

// seedTicket creates a ticket through the real service (audit event
// included). Defaults: category Bugs, priority medium, unassigned; mod may
// override any input field.
func (h *harness) seedTicket(t *testing.T, title string, mod func(*application.CreateTicketInput)) *domain.Ticket {
	t.Helper()
	in := application.CreateTicketInput{
		Title:       title,
		Description: "Test description",
		CategoryID:  h.bugCategory.ID,
		Priority:    domain.PriorityMedium,
	}
	if mod != nil {
		mod(&in)
	}
	tkt, err := h.tickets.Create(context.Background(), *h.admin, in)
	if err != nil {
		t.Fatalf("seed ticket %q: %v", title, err)
	}
	return tkt
}

// seedTransition moves a seeded ticket through the state machine via the
// real service.
func (h *harness) seedTransition(t *testing.T, id int64, to domain.State, reason string) {
	t.Helper()
	if _, err := h.tickets.Transition(context.Background(), *h.admin, id, to, reason); err != nil {
		t.Fatalf("transition ticket %d -> %s: %v", id, to, err)
	}
}

// loginCookie returns a fresh session token for email/password (or "" on
// failure).
func (h *harness) loginCookie(t *testing.T, email, password string) string {
	t.Helper()
	rec := h.postFormAs(t, "/login", url.Values{"email": {email}, "password": {password}}, "")
	if rec.Code != http.StatusSeeOther {
		return ""
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			return c.Value
		}
	}
	return ""
}
