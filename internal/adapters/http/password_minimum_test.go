package httpadapter

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// New-password minimum on the existing routes (issue #414, T1/S4/S6/S10). The
// instance settings section that will edit the policy waits for its design, so
// these tests cover what already ships: the administrator's set-password route
// (which S6 keeps) and the first-user setup both refuse a password below the
// default 12, and the refusal is rendered by the existing error banners in the
// instance language.

// TestAdministratorSetPasswordRefusesAShortPassword covers S6 under S4: the
// route that sets another person's password still exists and now obeys the
// minimum, leaving the stored hash untouched and the route itself working for
// an accepted password.
func TestAdministratorSetPasswordRefusesAShortPassword(t *testing.T) {
	h := newHarness(t)
	member := h.createUser(t, "Miembro", "miembro@tkt.test", "SuperSecret42!")
	before, err := h.store.UserStore().GetByID(t.Context(), member.ID)
	if err != nil {
		t.Fatalf("read member: %v", err)
	}

	rec := h.postForm(t, "/users/"+strconv.FormatInt(member.ID, 10)+"/password",
		url.Values{"password": {"s3cret-pass"}}, false)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST /users/{id}/password with eleven characters = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "the password must be at least 12 characters long") {
		t.Errorf("the refusal must name the minimum, got: %.600s", body)
	}
	after, err := h.store.UserStore().GetByID(t.Context(), member.ID)
	if err != nil {
		t.Fatalf("read member: %v", err)
	}
	if after.PasswordHash != before.PasswordHash {
		t.Fatal("a refused password must leave the stored hash unchanged")
	}

	// The route keeps working: twelve characters are accepted.
	rec = h.postForm(t, "/users/"+strconv.FormatInt(member.ID, 10)+"/password",
		url.Values{"password": {"s3cret-pass-12"}}, false)
	wantRedirect(t, rec, http.StatusSeeOther, "/users/"+strconv.FormatInt(member.ID, 10)+"/edit")
}

// TestSetupRefusesAShortPassword covers the setup path under S4: the first user
// is not created from a password below the minimum, and a valid password still
// bootstraps the root.
func TestSetupRefusesAShortPassword(t *testing.T) {
	h := newEmptyHarness(t)

	rec := h.postFormAs(t, "/setup", url.Values{
		"name":     {"Root"},
		"email":    {"root@tkt.test"},
		"password": {"s3cret-pass"},
	}, "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST /setup with eleven characters = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "the password must be at least 12 characters long") {
		t.Errorf("the setup refusal must name the minimum, got: %.600s", body)
	}
	if count, err := h.store.UserStore().Count(t.Context()); err != nil || count != 0 {
		t.Fatalf("a refused setup created %d users (err %v), want 0", count, err)
	}

	rec = h.postFormAs(t, "/setup", url.Values{
		"name":     {"Root"},
		"email":    {"root@tkt.test"},
		"password": {"s3cret-pass-12"},
	}, "")
	wantRedirect(t, rec, http.StatusSeeOther, "/login")
	if count, err := h.store.UserStore().Count(t.Context()); err != nil || count != 1 {
		t.Fatalf("an accepted setup created %d users (err %v), want 1", count, err)
	}
}

// TestShortPasswordRefusalFollowsTheInstanceLanguage proves S10 on the routes
// that already ship: the new refusal is catalogue copy, so a Spanish instance
// reads it in Spanish instead of the English fallback.
func TestShortPasswordRefusalFollowsTheInstanceLanguage(t *testing.T) {
	h := newHarness(t)
	if err := h.settings.SetInterfaceLanguage(t.Context(), *h.admin, application.InterfaceLanguageSpanish); err != nil {
		t.Fatalf("set interface language: %v", err)
	}
	member := h.createUser(t, "Miembro", "miembro@tkt.test", "SuperSecret42!")

	rec := h.postForm(t, "/users/"+strconv.FormatInt(member.ID, 10)+"/password",
		url.Values{"password": {"corta"}}, false)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST /users/{id}/password = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "la contraseña debe tener al menos 12 caracteres") {
		t.Errorf("the refusal must render in the instance language, got: %.600s", body)
	}
	if strings.Contains(body, "\"error.") {
		t.Errorf("the refusal rendered a raw key: %.600s", body)
	}
}

// TestSetupShortPasswordRefusalFollowsTheInstanceLanguage covers the same S10
// rule on the setup page, which renders before any session exists.
func TestSetupShortPasswordRefusalFollowsTheInstanceLanguage(t *testing.T) {
	h := newEmptyHarness(t)
	if err := h.store.SettingsStore().SetInterfaceLanguage(t.Context(), application.InterfaceLanguageSpanish); err != nil {
		t.Fatalf("set interface language: %v", err)
	}

	rec := h.postFormAs(t, "/setup", url.Values{
		"name":     {"Root"},
		"email":    {"root@tkt.test"},
		"password": {"s3cret-pass"},
	}, "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST /setup = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "la contraseña debe tener al menos 12 caracteres") {
		t.Errorf("the setup refusal must render in the instance language, got: %.600s", body)
	}
}

// TestSetPasswordRouteKeepsTheBaseRefusalPrecedence pins the ORDER the minimum
// must not change. The route that sets another person's password already
// answered 403 for the root account and 404 for an unknown id; a short password
// submitted to either must still answer exactly that, because a 422 there would
// both regress the base behavior and tell an operator "root" or "no such user"
// through a different status than before.
func TestSetPasswordRouteKeepsTheBaseRefusalPrecedence(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()

	// The harness admin is a regular admin, so seed the root through the real
	// store port; the DB allows exactly one root row.
	root := &domain.User{Name: "Root", Email: "root@example.com", PasswordHash: "hash",
		Role: domain.RoleRoot, Active: true, CreatedAt: fixedNow}
	if err := h.store.UserStore().Create(ctx, root); err != nil {
		t.Fatalf("seed root: %v", err)
	}

	short := url.Values{"password": {"s3cret-pass"}}

	// Root target: 403 root-protected, exactly as before the minimum existed.
	rec := h.postForm(t, "/users/"+strconv.FormatInt(root.ID, 10)+"/password", short, false)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /users/{root}/password with eleven characters = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, domain.ErrMsgRootProtected) {
		t.Errorf("the root refusal must still say %q, got: %.600s", domain.ErrMsgRootProtected, body)
	}
	storedRoot, err := h.store.UserStore().GetByID(ctx, root.ID)
	if err != nil {
		t.Fatalf("read root: %v", err)
	}
	if storedRoot.PasswordHash != "hash" {
		t.Error("a refused root password must leave the stored hash unchanged")
	}

	// Unknown id: 404 not-found, exactly as before the minimum existed.
	rec = h.postForm(t, "/users/99999/password", short, false)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /users/{unknown}/password with eleven characters = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}
