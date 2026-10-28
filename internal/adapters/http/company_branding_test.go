package httpadapter

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
)

// The company sign-in surfaces carry the providers' marks and keep their field
// explanations behind the toggletip, like every other Settings section
// (issue #383, S11; owner feedback on #419).

// TestLoginProviderButtonsCarryTheirLogo proves each provider button renders its
// own inline mark ahead of its label, decorative for assistive technology.
func TestLoginProviderButtonsCarryTheirLogo(t *testing.T) {
	h := newHarness(t)
	companySignInStore(t, h, companySignInIn(application.NewAccountsRequester))

	rec := doRequest(h.mux, h.mw, http.MethodGet, "/login", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /login = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, provider := range []string{"google", "microsoft"} {
		start := strings.Index(body, `href="/auth/`+provider+`/start"`)
		if start < 0 {
			t.Fatalf("no %s button in: %.2000s", provider, body)
		}
		button := body[start : start+strings.Index(body[start:], "</a>")]
		if !strings.Contains(button, `<svg class="company-logo"`) || !strings.Contains(button, `aria-hidden="true"`) {
			t.Errorf("the %s button must carry its decorative logo, got: %s", provider, button)
		}
	}
	if !strings.Contains(body, `fill="#4285F4"`) || !strings.Contains(body, `fill="#00A4EF"`) {
		t.Error("each button must render its own provider's mark")
	}
}

// TestSettingsSecurityFieldHelpIsVisible proves the Security section keeps every
// explanation as visible text (the maintainer's L5 decision for the
// tabs-and-rows design): a row description or a help paragraph, never a toggletip.
func TestSettingsSecurityFieldHelpIsVisible(t *testing.T) {
	h := newHarness(t)
	_, rootSession := seedRoot(t, h)
	body := sectionBodyFor(t, h, "/settings/security", rootSession.ID)

	if strings.Contains(body, `data-help-tip`) || strings.Contains(body, `role="tooltip"`) {
		t.Error("the Security section must not render a toggletip")
	}
	for _, help := range []string{
		"Off for everyone except root.",
		"Preferences gets a Password section.",
		"Sends a single-use link that expires in 1 hour.",
		"Between 8 and 64 characters. Applies to every new password",
		"The public address of this instance, such as",
		"Stored encrypted and never shown again. Leave it blank to keep the saved secret.",
		"Separate domains with a semicolon (;).",
		"Built from the TKT address above. Paste it into the Google Cloud console",
		"Only accounts from this tenant can sign in.",
		"Built from the TKT address above. Add it in the Entra portal",
	} {
		if !regexp.MustCompile(`<p( class="help")?>` + regexp.QuoteMeta(help)).MatchString(body) {
			t.Errorf("the Security section must render %q as visible text", help)
		}
	}
	for _, gone := range []string{"Company Google accounts only", "Your organization only", `id="help-tip-google-signin"`, `id="help-tip-microsoft-signin"`} {
		if strings.Contains(body, gone) {
			t.Errorf("the provider heads must not carry the vague copy %q", gone)
		}
	}
	if !strings.Contains(body, `<svg class="company-logo"`) {
		t.Error("the provider cards must carry their logo")
	}
}

// TestCompanyCallbackSessionCookieSurvivesTheCrossSiteReturn pins the one place a
// session cookie may be Lax. The callback is the provider's site sending the
// browser back, and the redirect to /tickets that follows is still part of that
// cross-site navigation, so a Strict cookie is withheld and the person lands on
// the sign-in page with a session the browser never presented. A password login
// starts on this site and keeps Strict.
func TestCompanyCallbackSessionCookieSurvivesTheCrossSiteReturn(t *testing.T) {
	h := newHarness(t)
	companySignInStore(t, h, companySignInIn(application.NewAccountsRequester))
	seedUser(t, h.store, "Ana", "ana@example.com")
	h.companyProvider.claims = googleClaims("ana@example.com")

	cookie, _ := companyStart(t, h, "google")
	rec := companyCallback(t, h, "google", cookie.Value, cookie.Value)

	session := findCookie(rec, sessionCookie)
	if session == nil || session.Value == "" {
		t.Fatal("the signed-in outcome issued no session cookie")
	}
	if session.SameSite != http.SameSiteLaxMode {
		t.Errorf("company session cookie SameSite = %v, want Lax so it travels with the redirect back from the provider", session.SameSite)
	}
	if !session.HttpOnly || session.Path != "/" {
		t.Errorf("company session cookie HttpOnly=%v Path=%q, want HttpOnly and /", session.HttpOnly, session.Path)
	}
}
