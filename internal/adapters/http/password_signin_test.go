package httpadapter

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/internal/i18n"
)

// Company sign-in keeps password sign-in to root alone (issue #419, S4/S5).
// The sign-in page refuses a verified non-root password with its own sentence
// on the same rendering path as the generic refusal, the Preferences password
// change is refused, and the switch ON leaves every path exactly as it was.
// Recovery reaches the same application rule: the public form answers its
// neutral result and mails nothing, and a link of a non-root account is
// refused, both covered where the recovery routes are wired.

// passwordSignInOff stores the company sign-in configuration with password
// sign-in OFF, the state every rule below reads.
func passwordSignInOff(t *testing.T, h *harness) {
	t.Helper()
	settings := application.DefaultCompanySignInSettings()
	settings.PasswordSignInEnabled = false
	if err := h.store.CompanySignInSettingsStore().SetCompanySignIn(context.Background(), settings); err != nil {
		t.Fatalf("turn password sign-in off: %v", err)
	}
}

// passwordSignInOn stores the switch in its shipped state.
func passwordSignInOn(t *testing.T, h *harness) {
	t.Helper()
	if err := h.store.CompanySignInSettingsStore().SetCompanySignIn(context.Background(), application.DefaultCompanySignInSettings()); err != nil {
		t.Fatalf("turn password sign-in on: %v", err)
	}
}

// storedSessionCount reads the sessions table directly: whether a refused
// sign-in created one is a persistence fact no store port counts.
func storedSessionCount(t *testing.T, h *harness) int64 {
	t.Helper()
	return scanOneInt(t, h.rawDB(t), `SELECT COUNT(*) FROM sessions`)
}

// seedRootWithPassword creates the one root account with a REAL bcrypt hash,
// so the break-glass path runs through the ordinary login route. The store's
// create path is used because root is immutable once stored: its password is
// set here, exactly as the first-user bootstrap would.
func seedRootWithPassword(t *testing.T, h *harness, email, password string) *domain.User {
	t.Helper()
	hash, err := application.HashPassword(password)
	if err != nil {
		t.Fatalf("hash the root password: %v", err)
	}
	root := &domain.User{Name: "Root", Email: email, PasswordHash: hash, Role: domain.RoleRoot, Active: true, CreatedAt: fixedNow}
	if err := h.store.UserStore().Create(context.Background(), root); err != nil {
		t.Fatalf("seed the root account: %v", err)
	}
	return root
}

// TestPasswordSignInOffRefusesANonRootSignIn is S4 at the surface: a verified
// password of an ordinary account answers the sign-in page with the dedicated
// sentence, issues no session cookie, and writes no session row.
func TestPasswordSignInOffRefusesANonRootSignIn(t *testing.T) {
	h := newHarness(t)
	passwordSignInOff(t, h)
	ana := h.createUser(t, "Ana", "ana@example.com", "s3cret-pass-12")
	before := storedSessionCount(t, h)

	rec := h.postFormAs(t, "/login", authForm(ana.Email, "s3cret-pass-12"), "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /login with password sign-in off = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	wantMessage := i18n.T(i18n.Default, "error.password_signin_off")
	if !strings.Contains(rec.Body.String(), wantMessage) {
		t.Fatalf("the sign-in page must show %q, got: %.2000s", wantMessage, rec.Body.String())
	}
	if cookie := findCookie(rec, sessionCookie); cookie != nil && cookie.Value != "" {
		t.Errorf("the refused sign-in issued a session cookie %q, want none", cookie.Value)
	}
	if got := storedSessionCount(t, h); got != before {
		t.Errorf("the refused sign-in wrote %d session rows, want 0 new", got-before)
	}

	// A wrong password keeps the ONE generic refusal, so the dedicated sentence
	// never tells anyone whether the account exists.
	rec = h.postFormAs(t, "/login", authForm(ana.Email, "wrong-password"), "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST /login with a wrong password = %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), application.ErrMsgInvalidCredentials) {
		t.Fatalf("a wrong password must keep the generic refusal, got: %.2000s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), wantMessage) {
		t.Error("a wrong password must never reveal that password sign-in is off")
	}
}

// TestPasswordSignInOffKeepsRootSignIn is S4's break-glass at the surface: root
// signs in with a password while the switch keeps everyone else out.
func TestPasswordSignInOffKeepsRootSignIn(t *testing.T) {
	h := newHarness(t)
	passwordSignInOff(t, h)
	root := seedRootWithPassword(t, h, "root@example.com", "root-pass-12-x")
	before := storedSessionCount(t, h)

	rec := h.postFormAs(t, "/login", authForm(root.Email, "root-pass-12-x"), "")
	wantRedirect(t, rec, http.StatusSeeOther, "/tickets")
	if cookie := findCookie(rec, sessionCookie); cookie == nil || cookie.Value == "" {
		t.Fatal("root's sign-in issued no session cookie")
	}
	if got := storedSessionCount(t, h); got != before+1 {
		t.Errorf("root's sign-in wrote %d session rows, want exactly 1", got-before)
	}
}

// TestPasswordSignInOffRefusesThePreferencesPasswordChange is S5's Preferences
// half: the route refuses and the stored hash stays exactly as it was.
func TestPasswordSignInOffRefusesThePreferencesPasswordChange(t *testing.T) {
	h := newHarness(t)
	passwordSignInOff(t, h)
	owner := ownPasswordUser(t, h, "owner@example.com")
	// The account signed in before the switch went off, so its session is
	// seeded: the switch removes the password path, never a session already
	// established.
	own := seedSession(t, h.store, owner.ID).ID
	before := storedOwnPasswordHash(t, h, owner.ID)

	rec := h.postFormAs(t, "/preferences/password", ownPasswordForm(nil), own)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /preferences/password with password sign-in off = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if got := storedOwnPasswordHash(t, h, owner.ID); got != before {
		t.Error("the refused change replaced the stored hash")
	}
	if !application.VerifyPassword(before, ownPasswordCurrent) {
		t.Fatal("the fixture's own hash no longer holds the current password")
	}
	if !sessionAuthenticates(t, h, own) {
		t.Error("the refused change signed the account out")
	}
}

// TestPasswordSignInOnKeepsEveryPasswordPath is the regression guard for S4
// and S5: with the switch in its shipped ON state the sign-in, the recovery
// form and the Preferences change behave exactly as before.
func TestPasswordSignInOnKeepsEveryPasswordPath(t *testing.T) {
	h := newHarness(t)
	passwordSignInOn(t, h)
	owner := ownPasswordUser(t, h, "owner@example.com")

	rec := h.postFormAs(t, "/login", authForm(owner.Email, ownPasswordCurrent), "")
	wantRedirect(t, rec, http.StatusSeeOther, "/tickets")
	if cookie := findCookie(rec, sessionCookie); cookie == nil || cookie.Value == "" {
		t.Fatal("with the switch ON the sign-in issued no session cookie")
	}

	own := ownPasswordSession(t, h, owner.Email)
	rec = h.postFormAs(t, "/preferences/password", ownPasswordForm(nil), own)
	wantRedirect(t, rec, http.StatusSeeOther, "/preferences")
	if !application.VerifyPassword(storedOwnPasswordHash(t, h, owner.ID), ownPasswordNext) {
		t.Error("with the switch ON the password was not replaced")
	}
}

// TestLoginPageOffersThePasswordFormWhileSignInIsOff is the surfaces' reading
// of the switch: the sign-in page keeps the password form root still needs, and
// no company button is offered before a provider is configured.
func TestLoginPageOffersThePasswordFormWhileSignInIsOff(t *testing.T) {
	h := newHarness(t)
	passwordSignInOff(t, h)
	body := doRequest(h.mux, h.mw, http.MethodGet, "/login", nil).Body.String()
	if !strings.Contains(body, `name="password"`) {
		t.Errorf("the sign-in page must keep the password form, got: %.2000s", body)
	}
	if strings.Contains(body, application.CompanySignInPathPrefix) {
		t.Errorf("no provider is configured, so no company sign-in link may render: %.2000s", body)
	}
}

// TestPreferencesPasswordFormIsUnreachableWhilePasswordSignInIsOff is the
// hidden half of S5, asserted on the page: the section and its route disappear
// for a non-root account, while root keeps them.
func TestPreferencesPasswordFormIsUnreachableWhilePasswordSignInIsOff(t *testing.T) {
	h := newHarness(t)
	passwordSignInOff(t, h)
	owner := ownPasswordUser(t, h, "owner@example.com")
	own := seedSession(t, h.store, owner.ID).ID

	rec := doRequest(h.mux, h.mw, http.MethodGet, "/preferences", map[string]string{"Cookie": sessionCookie + "=" + own})
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /preferences = %d, want 200", rec.Code)
	}
	for _, gone := range []string{"/preferences/password", `name="current_password"`} {
		if strings.Contains(rec.Body.String(), gone) {
			t.Errorf("with password sign-in off the page must not render %q, got: %.2000s", gone, rec.Body.String())
		}
	}

	root := seedRootWithPassword(t, h, "root@example.com", "root-pass-12-x")
	rootSession := seedSession(t, h.store, root.ID)
	rec = doRequest(h.mux, h.mw, http.MethodGet, "/preferences", map[string]string{"Cookie": sessionCookie + "=" + rootSession.ID})
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /preferences as root = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "/preferences/password") {
		t.Errorf("root must keep the password section, got: %.2000s", rec.Body.String())
	}
}

// TestPreferencesPasswordFormIsUnreachableWithoutTheSwitchOff is the regression
// guard for the same page: the section is there while the switch is ON.
func TestPreferencesPasswordFormIsUnreachableWithoutTheSwitchOff(t *testing.T) {
	h := newHarness(t)
	passwordSignInOn(t, h)
	owner := ownPasswordUser(t, h, "owner@example.com")
	own := ownPasswordSession(t, h, owner.Email)

	rec := doRequest(h.mux, h.mw, http.MethodGet, "/preferences", map[string]string{"Cookie": sessionCookie + "=" + own})
	if !strings.Contains(rec.Body.String(), "/preferences/password") {
		t.Errorf("with the switch ON the section must render, got: %.2000s", rec.Body.String())
	}
}
