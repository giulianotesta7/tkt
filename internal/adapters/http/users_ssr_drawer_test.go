package httpadapter

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

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
	mustContain(t, body, `id="users-root"`, `All <span>3</span>`, `Active <span>2</span>`, `Deactivated <span>1</span>`, `href="/users/new"`, active.Email, inactive.Email, "Created", "users.css", "users.js")
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
