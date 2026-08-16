package httpadapter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// userForm builds a valid create-user form; mod may override fields.
func userForm(mod func(url.Values)) url.Values {
	f := url.Values{"name": {"Ana Torres"}, "email": {"ana@example.com"}, "password": {"secret"}}
	if mod != nil {
		mod(f)
	}
	return f
}

// TestUsersIndexRenders proves GET /users lists the managed users with
// their status.
func TestUsersIndexRenders(t *testing.T) {
	h := newHarness(t)
	h.createUser(t, "Ana Torres", "ana@example.com", "secret")
	beto := h.createUser(t, "Beto", "beto@example.com", "secret")
	beto.Active = false
	if err := h.store.UserStore().Update(context.Background(), beto); err != nil {
		t.Fatalf("deactivate beto: %v", err)
	}

	rec := h.get(t, "/users", false)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"ana@example.com", "beto@example.com", "active", "inactive"} {
		if !strings.Contains(body, want) {
			t.Errorf("users index must contain %q, got: %s", want, body)
		}
	}
}

// TestUserCreateSuccess proves POST /users stores an ACTIVE user with only
// the bcrypt hash (the new password logs in, the plaintext does not appear).
func TestUserCreateSuccess(t *testing.T) {
	h := newHarness(t)

	rec := h.postForm(t, "/users", userForm(nil), false)

	wantRedirect(t, rec, http.StatusSeeOther, "/users")

	u, err := h.store.UserStore().GetByEmail(context.Background(), "ana@example.com")
	if err != nil {
		t.Fatalf("created user must exist: %v", err)
	}
	if !u.Active {
		t.Error("new users must be active by default")
	}
	if strings.Contains(u.PasswordHash, "secret") {
		t.Error("password must not be stored in plaintext")
	}
	// The real hash authenticates (login spec).
	if cookie := h.loginCookie(t, "ana@example.com", "secret"); cookie == "" {
		t.Error("new user must be able to log in")
	}
}

func TestUserCreateDeniedForNonAdmin(t *testing.T) {
	h := newHarness(t)
	user, err := h.users.Create(t.Context(), *h.admin, application.CreateUserInput{Name: "User", Email: "user@tkt.test", Password: "secret"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	session := h.loginCookie(t, user.Email, "secret")
	if session == "" {
		t.Fatal("user login must succeed")
	}
	rec := h.postFormAs(t, "/users", userForm(nil), session)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("user POST /users = %d, want 403", rec.Code)
	}
}

// TestUserCreateDuplicateEmail409 proves duplicate emails are rejected with
// a 409 and the message is re-rendered (create-user spec).
func TestUserCreateDuplicateEmail409(t *testing.T) {
	h := newHarness(t)
	h.createUser(t, "Ana", "ana@example.com", "secret")

	rec := h.postForm(t, "/users", userForm(nil), false)

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "already exists") {
		t.Errorf("re-render must show the duplicate message, got: %s", rec.Body.String())
	}
}

// TestUserCreateValidation422 proves an empty name is a 422 (create-user
// spec).
func TestUserCreateValidation422(t *testing.T) {
	h := newHarness(t)

	rec := h.postForm(t, "/users", userForm(func(f url.Values) { f.Set("name", "  ") }), false)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), domain.ErrMsgUserNameRequired) {
		t.Errorf("re-render must show %q, got: %s", domain.ErrMsgUserNameRequired, rec.Body.String())
	}
}

// TestUserEditFormPrefilled proves GET /users/{id}/edit pre-fills the form.
func TestUserEditFormPrefilled(t *testing.T) {
	h := newHarness(t)
	ana := h.createUser(t, "Ana Torres", "ana@example.com", "secret")

	rec := h.get(t, "/users/"+strconv.FormatInt(ana.ID, 10)+"/edit", false)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Ana Torres") || !strings.Contains(body, "ana@example.com") {
		t.Errorf("edit form must be prefilled, got: %s", body)
	}
}

// TestUserUpdateSuccess proves name/email/password edits persist and the new
// password replaces the old hash (update-user spec).
func TestUserUpdateSuccess(t *testing.T) {
	h := newHarness(t)
	ana := h.createUser(t, "Ana", "ana@example.com", "secret")

	form := url.Values{
		"name":     {"Ana Torres"},
		"email":    {"ana.torres@example.com"},
		"password": {"new-secret"},
		"active":   {"on"},
	}
	rec := h.postForm(t, "/users/"+strconv.FormatInt(ana.ID, 10)+"/edit", form, false)

	wantRedirect(t, rec, http.StatusSeeOther, "/users")

	u, err := h.store.UserStore().GetByEmail(context.Background(), "ana.torres@example.com")
	if err != nil {
		t.Fatalf("updated email must resolve: %v", err)
	}
	if u.Name != "Ana Torres" {
		t.Errorf("name = %q, want Ana Torres", u.Name)
	}
	if cookie := h.loginCookie(t, "ana.torres@example.com", "new-secret"); cookie == "" {
		t.Error("the new password must authenticate")
	}
	if cookie := h.loginCookie(t, "ana.torres@example.com", "secret"); cookie != "" {
		t.Error("the old password must no longer authenticate")
	}
}

// TestUserUpdateDuplicateEmail409 proves renaming to a taken email is
// rejected 409 (update-user spec).
func TestUserUpdateDuplicateEmail409(t *testing.T) {
	h := newHarness(t)
	h.createUser(t, "Ana", "ana@example.com", "secret")
	beto := h.createUser(t, "Beto", "beto@example.com", "secret")

	// Rename beto to ana's email: uniqueness applies to the new email.
	form := url.Values{"name": {"Beto"}, "email": {"ana@example.com"}, "active": {"on"}}
	rec := h.postForm(t, "/users/"+strconv.FormatInt(beto.ID, 10)+"/edit", form, false)

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "already exists") {
		t.Errorf("re-render must show the duplicate message, got: %s", rec.Body.String())
	}
}

// TestUserDeactivateKillsSessions proves D14 end-to-end through the edit
// form: deactivating a user destroys their active sessions — the next
// request with the old token is redirected to login and the row is gone.
func TestUserDeactivateKillsSessions(t *testing.T) {
	h := newHarness(t)
	beto := h.createUser(t, "Beto", "beto@example.com", "secret")
	betoSession := h.loginCookie(t, "beto@example.com", "secret")
	if betoSession == "" {
		t.Fatal("beto must log in first")
	}

	form := url.Values{"name": {"Beto"}, "email": {"beto@example.com"}} // no active checkbox → inactive
	rec := h.postForm(t, "/users/"+strconv.FormatInt(beto.ID, 10)+"/edit", form, false)
	wantRedirect(t, rec, http.StatusSeeOther, "/users")

	u, err := h.store.UserStore().GetByID(context.Background(), beto.ID)
	if err != nil || u.Active {
		t.Errorf("user must be deactivated (active=%v err=%v)", u.Active, err)
	}

	// Beto's next request: logged out, session destroyed.
	req := httptest.NewRequest(http.MethodGet, "/tickets", nil)
	req.Header.Set("Cookie", sessionCookie+"="+betoSession)
	recProbe := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(recProbe, req)
	if recProbe.Code != http.StatusSeeOther {
		t.Errorf("deactivated session request status = %d, want 303", recProbe.Code)
	}
	if _, err := h.store.SessionStore().GetByID(context.Background(), betoSession); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("deactivated user's session must be destroyed, err = %v (want ErrNotFound)", err)
	}
}

// TestUserDeleteUnreferenced proves an unreferenced user is deletable
// (user-deletion spec).
func TestUserDeleteUnreferenced(t *testing.T) {
	h := newHarness(t)
	ana := h.createUser(t, "Ana", "ana@example.com", "secret")

	rec := h.postForm(t, "/users/"+strconv.FormatInt(ana.ID, 10)+"/delete", url.Values{}, false)

	wantRedirect(t, rec, http.StatusSeeOther, "/users")
	if _, err := h.store.UserStore().GetByEmail(context.Background(), "ana@example.com"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("deleted user must be gone, err = %v (want ErrNotFound)", err)
	}
}

// TestUserDeleteWithLiveSession proves deleting a user who is logged in
// sweeps their sessions and succeeds (only ticket references block).
func TestUserDeleteWithLiveSession(t *testing.T) {
	h := newHarness(t)
	ana := h.createUser(t, "Ana", "ana@example.com", "secret")
	_ = h.loginCookie(t, "ana@example.com", "secret")

	rec := h.postForm(t, "/users/"+strconv.FormatInt(ana.ID, 10)+"/delete", url.Values{}, false)

	wantRedirect(t, rec, http.StatusSeeOther, "/users")
}

// TestUserDeleteReferenced409 proves deleting a user assigned to tickets is
// rejected 409 with the message (referenced-user spec).
func TestUserDeleteReferenced409(t *testing.T) {
	h := newHarness(t)
	beto := h.createUser(t, "Beto", "beto@example.com", "secret")
	h.seedTicket(t, "Assigned ticket", func(in *application.CreateTicketInput) { in.UserID = &beto.ID })

	rec := h.postForm(t, "/users/"+strconv.FormatInt(beto.ID, 10)+"/delete", url.Values{}, false)

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "referenced and cannot be deleted") {
		t.Errorf("re-render must show the referenced message, got: %s", rec.Body.String())
	}
	if _, err := h.store.UserStore().GetByID(context.Background(), beto.ID); err != nil {
		t.Errorf("referenced user must survive, err = %v", err)
	}
}

// TestCategoriesIndexRenders proves GET /categories lists the categories.
func TestCategoriesIndexRenders(t *testing.T) {
	h := newHarness(t)

	rec := h.get(t, "/categories", false)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Bugs") {
		t.Errorf("categories index must list the seeded category, got: %s", rec.Body.String())
	}
}

// TestCategoryCreateSuccess proves POST /categories stores a unique name and
// redirects 303.
func TestCategoryCreateSuccess(t *testing.T) {
	h := newHarness(t)

	rec := h.postForm(t, "/categories", url.Values{"name": {"Support"}}, false)

	wantRedirect(t, rec, http.StatusSeeOther, "/categories")
	_, err := h.categories.GetByID(t.Context(), 2) // 1 = Bugs seeded by the harness
	if err != nil {
		t.Errorf("created category must be readable: %v", err)
	}
}

// TestCategoryCreateDuplicate409 proves duplicate category names are
// rejected 409 (category spec).
func TestCategoryCreateDuplicate409(t *testing.T) {
	h := newHarness(t)

	rec := h.postForm(t, "/categories", url.Values{"name": {"Bugs"}}, false)

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "already exists") {
		t.Errorf("re-render must show the duplicate message, got: %s", rec.Body.String())
	}
}

// TestCategoryCreateValidation422 proves an empty name is a 422.
func TestCategoryCreateValidation422(t *testing.T) {
	h := newHarness(t)

	rec := h.postForm(t, "/categories", url.Values{"name": {"   "}}, false)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), domain.ErrMsgCategoryNameRequired) {
		t.Errorf("re-render must show %q, got: %s", domain.ErrMsgCategoryNameRequired, rec.Body.String())
	}
}

// TestCategoryRenameSuccess proves renaming stores the new name and frees
// the old one (rename spec).
func TestCategoryRenameSuccess(t *testing.T) {
	h := newHarness(t)

	rec := h.postForm(t, "/categories/1/edit", url.Values{"name": {"Defects"}}, false)

	wantRedirect(t, rec, http.StatusSeeOther, "/categories")
	c, err := h.categories.GetByID(t.Context(), 1)
	if err != nil || c.Name != "Defects" {
		t.Errorf("category = %+v err=%v, want name Defects", c, err)
	}
}

// TestCategoryRenameDuplicate409 proves renaming to a taken name is rejected
// 409 (rename spec).
func TestCategoryRenameDuplicate409(t *testing.T) {
	h := newHarness(t)
	h.postForm(t, "/categories", url.Values{"name": {"Support"}}, false)

	rec := h.postForm(t, "/categories/1/edit", url.Values{"name": {"Support"}}, false)

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "already exists") {
		t.Errorf("re-render must show the duplicate message, got: %s", rec.Body.String())
	}
}

// TestCategoryDeleteUnreferenced proves an unreferenced category is
// deletable.
func TestCategoryDeleteUnreferenced(t *testing.T) {
	h := newHarness(t)
	h.postForm(t, "/categories", url.Values{"name": {"Support"}}, false)

	rec := h.postForm(t, "/categories/2/delete", url.Values{}, false)

	wantRedirect(t, rec, http.StatusSeeOther, "/categories")
	if _, err := h.categories.GetByID(t.Context(), 2); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("deleted category must be gone, err = %v (want ErrNotFound)", err)
	}
}

// TestCategoryDeleteReferenced409 proves deleting a category used by a
// ticket is rejected 409 (delete-category spec).
func TestCategoryDeleteReferenced409(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Uses Bugs", nil)

	rec := h.postForm(t, "/categories/1/delete", url.Values{}, false)

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "referenced and cannot be deleted") {
		t.Errorf("re-render must show the referenced message, got: %s", rec.Body.String())
	}
}

// TestStaticServesVendoredHtmx proves GET /static/htmx.min.js returns the
// embedded script with a cache header (task 5.4 static asset route).
func TestStaticServesVendoredHtmx(t *testing.T) {
	h := newHarness(t)
	rec := h.get(t, "/static/htmx.min.js", false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=86400" {
		t.Errorf("Cache-Control = %q, want public, max-age=86400", cc)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "htmx") || len(body) < 10000 {
		t.Errorf("body must be the vendored htmx script, got %d bytes", len(body))
	}
}

// TestRootAccountRejectedAtHTTP proves the root invariants end to end (task
// 2.4; role-authorization "Nobody touches root"): an authenticated operator
// editing or deleting the root account is refused with 403 and the root row
// stays untouched. The root is bootstrapped through the real store port, so
// the whole handler → use-case → guard chain runs.
func TestRootAccountRejectedAtHTTP(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Seed a root through the real store port with an explicit role (the
	// harness admin is a regular agent-role user, so it cannot bootstrap a
	// root itself — and BootstrapRoot correctly refuses once users exist).
	// The store port persists the role; the DB allows exactly one root row.
	root := &domain.User{Name: "Root", Email: "root@example.com", PasswordHash: "hash",
		Role: domain.RoleRoot, Active: true, CreatedAt: fixedNow}
	if err := h.store.UserStore().Create(ctx, root); err != nil {
		t.Fatalf("seed root: %v", err)
	}

	// Edit (including deactivate) is refused with 403.
	editForm := url.Values{
		"name":   {"Hacker"},
		"email":  {"hack@example.com"},
		"active": {"false"},
	}
	rec := h.postForm(t, "/users/"+strconv.FormatInt(root.ID, 10)+"/edit", editForm, false)
	if rec.Code != http.StatusForbidden {
		t.Errorf("edit root status = %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), domain.ErrMsgRootProtected) {
		t.Errorf("edit root body must show %q, got: %s", domain.ErrMsgRootProtected, rec.Body.String())
	}

	// Delete is refused with 403.
	rec = h.postForm(t, "/users/"+strconv.FormatInt(root.ID, 10)+"/delete", url.Values{}, false)
	if rec.Code != http.StatusForbidden {
		t.Errorf("delete root status = %d, want 403", rec.Code)
	}

	// The root row is untouched: active, role root, original identity.
	got, err := h.store.UserStore().GetByID(ctx, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Root" || got.Email != "root@example.com" {
		t.Errorf("root mutated = %+v, want unchanged", got)
	}
	if !got.Active {
		t.Error("root must remain active")
	}
	if got.Role != domain.RoleRoot {
		t.Errorf("root role = %q, want %q", got.Role, domain.RoleRoot)
	}
}

// S7.4 RED: hidden management UI never substitutes for server authorization.
// A user direct-requesting management pages and forging a category POST gets
// denied before any restricted data or mutation is reached.
func TestManagementRoutesDenyDirectUserRequests(t *testing.T) {
	h := newHarness(t)
	user := seedUserRole(t, h.store, "User", "user@tkt.test", domain.RoleUser)
	session := seedSession(t, h.store, user.ID)

	for _, target := range []string{"/users", "/categories"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Header.Set("Cookie", sessionCookie+"="+session.ID)
		rec := httptest.NewRecorder()
		h.mw.Wrap(h.mux).ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("GET %s status = %d, want 403", target, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "admin@tkt.test") || strings.Contains(rec.Body.String(), "Bugs") {
			t.Errorf("GET %s leaked management data: %s", target, rec.Body.String())
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/categories", strings.NewReader(url.Values{"name": {"Forged"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", sessionCookie+"="+session.ID)
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /categories status = %d, want 403", rec.Code)
	}
	if _, err := h.categories.GetByID(context.Background(), 2); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("forged category POST created a category: %v", err)
	}
}

// S7.4 RED: user presentation exposes only own-ticket surfaces. Hidden
// assignment and management controls complement (but never replace) gates.
func TestUserTicketViewsHideManagementAndAssignmentControls(t *testing.T) {
	h := newHarness(t)
	user := seedUserRole(t, h.store, "User", "user-views@tkt.test", domain.RoleUser)
	session := seedSession(t, h.store, user.ID)

	req := httptest.NewRequest(http.MethodGet, "/tickets/new", nil)
	req.Header.Set("Cookie", sessionCookie+"="+session.ID)
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /tickets/new status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, forbidden := range []string{"name=\"user_id\"", "href=\"/users\"", "href=\"/categories\"", "href=\"/groups\""} {
		if strings.Contains(body, forbidden) {
			t.Errorf("user ticket form exposed %q", forbidden)
		}
	}
}

func TestUserRoleEndpointAppliesManagementMatrix(t *testing.T) {
	h := newHarness(t)
	member := seedUserRole(t, h.store, "Member", "member-role@tkt.test", domain.RoleUser)

	rec := h.postForm(t, "/users/"+strconv.FormatInt(member.ID, 10)+"/role", url.Values{"role": {"agent"}}, false)
	wantRedirect(t, rec, http.StatusSeeOther, "/users")
	stored, err := h.store.UserStore().GetByID(context.Background(), member.ID)
	if err != nil || stored.Role != domain.RoleAgent {
		t.Fatalf("admin user->agent result = %+v err=%v", stored, err)
	}

	rec = h.postForm(t, "/users/"+strconv.FormatInt(member.ID, 10)+"/role", url.Values{"role": {"admin"}}, false)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("admin agent->admin status = %d, want 403", rec.Code)
	}

	root := seedUserRole(t, h.store, "Root", "root-role@tkt.test", domain.RoleRoot)
	rootSession := seedSession(t, h.store, root.ID)
	rec = h.postFormAs(t, "/users/"+strconv.FormatInt(member.ID, 10)+"/role", url.Values{"role": {"admin"}}, rootSession.ID)
	wantRedirect(t, rec, http.StatusSeeOther, "/users")
	stored, err = h.store.UserStore().GetByID(context.Background(), member.ID)
	if err != nil || stored.Role != domain.RoleAdmin {
		t.Fatalf("root agent->admin result = %+v err=%v", stored, err)
	}
}

// TestManagementRouteRoleMatrix proves management routes enforce the same
// role boundary for full-page and HTMX requests. Presentation-specific
// responses must not turn a denied request into a data-bearing fragment.
func TestManagementRouteRoleMatrix(t *testing.T) {
	h := newHarness(t)

	type route struct {
		path string
	}
	routes := []route{
		{path: "/users"},
		{path: "/categories"},
		{path: "/groups"},
	}
	roles := []struct {
		role       domain.Role
		wantStatus int
	}{
		{role: domain.RoleUser, wantStatus: http.StatusForbidden},
		{role: domain.RoleAgent, wantStatus: http.StatusForbidden},
		{role: domain.RoleAdmin, wantStatus: http.StatusOK},
		{role: domain.RoleRoot, wantStatus: http.StatusOK},
	}

	for _, tt := range roles {
		t.Run(string(tt.role), func(t *testing.T) {
			actor := seedUserRole(t, h.store, string(tt.role), string(tt.role)+"-matrix@tkt.test", tt.role)
			session := seedSession(t, h.store, actor.ID)

			for _, r := range routes {
				for _, hx := range []bool{false, true} {
					name := r.path
					if hx {
						name += "/htmx"
					}
					t.Run(name, func(t *testing.T) {
						req := httptest.NewRequest(http.MethodGet, r.path, nil)
						req.Header.Set("Cookie", sessionCookie+"="+session.ID)
						if hx {
							req.Header.Set("HX-Request", "true")
						}
						rec := httptest.NewRecorder()
						h.mw.Wrap(h.mux).ServeHTTP(rec, req)

						if rec.Code != tt.wantStatus {
							t.Errorf("GET %s (HX=%t) as %s status = %d, want %d", r.path, hx, tt.role, rec.Code, tt.wantStatus)
						}
						if tt.wantStatus == http.StatusForbidden && strings.Contains(rec.Body.String(), "admin@tkt.test") {
							t.Errorf("GET %s (HX=%t) as %s leaked management data: %s", r.path, hx, tt.role, rec.Body.String())
						}
					})
				}
			}
		})
	}
}

// TestForbiddenManagementPostsRejectFullAndHTMX proves hidden management
// controls remain unavailable when a user or agent forges either submission
// style. No category may be created by either denied request.
func TestForbiddenManagementPostsRejectFullAndHTMX(t *testing.T) {
	for _, role := range []domain.Role{domain.RoleUser, domain.RoleAgent} {
		t.Run(string(role), func(t *testing.T) {
			h := newHarness(t)
			actor := seedUserRole(t, h.store, string(role), string(role)+"-post@tkt.test", role)
			session := seedSession(t, h.store, actor.ID)

			for _, hx := range []bool{false, true} {
				name := "direct"
				if hx {
					name = "htmx"
				}
				t.Run(name, func(t *testing.T) {
					req := httptest.NewRequest(http.MethodPost, "/categories", strings.NewReader(url.Values{"name": {"Forged " + string(role)}}.Encode()))
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					req.Header.Set("Cookie", sessionCookie+"="+session.ID)
					if hx {
						req.Header.Set("HX-Request", "true")
					}
					rec := httptest.NewRecorder()
					h.mw.Wrap(h.mux).ServeHTTP(rec, req)

					if rec.Code != http.StatusForbidden {
						t.Errorf("POST /categories (HX=%t) status = %d, want 403", hx, rec.Code)
					}
					if _, err := h.categories.GetByID(context.Background(), 2); !errors.Is(err, domain.ErrNotFound) {
						t.Errorf("forged category POST created a category: %v", err)
					}
				})
			}
		})
	}
}

type noQueryUserStore struct {
	application.UserStore
	listCalls int
}

func (s *noQueryUserStore) List(context.Context) ([]domain.User, error) {
	s.listCalls++
	return nil, errors.New("user list must not run for a denied request")
}

type noQueryCategoryStore struct {
	application.CategoryStore
	listCalls int
}

func (s *noQueryCategoryStore) List(context.Context) ([]domain.Category, error) {
	s.listCalls++
	return nil, errors.New("category list must not run for a denied request")
}

// TestDeniedManagementHandlersQueryNothing isolates the early authorization
// boundary: denied management requests must stop before their list stores.
func TestDeniedManagementHandlersQueryNothing(t *testing.T) {
	actor := &domain.User{Role: domain.RoleUser}
	renderer := NewRenderer()

	t.Run("users", func(t *testing.T) {
		store := &noQueryUserStore{}
		handler := NewUserHandlers(application.NewUserService(store, testClock{now: fixedNow}), renderer)
		req := httptest.NewRequest(http.MethodGet, "/users", nil).WithContext(context.WithValue(context.Background(), ctxKeyUser{}, actor))
		rec := httptest.NewRecorder()

		handler.index(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", rec.Code)
		}
		if store.listCalls != 0 {
			t.Errorf("user store list calls = %d, want 0", store.listCalls)
		}
	})

	t.Run("categories", func(t *testing.T) {
		store := &noQueryCategoryStore{}
		handler := NewCategoryHandlers(application.NewCategoryService(store, testClock{now: fixedNow}), renderer)
		req := httptest.NewRequest(http.MethodGet, "/categories", nil).WithContext(context.WithValue(context.Background(), ctxKeyUser{}, actor))
		rec := httptest.NewRecorder()

		handler.index(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", rec.Code)
		}
		if store.listCalls != 0 {
			t.Errorf("category store list calls = %d, want 0", store.listCalls)
		}
	})
}
