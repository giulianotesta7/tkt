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

// Root credentials (issue #414): the root account rotates its own password
// from Preferences and recovers it through the mailed link, while every other
// root protection stays where it was. The root row carries its own database
// guard, so these tests drive the two REAL routes with a real bcrypt credential
// and a real cookie session: the guard must let the password hash through and
// nothing else.

const (
	rootPasswordCurrent = "root-current-12"
	rootPasswordNext    = "root-next-12-x"
)

// rootPasswordForm builds one submission of the Preferences password section for
// the root account; mod may change or drop any field to build a refused one.
func rootPasswordForm(mod func(url.Values)) url.Values {
	form := url.Values{
		"current_password": {rootPasswordCurrent},
		"new_password":     {rootPasswordNext},
		"confirm_password": {rootPasswordNext},
	}
	if mod != nil {
		mod(form)
	}
	return form
}

// seedLoginReadyRoot stores the single root account with a real bcrypt password,
// so the sign-in route can mint a session for it. The store port is the arrange
// path here because no HTTP route creates a root.
func seedLoginReadyRoot(t *testing.T, h *harness) *domain.User {
	t.Helper()
	hash, err := application.HashPassword(rootPasswordCurrent)
	if err != nil {
		t.Fatalf("hash the root fixture password: %v", err)
	}
	root := &domain.User{
		Name: "Root", Email: "root@tkt.test", PasswordHash: hash,
		Role: domain.RoleRoot, Active: true, CreatedAt: fixedNow,
	}
	if err := h.store.UserStore().Create(t.Context(), root); err != nil {
		t.Fatalf("seed the root account: %v", err)
	}
	return root
}

// assertRootIdentityUnchanged proves a password write moved the hash and nothing
// else about the root account.
func assertRootIdentityUnchanged(t *testing.T, h *harness, root *domain.User) {
	t.Helper()
	stored, err := h.store.UserStore().GetByID(t.Context(), root.ID)
	if err != nil {
		t.Fatalf("read the root account: %v", err)
	}
	if stored.Name != root.Name || stored.Email != root.Email || stored.Role != root.Role ||
		stored.Active != root.Active || stored.CreatedAt.Unix() != root.CreatedAt.Unix() {
		t.Errorf("the password write moved another root field: got %+v, want the identity of %+v", stored, root)
	}
}

// TestRootChangesOwnPasswordFromPreferences proves the root credential contract on the own-password route:
// the root account signs in, submits current/new/confirmation, keeps the session
// it changed from, loses its other sessions and its pending recovery links, and
// can sign in with the new password only.
func TestRootChangesOwnPasswordFromPreferences(t *testing.T) {
	h := newHarness(t)
	root := seedLoginReadyRoot(t, h)
	configureEmailDeliveryAs(t, h, *root)
	own := h.loginCookie(t, root.Email, rootPasswordCurrent)
	other := h.loginCookie(t, root.Email, rootPasswordCurrent)
	if own == "" || other == "" {
		t.Fatal("the root account could not sign in")
	}
	// A link mailed before the change must not survive it.
	requestResetLink(t, h, root.Email)
	if got := pendingResetLinks(t, h); got != 1 {
		t.Fatalf("pending reset links = %d, want 1 before the change", got)
	}
	before := storedOwnPasswordHash(t, h, root.ID)

	rec := h.postFormAs(t, "/preferences/password", rootPasswordForm(nil), own)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /preferences/password as root = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/preferences" {
		t.Errorf("Location = %q, want /preferences", loc)
	}

	hash := storedOwnPasswordHash(t, h, root.ID)
	if hash == before {
		t.Error("the stored root hash did not change")
	}
	if !application.VerifyPassword(hash, rootPasswordNext) {
		t.Error("the stored root hash does not hold the submitted password")
	}
	if application.VerifyPassword(hash, rootPasswordCurrent) {
		t.Error("the previous root password still verifies")
	}
	if h.loginCookie(t, root.Email, rootPasswordCurrent) != "" {
		t.Error("the previous root password still signs in")
	}
	if h.loginCookie(t, root.Email, rootPasswordNext) == "" {
		t.Error("the new root password does not sign in")
	}
	if !sessionAuthenticates(t, h, own) {
		t.Error("the root session making the change was signed out")
	}
	wantRedirect(t, doRequest(h.mux, h.mw, http.MethodGet, "/preferences",
		map[string]string{"Cookie": sessionCookie + "=" + other}), http.StatusSeeOther, "/login")
	if got := pendingResetLinks(t, h); got != 0 {
		t.Errorf("pending reset links = %d, want 0 after the change", got)
	}
	assertRootIdentityUnchanged(t, h, root)
}

// TestRootRecoversPasswordThroughTheResetLink proves the root credential contract on the recovery route:
// the mailed link stores a new root password, signs every root session out, and
// works exactly once.
func TestRootRecoversPasswordThroughTheResetLink(t *testing.T) {
	h := newHarness(t)
	root := seedLoginReadyRoot(t, h)
	configureEmailDeliveryAs(t, h, *root)
	laptop := h.loginCookie(t, root.Email, rootPasswordCurrent)
	phone := h.loginCookie(t, root.Email, rootPasswordCurrent)
	if laptop == "" || phone == "" {
		t.Fatal("the root account could not sign in")
	}

	token := requestResetLink(t, h, root.Email)
	rec := resetForm(t, h, token, rootPasswordNext, rootPasswordNext)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /login/reset for the root account = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	wantRedirect(t, rec, http.StatusSeeOther, "/login?password_changed=1")
	assertNoTokenLeak(t, rec, token)

	if h.loginCookie(t, root.Email, rootPasswordNext) == "" {
		t.Error("the recovered root password must sign in")
	}
	if rec := h.postFormAs(t, "/login", authForm(root.Email, rootPasswordCurrent), ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("the old root password must be refused, status = %d", rec.Code)
	}
	for name, session := range map[string]string{"laptop": laptop, "phone": phone} {
		refused := doRequest(h.mux, h.mw, http.MethodGet, "/tickets",
			map[string]string{"Cookie": sessionCookie + "=" + session})
		if refused.Code != http.StatusSeeOther {
			t.Errorf("%s root session status = %d, want 303 after the reset", name, refused.Code)
		}
		if location := refused.Header().Get("Location"); location != "/login" {
			t.Errorf("%s root session Location = %q, want /login", name, location)
		}
	}
	assertRootIdentityUnchanged(t, h, root)

	// The link is single use: the second submission is the ordinary dead-link
	// refusal and stores nothing.
	again := resetForm(t, h, token, "root-second-45", "root-second-45")
	if again.Code != http.StatusGone {
		t.Fatalf("reused root link = %d, want 410: %s", again.Code, again.Body.String())
	}
	if rec := h.postFormAs(t, "/login", authForm(root.Email, "root-second-45"), ""); rec.Code != http.StatusUnauthorized {
		t.Error("a reused root link must not store its password")
	}
}

// TestRootPasswordRefusalsAreUnchanged proves the guards around the new root
// allowance did not move: a wrong current password, a mismatched confirmation and
// a disabled instance switch answer exactly what they answered before and change
// nothing, and an administrator still cannot set the root password at all.
func TestRootPasswordRefusalsAreUnchanged(t *testing.T) {
	h := newHarness(t)
	root := seedLoginReadyRoot(t, h)
	own := h.loginCookie(t, root.Email, rootPasswordCurrent)
	other := h.loginCookie(t, root.Email, rootPasswordCurrent)
	if own == "" || other == "" {
		t.Fatal("the root account could not sign in")
	}
	before := storedOwnPasswordHash(t, h, root.ID)

	for _, tc := range []struct {
		name    string
		form    url.Values
		field   string
		message string
	}{
		{
			name:    "wrong current password",
			form:    rootPasswordForm(func(f url.Values) { f.Set("current_password", "wrong-pass-12") }),
			field:   "current_password",
			message: "the current password is incorrect",
		},
		{
			name:    "mismatched confirmation",
			form:    rootPasswordForm(func(f url.Values) { f.Set("confirm_password", "different-pass-12") }),
			field:   "confirm_password",
			message: "the new passwords do not match",
		},
	} {
		rec := h.postFormAs(t, "/preferences/password", tc.form, own)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s = %d, want 422: %s", tc.name, rec.Code, rec.Body.String())
		}
		if body := rec.Body.String(); !strings.Contains(body, tc.message) {
			t.Errorf("%s must read %q, got: %.1200s", tc.name, tc.message, body)
		}
		assertOnlyFieldIsInvalid(t, rec.Body.String(), tc.field)
		if got := storedOwnPasswordHash(t, h, root.ID); got != before {
			t.Errorf("%s wrote the root hash", tc.name)
		}
		if !sessionAuthenticates(t, h, own) || !sessionAuthenticates(t, h, other) {
			t.Errorf("%s signed a root session out", tc.name)
		}
	}

	// The administrator route keeps refusing the root account (S6, unchanged).
	rec := h.postForm(t, "/users/"+strconv.FormatInt(root.ID, 10)+"/password",
		url.Values{"password": {"admin-set-root-12"}}, false)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /users/{root}/password = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, domain.ErrMsgRootProtected) {
		t.Errorf("the root refusal must still say %q, got: %.1200s", domain.ErrMsgRootProtected, body)
	}
	if got := storedOwnPasswordHash(t, h, root.ID); got != before {
		t.Error("the refused administrator write changed the root hash")
	}

	// The instance switch OFF still refuses the whole path (S1, unchanged).
	if err := h.passwords.Update(t.Context(), *h.admin, application.PasswordSettingsInput{
		ChangeEnabled: false, RecoveryEnabled: true, MinLength: application.DefaultPasswordMinLength,
	}); err != nil {
		t.Fatalf("switch the password change off: %v", err)
	}
	rec = h.postFormAs(t, "/preferences/password", rootPasswordForm(nil), own)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST with the switch OFF = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if got := storedOwnPasswordHash(t, h, root.ID); got != before {
		t.Error("the refused switch-off path wrote the root hash")
	}
	if !sessionAuthenticates(t, h, own) || !sessionAuthenticates(t, h, other) {
		t.Error("the refused switch-off path signed a root session out")
	}
	assertRootIdentityUnchanged(t, h, root)
}
