package httpadapter

import (
	"net/http"
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

// TestSettingsSecurityFieldHelpIsBehindAToggletip proves the Security section
// keeps no field explanation as a paragraph: each is a described tooltip.
func TestSettingsSecurityFieldHelpIsBehindAToggletip(t *testing.T) {
	h := newHarness(t)
	_, rootSession := seedRoot(t, h)
	body := sectionBodyFor(t, h, "/settings/security", rootSession.ID)

	for _, id := range []string{
		"security-password-signin", "security-password-change", "security-password-recovery", "security-min-length",
		"company-address", "google-secret", "google-domains", "google-redirect",
		"microsoft-secret", "microsoft-tenant", "microsoft-redirect",
	} {
		if !strings.Contains(body, `role="tooltip" id="help-tip-`+id+`"`) {
			t.Errorf("the Security section must render the toggletip %q", id)
		}
	}
	if strings.Contains(body, `<p class="help"`) {
		t.Error("the Security section must not render a field explanation as a visible paragraph")
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
