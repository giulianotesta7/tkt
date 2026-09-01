package httpadapter

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func mustContain(t *testing.T, body string, values ...string) {
	t.Helper()
	for _, value := range values {
		if !strings.Contains(body, value) {
			t.Errorf("response missing %q: %s", value, body)
		}
	}
}

func TestUsersListNormalAndHTMXContracts(t *testing.T) {
	h := newHarness(t)
	active := h.createUser(t, "Ana Torres", "ana@example.com", "secret")
	inactive := h.createUser(t, "Beto Ruiz", "beto@example.com", "secret")
	inactive.Active = false
	if err := h.store.UserStore().Update(context.Background(), inactive); err != nil {
		t.Fatal(err)
	}
	normal := h.get(t, "/users?status=invalid", false)
	if normal.Code != http.StatusOK || normal.Header().Get("Vary") != "HX-Request" {
		t.Fatalf("normal status/vary = %d/%q", normal.Code, normal.Header().Get("Vary"))
	}
	body := normal.Body.String()
	mustContain(t, body, `id="users-root"`, `All <span>3</span>`, `Active <span>2</span>`, `Deactivated <span>1</span>`, `href="/users/new"`, active.Email, inactive.Email, "Created", "3 accounts", `type="search"`, `aria-label="Search users"`, `placeholder="Search by name or email..."`, "Access levels", "Permissions by role", "Full instance access.", "Manages users, desks, categories and workflows.", "Handles assigned tickets and moves requests toward resolution.", "Creates requests and tracks their progress.", "users.css", "users.js")
	if strings.Count(body, `id="users-root"`) != 1 || strings.Contains(body, `>Edit<`) || strings.Contains(body, `>Delete<`) || strings.Contains(body, `name="password"`) {
		t.Fatalf("invalid Users markup: %s", body)
	}
	hx := h.get(t, "/users?status=active", true)
	if hx.Code != http.StatusOK || hx.Header().Get("Vary") != "HX-Request" || strings.Contains(hx.Body.String(), "<html") {
		t.Fatalf("HX list contract = %d/%s", hx.Code, hx.Body.String())
	}
	mustContain(t, hx.Body.String(), `id="users-root"`, active.Email)
	if strings.Contains(hx.Body.String(), inactive.Email) || strings.Contains(hx.Body.String(), "users.css") {
		t.Fatalf("HX list must contain only active screen fragment: %s", hx.Body.String())
	}
}

func TestUsersEmptyAndIneligibleRows(t *testing.T) {
	h := newHarness(t)
	h.createUser(t, "Ana", "ana@example.com", "secret")
	peer := seedUserRole(t, h.store, "Peer Admin", "peer-admin@tkt.test", domain.RoleAdmin)
	empty := h.get(t, "/users?status=deactivated", true).Body.String()
	mustContain(t, empty, "No deactivated users.", "All <span>3</span>", "Deactivated <span>0</span>")
	body := h.get(t, "/users", true).Body.String()
	mustContain(t, body, peer.Email)
	if strings.Contains(body, `/users/`+strconv.FormatInt(peer.ID, 10)+`/edit`) {
		t.Fatalf("Admin peer must have no launcher: %s", body)
	}
}

func TestUsersDrawerGETUpdateAndFailureContracts(t *testing.T) {
	h := newHarness(t)
	target := h.createUser(t, "Ana Torres", "ana@example.com", "secret")
	other := h.createUser(t, "Bea Smith", "bea@example.com", "secret")
	path := "/users/" + strconv.FormatInt(target.ID, 10) + "/edit"
	normal := h.get(t, path+"?status=active", false)
	if normal.Code != http.StatusOK || normal.Header().Get("Vary") != "HX-Request" {
		t.Fatalf("normal edit status/vary = %d/%q", normal.Code, normal.Header().Get("Vary"))
	}
	mustContain(t, normal.Body.String(), `id="users-root"`, `id="users-drawer-host"`, `role="dialog"`, `hx-post="`, `data-server-error="false"`, `name="name"`, `name="email"`, `name="role"`, `name="active"`, "Includes User access")
	if strings.Contains(normal.Body.String(), `name="password"`) || strings.Contains(normal.Body.String(), `>Delete<`) {
		t.Fatalf("drawer exposes forbidden data: %s", normal.Body.String())
	}
	hx := h.get(t, path, true)
	if hx.Code != http.StatusOK || strings.Contains(hx.Body.String(), `id="users-root"`) {
		t.Fatalf("HX drawer must be fragment only: %d/%s", hx.Code, hx.Body.String())
	}
	form := url.Values{"name": {"Ana Updated"}, "email": {"ana@example.com"}, "role": {"agent"}, "active": {"true"}, "status": {"active"}}
	saved := h.postForm(t, path, form, true)
	if saved.Code != http.StatusOK || strings.Count(saved.Body.String(), `id="users-root"`) != 1 {
		t.Fatalf("HX save body/status = %d/%s", saved.Code, saved.Body.String())
	}
	for key, want := range map[string]string{"HX-Retarget": "#users-root", "HX-Reswap": "outerHTML", "HX-Trigger-After-Swap": "users:saved"} {
		if saved.Header().Get(key) != want {
			t.Errorf("%s = %q, want %q", key, saved.Header().Get(key), want)
		}
	}
	invalid := url.Values{"name": {""}, "email": {"ana@example.com"}, "role": {"agent"}, "active": {"true"}, "status": {"active"}}
	failure := h.postForm(t, path, invalid, true)
	if failure.Code != http.StatusUnprocessableEntity || failure.Header().Get("HX-Retarget") != "#users-drawer-host" || !strings.Contains(failure.Body.String(), `id="user-name" name="name" type="text" value="" required aria-invalid="true"`) {
		t.Fatalf("HX validation contract = %d/%s", failure.Code, failure.Body.String())
	}
	duplicate := form
	duplicate.Set("email", other.Email)
	conflict := h.postForm(t, path, duplicate, true)
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), `id="user-email" name="email" type="email" value="bea@example.com" required aria-invalid="true"`) {
		t.Fatalf("HX duplicate field contract = %d/%s", conflict.Code, conflict.Body.String())
	}
}

// staleRoleUserStore simulates a concurrent role change after the service
// reads the target but before its guarded update. The real store then returns
// its established not-found error for the stale expected role.
type staleRoleUserStore struct {
	application.UserStore
	targetID int64
	newRole  domain.Role
}

func (s staleRoleUserStore) UpdateManagedUser(ctx context.Context, u *domain.User, expectedRole domain.Role, actorID int64, at time.Time) error {
	if u.ID == s.targetID {
		current, err := s.UserStore.GetByID(ctx, s.targetID)
		if err != nil {
			return err
		}
		current.Role = s.newRole
		if err := s.UserStore.Update(ctx, current); err != nil {
			return err
		}
	}
	return s.UserStore.UpdateManagedUser(ctx, u, expectedRole, actorID, at)
}

func postUsersFormWithService(t *testing.T, h *harness, users *application.UserService, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	NewUserHandlers(users, h.renderer).Register(mux)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	h.mw.Wrap(mux).ServeHTTP(rec, req)
	return rec
}

func TestUsersHTMX403OperationFailureDrawerContract(t *testing.T) {
	h := newHarness(t)
	target := seedUserRole(t, h.store, "Peer Admin", "peer-admin@tkt.test", domain.RoleAdmin)
	other := h.createUser(t, "Unrelated", "unrelated@tkt.test", "secret")
	path := "/users/" + strconv.FormatInt(target.ID, 10) + "/edit"
	form := url.Values{
		"name":   {"Attempted Name"},
		"email":  {"attempted@example.com"},
		"role":   {"user"},
		"active": {"true"},
		"status": {"active"},
	}

	rec := h.postForm(t, path, form, true)
	body := rec.Body.String()
	if rec.Code != http.StatusForbidden {
		t.Fatalf("HTMX forbidden status = %d, want 403: %s", rec.Code, body)
	}
	if rec.Header().Get("HX-Retarget") != "#users-drawer-host" || rec.Header().Get("HX-Reswap") != "outerHTML" {
		t.Fatalf("HTMX forbidden swap headers = %q/%q", rec.Header().Get("HX-Retarget"), rec.Header().Get("HX-Reswap"))
	}
	mustContain(t, body, `id="users-drawer-host"`, "admin accounts require root", "Attempted Name", "attempted@example.com")
	if strings.Contains(body, "<html") || strings.Contains(body, other.Email) || strings.Contains(body, `name="password"`) {
		t.Fatalf("forbidden drawer leaked shell or unrelated restricted data: %s", body)
	}
}

func TestUsersHTMX404StaleUpdateDrawerContract(t *testing.T) {
	h := newHarness(t)
	target := seedUserRole(t, h.store, "Concurrent Target", "concurrent@tkt.test", domain.RoleAgent)
	users := application.NewUserService(staleRoleUserStore{
		UserStore: h.store.UserStore(),
		targetID:  target.ID,
		newRole:   domain.RoleUser,
	}, h.clock)
	path := "/users/" + strconv.FormatInt(target.ID, 10) + "/edit"
	form := url.Values{
		"name":   {"Stale Submitted Name"},
		"email":  {"stale@example.com"},
		"role":   {"agent"},
		"active": {"true"},
		"status": {"active"},
	}

	rec := postUsersFormWithService(t, h, users, path, form)
	body := rec.Body.String()
	if rec.Code != http.StatusNotFound {
		t.Fatalf("HTMX stale update status = %d, want 404: %s", rec.Code, body)
	}
	if rec.Header().Get("HX-Retarget") != "#users-drawer-host" || rec.Header().Get("HX-Reswap") != "outerHTML" {
		t.Fatalf("HTMX stale update swap headers = %q/%q", rec.Header().Get("HX-Retarget"), rec.Header().Get("HX-Reswap"))
	}
	mustContain(t, body, `id="users-drawer-host"`, "user not found", "Stale Submitted Name", "stale@example.com")
	if strings.Contains(body, "<html") || strings.Contains(body, `name="password"`) {
		t.Fatalf("stale update returned non-drawer or restricted data: %s", body)
	}
	persisted, err := h.store.UserStore().GetByID(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("read concurrently changed user: %v", err)
	}
	if persisted.Name != target.Name || persisted.Email != target.Email || persisted.Role != domain.RoleUser {
		t.Fatalf("stale update overwrote newer persisted state: before=%+v after=%+v", target, persisted)
	}
}

func TestUsersDrawerLifecycleSubmitter(t *testing.T) {
	h := newHarness(t)
	target := h.createUser(t, "Ana", "ana@example.com", "secret")
	path := "/users/" + strconv.FormatInt(target.ID, 10) + "/edit"
	form := url.Values{"name": {"Ana"}, "email": {"ana@example.com"}, "role": {"agent"}, "active": {"true", "false"}}
	rec := h.postForm(t, path, form, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/users" {
		t.Fatalf("normal lifecycle response = %d/%q", rec.Code, rec.Header().Get("Location"))
	}
	stored, err := h.store.UserStore().GetByID(context.Background(), target.ID)
	if err != nil || stored.Active {
		t.Fatalf("last active value must deactivate target: %+v/%v", stored, err)
	}
}

// TestUsersDrawerDowngradeNoGeneric500 (issue #47): downgrading a desk-member
// agent via /users/{id}/edit must succeed (200 HX save contract) instead of
// the legacy generic 500 the trigger used to cause, must remove the desk
// membership, and must hand the open assigned ticket to the other eligible
// member. Closed tickets keep their assignment.
func TestUsersDrawerDowngradeNoGeneric500(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	desk := &domain.Desk{Name: "Support", CreatedAt: time.Now()}
	if err := h.store.DeskStore().Create(ctx, desk); err != nil {
		t.Fatal(err)
	}
	target := h.createUser(t, "Nico Agent", "nico@example.com", "secret")
	peer := h.createUser(t, "Bea Peer", "bea@example.com", "secret")
	if err := h.store.DeskStore().AddMember(ctx, desk.ID, target.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := h.store.DeskStore().AddMember(ctx, desk.ID, peer.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	var catID int64
	if err := h.rawDB(t).QueryRowContext(ctx, `SELECT id FROM categories LIMIT 1`).Scan(&catID); err != nil {
		t.Fatal(err)
	}
	res, err := h.rawDB(t).ExecContext(ctx, `INSERT INTO tickets (number, title, description, requester_name, requester_email, requester_user_id, category_id, priority, state, user_id, created_at, updated_at)
		VALUES (1, 'Orphan risk', '', 'Req', 'r@x', NULL, ?, 'medium', 'new', ?, '2026-08-06T10:00:00Z', '2026-08-06T10:00:00Z')`, catID, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	ticketID, _ := res.LastInsertId()
	// Assignment-context audit row so the handoff resolves the desk (priority
	// 1 desk resolution); actor_user_id must reference a real user (FK).
	if _, err := h.rawDB(t).ExecContext(ctx, `INSERT INTO audit_events (ticket_id, actor, action, field, from_value, to_value, actor_user_id, created_at, desk_id)
		VALUES (?, 'Admin', 'update', 'user', '', ?, ?, '2026-08-06T10:00:00Z', ?)`, ticketID, strconv.FormatInt(target.ID, 10), h.admin.ID, desk.ID); err != nil {
		t.Fatal(err)
	}

	path := "/users/" + strconv.FormatInt(target.ID, 10) + "/edit"
	form := url.Values{"name": {"Nico Agent"}, "email": {"nico@example.com"}, "role": {"user"}, "active": {"true"}, "status": {"active"}}
	saved := h.postForm(t, path, form, true)
	if saved.Code != http.StatusOK || strings.Count(saved.Body.String(), `id="users-root"`) != 1 {
		t.Fatalf("downgrade save status/body = %d (want 200 HX save, not 500): %s", saved.Code, saved.Body.String())
	}
	for key, want := range map[string]string{"HX-Retarget": "#users-root", "HX-Reswap": "outerHTML", "HX-Trigger-After-Swap": "users:saved"} {
		if saved.Header().Get(key) != want {
			t.Errorf("%s = %q, want %q", key, saved.Header().Get(key), want)
		}
	}
	db := h.rawDB(t)
	var role string
	if err := db.QueryRowContext(ctx, `SELECT role FROM users WHERE id = ?`, target.ID).Scan(&role); err != nil || role != string(domain.RoleUser) {
		t.Errorf("role = %q (err %v), want user", role, err)
	}
	var memberships int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM desk_members WHERE user_id = ?`, target.ID).Scan(&memberships); err != nil || memberships != 0 {
		t.Errorf("memberships = %d (err %v), want 0", memberships, err)
	}
	var assignee sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT user_id FROM tickets WHERE id = ?`, ticketID).Scan(&assignee); err != nil {
		t.Fatal(err)
	}
	if !assignee.Valid || assignee.Int64 != peer.ID {
		t.Errorf("ticket assignee = %v, want peer agent (%d)", assignee, peer.ID)
	}
}
