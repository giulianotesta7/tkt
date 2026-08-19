package httpadapter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// authForm builds login/setup form values.
func authForm(email, password string) url.Values {
	return url.Values{"email": {email}, "password": {password}}
}

// TestLoginPageRendered proves GET /login serves the login form page.
func TestLoginPageRendered(t *testing.T) {
	h := newHarness(t)
	h.createUser(t, "Ana", "ana@example.com", "secret")

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Sign in") {
		t.Errorf("login page must show the sign-in form, got: %s", body)
	}
	if strings.Contains(body, `<aside class="rail">`) {
		t.Errorf("login page must NOT render the app rail, got: %s", body)
	}
}

// TestAuthEntryCopyAndFormContracts defines the approved setup/login identity
// while pinning the existing server-rendered form contracts.
func TestAuthEntryCopyAndFormContracts(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		newHarness func(*testing.T) *harness
		mustHave   []string
		mustMiss   []string
		form       []string
	}{
		{
			name:       "setup",
			path:       "/setup",
			newHarness: newEmptyHarness,
			mustHave: []string{
				"Set up tkt",
				"Create the first account for your support team. This only happens once.",
				"Create account",
				"Your password is stored securely.",
			},
			mustMiss: []string{"Ticket Desk", "Server-side ticketing", "audit trail", "Go + HTMX + SQLite", "bcrypt", "Open source"},
			form: []string{
				`<form class="login-form" method="post" action="/setup" novalidate>`,
				`<label for="name">Name</label>`, `name="name"`, `autocomplete="name"`,
				`<label for="email">Email</label>`, `name="email"`, `autocomplete="email"`,
				`<label for="password">Password</label>`, `name="password"`, `autocomplete="new-password"`,
			},
		},
		{
			name:       "login",
			path:       "/login",
			newHarness: newHarness,
			mustHave:   []string{"tkt", "Sign in"},
			mustMiss:   []string{"Ticket Desk", "Server-side ticketing", "audit trail", "Go + HTMX + SQLite", "bcrypt", "Open source"},
			form: []string{
				`<form class="login-form" method="post" action="/login" novalidate>`,
				`<label for="email">Email</label>`, `name="email"`, `autocomplete="email"`,
				`<label for="password">Password</label>`, `name="password"`, `autocomplete="current-password"`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := tt.newHarness(t)
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()
			h.mw.Wrap(h.mux).ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			body := rec.Body.String()
			for _, want := range tt.mustHave {
				if !strings.Contains(body, want) {
					t.Errorf("page must contain %q", want)
				}
			}
			for _, prohibited := range tt.mustMiss {
				if strings.Contains(body, prohibited) {
					t.Errorf("page must not contain prohibited copy %q", prohibited)
				}
			}
			for _, contract := range tt.form {
				if !strings.Contains(body, contract) {
					t.Errorf("page must retain form contract %q", contract)
				}
			}
		})
	}
}

// TestLoginSuccess proves the login-spec happy path: correct credentials
// create a fresh server-side session, issue the session cookie, and redirect
// into the application (303 /tickets).
func TestLoginSuccess(t *testing.T) {
	h := newHarness(t)
	h.createUser(t, "Ana", "ana@example.com", "secret")

	rec := h.postFormAs(t, "/login", authForm("ana@example.com", "secret"), "")

	wantRedirect(t, rec, http.StatusSeeOther, "/tickets")
	cookies := rec.Result().Cookies()
	var session *http.Cookie
	for _, c := range cookies {
		if c.Name == sessionCookie {
			session = c
		}
	}
	if session == nil {
		t.Fatalf("login must Set-Cookie %s, got %v", sessionCookie, cookies)
	}
	if session.Value == "" {
		t.Fatal("session cookie must carry the opaque token")
	}
	if !session.HttpOnly || session.SameSite != http.SameSiteStrictMode {
		t.Errorf("cookie flags: HttpOnly=%v SameSite=%v, want HttpOnly+Strict", session.HttpOnly, session.SameSite)
	}

	// The issued token resolves to a real server-side session row.
	if _, err := h.store.SessionStore().GetByID(context.Background(), session.Value); err != nil {
		t.Errorf("issued token must resolve to a session row: %v", err)
	}
}

// TestLoginFailureSameGenericError proves wrong password, unknown email, and
// deactivated users all fail with the SAME generic 401 and no session
// (login spec: no user enumeration).
func TestLoginFailureSameGenericError(t *testing.T) {
	h := newHarness(t)
	ana := h.createUser(t, "Ana", "ana@example.com", "secret")
	// Deactivate ana (store-level; the deactivation UI lands in 5.6).
	ana.Active = false
	if err := h.store.UserStore().Update(context.Background(), ana); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	cases := []struct {
		name  string
		email string
		pw    string
	}{
		{"wrong password", "ana@example.com", "wrong"},
		{"unknown email", "nobody@example.com", "secret"},
		{"deactivated user", "ana@example.com", "secret"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			rec := h.postFormAs(t, "/login", authForm(tt.email, tt.pw), "")

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rec.Code)
			}
			body := rec.Body.String()
			if !strings.Contains(body, "invalid credentials") {
				t.Errorf("re-render must show the generic message, got: %s", body)
			}
			if got := len(rec.Result().Cookies()); got != 0 {
				t.Errorf("failed login must not Set-Cookie, got %d cookies", got)
			}
		})
	}
}

// TestLoginEmptyFieldsGeneric proves empty fields degrade to the same
// generic 401 (an empty email is an unknown email; no validation branch
// distinguishes it).
func TestLoginEmptyFieldsGeneric(t *testing.T) {
	h := newHarness(t)
	h.createUser(t, "Ana", "ana@example.com", "secret")

	rec := h.postFormAs(t, "/login", authForm("", ""), "")

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

// TestLogoutDestroysSessionAndClearsCookie proves the logout spec: the
// server-side session row is destroyed, the cookie is cleared, and the next
// request is unauthenticated.
func TestLogoutDestroysSessionAndClearsCookie(t *testing.T) {
	h := newHarness(t)
	h.createUser(t, "Ana", "ana@example.com", "secret")

	login := h.postFormAs(t, "/login", authForm("ana@example.com", "secret"), "")
	session := cookieValue(t, login, sessionCookie)

	rec := h.postFormAs(t, "/logout", url.Values{}, session)

	wantRedirect(t, rec, http.StatusSeeOther, "/login")
	cleared := cookieValue(t, rec, sessionCookie)
	if cleared != "" {
		t.Errorf("logout must clear the cookie, got value %q", cleared)
	}
	if _, err := h.store.SessionStore().GetByID(context.Background(), session); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("session row must be destroyed, GetByID err = %v (want ErrNotFound)", err)
	}

	// The old token no longer authenticates.
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec2 := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec2, req)
	_ = rec2
	probe := httptest.NewRequest(http.MethodGet, "/login", nil)
	probe.Header.Set("Cookie", sessionCookie+"="+session)
	recProbe := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(recProbe, probe)
	if recProbe.Code != http.StatusOK {
		t.Errorf("old token must be unauthenticated (login form served), got %d", recProbe.Code)
	}
}

// TestLogoutIdempotent proves logout with a stale cookie still redirects to
// login (Delete on a missing row is a no-op).
func TestLogoutIdempotent(t *testing.T) {
	h := newHarness(t)
	h.createUser(t, "Ana", "ana@example.com", "secret")

	rec := h.postFormAs(t, "/logout", url.Values{}, "never-issued")

	wantRedirect(t, rec, http.StatusSeeOther, "/login")
}

// TestLogoutSessionStoreFailure500 proves a session-revocation failure
// answers 500 and never reports a successful logout while the server-side
// session survives.
func TestLogoutSessionStoreFailure500(t *testing.T) {
	s := openTestStore(t)
	user := seedUser(t, s, "Ana", "ana@example.com")
	session := seedSession(t, s, user.ID)
	clock := testClock{now: fixedNow}
	usersSvc := application.NewUserService(s.UserStore(), clock)
	authSvc := application.NewAuthService(s.UserStore(), failingSessionStore{}, clock)
	renderer := NewRenderer()

	mux := http.NewServeMux()
	NewAuthHandlers(authSvc, usersSvc, renderer).Register(mux)
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore(), s.SettingsStore())

	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.Header.Set("Cookie", sessionCookie+"="+session.ID)
	rec := httptest.NewRecorder()
	mw.Wrap(mux).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("logout store failure status = %d, want 500", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("failed logout must not redirect, got Location %q", loc)
	}
	if got := len(rec.Result().Cookies()); got != 0 {
		t.Errorf("failed logout must not Set-Cookie (client keeps it for retry), got %d cookies", got)
	}
}

// TestSetupPageShownWhenEmpty proves the first-user bootstrap flow: with an
// empty users table the setup form is served.
func TestSetupPageShownWhenEmpty(t *testing.T) {
	h := newEmptyHarness(t)

	req := httptest.NewRequest(http.MethodGet, "/setup", nil)
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Set up tkt") {
		t.Errorf("setup page must show the first-user form, got: %s", rec.Body.String())
	}
}

// TestSetupCreatesFirstActiveUser proves POST /setup creates the first user
// atomically as an ACTIVE ROOT (role-authorization first-user bootstrap) and
// redirects to /login; that user can then log in (never-locked-out spec).
// The role assertion is the S2 RED: until BootstrapRoot lands, the store
// does not persist a role and the setup flow creates an ordinary account.
func TestSetupCreatesFirstActiveUser(t *testing.T) {
	h := newEmptyHarness(t)

	form := url.Values{"name": {"Ana"}, "email": {"ana@example.com"}, "password": {"secret"}}
	rec := h.postFormAs(t, "/setup", form, "")

	wantRedirect(t, rec, http.StatusSeeOther, "/login")

	u, err := h.store.UserStore().GetByEmail(context.Background(), "ana@example.com")
	if err != nil {
		t.Fatalf("created user must exist: %v", err)
	}
	if !u.Active {
		t.Error("first user must be active")
	}
	if u.Role != domain.RoleRoot {
		t.Errorf("first user role = %q, want %q", u.Role, domain.RoleRoot)
	}
	if u.ID == 0 {
		t.Error("first user must receive a unique identifier")
	}

	// The bootstrap user can log in.
	login := h.postFormAs(t, "/login", authForm("ana@example.com", "secret"), "")
	wantRedirect(t, login, http.StatusSeeOther, "/tickets")
}

// TestSetupConcurrentSubmissionsProduceOneRoot proves the atomic-bootstrap
// contract (role-authorization "Concurrent bootstrap"): two simultaneous
// /setup submissions create EXACTLY one root and the loser fails without
// creating an account. BootstrapRoot runs under BEGIN IMMEDIATE with a
// conditional insert, so the second writer sees the first user and is
// redirected away — never a second user, never an ordinary account, never
// two roots. Written before BootstrapRoot exists: it fails at compile time
// (RED) until the use case replaces the plain create in the setup flow.
func TestSetupConcurrentSubmissionsProduceOneRoot(t *testing.T) {
	h := newEmptyHarness(t)
	handler := h.mw.Wrap(h.mux)

	const (
		emailA = "ana@example.com"
		emailB = "beto@example.com"
	)

	// Two simultaneous submissions, each with distinct credentials. Both
	// requests are fully independent (separate goroutines, separate DB
	// pool connections — the harness store is file-backed with the default
	// pool, so this exercises the real BEGIN IMMEDIATE write serialization).
	start := make(chan struct{})
	recs := make([]*httptest.ResponseRecorder, 2)
	forms := []url.Values{
		{"name": {"Ana"}, "email": {emailA}, "password": {"secret-a"}},
		{"name": {"Beto"}, "email": {emailB}, "password": {"secret-b"}},
	}
	var wg sync.WaitGroup
	for i := range forms {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/setup", strings.NewReader(forms[i].Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			<-start
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			recs[i] = rec
		}(i)
	}
	close(start)
	wg.Wait()

	// Exactly one root survives; the other submission created nothing.
	for i, rec := range recs {
		if rec.Code != http.StatusSeeOther {
			t.Errorf("request %d status = %d, want 303 redirect", i, rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/login" {
			t.Errorf("request %d Location = %q, want /login", i, loc)
		}
	}

	count, err := h.store.UserStore().Count(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("user count = %d, want exactly 1 (one root, no extra accounts)", count)
	}

	var root *domain.User
	for _, email := range []string{emailA, emailB} {
		u, err := h.store.UserStore().GetByEmail(context.Background(), email)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				continue
			}
			t.Fatal(err)
		}
		root = u
	}
	if root == nil {
		t.Fatal("no user was created at all")
	}
	if root.Role != domain.RoleRoot {
		t.Errorf("surviving user role = %q, want %q", root.Role, domain.RoleRoot)
	}
	if !root.Active {
		t.Error("surviving user must be active")
	}
}

// TestSetupValidationErrorReRenders proves invalid setup input re-renders
// the form with the validation message (422).
func TestSetupValidationErrorReRenders(t *testing.T) {
	h := newEmptyHarness(t)

	const submittedPassword = "do-not-echo-this-password"
	form := url.Values{"name": {"  "}, "email": {"ana@example.com"}, "password": {submittedPassword}}
	rec := h.postFormAs(t, "/setup", form, "")

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, domain.ErrMsgUserNameRequired) {
		t.Errorf("re-render must show %q, got: %s", domain.ErrMsgUserNameRequired, body)
	}
	if !strings.Contains(body, `value="  "`) {
		t.Error("re-render must retain the submitted name")
	}
	if !strings.Contains(body, `value="ana@example.com"`) {
		t.Error("re-render must retain the submitted email")
	}
	if strings.Contains(body, submittedPassword) {
		t.Error("re-render must not echo the submitted password")
	}
	if !strings.Contains(body, `role="alert"`) {
		t.Error("validation error must remain an alert")
	}
}

// TestLoginValidationErrorReRendersPreservesSafeValues protects the existing
// generic login failure contract while the entry presentation is refactored.
func TestLoginValidationErrorReRendersPreservesSafeValues(t *testing.T) {
	h := newHarness(t)
	h.createUser(t, "Ana", "ana@example.com", "secret")
	const submittedPassword = "do-not-echo-this-password"
	rec := h.postFormAs(t, "/login", authForm("ana@example.com", submittedPassword), "")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `value="ana@example.com"`) {
		t.Error("re-render must retain the submitted email")
	}
	if strings.Contains(body, submittedPassword) {
		t.Error("re-render must not echo the submitted password")
	}
	if !strings.Contains(body, `role="alert"`) || !strings.Contains(body, "invalid credentials") {
		t.Error("generic login error must remain an alert")
	}
}

// TestLoginRedirectsToSetupWhenEmptyUsers proves a visitor hitting /login
// with an empty users table is sent to the bootstrap flow.
func TestLoginRedirectsToSetupWhenEmptyUsers(t *testing.T) {
	h := newEmptyHarness(t)

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)

	wantRedirect(t, rec, http.StatusSeeOther, "/setup")
}

// cookieValue extracts a cookie's value from a recorder response, "" when
// absent or cleared.
func cookieValue(t *testing.T, rec *httptest.ResponseRecorder, name string) string {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}
