package httpadapter

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
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

// TestAdministratorSetPasswordVoidsOnlyTheTargetAccountsLink proves the void is
// scoped: the account whose password was set loses its pending link, another
// account's link is untouched, and the refusals on the way (an administrator
// without the capability, an unknown target, a password under the minimum) leave
// the target's own link usable.
func TestAdministratorSetPasswordVoidsOnlyTheTargetAccountsLink(t *testing.T) {
	h := newHarness(t)
	const oldPassword = "s3cret-pass-12"
	member := h.createUser(t, "Miembro", "miembro@tkt.test", oldPassword)
	h.createUser(t, "Otra", "otra@tkt.test", oldPassword)
	configureEmailDelivery(t, h)
	memberSession := h.loginCookie(t, "miembro@tkt.test", oldPassword)
	if memberSession == "" {
		t.Fatal("the member could not sign in")
	}

	// Two accounts, two links. The throttle window belongs to the account, so the
	// clock only has to pass it before asking again for the first one.
	otherToken := requestResetLink(t, h, "otra@tkt.test")
	h.clock.Advance(application.PasswordResetThrottle)
	token := requestResetLink(t, h, "miembro@tkt.test")

	target := "/users/" + itoa(member.ID) + "/password"

	// The refusals keep their codes and leave the link alone.
	if rec := h.postFormAs(t, target, url.Values{"password": {"admin-set-pass-12"}}, memberSession); rec.Code != http.StatusForbidden {
		t.Errorf("as a member without the capability = %d, want 403", rec.Code)
	}
	if rec := h.postForm(t, "/users/999999/password", url.Values{"password": {"admin-set-pass-12"}}, false); rec.Code != http.StatusNotFound {
		t.Errorf("unknown target = %d, want 404", rec.Code)
	}
	if rec := h.postForm(t, target, url.Values{"password": {"short"}}, false); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("password under the minimum = %d, want 422", rec.Code)
	}
	if rec := doRequest(h.mux, h.mw, http.MethodGet, "/login/reset?token="+token, nil); rec.Code != http.StatusOK {
		t.Fatalf("a refused administrator change must leave the link usable, got %d", rec.Code)
	}

	wantRedirect(t, h.postForm(t, target, url.Values{"password": {"admin-set-pass-12"}}, false),
		http.StatusSeeOther, "/users/"+itoa(member.ID)+"/edit")

	// The target's link is dead and the other account's link still works.
	if rec := doRequest(h.mux, h.mw, http.MethodGet, "/login/reset?token="+token, nil); rec.Code != http.StatusGone {
		t.Errorf("the target's link = %d, want the refusal state", rec.Code)
	}
	if rec := doRequest(h.mux, h.mw, http.MethodGet, "/login/reset?token="+otherToken, nil); rec.Code != http.StatusOK {
		t.Errorf("another account's link = %d, want the form (the void is scoped)", rec.Code)
	}
}
