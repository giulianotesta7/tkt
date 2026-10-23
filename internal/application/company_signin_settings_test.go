package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Company sign-in settings (issue #419): Google Workspace and Microsoft Entra
// are configured by an administrator, the client secrets are write-only and
// sealed like the SMTP password, and the same save carries the new-account
// policy and the password sign-in switch.

// tagSealer is a reversible SecretSealer double that tags each value with its
// purpose, so a test can tell which purpose sealed a stored secret.
type tagSealer struct{ purpose string }

func (s tagSealer) Seal(plaintext []byte) (string, error) {
	return s.purpose + ":" + string(plaintext), nil
}

func (s tagSealer) Open(sealed string) ([]byte, error) {
	plain, ok := strings.CutPrefix(sealed, s.purpose+":")
	if !ok {
		return nil, errors.New("sealed for another purpose")
	}
	return []byte(plain), nil
}

func newCompanySignInSettingsService() (*application.CompanySignInSettingsService, *fakeCompanySignInStore) {
	store := newFakeCompanySignInStore()
	return application.NewCompanySignInSettingsService(store, tagSealer{"google"}, tagSealer{"microsoft"}), store
}

func validCompanySignInInput() application.CompanySignInInput {
	return application.CompanySignInInput{
		Address:               "https://tkt.example.com/",
		NewAccounts:           application.NewAccountsRequester,
		PasswordSignInEnabled: true,
		Google: application.GoogleSignInInput{
			Enabled:      true,
			ClientID:     " google-client ",
			ClientSecret: "g-secret",
			Domains:      " Example.com \n\nexample.org\nexample.com ",
		},
		Microsoft: application.MicrosoftSignInInput{
			Enabled:      true,
			TenantID:     "0F8FAD5B-D9CB-469F-A165-70867728950E",
			ClientID:     "ms-client",
			ClientSecret: "m-secret",
		},
	}
}

func TestDefaultCompanySignInSettingsOfferOnlyPasswords(t *testing.T) {
	got := application.DefaultCompanySignInSettings()
	if got.Google.Enabled || got.Microsoft.Enabled {
		t.Fatalf("a fresh instance enables a provider: %+v", got)
	}
	if !got.PasswordSignInEnabled {
		t.Fatal("a fresh instance turns password sign-in off")
	}
	if got.NewAccounts != application.NewAccountsRequester {
		t.Fatalf("NewAccounts = %q, want requester by default", got.NewAccounts)
	}
}

func TestUpdateCompanySignInNormalizesAndSealsEachSecretForItsProvider(t *testing.T) {
	svc, store := newCompanySignInSettingsService()
	if err := svc.Update(context.Background(), domain.User{ID: 1, Role: domain.RoleAdmin}, validCompanySignInInput()); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got := store.settings
	if got.Address != "https://tkt.example.com" {
		t.Errorf("Address = %q, want the trailing slash trimmed", got.Address)
	}
	if got.Google.ClientID != "google-client" {
		t.Errorf("Google.ClientID = %q, want trimmed", got.Google.ClientID)
	}
	if strings.Join(got.Google.Domains, ",") != "example.com,example.org" {
		t.Errorf("Google.Domains = %q, want lowercased, deduplicated, blank lines dropped", got.Google.Domains)
	}
	if got.Microsoft.TenantID != "0f8fad5b-d9cb-469f-a165-70867728950e" {
		t.Errorf("Microsoft.TenantID = %q, want the lowercase GUID", got.Microsoft.TenantID)
	}
	if got.Google.SealedSecret != "google:g-secret" || got.Microsoft.SealedSecret != "microsoft:m-secret" {
		t.Errorf("sealed secrets = %q, %q; want each sealed for its own provider",
			got.Google.SealedSecret, got.Microsoft.SealedSecret)
	}
}

func TestGetCompanySignInNeverAnswersASecret(t *testing.T) {
	svc, _ := newCompanySignInSettingsService()
	ctx := context.Background()
	if err := svc.Update(ctx, domain.User{ID: 1, Role: domain.RoleRoot}, validCompanySignInInput()); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := svc.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Google.SealedSecret != "" || got.Microsoft.SealedSecret != "" {
		t.Fatalf("Get answered sealed secrets: %+v", got)
	}
	if !got.Google.SecretSet || !got.Microsoft.SecretSet {
		t.Fatalf("Get lost the secret-is-set facts: %+v", got)
	}
}

func TestUpdateCompanySignInWithABlankSecretKeepsTheStoredOne(t *testing.T) {
	svc, store := newCompanySignInSettingsService()
	ctx := context.Background()
	admin := domain.User{ID: 1, Role: domain.RoleAdmin}
	if err := svc.Update(ctx, admin, validCompanySignInInput()); err != nil {
		t.Fatalf("Update: %v", err)
	}
	in := validCompanySignInInput()
	in.Google.ClientSecret = ""
	in.Microsoft.ClientSecret = "   "
	if err := svc.Update(ctx, admin, in); err != nil {
		t.Fatalf("second Update: %v", err)
	}
	if store.settings.Google.SealedSecret != "google:g-secret" || store.settings.Microsoft.SealedSecret != "microsoft:m-secret" {
		t.Fatalf("a blank secret replaced the stored ones: %+v", store.settings)
	}
}

func TestUpdateCompanySignInRefusesInvalidInputAndStoresNothing(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*application.CompanySignInInput)
		field  string
	}{
		{"address missing while a provider is on", func(in *application.CompanySignInInput) { in.Address = " " }, "company_signin_address"},
		{"address not a URL", func(in *application.CompanySignInInput) { in.Address = "tkt.example.com" }, "company_signin_address"},
		{"address with a query", func(in *application.CompanySignInInput) { in.Address = "https://tkt.example.com/?a=1" }, "company_signin_address"},
		{"unknown new-account policy", func(in *application.CompanySignInInput) { in.NewAccounts = "admin" }, "company_signin_new_accounts"},
		{"google without client id", func(in *application.CompanySignInInput) { in.Google.ClientID = "" }, "google_client_id"},
		{"google without secret", func(in *application.CompanySignInInput) { in.Google.ClientSecret = "" }, "google_client_secret"},
		{"google without domains", func(in *application.CompanySignInInput) { in.Google.Domains = "\n " }, "google_domains"},
		{"google domain with a scheme", func(in *application.CompanySignInInput) { in.Google.Domains = "https://example.com" }, "google_domains"},
		{"google domain without a dot", func(in *application.CompanySignInInput) { in.Google.Domains = "localhost" }, "google_domains"},
		{"google domain with an at sign", func(in *application.CompanySignInInput) { in.Google.Domains = "ana@example.com" }, "google_domains"},
		{"microsoft multi-tenant authority", func(in *application.CompanySignInInput) { in.Microsoft.TenantID = "common" }, "microsoft_tenant_id"},
		{"microsoft organizations authority", func(in *application.CompanySignInInput) { in.Microsoft.TenantID = "organizations" }, "microsoft_tenant_id"},
		{"microsoft tenant not a GUID", func(in *application.CompanySignInInput) { in.Microsoft.TenantID = "contoso.onmicrosoft.com" }, "microsoft_tenant_id"},
		{"microsoft without client id", func(in *application.CompanySignInInput) { in.Microsoft.ClientID = " " }, "microsoft_client_id"},
		{"microsoft without secret", func(in *application.CompanySignInInput) { in.Microsoft.ClientSecret = "" }, "microsoft_client_secret"},
		{"password sign-in off with no provider on", func(in *application.CompanySignInInput) {
			in.PasswordSignInEnabled = false
			in.Google.Enabled = false
			in.Microsoft.Enabled = false
		}, "password_signin_enabled"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, store := newCompanySignInSettingsService()
			in := validCompanySignInInput()
			tc.mutate(&in)
			err := svc.Update(context.Background(), domain.User{ID: 1, Role: domain.RoleAdmin}, in)
			var ve *domain.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("Update err = %v, want a ValidationError", err)
			}
			if ve.Field != tc.field {
				t.Errorf("Field = %q, want %q", ve.Field, tc.field)
			}
			if ve.Key == "" {
				t.Error("the refusal carries no catalogue key")
			}
			if store.writes != 0 {
				t.Errorf("a refused submission wrote %d times", store.writes)
			}
		})
	}
}

// A provider that is off keeps whatever it had, so an administrator can pause
// one without retyping its credentials, and an off provider needs none.
func TestUpdateCompanySignInAcceptsAnIncompleteProviderThatIsOff(t *testing.T) {
	svc, store := newCompanySignInSettingsService()
	in := validCompanySignInInput()
	in.Microsoft = application.MicrosoftSignInInput{Enabled: false, TenantID: "common"}
	in.Address = ""
	in.Google.Enabled = false
	if err := svc.Update(context.Background(), domain.User{ID: 1, Role: domain.RoleAdmin}, in); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if store.settings.Microsoft.Enabled || store.settings.Google.Enabled {
		t.Fatalf("stored = %+v, want both providers off", store.settings)
	}
}

func TestUpdateCompanySignInIsAdminOrRootOnly(t *testing.T) {
	for _, role := range []domain.Role{domain.RoleUser, domain.RoleAgent} {
		svc, store := newCompanySignInSettingsService()
		err := svc.Update(context.Background(), domain.User{ID: 2, Role: role}, validCompanySignInInput())
		var fe *domain.ForbiddenError
		if !errors.As(err, &fe) {
			t.Fatalf("role %s: Update err = %v, want ForbiddenError", role, err)
		}
		if store.writes != 0 {
			t.Fatalf("role %s: a forbidden submission wrote", role)
		}
	}
}

// ClientSecret opens the stored secret for the sign-in flow, and only with the
// sealer of its own provider.
func TestCompanySignInClientSecretOpensWithItsProviderSealer(t *testing.T) {
	svc, store := newCompanySignInSettingsService()
	ctx := context.Background()
	if err := svc.Update(ctx, domain.User{ID: 1, Role: domain.RoleAdmin}, validCompanySignInInput()); err != nil {
		t.Fatalf("Update: %v", err)
	}
	secret, err := svc.ClientSecret(ctx, application.ProviderGoogle)
	if err != nil || secret != "g-secret" {
		t.Fatalf("ClientSecret(google) = %q, %v", secret, err)
	}
	store.settings.Microsoft.SealedSecret = store.settings.Google.SealedSecret
	if _, err := svc.ClientSecret(ctx, application.ProviderMicrosoft); err == nil {
		t.Fatal("a Google secret moved into the Microsoft column opened")
	}
}

// An administrator who edits the domains of a provider that is off still
// expects the edit to be kept or refused, never silently dropped.
func TestUpdateCompanySignInRefusesInvalidDomainsOfAProviderThatIsOff(t *testing.T) {
	svc, store := newCompanySignInSettingsService()
	ctx := context.Background()
	admin := domain.User{ID: 1, Role: domain.RoleAdmin}
	if err := svc.Update(ctx, admin, validCompanySignInInput()); err != nil {
		t.Fatalf("Update: %v", err)
	}
	in := validCompanySignInInput()
	in.Google.Enabled = false
	in.Google.Domains = "https://changed.example.org"
	err := svc.Update(ctx, admin, in)
	var ve *domain.ValidationError
	if !errors.As(err, &ve) || ve.Field != "google_domains" {
		t.Fatalf("Update err = %v, want a google_domains ValidationError", err)
	}
	if store.writes != 1 {
		t.Fatalf("writes = %d, want only the first save", store.writes)
	}
	if strings.Join(store.settings.Google.Domains, ",") != "example.com,example.org" || !store.settings.Google.Enabled {
		t.Fatalf("a refused save changed the stored Google settings: %+v", store.settings.Google)
	}
}

func TestCompanySignInClientSecretRefusesAnUnknownProvider(t *testing.T) {
	svc, _ := newCompanySignInSettingsService()
	ctx := context.Background()
	if err := svc.Update(ctx, domain.User{ID: 1, Role: domain.RoleAdmin}, validCompanySignInInput()); err != nil {
		t.Fatalf("Update: %v", err)
	}
	for _, p := range []application.CompanyProvider{"", "oidc", "Google"} {
		if secret, err := svc.ClientSecret(ctx, p); err == nil {
			t.Errorf("ClientSecret(%q) = %q, want a refusal", p, secret)
		}
	}
}
