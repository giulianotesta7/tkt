package httpadapter

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestUsersCreationDrawerGETContracts(t *testing.T) {
	h := newHarness(t)

	normal := h.get(t, "/users/new", false)
	if normal.Code != http.StatusOK || normal.Header().Get("Vary") != "HX-Request" {
		t.Fatalf("normal creation GET = %d/%q, want 200 and HX-Request vary", normal.Code, normal.Header().Get("Vary"))
	}
	normalBody := normal.Body.String()
	mustContain(t, normalBody,
		`id="users-root"`,
		`id="users-drawer-host"`,
		`role="dialog"`,
		`<p class="users-eyebrow">User details</p>`,
		`<h2 id="user-drawer-title">New user</h2>`,
		`name="name"`,
		`name="email"`,
		`name="password"`,
		`New accounts are created with the User role. You can change the role after creation.`,
		`name="status"`,
		`Create user`,
		`Cancel`,
	)
	if strings.Contains(normalBody, `name="role"`) || strings.Contains(normalBody, `name="active"`) || strings.Contains(normalBody, "Edit user") {
		t.Fatalf("creation drawer exposes edit-only controls: %s", normalBody)
	}
	if strings.Contains(normalBody, "Create a user account with standard access.") || strings.Contains(normalBody, "Update this user's name, email, role, or sign-in state.") || strings.Contains(normalBody, "user-drawer-summary") {
		t.Fatalf("drawer includes a redundant summary: %s", normalBody)
	}

	hx := h.get(t, "/users/new?status=active", true)
	if hx.Code != http.StatusOK || hx.Header().Get("Vary") != "HX-Request" {
		t.Fatalf("HTMX creation GET = %d/%q, want 200 and HX-Request vary", hx.Code, hx.Header().Get("Vary"))
	}
	hxBody := hx.Body.String()
	if strings.Contains(hxBody, "<html") || strings.Contains(hxBody, `id="users-root"`) {
		t.Fatalf("HTMX creation GET returned the full shell: %s", hxBody)
	}
	mustContain(t, hxBody, `id="users-drawer-host"`, `data-close-url="/users?status=active"`, `action="/users"`, `hx-post="/users"`, "New user")
}

func TestUsersCreationDrawerNormalSubmitRemainsRedirect(t *testing.T) {
	h := newHarness(t)
	form := url.Values{
		"name":     {"Normal Drawer User"},
		"email":    {"normal-drawer@example.com"},
		"password": {"DrawerSecret123!"},
	}

	rec := h.postForm(t, "/users", form, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/users" {
		t.Fatalf("normal creation = %d/%q, want 303 to /users", rec.Code, rec.Header().Get("Location"))
	}
}

func TestUsersCreationDrawerHTMXSuccessContract(t *testing.T) {
	h := newHarness(t)
	form := url.Values{
		"name":     {"Created Drawer User"},
		"email":    {"created-drawer@example.com"},
		"password": {"DrawerSecret123!"},
	}

	rec := h.postForm(t, "/users", form, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTMX creation status = %d: %s", rec.Code, rec.Body.String())
	}
	for key, want := range map[string]string{
		"HX-Retarget":           "#users-root",
		"HX-Reswap":             "outerHTML",
		"HX-Trigger-After-Swap": "users:saved",
	} {
		if got := rec.Header().Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	body := rec.Body.String()
	mustContain(t, body, `id="users-root"`, "Created Drawer User", "created-drawer@example.com", `id="users-drawer-host"></div>`)
	if strings.Contains(body, "DrawerSecret123!") || !strings.Contains(body, `id="users-drawer-host"></div>`) {
		t.Fatalf("successful creation response leaked password or kept drawer open: %s", body)
	}

	users, err := h.users.List(t.Context(), *h.admin)
	if err != nil {
		t.Fatal(err)
	}
	var created *domain.User
	for i := range users {
		if users[i].Email == "created-drawer@example.com" {
			created = &users[i]
			break
		}
	}
	if created == nil || !created.Active || created.Role != domain.RoleUser {
		t.Fatalf("created user = %#v, want active User", created)
	}
}

func TestUsersCreationDrawerValidationPreservesSafeValues(t *testing.T) {
	h := newHarness(t)
	invalid := url.Values{
		"name":     {""},
		"email":    {"draft@example.com"},
		"password": {"DrawerSecret123!"},
		"status":   {"active"},
	}

	hx := h.postForm(t, "/users", invalid, true)
	if hx.Code != http.StatusUnprocessableEntity || hx.Header().Get("HX-Retarget") != "#users-drawer-host" || hx.Header().Get("HX-Reswap") != "outerHTML" {
		t.Fatalf("HTMX validation = %d/%q: %s", hx.Code, hx.Header().Get("HX-Retarget"), hx.Body.String())
	}
	body := hx.Body.String()
	mustContain(t, body, `id="users-drawer-host"`, `id="user-name" name="name" type="text" value="" required aria-invalid="true"`, "draft@example.com", "name is required", "New user")
	if strings.Contains(body, "DrawerSecret123!") || strings.Contains(body, `name="role"`) || strings.Contains(body, `name="active"`) {
		t.Fatalf("validation response exposed unsafe or edit-only content: %s", body)
	}

	normal := h.postForm(t, "/users", invalid, false)
	if normal.Code != http.StatusUnprocessableEntity || !strings.Contains(normal.Body.String(), `id="users-root"`) || !strings.Contains(normal.Body.String(), `id="users-drawer-host"`) {
		t.Fatalf("normal validation did not keep the Users drawer open: %d/%s", normal.Code, normal.Body.String())
	}
}

func TestUsersCreationDrawerDuplicateEmailPreservesEmailField(t *testing.T) {
	h := newHarness(t)
	h.createUser(t, "Existing User", "existing@example.com", "secret")
	form := url.Values{
		"name":     {"Duplicate Drawer User"},
		"email":    {"existing@example.com"},
		"password": {"DrawerSecret123!"},
	}

	rec := h.postForm(t, "/users", form, true)
	if rec.Code != http.StatusConflict || rec.Header().Get("HX-Retarget") != "#users-drawer-host" {
		t.Fatalf("HTMX duplicate = %d/%q: %s", rec.Code, rec.Header().Get("HX-Retarget"), rec.Body.String())
	}
	body := rec.Body.String()
	mustContain(t, body, `id="user-email" name="email" type="email" value="existing@example.com" required aria-invalid="true"`, "New user")
	if strings.Contains(body, "DrawerSecret123!") {
		t.Fatalf("duplicate response leaked plaintext password: %s", body)
	}
}
