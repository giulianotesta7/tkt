package httpadapter

import (
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// The Security section's company sign-in configuration (issue #419; specs S1,
// S2, S4, S11, S12, S13): one shared public TKT address, one card per provider
// under it, and the new-account policy, all saved as one unit through the real
// configuration service over the sealed store. The password sign-in switch sits
// in the Passwords card and belongs to this form, because it is part of the same
// configuration: turning it off leaves root as the only password account and is
// refused while no provider is on.
//
// These tests drive the public routes through the real harness (real database,
// real services, real renderer) and assert persistence through the same service
// the product uses, never through raw SQL. Every client secret stays write-only:
// a blank field keeps the saved secret, and no stored or submitted secret ever
// reaches the HTML.

// companySignInForm is one complete company sign-in submission; mod may change
// or drop any field to build a rejected or partial one. Microsoft stays off
// unless a case turns it on, which is how the rendered form submits it.
func companySignInForm(mod func(url.Values)) url.Values {
	form := url.Values{
		"company_signin_address":      {companyAddress},
		"company_signin_new_accounts": {"requester"},
		"password_signin_enabled":     {"1"},
		"google_enabled":              {"1"},
		"google_client_id":            {"google-client-id"},
		"google_client_secret":        {"google-client-secret"},
		"google_domains":              {"example.com\nexample-support.com"},
	}
	if mod != nil {
		mod(form)
	}
	return form
}

// storedCompanySignIn reads the configuration through its own port: a test never
// asserts persistence against a rendered page.
func storedCompanySignIn(t *testing.T, h *harness) application.CompanySignInSettings {
	t.Helper()
	got, err := h.companySettings.Get(t.Context())
	if err != nil {
		t.Fatalf("read company sign-in settings: %v", err)
	}
	return got
}

// companySealedSecrets reads the store side of the port, which carries the
// sealed ciphertext, so a test can prove the HTML never renders it.
func companySealedSecrets(t *testing.T, h *harness) application.CompanySignInSettings {
	t.Helper()
	got, err := h.store.CompanySignInSettingsStore().GetCompanySignIn(t.Context())
	if err != nil {
		t.Fatalf("read sealed company sign-in settings: %v", err)
	}
	return got
}

// wantCompanySwitch asserts one provider enable switch's rendered state.
func wantCompanySwitch(t *testing.T, body, name string, checked bool) {
	t.Helper()
	want := `<input type="checkbox" id="` + name + `" name="` + name + `" value="1" aria-labelledby="`
	if !strings.Contains(body, want) {
		t.Fatalf("the section must render the %s switch, got: %.1200s", name, body)
	}
	field := body[strings.Index(body, want):]
	field = field[:strings.Index(field, ">")+1]
	if got := strings.Contains(field, " checked"); got != checked {
		t.Errorf("%s rendered checked = %v, want %v (%s)", name, got, checked, field)
	}
}

// TestSettingsSecurityRendersCompanySignInCards proves the read half for the
// stored configuration an administrator saved: the shared TKT address, the two
// provider cards with the console field names, both redirect URIs built from the
// shared address rather than the request host, the write-only secret input, and
// the new-account radio pair. The password sign-in switch is part of the
// Passwords card and carries the form association of this configuration.
func TestSettingsSecurityRendersCompanySignInCards(t *testing.T) {
	h := newHarness(t)
	companySignInStore(t, h, companySignInIn(application.NewAccountsRequester))

	body := sectionGet(t, h, "/settings/security")
	for _, want := range []string{
		`<form class="settings-company-form" id="company-signin-form" method="post" action="/settings/security/company" data-settings-form>`,
		`Company sign-in`,
		`TKT address`,
		`name="company_signin_address" value="` + companyAddress + `"`,
		`Google Workspace`,
		`Microsoft Entra`,
		`name="google_client_id" value="google-client-id"`,
		`name="google_client_secret" value="" autocomplete="new-password"`,
		`name="google_domains"`,
		`example.com`,
		`value="` + companyAddress + `/auth/google/callback"`,
		`value="` + companyAddress + `/auth/microsoft/callback"`,
		`name="microsoft_tenant_id" value="` + companyTenant + `"`,
		`name="microsoft_client_id" value="microsoft-client-id"`,
		`name="microsoft_client_secret" value="" autocomplete="new-password"`,
		`Client ID`,
		`Client secret`,
		`Allowed domains`,
		`Redirect URI`,
		`Directory (tenant) ID`,
		`New accounts`,
		`Create the account as a requester`,
		`value="requester" checked`,
		`Save company sign-in`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /settings/security must contain %q, got: %.1800s", want, body)
		}
	}

	// The redirect URI is built from the configured public address: the request
	// host is never a source (S11).
	if strings.Contains(body, `value="https://example.com/auth/`) {
		t.Errorf("the redirect URI must not come from the request host, got: %.1800s", body)
	}

	// Both saved client secrets stay write-only: the section reports that one is
	// stored and never renders the secret or its ciphertext.
	if got := strings.Count(body, `<span class="chip">Saved</span>`); got != 2 {
		t.Errorf("the section renders %d saved-secret indicators, want one per provider (2)", got)
	}
	for _, secret := range []string{"google-client-secret", "microsoft-client-secret"} {
		if strings.Contains(body, secret) {
			t.Errorf("the section rendered the stored client secret %q", secret)
		}
	}
	sealed := companySealedSecrets(t, h)
	for name, value := range map[string]string{"google": sealed.Google.SealedSecret, "microsoft": sealed.Microsoft.SealedSecret} {
		if value == "" {
			t.Fatalf("the %s client secret was not sealed, so the fixture proves nothing", name)
		}
		if strings.Contains(body, value) {
			t.Errorf("the section rendered the sealed %s client secret", name)
		}
	}

	// The password sign-in switch sits at the end of the Company sign-in group
	// (design 02) and belongs to the company form, which owns that configuration.
	wantCompanyChangeSwitch := `<input type="checkbox" id="password_signin_enabled" name="password_signin_enabled" value="1" form="company-signin-form" aria-labelledby="password-signin-label" checked>`
	if !strings.Contains(body, wantCompanyChangeSwitch) {
		t.Errorf("the Passwords card must render the password sign-in switch, got: %.1800s", body)
	}
	if strings.Index(body, `id="password_signin_enabled"`) < strings.Index(body, `id="company-signin-form"`) {
		t.Errorf("the password sign-in switch must render inside the company sign-in form it belongs to")
	}
	assertNoWhitespaceViolations(t, body)
}

// TestSettingsSecurityOffersCopyForEachRedirectURI proves the approved control
// around each redirect URI: the URI is a readonly field and a plain button
// copies it. The button names the field it copies, carries the translated copy
// and copied labels the accessible status announces, and holds no form field
// name, so it can never submit or save the configuration.
func TestSettingsSecurityOffersCopyForEachRedirectURI(t *testing.T) {
	h := newHarness(t)
	companySignInStore(t, h, companySignInIn(application.NewAccountsRequester))

	body := sectionGet(t, h, "/settings/security")
	if got := strings.Count(body, `class="copy-field"`); got != 2 {
		t.Errorf("the section renders %d copy fields, want one per redirect URI (2)", got)
	}
	if got := strings.Count(body, `<span class="visually-hidden" role="status" aria-live="polite" data-copy-status></span>`); got != 2 {
		t.Errorf("the section renders %d accessible copy status regions, want one per copy control (2)", got)
	}
	for _, tc := range []struct {
		provider string
		fieldID  string
		uri      string
	}{
		{provider: "google", fieldID: "google_redirect_uri", uri: companyAddress + "/auth/google/callback"},
		{provider: "microsoft", fieldID: "microsoft_redirect_uri", uri: companyAddress + "/auth/microsoft/callback"},
	} {
		wantInput := `<input type="text" id="` + tc.fieldID + `" class="settings-readonly" value="` + tc.uri + `" readonly spellcheck="false">`
		if !strings.Contains(body, wantInput) {
			t.Errorf("the %s redirect URI must stay a readonly field carrying the configured address, got: %.1800s", tc.provider, body)
		}
		wantButton := `<button type="button" class="btn" data-copy-target="` + tc.fieldID + `" data-copied-label="Copied">Copy</button>`
		if !strings.Contains(body, wantButton) {
			t.Errorf("the %s redirect URI must offer the approved copy control %q, got: %.1800s", tc.provider, wantButton, body)
		}
		// A copy control is not a form field: it carries no name, so it can
		// never be submitted with the configuration.
		marker := strings.Index(body, `data-copy-target="`+tc.fieldID+`"`)
		if marker >= 0 {
			start := strings.LastIndex(body[:marker], "<button")
			end := marker + strings.Index(body[marker:], "</button>")
			if button := body[start:end]; strings.Contains(button, `name=`) {
				t.Errorf("the %s copy control must not carry a form field name: %s", tc.provider, button)
			}
		}
	}
	assertNoWhitespaceViolations(t, body)
}

// TestSettingsSecuritySavesCompanySignIn proves the write half: one submission
// stores the address, the new-account policy, the password switch and both
// providers as one configuration, both client secrets are sealed and open
// through the service, the password policy is untouched, and the saved state
// renders back.
func TestSettingsSecuritySavesCompanySignIn(t *testing.T) {
	h := newHarness(t)

	rec := h.postForm(t, "/settings/security/company", companySignInForm(func(form url.Values) {
		form.Set("company_signin_new_accounts", "pending")
		form.Set("microsoft_enabled", "1")
		form.Set("microsoft_tenant_id", strings.ToUpper(companyTenant))
		form.Set("microsoft_client_id", "microsoft-client-id")
		form.Set("microsoft_client_secret", "microsoft-client-secret")
	}), false)

	wantRedirect(t, rec, http.StatusSeeOther, "/settings/security")

	stored := storedCompanySignIn(t, h)
	if stored.Address != companyAddress {
		t.Errorf("stored address = %q, want %q", stored.Address, companyAddress)
	}
	if stored.NewAccounts != application.NewAccountsPending {
		t.Errorf("stored new-account policy = %q, want %q", stored.NewAccounts, application.NewAccountsPending)
	}
	if !stored.PasswordSignInEnabled {
		t.Errorf("stored password sign-in = false, want true")
	}
	if !stored.Google.Enabled || stored.Google.ClientID != "google-client-id" {
		t.Errorf("stored Google = %+v, want it enabled with its client ID", stored.Google)
	}
	if want := []string{"example.com", "example-support.com"}; !reflect.DeepEqual(stored.Google.Domains, want) {
		t.Errorf("stored Google domains = %v, want %v", stored.Google.Domains, want)
	}
	if !stored.Google.SecretSet || !stored.Microsoft.SecretSet {
		t.Errorf("stored secrets = google set %v, microsoft set %v, want both saved", stored.Google.SecretSet, stored.Microsoft.SecretSet)
	}
	if stored.Microsoft.TenantID != companyTenant {
		t.Errorf("stored Microsoft tenant = %q, want the lowercased %q", stored.Microsoft.TenantID, companyTenant)
	}

	// Both secrets open through the service, which proves they were sealed rather
	// than stored as plain text.
	for provider, want := range map[application.CompanyProvider]string{
		application.ProviderGoogle:    "google-client-secret",
		application.ProviderMicrosoft: "microsoft-client-secret",
	} {
		secret, err := h.companySettings.ClientSecret(t.Context(), provider)
		if err != nil {
			t.Fatalf("open the %s client secret: %v", provider, err)
		}
		if secret != want {
			t.Errorf("%s client secret = %q, want %q", provider, secret, want)
		}
	}

	// The Passwords card's own policy is a separate form and stays untouched.
	if got, want := storedPasswordSettings(t, h), application.DefaultPasswordSettings(); got != want {
		t.Errorf("a company sign-in save changed the password policy: %+v, want %+v", got, want)
	}

	body := sectionGet(t, h, "/settings/security")
	for _, want := range []string{
		`name="company_signin_address" value="` + companyAddress + `"`,
		`value="pending" checked`,
		`name="microsoft_tenant_id" value="` + companyTenant + `"`,
		`example-support.com`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the saved configuration must render back %q, got: %.1800s", want, body)
		}
	}
	wantCompanySwitch(t, body, "google_enabled", true)
	wantCompanySwitch(t, body, "microsoft_enabled", true)
	for _, secret := range []string{"google-client-secret", "microsoft-client-secret"} {
		if strings.Contains(body, secret) {
			t.Errorf("the section rendered the stored client secret %q", secret)
		}
	}
}

// TestSettingsSecurityCompanySignInRefusesInvalidSubmissions proves every
// refusal is the service's own catalogued sentence, rendered locally with 422,
// that a rejected submission echoes its non-secret fields, and that nothing is
// stored. The submitted client secret never comes back in the page.
func TestSettingsSecurityCompanySignInRefusesInvalidSubmissions(t *testing.T) {
	cases := []struct {
		name    string
		mod     func(url.Values)
		message string
	}{
		{
			name: "domain list",
			mod: func(form url.Values) {
				form.Set("google_domains", "not a domain")
			},
			message: "enter at least one domain, one per line, such as example.com",
		},
		{
			name: "google client id",
			mod: func(form url.Values) {
				form.Set("google_client_id", "")
			},
			message: "enter the Google Client ID",
		},
		{
			name: "google client secret",
			mod: func(form url.Values) {
				form.Set("google_client_secret", "")
			},
			message: "enter the Google Client secret",
		},
		{
			name: "microsoft tenant",
			mod: func(form url.Values) {
				form.Set("microsoft_enabled", "1")
				form.Set("microsoft_tenant_id", "common")
				form.Set("microsoft_client_id", "microsoft-client-id")
				form.Set("microsoft_client_secret", "microsoft-client-secret")
			},
			message: "enter the Directory (tenant) ID, a GUID such as 0f8fad5b-d9cb-469f-a165-70867728950e",
		},
		{
			name: "password sign-in without a provider",
			mod: func(form url.Values) {
				form.Del("password_signin_enabled")
				form.Del("google_enabled")
				form.Del("google_client_id")
				form.Del("google_client_secret")
				form.Del("google_domains")
				form.Set("google_domains", "")
			},
			message: "turn on Google or Microsoft sign-in before turning password sign-in off",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)

			rec := h.postForm(t, "/settings/security/company", companySignInForm(tc.mod), false)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("POST /settings/security/company = %d, want 422: %.1200s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if !strings.Contains(body, tc.message) {
				t.Errorf("the refusal must carry the service's catalogued sentence %q, got: %.1800s", tc.message, body)
			}
			if !strings.Contains(body, `role="alert"`) {
				t.Errorf("the refusal must render the local error banner, got: %.1800s", body)
			}
			// A rejected submission keeps the non-secret values the administrator
			// typed; the client secret is never one of them.
			if !strings.Contains(body, `name="company_signin_address" value="`+companyAddress+`"`) {
				t.Errorf("the refusal must echo the submitted TKT address, got: %.1800s", body)
			}
			if strings.Contains(body, "google-client-secret") || strings.Contains(body, "microsoft-client-secret") {
				t.Errorf("the refusal must never render a submitted client secret")
			}
			if got, want := storedCompanySignIn(t, h), application.DefaultCompanySignInSettings(); !reflect.DeepEqual(got, want) {
				t.Errorf("a refused submission stored a configuration: %+v, want %+v", got, want)
			}
			assertNoWhitespaceViolations(t, body)
		})
	}
}

// TestSettingsSecurityCompanySignInRefusalKeepsTheSavedConfiguration proves the
// no-partial-write rule against a real saved configuration: a rejected second
// save leaves the address, providers and sealed secrets exactly as they were,
// and the page still echoes what the administrator submitted.
func TestSettingsSecurityCompanySignInRefusalKeepsTheSavedConfiguration(t *testing.T) {
	h := newHarness(t)
	companySignInStore(t, h, companySignInIn(application.NewAccountsRequester))
	before := companySealedSecrets(t, h)

	rec := h.postForm(t, "/settings/security/company", companySignInForm(func(form url.Values) {
		form.Set("company_signin_address", "https://other.example.com")
		form.Set("google_domains", "not a domain")
	}), false)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST /settings/security/company = %d, want 422: %.1200s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "enter at least one domain") {
		t.Errorf("the refusal must carry the service's sentence, got: %.1800s", body)
	}
	if !strings.Contains(body, `name="company_signin_address" value="https://other.example.com"`) {
		t.Errorf("the refusal must echo the submitted address, got: %.1800s", body)
	}

	after := companySealedSecrets(t, h)
	if !reflect.DeepEqual(after, before) {
		t.Errorf("a refused save changed the stored configuration:\n got %+v\nwant %+v", after, before)
	}
	if reflect.DeepEqual(after, application.DefaultCompanySignInSettings()) {
		t.Errorf("the fixture did not store a configuration, so preservation proves nothing")
	}
}

// TestSettingsSecurityCompanySignInKeepsSavedSecretWhenLeftBlank proves the
// secret is write-only: a later save that leaves the field blank keeps the saved
// secret while the other fields change, and only a non-blank value replaces it.
func TestSettingsSecurityCompanySignInKeepsSavedSecretWhenLeftBlank(t *testing.T) {
	h := newHarness(t)
	companySignInStore(t, h, companySignInIn(application.NewAccountsRequester))

	rec := h.postForm(t, "/settings/security/company", companySignInForm(func(form url.Values) {
		form.Set("google_client_id", "rotated-google-client-id")
		form.Set("google_client_secret", "")
		form.Set("microsoft_enabled", "1")
		form.Set("microsoft_tenant_id", companyTenant)
		form.Set("microsoft_client_id", "microsoft-client-id")
		form.Set("microsoft_client_secret", "")
	}), false)
	wantRedirect(t, rec, http.StatusSeeOther, "/settings/security")

	stored := storedCompanySignIn(t, h)
	if stored.Google.ClientID != "rotated-google-client-id" {
		t.Errorf("stored Google client ID = %q, want the rotated one", stored.Google.ClientID)
	}
	if !stored.Google.SecretSet || !stored.Microsoft.SecretSet {
		t.Fatalf("a blank secret field dropped a saved secret: google set %v, microsoft set %v", stored.Google.SecretSet, stored.Microsoft.SecretSet)
	}
	for provider, want := range map[application.CompanyProvider]string{
		application.ProviderGoogle:    "google-client-secret",
		application.ProviderMicrosoft: "microsoft-client-secret",
	} {
		secret, err := h.companySettings.ClientSecret(t.Context(), provider)
		if err != nil {
			t.Fatalf("open the %s client secret: %v", provider, err)
		}
		if secret != want {
			t.Errorf("%s client secret = %q, want the originally saved %q", provider, secret, want)
		}
	}

	// A non-blank value replaces the saved secret.
	rec = h.postForm(t, "/settings/security/company", companySignInForm(func(form url.Values) {
		form.Set("google_client_secret", "replacement-google-client-secret")
		form.Set("microsoft_enabled", "1")
		form.Set("microsoft_tenant_id", companyTenant)
		form.Set("microsoft_client_id", "microsoft-client-id")
		form.Set("microsoft_client_secret", "microsoft-client-secret")
	}), false)
	wantRedirect(t, rec, http.StatusSeeOther, "/settings/security")
	if secret, err := h.companySettings.ClientSecret(t.Context(), application.ProviderGoogle); err != nil || secret != "replacement-google-client-secret" {
		t.Errorf("the replacement Google client secret was not stored: %q, %v", secret, err)
	}

	body := sectionGet(t, h, "/settings/security")
	for _, secret := range []string{"google-client-secret", "replacement-google-client-secret", "microsoft-client-secret"} {
		if strings.Contains(body, secret) {
			t.Errorf("the section rendered a client secret %q", secret)
		}
	}
}

// TestSettingsSecurityCompanySignInIsGatedOnCapManageSettings proves the gate on
// the new write route: an agent and a requester are refused and store nothing.
func TestSettingsSecurityCompanySignInIsGatedOnCapManageSettings(t *testing.T) {
	h := newHarness(t)

	for _, role := range []domain.Role{domain.RoleAgent, domain.RoleUser} {
		t.Run(string(role), func(t *testing.T) {
			actor := seedUserRole(t, h.store, "Company "+string(role), "company-"+string(role)+"@tkt.test", role)
			session := seedSession(t, h.store, actor.ID)

			rec := h.postFormAs(t, "/settings/security/company", companySignInForm(nil), session.ID)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s POST /settings/security/company = %d, want 403", role, rec.Code)
			}
			if got, want := storedCompanySignIn(t, h), application.DefaultCompanySignInSettings(); !reflect.DeepEqual(got, want) {
				t.Errorf("%s refused write stored a configuration: %+v, want %+v", role, got, want)
			}
		})
	}
}

// TestSettingsSecurityCompanySignInRendersInSpanish proves S13 for this slice:
// the section's own copy reads neutral Spanish while the provider-integration
// field names keep the exact English spelling of the Google and Entra consoles
// in both catalogues, and a refusal is localized.
func TestSettingsSecurityCompanySignInRendersInSpanish(t *testing.T) {
	h := newHarness(t)
	root, rootSession := seedRoot(t, h)
	if err := h.settings.SetInterfaceLanguage(t.Context(), *root, application.InterfaceLanguageSpanish); err != nil {
		t.Fatalf("switch the instance to Spanish: %v", err)
	}

	body := getAs(t, h, "/settings/security", rootSession.ID).Body.String()
	for _, want := range []string{
		"Inicio de sesión de la empresa",
		"Dirección de TKT",
		"Cuentas nuevas",
		"Crear la cuenta como solicitante",
		"Guardar el inicio de sesión de la empresa",
		"Iniciar sesión con contraseña",
		"Copiar",
		"Copiado",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Spanish company sign-in section must render %q, got: %.1800s", want, body)
		}
	}
	// The console field names are integration terms: English in both catalogues.
	for _, want := range []string{"Client ID", "Client secret", "Redirect URI", "Directory (tenant) ID"} {
		if !strings.Contains(body, want) {
			t.Errorf("Spanish company sign-in section must keep the console field name %q, got: %.1800s", want, body)
		}
	}

	rec := h.postFormAs(t, "/settings/security/company", companySignInForm(func(form url.Values) {
		form.Set("google_domains", "not a domain")
	}), rootSession.ID)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("Spanish POST /settings/security/company = %d, want 422: %.1200s", rec.Code, rec.Body.String())
	}
	refusal := rec.Body.String()
	if want := "ingresa al menos un dominio, uno por línea, como example.com"; !strings.Contains(refusal, want) {
		t.Errorf("the Spanish refusal must read %q, got: %.1800s", want, refusal)
	}
	if !strings.Contains(refusal, "Client ID") {
		t.Errorf("the Spanish refusal must keep the console field name, got: %.1800s", refusal)
	}
}

// TestSettingsSecurityCompanySavePreservesThePasswordPolicy proves the two card
// forms are independent: saving the company configuration leaves the password
// policy standing, and saving the password policy leaves the company
// configuration — providers, address, policy and sealed secrets — untouched.
func TestSettingsSecurityCompanySavePreservesThePasswordPolicy(t *testing.T) {
	h := newHarness(t)

	rec := h.postForm(t, "/settings/security", securityForm(func(form url.Values) {
		form.Set("password_min_length", "18")
		form.Del("password_recovery_enabled")
	}), false)
	wantRedirect(t, rec, http.StatusSeeOther, "/settings/security")
	policy := storedPasswordSettings(t, h)

	rec = h.postForm(t, "/settings/security/company", companySignInForm(nil), false)
	wantRedirect(t, rec, http.StatusSeeOther, "/settings/security")
	if got := storedPasswordSettings(t, h); got != policy {
		t.Errorf("a company sign-in save changed the password policy: %+v, want %+v", got, policy)
	}

	company := companySealedSecrets(t, h)
	rec = h.postForm(t, "/settings/security", securityForm(nil), false)
	wantRedirect(t, rec, http.StatusSeeOther, "/settings/security")
	if got := companySealedSecrets(t, h); !reflect.DeepEqual(got, company) {
		t.Errorf("a password policy save changed the company sign-in configuration:\n got %+v\nwant %+v", got, company)
	}
}
