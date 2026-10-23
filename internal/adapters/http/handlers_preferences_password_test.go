package httpadapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// The Preferences password section (issue #414, specs S1, S2, S4, S10): the
// signed-in user changes their own password from an always-visible section of
// /preferences. The current password is required, the session making the request
// stays signed in while every other session of that account is signed out, and
// the new password obeys the instance minimum. The instance switch removes the
// section and refuses the route.
//
// The password rules themselves (runes counted, hashes, refusal order) belong to
// the application and sqlite layers. This file proves the surface: what renders,
// what a submission answers, and what a refusal leaves untouched.

const (
	ownPasswordCurrent = "current-pass-12"
	ownPasswordNext    = "next-pass-12-x"
)

// ownPasswordForm builds one submission of the section; mod may change or drop
// any field to build a refused one.
func ownPasswordForm(mod func(url.Values)) url.Values {
	form := url.Values{
		"current_password": {ownPasswordCurrent},
		"new_password":     {ownPasswordNext},
		"confirm_password": {ownPasswordNext},
	}
	if mod != nil {
		mod(form)
	}
	return form
}

// ownPasswordUser creates a login-ready account and answers its user row.
func ownPasswordUser(t *testing.T, h *harness, email string) *domain.User {
	t.Helper()
	return h.createUser(t, "Password Owner", email, ownPasswordCurrent)
}

// ownPasswordSession signs email in and answers the session id the login route
// issued, so a test owns the exact cookie it asserts on.
func ownPasswordSession(t *testing.T, h *harness, email string) string {
	t.Helper()
	session := h.loginCookie(t, email, ownPasswordCurrent)
	if session == "" {
		t.Fatalf("signing in %s failed", email)
	}
	return session
}

// storedOwnPasswordHash reads the account's stored hash through its own port.
func storedOwnPasswordHash(t *testing.T, h *harness, userID int64) string {
	t.Helper()
	user, err := h.store.UserStore().GetByID(context.Background(), userID)
	if err != nil {
		t.Fatalf("read the stored user %d: %v", userID, err)
	}
	return user.PasswordHash
}

// sessionAuthenticates answers whether one session id still reaches an
// authenticated page.
func sessionAuthenticates(t *testing.T, h *harness, sessionID string) bool {
	t.Helper()
	rec := doRequest(h.mux, h.mw, http.MethodGet, "/preferences",
		map[string]string{"Cookie": sessionCookie + "=" + sessionID})
	return rec.Code == http.StatusOK
}

// passwordInputTag returns the markup of one password input by id, so an
// assertion about its state cannot be satisfied by a sibling field.
func passwordInputTag(t *testing.T, body, id string) string {
	t.Helper()
	marker := `<input id="` + id + `"`
	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatalf("the page renders no %s input: %.1500s", id, body)
	}
	rest := body[start:]
	end := strings.Index(rest, ">")
	if end < 0 {
		t.Fatalf("the %s input is not closed: %s", id, rest)
	}
	return rest[:end+1]
}

// assertOnlyFieldIsInvalid proves exactly one field carries the refused state and
// that it is the expected one.
func assertOnlyFieldIsInvalid(t *testing.T, body, want string) {
	t.Helper()
	for _, id := range []string{"current_password", "new_password", "confirm_password"} {
		tag := passwordInputTag(t, body, id)
		invalid := strings.Contains(tag, `aria-invalid="true"`)
		if id == want && !invalid {
			t.Errorf("the %s field must carry aria-invalid (the refusal belongs to it): %s", id, tag)
		}
		if id != want && invalid {
			t.Errorf("the %s field must not carry aria-invalid: %s", id, tag)
		}
	}
}

// TestPreferencesPasswordSectionFollowsTheInstanceSwitch covers the read half:
// the section and its minimum are rendered while the switch is ON, the configured
// minimum is the one it shows, and the whole section disappears with the switch
// OFF — including its route.
func TestPreferencesPasswordSectionFollowsTheInstanceSwitch(t *testing.T) {
	h := newHarness(t)

	body := h.get(t, "/preferences", false).Body.String()
	for _, want := range []string{
		`<form class="preferences-form" method="post" action="/preferences/password">`,
		`<h2>Password</h2>`,
		`id="current_password" name="current_password" type="password" autocomplete="current-password"`,
		`id="new_password" name="new_password" type="password" autocomplete="new-password"`,
		`id="confirm_password" name="confirm_password" type="password" autocomplete="new-password"`,
		`id="password-min-help">At least 12 characters.`,
		"Saving signs out every other session. This one stays signed in.",
		">Change password<",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the password section must render %q, got: %.2000s", want, body)
		}
	}

	// The instance minimum is what the section teaches.
	if err := h.passwords.Update(t.Context(), *h.admin, application.PasswordSettingsInput{
		ChangeEnabled: true, RecoveryEnabled: true, MinLength: 16,
	}); err != nil {
		t.Fatalf("raise the instance minimum: %v", err)
	}
	body = h.get(t, "/preferences", false).Body.String()
	if !strings.Contains(body, "At least 16 characters.") {
		t.Errorf("the section must show the configured minimum, got: %.2000s", body)
	}

	// S1: the switch OFF removes the path, so the section and its controls go.
	if err := h.passwords.Update(t.Context(), *h.admin, application.PasswordSettingsInput{
		ChangeEnabled: false, RecoveryEnabled: true, MinLength: 16,
	}); err != nil {
		t.Fatalf("switch the section off: %v", err)
	}
	body = h.get(t, "/preferences", false).Body.String()
	for _, gone := range []string{
		"/preferences/password",
		`name="current_password"`,
		">Change password<",
	} {
		if strings.Contains(body, gone) {
			t.Errorf("with the switch OFF the page must not render %q, got: %.2000s", gone, body)
		}
	}
	// The other preferences keep their own controls.
	if !strings.Contains(body, `name="queue_order"`) {
		t.Error("with the switch OFF the page must keep the other preference sections")
	}
}

// TestPreferencesPasswordChangeSignsOutOnlyTheOtherSessions covers S2's success
// contract: the stored hash really holds the new password, the session making
// the request keeps working, every other session of that account is signed out,
// and another account keeps its session.
func TestPreferencesPasswordChangeSignsOutOnlyTheOtherSessions(t *testing.T) {
	h := newHarness(t)
	user := ownPasswordUser(t, h, "owner@example.com")
	own := ownPasswordSession(t, h, user.Email)
	other := ownPasswordSession(t, h, user.Email)
	before := storedOwnPasswordHash(t, h, user.ID)

	rec := h.postFormAs(t, "/preferences/password", ownPasswordForm(nil), own)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /preferences/password = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/preferences" {
		t.Errorf("Location = %q, want /preferences", loc)
	}
	if cookie := findCookie(rec, saveFeedbackCookie); cookie == nil || cookie.Value == "" {
		t.Errorf("a successful change issued no %s cookie, want the shared save feedback", saveFeedbackCookie)
	}

	hash := storedOwnPasswordHash(t, h, user.ID)
	if hash == before {
		t.Error("the stored hash did not change")
	}
	if !application.VerifyPassword(hash, ownPasswordNext) {
		t.Error("the stored hash does not hold the submitted password")
	}
	if application.VerifyPassword(hash, ownPasswordCurrent) {
		t.Error("the previous password still verifies")
	}
	if h.loginCookie(t, user.Email, ownPasswordCurrent) != "" {
		t.Error("the previous password still signs in")
	}
	if h.loginCookie(t, user.Email, ownPasswordNext) == "" {
		t.Error("the new password does not sign in")
	}

	if !sessionAuthenticates(t, h, own) {
		t.Error("the session making the change was signed out")
	}
	// Every other session of that account is gone, so the next request it makes
	// is the ordinary sign-in redirect; another account keeps working.
	wantRedirect(t, doRequest(h.mux, h.mw, http.MethodGet, "/preferences",
		map[string]string{"Cookie": sessionCookie + "=" + other}), http.StatusSeeOther, "/login")
	if !sessionAuthenticates(t, h, h.adminSession.ID) {
		t.Error("another account's session was signed out")
	}
}

// TestPreferencesPasswordChangeRefusals covers every refusal the form can reach:
// the message, the field it belongs to, and that nothing changed — neither the
// stored hash nor one session, and never a submitted password echoed back.
func TestPreferencesPasswordChangeRefusals(t *testing.T) {
	for _, tc := range []struct {
		name    string
		form    url.Values
		field   string
		message string
		echoed  []string
	}{
		{
			name:    "wrong current password",
			form:    ownPasswordForm(func(f url.Values) { f.Set("current_password", "wrong-pass-12") }),
			field:   "current_password",
			message: "the current password is incorrect",
			echoed:  []string{"wrong-pass-12", ownPasswordNext},
		},
		{
			name:    "mismatched confirmation",
			form:    ownPasswordForm(func(f url.Values) { f.Set("confirm_password", "different-pass-12") }),
			field:   "confirm_password",
			message: "the new passwords do not match",
			echoed:  []string{ownPasswordNext, "different-pass-12"},
		},
		{
			name:    "short new password",
			form:    ownPasswordForm(func(f url.Values) { f.Set("new_password", "short-pass"); f.Set("confirm_password", "short-pass") }),
			field:   "new_password",
			message: "the password must be at least 12 characters long",
			echoed:  []string{"short-pass"},
		},
		{
			name:    "blank new password",
			form:    ownPasswordForm(func(f url.Values) { f.Set("new_password", "   "); f.Set("confirm_password", "   ") }),
			field:   "new_password",
			message: "password is required",
			echoed:  []string{ownPasswordCurrent},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			user := ownPasswordUser(t, h, "owner@example.com")
			own := ownPasswordSession(t, h, user.Email)
			other := ownPasswordSession(t, h, user.Email)
			before := storedOwnPasswordHash(t, h, user.ID)

			rec := h.postFormAs(t, "/preferences/password", tc.form, own)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("a refused change = %d, want 422: %s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if !strings.Contains(body, tc.message) {
				t.Errorf("the refusal must read %q, got: %.2000s", tc.message, body)
			}
			assertOnlyFieldIsInvalid(t, body, tc.field)
			for _, value := range tc.echoed {
				if strings.Contains(body, value) {
					t.Errorf("the refused page echoed the submitted password %q", value)
				}
			}

			if got := storedOwnPasswordHash(t, h, user.ID); got != before {
				t.Error("a refused change wrote the hash")
			}
			if !sessionAuthenticates(t, h, own) || !sessionAuthenticates(t, h, other) {
				t.Error("a refused change signed a session out")
			}
		})
	}
}

// TestPreferencesPasswordChangeRefusedWhileTheSwitchIsOff covers S1's write half:
// with the switch OFF the route answers 403 and changes nothing, even for a
// submission that would otherwise succeed.
func TestPreferencesPasswordChangeRefusedWhileTheSwitchIsOff(t *testing.T) {
	h := newHarness(t)
	user := ownPasswordUser(t, h, "owner@example.com")
	own := ownPasswordSession(t, h, user.Email)
	other := ownPasswordSession(t, h, user.Email)
	before := storedOwnPasswordHash(t, h, user.ID)

	if err := h.passwords.Update(t.Context(), *h.admin, application.PasswordSettingsInput{
		ChangeEnabled: false, RecoveryEnabled: true, MinLength: 12,
	}); err != nil {
		t.Fatalf("switch the section off: %v", err)
	}

	rec := h.postFormAs(t, "/preferences/password", ownPasswordForm(nil), own)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST with the switch OFF = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if got := storedOwnPasswordHash(t, h, user.ID); got != before {
		t.Error("the refused route wrote the hash")
	}
	if !sessionAuthenticates(t, h, own) || !sessionAuthenticates(t, h, other) {
		t.Error("the refused route signed a session out")
	}
}

// TestPreferencesPasswordRequiresAuthentication proves the route is behind the
// session gate like the rest of the page.
func TestPreferencesPasswordRequiresAuthentication(t *testing.T) {
	h := newHarness(t)
	req := httptest.NewRequest(http.MethodPost, "/preferences/password", strings.NewReader(ownPasswordForm(nil).Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)
	wantRedirect(t, rec, http.StatusSeeOther, "/login")
}

// TestPreferencesPasswordSpeaksSpanish covers S10 for this section: the section,
// its minimum and every refusal read Spanish on a Spanish instance.
func TestPreferencesPasswordSpeaksSpanish(t *testing.T) {
	h := newHarness(t)
	user := ownPasswordUser(t, h, "owner@example.com")
	own := ownPasswordSession(t, h, user.Email)
	if err := h.settings.SetInterfaceLanguage(t.Context(), *h.admin, application.InterfaceLanguageSpanish); err != nil {
		t.Fatalf("switch the instance to Spanish: %v", err)
	}

	body := h.get(t, "/preferences", false).Body.String()
	for _, want := range []string{
		"<h2>Contraseña</h2>",
		"Cambia la contraseña con la que inicias sesión.",
		"Contraseña actual",
		"Contraseña nueva",
		"Confirma la contraseña nueva",
		"Al menos 12 caracteres.",
		"Al guardar se cierran las demás sesiones. Esta sigue abierta.",
		"Cambiar contraseña",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Spanish preferences must render %q, got: %.2000s", want, body)
		}
	}

	for _, tc := range []struct {
		name string
		form url.Values
		want string
	}{
		{"wrong current password", ownPasswordForm(func(f url.Values) { f.Set("current_password", "wrong-pass-12") }), "la contraseña actual no es correcta"},
		{"mismatched confirmation", ownPasswordForm(func(f url.Values) { f.Set("confirm_password", "different-pass-12") }), "las contraseñas nuevas no coinciden"},
		{"short new password", ownPasswordForm(func(f url.Values) { f.Set("new_password", "short-pass"); f.Set("confirm_password", "short-pass") }), "la contraseña debe tener al menos 12 caracteres"},
	} {
		rec := h.postFormAs(t, "/preferences/password", tc.form, own)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("a refused %s = %d, want 422: %s", tc.name, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("the Spanish refusal for %s must read %q, got: %.2000s", tc.name, tc.want, rec.Body.String())
		}
	}
}
