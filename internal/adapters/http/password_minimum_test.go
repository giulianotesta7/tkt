package httpadapter

import (
	"net/http"
	"net/http/httptest"
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
//
// The same file carries the other length rule every new password obeys: the
// byte ceiling of the hash function, proved on every route that stores one.

// overByteCeilingPassword is one byte past the ceiling the hash accepts, so a
// route that lets it through answers 500 instead of a refusal.
var overByteCeilingPassword = strings.Repeat("a", 73)

// wantTooLongPasswordRefusal asserts the ONE refusal a password over the ceiling
// answers: status 422, the catalogued sentence naming the ceiling in plain words,
// and never a mention of the hash function or a raw key.
func wantTooLongPasswordRefusal(t *testing.T, rec *httptest.ResponseRecorder, path string) string {
	t.Helper()
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST %s with a 73-byte password = %d, want 422: %.600s", path, rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "the password must be at most 72 bytes long") {
		t.Errorf("POST %s must refuse the password in words, got: %.800s", path, body)
	}
	for _, leaked := range []string{"bcrypt", "error.password_too_long"} {
		if strings.Contains(body, leaked) {
			t.Errorf("POST %s leaked %q into the refusal", path, leaked)
		}
	}
	return body
}

// TestUserCreationRefusesAnOversizedPassword covers the create-user route: a
// password over the byte ceiling is a refused submission on the password field,
// and no account is created.
func TestUserCreationRefusesAnOversizedPassword(t *testing.T) {
	h := newHarness(t)
	before, err := h.store.UserStore().Count(t.Context())
	if err != nil {
		t.Fatalf("count users: %v", err)
	}

	rec := h.postForm(t, "/users", url.Values{
		"name":     {"Nueva"},
		"email":    {"nueva@tkt.test"},
		"password": {overByteCeilingPassword},
	}, false)
	wantTooLongPasswordRefusal(t, rec, "/users")
	if tag := passwordInputTag(t, rec.Body.String(), "user-password"); !strings.Contains(tag, `aria-invalid="true"`) {
		t.Errorf("the refusal must mark the password field, got: %s", tag)
	}
	after, err := h.store.UserStore().Count(t.Context())
	if err != nil {
		t.Fatalf("count users: %v", err)
	}
	if after != before {
		t.Fatalf("a refused creation stored %d users, want %d", after-before, 0)
	}

	// The route keeps working for a password the ceiling accepts.
	rec = h.postForm(t, "/users", url.Values{
		"name":     {"Nueva"},
		"email":    {"nueva@tkt.test"},
		"password": {"s3cret-pass-12"},
	}, false)
	wantRedirect(t, rec, http.StatusSeeOther, "/users")
}

// TestPreferencesPasswordRefusesAnOversizedPassword covers the self-service
// route: the refusal lands on the new-password field, nothing is stored, and no
// session is signed out.
func TestPreferencesPasswordRefusesAnOversizedPassword(t *testing.T) {
	h := newHarness(t)
	user := ownPasswordUser(t, h, "owner@example.com")
	own := ownPasswordSession(t, h, user.Email)
	other := ownPasswordSession(t, h, user.Email)
	before := storedOwnPasswordHash(t, h, user.ID)

	rec := h.postFormAs(t, "/preferences/password", ownPasswordForm(func(f url.Values) {
		f.Set(passwordFieldNew, overByteCeilingPassword)
		f.Set(passwordFieldConfirm, overByteCeilingPassword)
	}), own)
	body := wantTooLongPasswordRefusal(t, rec, "/preferences/password")
	assertOnlyFieldIsInvalid(t, body, passwordFieldNew)
	if strings.Contains(body, overByteCeilingPassword) {
		t.Error("the refused page echoed the submitted password")
	}
	if got := storedOwnPasswordHash(t, h, user.ID); got != before {
		t.Error("a refused change wrote the hash")
	}
	if !sessionAuthenticates(t, h, own) || !sessionAuthenticates(t, h, other) {
		t.Error("a refused change signed a session out")
	}
}

// TestResetRefusesAnOversizedPasswordAndKeepsTheLink covers the recovery form:
// the refusal lands on the new-password field, the stored password is untouched,
// and the link is still usable.
func TestResetRefusesAnOversizedPasswordAndKeepsTheLink(t *testing.T) {
	h := newHarness(t)
	const oldPassword = "s3cret-pass-12"
	h.createUser(t, "Ana", "ana@example.com", oldPassword)
	configureEmailDelivery(t, h)
	token := requestResetLink(t, h, "ana@example.com")

	rec := resetForm(t, h, token, overByteCeilingPassword, overByteCeilingPassword)
	body := wantTooLongPasswordRefusal(t, rec, "/login/reset")
	for _, want := range []string{
		`id="new_password" name="new_password" autocomplete="new-password" aria-describedby="password-min-help" aria-invalid="true"`,
		`<input type="hidden" name="token" value="` + token + `">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the refused form must contain %q, got: %.1500s", want, body)
		}
	}
	if rec := h.postFormAs(t, "/login", authForm("ana@example.com", oldPassword), ""); rec.Code != http.StatusSeeOther {
		t.Error("a refused password must not change the stored one")
	}
	if again := resetForm(t, h, token, "recovered-pass-12", "recovered-pass-12"); again.Code != http.StatusSeeOther {
		t.Errorf("the link must still work after a refused password, status = %d", again.Code)
	}
}

// TestOversizedPasswordRefusalFollowsTheInstanceLanguage covers S10 for the new
// refusal: it is catalogue copy, so a Spanish instance reads it in Spanish.
func TestOversizedPasswordRefusalFollowsTheInstanceLanguage(t *testing.T) {
	h := newHarness(t)
	if err := h.settings.SetInterfaceLanguage(t.Context(), *h.admin, application.InterfaceLanguageSpanish); err != nil {
		t.Fatalf("set interface language: %v", err)
	}
	member := h.createUser(t, "Miembro", "miembro@tkt.test", "SuperSecret42!")

	rec := h.postForm(t, "/users/"+strconv.FormatInt(member.ID, 10)+"/password",
		url.Values{"password": {overByteCeilingPassword}}, false)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST /users/{id}/password = %d, want 422: %.600s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "la contraseña debe tener como máximo 72 bytes") {
		t.Errorf("the refusal must render in the instance language, got: %.800s", body)
	}
}

// TestAdministratorSetPasswordRefusesAnOversizedPassword covers S6 under the
// byte ceiling: the route that sets another person's password refuses a password
// the hash cannot take, leaving the stored hash untouched.
func TestAdministratorSetPasswordRefusesAnOversizedPassword(t *testing.T) {
	h := newHarness(t)
	member := h.createUser(t, "Miembro", "miembro@tkt.test", "SuperSecret42!")
	before, err := h.store.UserStore().GetByID(t.Context(), member.ID)
	if err != nil {
		t.Fatalf("read member: %v", err)
	}

	rec := h.postForm(t, "/users/"+strconv.FormatInt(member.ID, 10)+"/password",
		url.Values{"password": {overByteCeilingPassword}}, false)
	wantTooLongPasswordRefusal(t, rec, "/users/{id}/password")
	after, err := h.store.UserStore().GetByID(t.Context(), member.ID)
	if err != nil {
		t.Fatalf("read member: %v", err)
	}
	if after.PasswordHash != before.PasswordHash {
		t.Fatal("a refused password must leave the stored hash unchanged")
	}
}

// TestSetupRefusesAnOversizedPassword covers the bootstrap route: no root is
// created from a password the hash cannot take.
func TestSetupRefusesAnOversizedPassword(t *testing.T) {
	h := newEmptyHarness(t)

	rec := h.postFormAs(t, "/setup", url.Values{
		"name":     {"Root"},
		"email":    {"root@tkt.test"},
		"password": {overByteCeilingPassword},
	}, "")
	wantTooLongPasswordRefusal(t, rec, "/setup")
	if count, err := h.store.UserStore().Count(t.Context()); err != nil || count != 0 {
		t.Fatalf("a refused setup created %d users (err %v), want 0", count, err)
	}
}

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
