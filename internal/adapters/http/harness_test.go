package httpadapter

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/adapters/msgtext"
	"github.com/giulianotesta7/tkt/internal/adapters/secretbox"
	"github.com/giulianotesta7/tkt/internal/adapters/sqlite"
	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// openTestStore opens a real modernc SQLite database in a temp dir, applies
// ALL migrations, and registers cleanup. File-backed (not shared-cache
// memory) because the http package cannot reach the sqlite package-private
// test helpers; the real driver and the real pragmas (FK on, WAL, immediate
// tx) are the same code path production runs — with one deliberate exception:
// the harness opens at synchronous=NORMAL via sqlite.OpenForTests instead of
// production's FULL. That divergence is safe because these databases are
// disposable and a test does not prove power-loss durability, so a durable
// fsync per commit buys nothing here. See sqlite.OpenForTests for the full
// rationale; the shared pragmas are single-sourced there, not duplicated.
func openTestStore(t *testing.T) *sqlite.Store {
	t.Helper()
	return openTestStoreAt(t, t.TempDir()+"/app.db")
}

// openTestStoreAt opens a real modernc SQLite database at an explicit path,
// applies ALL migrations, and registers cleanup. The path is recorded by the
// harness so integration tests can open a second read handle for raw-row
// assertions (e.g. proving a workflow create pinned the version and that a
// rollback left zero run/audit rows).
func openTestStoreAt(t *testing.T, dbPath string) *sqlite.Store {
	t.Helper()
	// Deliberate divergence from production: synchronous=NORMAL instead of
	// FULL. These databases are disposable and the tests do not prove
	// power-loss durability, so the durable fsync on every commit is pure
	// cost. Every other pragma stays identical through the shared fragment in
	// sqlite.OpenForTests; see that function for the full reason.
	s, err := sqlite.OpenForTests(dbPath)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
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

// advanceableClock is the harness clock a test can move forward. The recovery
// path measures an hour of validity and a five-minute throttle against injected
// instants, so a test that needs an expired link advances the clock instead of
// sleeping (no test in this repository sleeps).
type advanceableClock struct{ now time.Time }

func (c *advanceableClock) Now() time.Time { return c.now }

// Advance moves the clock forward by d.
func (c *advanceableClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

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

// fakeMailer is the harness's Mailer: it records every message the email
// settings service hands it, so a test proves what was sent, to whom, and
// through which server without opening a socket (this layer never does). err
// makes every send fail with a transport-style refusal. The harness is
// single-goroutine (no test here uses t.Parallel), so the fake needs no lock;
// a background sender would need one.
type fakeMailer struct {
	sent []application.OutgoingEmail
	cfg  application.SMTPConfig
	err  error
}

var _ application.Mailer = (*fakeMailer)(nil)

func (m *fakeMailer) Send(_ context.Context, cfg application.SMTPConfig, email application.OutgoingEmail) error {
	m.cfg = cfg
	if m.err != nil {
		return m.err
	}
	m.sent = append(m.sent, email)
	return nil
}

// harness is the fully wired test server: every application service over the
// real sqlite store, the real renderer, all registered routes, and the
// middleware-wrapped mux. An admin user with a live session is seeded so
// tests can exercise authenticated routes directly.
type harness struct {
	store         *sqlite.Store
	dbPath        string
	clock         *advanceableClock
	tickets       *application.TicketService
	comments      *application.CommentService
	users         *application.UserService
	auth          *application.AuthService
	categories    *application.CategoryService
	desks         *application.DeskService
	search        *application.SearchService
	settings      *application.SettingsService
	workflows     *application.WorkflowService
	sla           *application.SLAService
	preferences   *application.PreferencesService
	emailSettings *application.EmailSettingsService
	passwords     *application.PasswordSettingsService
	reset         *application.PasswordResetService
	visits        *application.TicketVisitService
	mailer        *fakeMailer
	// companySettings and companySignIn are the company sign-in surface (issue
	// #419, S11): the real configuration service over the sealed store, and the
	// flow over the real identity and session stores. Only the provider is
	// faked, because this layer never opens a socket.
	companySettings *application.CompanySignInSettingsService
	companySignIn   *application.CompanySignInService
	companyProvider *fakeCompanyProvider
	// Category suggestions (issue #416): the settings use case, the composition,
	// and the fake decision model wired at its port. The local engine runs over
	// the REAL sqlite index, so a test proves the triggers and the ranking, not a
	// stand-in.
	suggestionSettings *application.CategorySuggestionSettingsService
	suggestionSvc      *application.CategorySuggestionService
	decisionModel      *fakeDecisionModel
	renderer           *Renderer
	mux                *http.ServeMux
	mw                 *SessionMiddleware
	admin              *domain.User
	adminSession       *domain.Session
	bugCategory        *domain.Category
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
	dbPath := t.TempDir() + "/app.db"
	s := openTestStoreAt(t, dbPath)
	clock := &advanceableClock{now: fixedNow}

	usersSvc := application.NewUserService(s.UserStore(), clock).WithPasswordSettings(s.PasswordSettingsStore()).WithPasswordChangeStore(s.PasswordChangeStore()).WithCompanySignIn(s.CompanySignInSettingsStore())
	authSvc := application.NewAuthService(s.UserStore(), s.SessionStore(), clock).WithCompanySignIn(s.CompanySignInSettingsStore())
	catSvc := application.NewCategoryService(s.CategoryStore(), clock)
	deskSvc := application.NewDeskService(s.DeskStore(), s.UserStore(), clock)
	viewBuilder := application.NewViewBuilder(s.TicketStore(), s.UserStore(), s.CategoryStore(), s.CommentStore(), s.AuditStore(), s.DeskStore(), s.WorkflowResponseStore())
	slaSvc := application.NewSLAService(s.SLAStore(), s.SettingsStore(), clock)
	ticketSvc := application.NewTicketServiceWithWorkflowCreate(s.TicketStore(), s.UserStore(), s.CategoryStore(), s.TicketUnitOfWork(), viewBuilder, clock, s.WorkflowVersionStore(), application.NewWorkflowRunner(clock), s.WorkflowUnitOfWork(), slaSvc)
	commentSvc := application.NewCommentService(s.TicketStore(), s.CommentStore(), s.UserStore(), s.CommentUnitOfWork(), clock).WithSLA(slaSvc)
	attachmentSvc := application.NewAttachmentService(s.TicketStore(), s.AttachmentStore())
	searchSvc := application.NewSearchService(s.TicketStore(), s.SearchStore())
	settingsSvc := application.NewSettingsService(s.SettingsStore())
	preferencesSvc := application.NewPreferencesService(s.PreferencesStore(), s.EmailPreferences(), s.SidebarPreferences(), s.ThemePreferences())
	// Issue #415: the last-visit mark, wired exactly where WithSLA and WithClock
	// are, so every route below renders and records it.
	visitSvc := application.NewTicketVisitService(s.TicketVisitStore(), clock)
	workflowSvc := application.NewWorkflowService(s.WorkflowStore())
	metricsSvc := application.NewTicketMetricsService(s.TicketMetricsStore(), clock)
	// Issue #362: the SMTP password is sealed with the key file that lives next
	// to the database in the data volume (S9), so the harness uses the REAL box
	// over its own temp dir and fakes only the transport — a test must observe
	// the send without opening a socket.
	mailer := &fakeMailer{}
	secretBox := secretbox.New(filepath.Dir(dbPath))
	emailSettingsSvc := application.NewEmailSettingsService(s.EmailSettingsStore(), secretBox, mailer, s.SettingsStore(), msgtext.Renderer{})
	// Issue #419: the company sign-in configuration seals each client secret
	// with its own purpose, and the flow drives the real identity store, the
	// real session store, and a fake provider.
	companySettingsSvc := application.NewCompanySignInSettingsService(s.CompanySignInSettingsStore(),
		secretBox.For("google-client-secret"), secretBox.For("microsoft-client-secret"))
	companyProvider := &fakeCompanyProvider{}
	companySignInSvc := application.NewCompanySignInService(companySettingsSvc, s.CompanyIdentityStore(), companyProvider, s.SessionStore(), nil, clock)
	// Issue #416: the suggestion engines answer the creation screens. The local
	// one reads the real index the migration created; the decision one answers
	// through a fake at its port, so no test opens a socket.
	decisionModel := &fakeDecisionModel{}
	suggestionSettingsSvc := application.NewCategorySuggestionSettingsService(s.CategorySuggestionSettingsStore(), secretbox.New(filepath.Dir(dbPath)))
	suggestionSvc := application.NewCategorySuggestionService(suggestionSettingsSvc, application.NewLocalCategorySuggester(s.CategorySuggestionIndex()), application.NewDecisionCategorySuggester(decisionModel))
	signinSvc := application.NewSigninService(s.SigninStore())
	passwordSettingsSvc := application.NewPasswordSettingsService(s.PasswordSettingsStore())
	// Issue #414: the recovery use cases run over the REAL store and the
	// harness's fake mailer, and the send runs where it stands (an inline
	// dispatcher), so a test observes the mailed link as soon as the form
	// answers. Production hands the send to its own goroutine instead.
	resetSvc := application.NewPasswordResetService(s.UserStore(), s.PasswordResetStore(), s.PasswordSettingsStore(), emailSettingsSvc, mailer, msgtext.Renderer{}, func(send func()) { send() }, clock).WithCompanySignIn(s.CompanySignInSettingsStore())
	renderer := NewRenderer()

	mux := http.NewServeMux()
	RegisterStatic(mux, s.SettingsStore())
	NewBrandHandlers(s.OrganizationStore()).WithSignin(s.SigninStore()).Register(mux)
	NewAuthHandlers(authSvc, usersSvc, renderer).WithSignin(signinSvc).WithPasswordRecovery(resetSvc, passwordSettingsSvc, emailSettingsSvc).WithCompanySignIn(companySettingsSvc, companySignInSvc).Register(mux)
	ticketHandlers := NewTicketHandlers(ticketSvc, commentSvc, searchSvc, catSvc, usersSvc, s.DeskStore(), workflowSvc, application.NewWorkflowRunner(clock), s.WorkflowRunStore(), s.WorkflowUnitOfWork(), renderer).WithMetrics(metricsSvc).WithSLA(slaSvc).WithPreferences(preferencesSvc).WithClock(clock).WithVisits(visitSvc).WithAttachments(s.AttachmentStore(), attachmentSvc).WithCategorySuggestions(suggestionSvc)
	ticketHandlers.Register(mux)
	// Issue #419: the Pending tab is wired exactly as production wires it, over
	// the real pending store, so the tab, its count and the bulk approval all run
	// through the real use case.
	NewUserHandlers(usersSvc, renderer).WithPendingUsers(application.NewPendingUsersService(s.PendingUserStore(), clock)).Register(mux)
	NewCategoryHandlersWithWorkflows(catSvc, workflowSvc, renderer, application.NewCatalogService(s.CatalogStore(), s.CategoryStore(), clock)).WithSettings(s.SettingsStore()).Register(mux)
	NewCategoryWorkflowHandlers(catSvc, workflowSvc, deskSvc, renderer).Register(mux)
	NewDeskHandlers(deskSvc, renderer).Register(mux)
	NewSettingsHandlers(settingsSvc, slaSvc, s.SLAStore(), s.SettingsStore(), renderer).WithEmail(emailSettingsSvc).WithOrganization(application.NewOrganizationService(s.OrganizationStore())).WithSignin(signinSvc).WithPasswordSettings(passwordSettingsSvc).WithCategorySuggestions(suggestionSettingsSvc, suggestionSvc, ticketHandlers).WithCompanySignIn(companySettingsSvc).Register(mux)
	NewCategorySLAHandlers(catSvc, slaSvc, s.SLAStore(), renderer).Register(mux)
	NewPreferencesHandlers(preferencesSvc, renderer).WithPasswordChange(usersSvc, passwordSettingsSvc).Register(mux)
	// Issue #419: the pending-account notice renders the stored account and its
	// identities, wired here exactly as production wires it.
	NewNotificationHandlers(s.NotificationStore(), s.TicketStore(), s.UserStore(), renderer, clock).
		WithNotificationSubjects(s.NotificationSubjectStore()).Register(mux)
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore(), s.SettingsStore()).WithSidebarPreferences(s.SidebarPreferences()).WithThemePreferences(s.ThemePreferences()).WithOrganization(s.OrganizationStore())

	h := &harness{
		store: s, dbPath: dbPath, clock: clock,
		tickets: ticketSvc, comments: commentSvc, users: usersSvc, auth: authSvc,
		categories: catSvc, desks: deskSvc, search: searchSvc, settings: settingsSvc, workflows: workflowSvc, sla: slaSvc,
		preferences: preferencesSvc, emailSettings: emailSettingsSvc, passwords: passwordSettingsSvc, reset: resetSvc, mailer: mailer,
		suggestionSettings: suggestionSettingsSvc, suggestionSvc: suggestionSvc, decisionModel: decisionModel,
		visits:          visitSvc,
		companySettings: companySettingsSvc, companySignIn: companySignInSvc, companyProvider: companyProvider,
		renderer: renderer,
		mux:      mux, mw: mw,
	}
	if !seedAdmin {
		return h
	}

	// Admin operator + live session for authenticated requests. Seed the
	// fixture directly so generic HTTP harness setup does not pay bcrypt's
	// hashing and verification cost; authentication-specific tests still use
	// the real user and auth services.
	admin := seedUserRole(t, s, "Admin", "admin@tkt.test", domain.RoleAdmin)
	sess := seedSession(t, s, admin.ID)
	bugs, err := catSvc.Create(context.Background(), "Bugs")
	if err != nil {
		t.Fatalf("seed category: %v", err)
	}
	h.admin = admin
	h.adminSession = sess
	h.bugCategory = bugs
	// Publish a simple valid workflow for the default Bugs category so every
	// ticket-seeding test exercises the REAL workflow-aware create path (design
	// S5 availability = a published version exists). The manual-task fixture is
	// human-pending, so created tickets stay "new" with an active run — exactly
	// the shape unrelated create tests already expect.
	h.publishWorkflow(t, bugs.ID, simpleManualDef())
	return h
}

// seededDesk resolves the compatibility Department/Desk pair migration 0011
// creates ("General"/"General"). A catalog-aware category route needs both, since
// that is what the drawer submits; these tests assert the create/rename/delete
// contracts, not the drawer's preselection.
func (h *harness) seededDesk(t *testing.T) (departmentID, deskID int64) {
	t.Helper()
	departments, err := h.store.CatalogStore().ListDepartments(t.Context())
	if err != nil {
		t.Fatalf("list departments: %v", err)
	}
	for _, department := range departments {
		desks, err := h.store.CatalogStore().ListDesks(t.Context(), department.ID)
		if err != nil {
			t.Fatalf("list desks: %v", err)
		}
		if len(desks) > 0 {
			return department.ID, desks[0].ID
		}
	}
	t.Fatal("no seeded desk")
	return 0, 0
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

// multipartFile is one file of a multipart submission (issue #366).
type multipartFile struct {
	name string
	data []byte
}

// postMultipart runs a multipart/form-data POST through the middleware-wrapped
// mux with an explicit session cookie: the browser's shape for the two forms
// that carry files (ticket creation and comments). Every file travels under
// the `attachments` field, the name both forms use.
func (h *harness) postMultipart(t *testing.T, path string, fields url.Values, files []multipartFile, sessionID string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for name, values := range fields {
		for _, v := range values {
			if err := w.WriteField(name, v); err != nil {
				t.Fatalf("write multipart field %q: %v", name, err)
			}
		}
	}
	for _, f := range files {
		part, err := w.CreateFormFile("attachments", f.name)
		if err != nil {
			t.Fatalf("create file part %q: %v", f.name, err)
		}
		if _, err := part.Write(f.data); err != nil {
			t.Fatalf("write file part %q: %v", f.name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Cookie", sessionCookie+"="+sessionID)
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
	u, err := h.users.Create(context.Background(), *h.admin, application.CreateUserInput{Name: name, Email: email, Password: password})
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

// assignTicket seeds an assignment through the audited Assign service call
// (Amendment 2: creation no longer accepts an assignee, so test fixtures that
// need an assigned ticket create unassigned and then assign — the same audited
// path the production assign flow uses).
func (h *harness) assignTicket(t *testing.T, ticketID, assigneeID int64) {
	t.Helper()
	if _, err := h.tickets.Assign(context.Background(), *h.admin, ticketID, &assigneeID, ""); err != nil {
		t.Fatalf("assign ticket %d to user %d: %v", ticketID, assigneeID, err)
	}
}

// seedTransition moves a seeded ticket through the state machine via the
// real service.
func (h *harness) seedTransition(t *testing.T, id int64, to domain.State, reason string) {
	t.Helper()
	if _, err := h.tickets.Transition(context.Background(), *h.admin, id, to, reason); err != nil {
		t.Fatalf("transition ticket %d -> %s: %v", id, to, err)
	}
}

// makeLegacy strips the requester link so the ticket follows the pre-#55
// requester-NULL semantics: manual closure and the closed-reopen journeys
// remain legal on requester-less tickets (the requester-confirmation paths
// are covered by their own tests).
func (h *harness) makeLegacy(t *testing.T, ticketID int64) {
	t.Helper()
	if _, err := h.rawDB(t).Exec(`UPDATE tickets SET requester_user_id = NULL WHERE id = ?`, ticketID); err != nil {
		t.Fatalf("strip requester from ticket %d: %v", ticketID, err)
	}
}

// rawDB opens a second read handle to the harness's file-backed sqlite db so
// integration tests can assert raw persistence rows (workflow pin, run rows,
// audit rows) that no public store port exposes. The sqlite driver is already
// registered by the sqlite package import above.
func (h *harness) rawDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+h.dbPath)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// scanOneInt runs a scalar-SELECT query against the harness db and returns the
// single int64 it produced, fataling on no rows or scan error.
func scanOneInt(t *testing.T, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var v int64
	if err := db.QueryRow(query, args...).Scan(&v); err != nil {
		t.Fatalf("raw query %q: %v", query, err)
	}
	return v
}

// scanNullInt runs a scalar-SELECT query against the harness db and returns a
// sql.NullInt64, so a NULL column (e.g. a detached workflow_version_id) is
// read without an unsupported NULL->int64 conversion error.
func scanNullInt(t *testing.T, db *sql.DB, query string, args ...any) sql.NullInt64 {
	t.Helper()
	var v sql.NullInt64
	if err := db.QueryRow(query, args...).Scan(&v); err != nil {
		t.Fatalf("raw query %q: %v", query, err)
	}
	return v
}

// scanOneString runs a scalar-SELECT query against the harness db and returns
// the single string it produced, fataling on no rows or scan error.
func scanOneString(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	var v string
	if err := db.QueryRow(query, args...).Scan(&v); err != nil {
		t.Fatalf("raw query %q: %v", query, err)
	}
	return v
}

// scanOneNullableInt runs a scalar-SELECT that may return NULL and returns the
// value plus whether it was non-NULL.
func scanOneNullableInt(t *testing.T, db *sql.DB, query string, args ...any) (int64, bool) {
	t.Helper()
	var v sql.NullInt64
	if err := db.QueryRow(query, args...).Scan(&v); err != nil {
		t.Fatalf("raw query %q: %v", query, err)
	}
	if !v.Valid {
		return 0, false
	}
	return v.Int64, true
}

// staff makes a desk able to host an assignment step by giving it an eligible
// member. It is idempotent, so a test may call it both before and after
// publishing, and the second call is not a failure.
//
// It exists because a desk with no eligible member cannot host an assignment
// step: the publish gate refuses one, since a ticket routed there can never
// move. Several fixtures used to staff their desk AFTER publishing, which left
// the workflow unrunnable at the moment it became live.
func (h *harness) staff(t *testing.T, deskID int64) {
	t.Helper()
	err := h.desks.AddMember(t.Context(), *h.admin, deskID, h.admin.ID)
	if err == nil {
		return
	}
	var dup *domain.DuplicateError
	if !errors.As(err, &dup) {
		t.Fatalf("staff desk %d: %v", deskID, err)
	}
}

// publishWorkflow publishes a simple valid workflow for a category so that the
// category becomes available for new tickets (design S5 availability = a
// published version exists). Tests that merely arrange a working create use
// this; tests that exercise workflow-create semantics use explicit fixtures.
// It returns the published immutable version id.
//
// Every desk the definition routes to is staffed FIRST, so a fixture cannot
// publish a workflow its own tickets could never move through.
func (h *harness) publishWorkflow(t *testing.T, catID int64, def domain.WorkflowDefinition) int64 {
	t.Helper()
	for _, st := range def {
		if st.Type == domain.StepAssignToDesk && st.AssignToDesk != nil {
			h.staff(t, st.AssignToDesk.DeskID)
		}
	}
	b, err := def.MarshalCanonical()
	if err != nil {
		t.Fatalf("canonical workflow: %v", err)
	}
	vid, iss, err := h.store.WorkflowStore().Publish(t.Context(), catID, b, nil)
	if err != nil {
		t.Fatalf("publish workflow: %v", err)
	}
	if len(iss) > 0 {
		t.Fatalf("publish workflow rejected: %v", iss)
	}
	return vid
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
