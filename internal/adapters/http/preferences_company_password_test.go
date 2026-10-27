package httpadapter

import (
	"net/http"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
)

// An account a company sign-in created has no password. The own-password section
// asks for the current one and can never receive it, so it would only answer
// "current password is incorrect" to someone who never had a password: the
// section is not offered to that account and its route refuses (issue #419,
// owner decision: hide it; "Forgot your password?" remains the way to ask for one).

// companyOnlySession signs a brand-new account in through the provider flow and
// answers the session id the callback issued.
func companyOnlySession(t *testing.T, h *harness) string {
	t.Helper()
	companySignInStore(t, h, companySignInIn(application.NewAccountsRequester))
	h.companyProvider.claims = googleClaims("nuevo@example.com")
	cookie, _ := companyStart(t, h, "google")
	rec := companyCallback(t, h, "google", cookie.Value, cookie.Value)
	session := findCookie(rec, sessionCookie)
	if session == nil || session.Value == "" {
		t.Fatalf("the provider sign-in issued no session: %d %.400s", rec.Code, rec.Body.String())
	}
	return session.Value
}

func TestPreferencesHidesThePasswordSectionFromAnAccountWithoutAPassword(t *testing.T) {
	h := newHarness(t)
	session := companyOnlySession(t, h)

	rec := getAs(t, h, "/preferences", session)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /preferences = %d, want 200: %.600s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `action="/preferences/password"`) {
		t.Error("an account without a password must not be offered the own-password section")
	}
}

func TestPreferencesPasswordRouteRefusesAnAccountWithoutAPassword(t *testing.T) {
	h := newHarness(t)
	session := companyOnlySession(t, h)

	rec := h.postFormAs(t, "/preferences/password", ownPasswordForm(nil), session)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /preferences/password = %d, want 403: %.600s", rec.Code, rec.Body.String())
	}
}

func TestPreferencesKeepsThePasswordSectionForAnAccountWithAPassword(t *testing.T) {
	h := newHarness(t)
	ownPasswordUser(t, h, "tiene@example.com")
	session := ownPasswordSession(t, h, "tiene@example.com")

	rec := getAs(t, h, "/preferences", session)
	if !strings.Contains(rec.Body.String(), `action="/preferences/password"`) {
		t.Error("an account with a password keeps the own-password section")
	}
}
