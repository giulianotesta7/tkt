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

// seedInstanceAddress arranges configuration through the atomic audited store
// path; authorization is exercised separately through real HTTP requests.
func seedInstanceAddress(t *testing.T, h *harness, address string) {
	t.Helper()
	if err := h.store.InstanceSettingsStore().SetInstanceAddress(t.Context(), h.admin.ID, address, h.clock.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestInstanceValidationAuditAndConsumerWrites(t *testing.T) {
	h := newHarness(t)
	_, root := seedRoot(t, h)
	post := func(address string) int {
		return h.postFormAs(t, "/settings/instance", url.Values{"instance_address": {address}}, root.ID).Code
	}
	address := "https://tkt.example.com/help"
	if got := post(address); got != http.StatusSeeOther {
		t.Fatalf("save = %d", got)
	}
	before, err := h.store.InstanceSettingsStore().ListInstanceAddressEvents(t.Context())
	if err != nil || len(before) != 1 || before[0].Address != address || before[0].CreatedAt.Unix() != fixedNow.Unix() {
		t.Fatalf("audit = %+v, %v", before, err)
	}
	for _, input := range []string{"ftp://tkt.example.com", "https://", "https://user:pass@tkt.example.com", "https://tkt.example.com/?q=1", "https://tkt.example.com/#x", "https://tkt.example.com/auth/google/callback", "https://tkt.example.com/help/auth/microsoft/callback"} {
		if got := post(input); got != http.StatusUnprocessableEntity {
			t.Errorf("save %q = %d, want 422", input, got)
		}
	}
	for _, role := range []domain.Role{domain.RoleAdmin, domain.RoleAgent, domain.RoleUser} {
		actor := seedUserRole(t, h.store, string(role), string(role)+"-instance@example.com", role)
		session := seedSession(t, h.store, actor.ID)
		if got := getAs(t, h, "/settings/instance", session.ID).Code; got != http.StatusForbidden {
			t.Errorf("%s GET = %d", role, got)
		}
		if got := h.postFormAs(t, "/settings/instance", url.Values{"instance_address": {"https://other.example.com"}}, session.ID).Code; got != http.StatusForbidden {
			t.Errorf("%s POST = %d", role, got)
		}
	}
	// A stale email form cannot edit the address; its original SMTP behavior survives.
	wantRedirect(t, h.postFormAs(t, "/settings/email", emailSettingsForm(nil), root.ID), http.StatusSeeOther, "/settings/email")
	cfg, ok, err := h.emailSettings.DeliveryConfig(t.Context())
	if err != nil || !ok || cfg.BaseURL != address || cfg.SMTP.Password != "smtp-s3cret" {
		t.Fatalf("delivery = %+v, %v, %v", cfg, ok, err)
	}
	if got := post(""); got != http.StatusUnprocessableEntity {
		t.Fatalf("clear with email on = %d", got)
	}
	// Admin can still configure providers but cannot replace the canonical address.
	wantRedirect(t, h.postForm(t, "/settings/security/company", companySignInForm(nil), false), http.StatusSeeOther, "/settings/security")
	company := storedCompanySignIn(t, h)
	if company.Address != address || !company.Google.Enabled || !company.Google.SecretSet || company.RedirectURI(application.ProviderGoogle) != address+"/auth/google/callback" {
		t.Fatalf("company = %+v", company)
	}
	current, err := h.store.InstanceSettingsStore().GetInstanceAddress(t.Context())
	if err != nil || current.Address != address {
		t.Fatalf("stored address = %+v, %v", current, err)
	}
	after, err := h.store.InstanceSettingsStore().ListInstanceAddressEvents(t.Context())
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("rejections/consumer writes changed audit: %+v, %v", after, err)
	}
}

func TestInstanceMigrationNoticeAndSpanish(t *testing.T) {
	h := newHarness(t)
	root, session := seedRoot(t, h)
	seedInstanceAddress(t, h, "https://email.example/help")
	// Migration-only evidence has deliberately no application write endpoint.
	if _, err := h.rawDB(t).Exec(`INSERT INTO settings (key, value) VALUES ('instance_address_company_conflict', 'https://company.example/help')`); err != nil {
		t.Fatal(err)
	}
	for _, language := range []string{"en", "es"} {
		if err := h.settings.SetInterfaceLanguage(t.Context(), *root, language); err != nil {
			t.Fatal(err)
		}
		rec := getAs(t, h, "/settings/instance", session.ID)
		body := rec.Body.String()
		for _, want := range []string{`role="alert"`, "https://company.example/help", "https://email.example/help", "Google", "Microsoft"} {
			if rec.Code != http.StatusOK || !strings.Contains(body, want) {
				t.Errorf("%s notice missing %q (status %d)", language, want, rec.Code)
			}
		}
		if language == "es" && !strings.Contains(body, "Guardar la dirección de TKT") {
			t.Error("Instance section not localized")
		}
		assertNoWhitespaceViolations(t, body)
	}
	if body := h.get(t, "/settings/organization", false).Body.String(); strings.Contains(body, "https://company.example/help") {
		t.Error("migration notice leaked outside root Instance section")
	}
}

func TestInstanceMissingAddressLinksToSettings(t *testing.T) {
	h := newHarness(t)
	_, root := seedRoot(t, h)
	for path, form := range map[string]url.Values{"/settings/email": emailSettingsForm(nil), "/settings/security/company": companySignInForm(nil)} {
		rec := h.postFormAs(t, path, form, root.ID)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "set the TKT address in Settings") || !strings.Contains(rec.Body.String(), `href="/settings/instance"`) {
			t.Fatalf("%s = %d, missing address guidance", path, rec.Code)
		}
	}
	email, err := h.store.EmailSettingsStore().GetEmailSettings(t.Context())
	if err != nil || email.Enabled {
		t.Fatalf("rejected email persisted: %+v, %v", email, err)
	}
	if company := storedCompanySignIn(t, h); company.Google.Enabled || company.Microsoft.Enabled {
		t.Fatal("rejected provider enable persisted")
	}
}

func TestInstanceSectionAndAddress(t *testing.T) {
	h := newHarness(t)
	_, root := seedRoot(t, h)
	rec := getAs(t, h, "/settings/instance", root.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("root GET instance = %d, want 200", rec.Code)
	}
	for _, want := range []string{`action="/settings/instance"`, `name="instance_address"`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("missing %s", want)
		}
	}
	if rec := h.get(t, "/settings/instance", false); rec.Code != http.StatusForbidden {
		t.Errorf("admin GET = %d, want 403", rec.Code)
	}
	if strings.Contains(sectionGet(t, h, "/settings/organization"), `href="/settings/instance"`) {
		t.Error("admin sees Instance navigation")
	}
	form := url.Values{"instance_address": {"https://tkt.example.com/help"}}
	if rec := h.postForm(t, "/settings/instance", form, false); rec.Code != http.StatusForbidden {
		t.Fatalf("admin POST = %d, want 403", rec.Code)
	}
	wantRedirect(t, h.postFormAs(t, "/settings/instance", form, root.ID), http.StatusSeeOther, "/settings/instance")
	email, err := h.store.EmailSettingsStore().GetEmailSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	company, err := h.companySettings.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if email.BaseURL != form.Get("instance_address") || company.Address != email.BaseURL {
		t.Fatalf("consumers disagree: email=%q company=%q", email.BaseURL, company.Address)
	}
	for _, path := range []string{"/settings/email", "/settings/security"} {
		body := getAs(t, h, path, root.ID).Body.String()
		if !strings.Contains(body, email.BaseURL) || !strings.Contains(body, `href="/settings/instance"`) {
			t.Errorf("%s missing canonical address/link", path)
		}
		if strings.Contains(body, `name="email_base_url"`) || strings.Contains(body, `name="company_signin_address"`) {
			t.Errorf("%s still edits legacy address", path)
		}
	}
}
