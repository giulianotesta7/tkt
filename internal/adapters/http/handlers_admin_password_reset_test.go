package httpadapter

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// The administrator set-password route under the rule that every password change
// voids pending recovery links (issue #414, S3/S6): S6 keeps the route exactly as
// it was — 303 back to the edit form, sessions untouched — and a link mailed
// before an administrator sets a password can no longer set one after it.

// TestAdministratorSetPasswordVoidsThePendingRecoveryLink proves the route keeps
// its behaviour and that the link mailed before it is dead: the reset page answers
// the refusal state, and the submission carrying that link stores nothing.
func TestAdministratorSetPasswordVoidsThePendingRecoveryLink(t *testing.T) {
	h := newHarness(t)
	const oldPassword = "SuperSecret42!"
	member := h.createUser(t, "Miembro", "miembro@tkt.test", oldPassword)
	configureEmailDelivery(t, h)
	session := h.loginCookie(t, "miembro@tkt.test", oldPassword)
	if session == "" {
		t.Fatal("the member could not sign in before the change")
	}
	token := requestResetLink(t, h, "miembro@tkt.test")
	if rec := doRequest(h.mux, h.mw, http.MethodGet, "/login/reset?token="+token, nil); rec.Code != http.StatusOK {
		t.Fatalf("GET /login/reset before the change = %d, want the form", rec.Code)
	}

	const adminPassword = "admin-set-pass-12"
	rec := h.postForm(t, "/users/"+strconv.FormatInt(member.ID, 10)+"/password",
		url.Values{"password": {adminPassword}}, false)
	wantRedirect(t, rec, http.StatusSeeOther, "/users/"+strconv.FormatInt(member.ID, 10)+"/edit")

	// S6: the route signs nobody out and the password it set is the one that works.
	signedIn := doRequest(h.mux, h.mw, http.MethodGet, "/tickets", map[string]string{
		"Cookie": sessionCookie + "=" + session,
	})
	if signedIn.Code != http.StatusOK {
		t.Errorf("the member's session after the administrator's change = %d, want 200 (S6 signs nobody out)", signedIn.Code)
	}
	if h.loginCookie(t, "miembro@tkt.test", adminPassword) == "" {
		t.Error("the administrator's new password must sign in")
	}

	// The link mailed before the change is refused, and redeeming it writes nothing.
	page := doRequest(h.mux, h.mw, http.MethodGet, "/login/reset?token="+token, nil)
	if page.Code != http.StatusGone {
		t.Fatalf("GET /login/reset after the change = %d, want the refusal state", page.Code)
	}
	if !strings.Contains(page.Body.String(), "This link is no longer valid") {
		t.Errorf("the refused link must render the refusal state, got: %.1500s", page.Body.String())
	}
	assertNoTokenLeak(t, page, token)

	const linkPassword = "recovered-pass-12"
	if submitted := resetForm(t, h, token, linkPassword, linkPassword); submitted.Code != http.StatusGone {
		t.Errorf("POST /login/reset with the refused link = %d, want 410", submitted.Code)
	}
	if rec := h.postFormAs(t, "/login", authForm("miembro@tkt.test", linkPassword), ""); rec.Code != http.StatusUnauthorized {
		t.Error("the refused link must not set the password it carried")
	}
	if h.loginCookie(t, "miembro@tkt.test", adminPassword) == "" {
		t.Error("the administrator's password must still sign in after the refused link")
	}
}
