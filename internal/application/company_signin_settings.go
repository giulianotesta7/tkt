package application

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Company sign-in (issue #419): employees sign in with their Google Workspace
// or Microsoft Entra account. An administrator configures both providers in
// the Security settings; the configuration lives in the database, with each
// client secret sealed like the SMTP password, so the container contract gains
// no environment variable.

// CompanyProvider names one supported identity provider. Generic OpenID
// Connect providers are out of scope: the list is closed.
type CompanyProvider string

const (
	ProviderGoogle    CompanyProvider = "google"
	ProviderMicrosoft CompanyProvider = "microsoft"
)

// CompanyProviders lists the supported providers in display order.
var CompanyProviders = []CompanyProvider{ProviderGoogle, ProviderMicrosoft}

// ParseCompanyProvider answers the provider a path segment names, refusing
// anything outside the closed list.
func ParseCompanyProvider(s string) (CompanyProvider, bool) {
	p := CompanyProvider(s)
	return p, slices.Contains(CompanyProviders, p)
}

// NewAccountPolicy decides what a first company sign-in creates for someone
// who has no account yet.
type NewAccountPolicy string

const (
	// NewAccountsRequester creates an active requester (the default).
	NewAccountsRequester NewAccountPolicy = "requester"
	// NewAccountsPending creates an account that waits for an administrator's
	// approval and cannot sign in until then.
	NewAccountsPending NewAccountPolicy = "pending"
)

// GoogleSignIn is the stored Google Workspace configuration. Accounts are
// restricted to Domains through the hd claim plus a verified email.
type GoogleSignIn struct {
	Enabled  bool
	ClientID string
	// SealedSecret is set only on the store side of the port.
	SealedSecret string
	// SecretSet reports, on the read side, whether a secret is stored.
	SecretSet bool
	Domains   []string
}

// MicrosoftSignIn is the stored Microsoft Entra configuration. Accounts are
// restricted to the one organization TenantID names.
type MicrosoftSignIn struct {
	Enabled  bool
	TenantID string
	ClientID string
	// SealedSecret is set only on the store side of the port.
	SealedSecret string
	// SecretSet reports, on the read side, whether a secret is stored.
	SecretSet bool
}

// CompanySignInSettings is the whole company sign-in configuration, saved as
// one unit.
type CompanySignInSettings struct {
	// Address is the public address of this instance; the provider redirect
	// URIs are built from it, never from a request's Host header.
	Address     string
	NewAccounts NewAccountPolicy
	// PasswordSignInEnabled lets everyone sign in with a password. When it is
	// off, only root still can (the break-glass for a failing provider).
	PasswordSignInEnabled bool
	Google                GoogleSignIn
	Microsoft             MicrosoftSignIn
}

// DefaultCompanySignInSettings is the configuration of an instance that never
// saved it: no provider, password sign-in on, new accounts as requesters.
func DefaultCompanySignInSettings() CompanySignInSettings {
	return CompanySignInSettings{
		NewAccounts:           NewAccountsRequester,
		PasswordSignInEnabled: true,
	}
}

// Configured reports whether a provider is on and complete enough to start a
// sign-in, so the sign-in page offers only buttons that can work.
func (c CompanySignInSettings) Configured(p CompanyProvider) bool {
	if c.Address == "" {
		return false
	}
	switch p {
	case ProviderGoogle:
		g := c.Google
		return g.Enabled && g.ClientID != "" && (g.SealedSecret != "" || g.SecretSet) && len(g.Domains) > 0
	case ProviderMicrosoft:
		m := c.Microsoft
		return m.Enabled && m.TenantID != "" && m.ClientID != "" && (m.SealedSecret != "" || m.SecretSet)
	}
	return false
}

// RedirectURI is the callback address a provider sends the browser back to.
func (c CompanySignInSettings) RedirectURI(p CompanyProvider) string {
	return c.Address + CompanySignInPathPrefix + string(p) + "/callback"
}

// CompanySignInPathPrefix roots the sign-in routes. It sits outside /login/ on
// purpose: that branch sends a signed-in browser to its tickets, which would
// swallow a callback.
const CompanySignInPathPrefix = "/auth/"

// GoogleSignInInput and MicrosoftSignInInput are one submission of a provider
// card. A blank secret keeps the stored one.
type GoogleSignInInput struct {
	Enabled      bool
	ClientID     string
	ClientSecret string
	// Domains is the textarea value: one domain per line.
	Domains string
}

type MicrosoftSignInInput struct {
	Enabled      bool
	TenantID     string
	ClientID     string
	ClientSecret string
}

// CompanySignInInput is one submission of the company sign-in settings.
type CompanySignInInput struct {
	Address               string
	NewAccounts           NewAccountPolicy
	PasswordSignInEnabled bool
	Google                GoogleSignInInput
	Microsoft             MicrosoftSignInInput
}

// CompanySignInSettingsService implements the company sign-in configuration.
// Reads are ungated because the sign-in page asks which providers to offer;
// the write is admin/root only through CapManageSettings.
type CompanySignInSettingsService struct {
	store     CompanySignInSettingsStore
	google    SecretSealer
	microsoft SecretSealer
}

// NewCompanySignInSettingsService wires the configuration against its port
// and one sealer per provider, so a secret sealed for one provider never opens
// as the other's.
func NewCompanySignInSettingsService(store CompanySignInSettingsStore, google, microsoft SecretSealer) *CompanySignInSettingsService {
	return &CompanySignInSettingsService{store: store, google: google, microsoft: microsoft}
}

// Get answers the stored configuration with the secrets reduced to whether
// one is stored: no sealed value ever leaves the service.
func (s *CompanySignInSettingsService) Get(ctx context.Context) (CompanySignInSettings, error) {
	stored, err := s.store.GetCompanySignIn(ctx)
	if err != nil {
		return CompanySignInSettings{}, err
	}
	stored.Google.SecretSet = stored.Google.SealedSecret != ""
	stored.Google.SealedSecret = ""
	stored.Microsoft.SecretSet = stored.Microsoft.SealedSecret != ""
	stored.Microsoft.SealedSecret = ""
	return stored, nil
}

// ClientSecret opens the stored client secret of one provider for the
// sign-in flow.
func (s *CompanySignInSettingsService) ClientSecret(ctx context.Context, p CompanyProvider) (string, error) {
	stored, err := s.store.GetCompanySignIn(ctx)
	if err != nil {
		return "", err
	}
	var sealed string
	var sealer SecretSealer
	switch p {
	case ProviderGoogle:
		sealed, sealer = stored.Google.SealedSecret, s.google
	case ProviderMicrosoft:
		sealed, sealer = stored.Microsoft.SealedSecret, s.microsoft
	default:
		return "", fmt.Errorf("company sign-in: unknown provider %q", p)
	}
	if sealed == "" {
		return "", fmt.Errorf("company sign-in: no client secret stored for %s", p)
	}
	plain, err := sealer.Open(sealed)
	if err != nil {
		return "", fmt.Errorf("company sign-in: open the %s client secret: %w", p, err)
	}
	return string(plain), nil
}

// Update validates one submission and writes it. Every refusal happens before
// the store is touched, so a rejected submission leaves the previous
// configuration standing. A provider that is off keeps what it had without
// being validated, so it can be paused without retyping its credentials.
func (s *CompanySignInSettingsService) Update(ctx context.Context, actor domain.User, in CompanySignInInput) error {
	if err := requireManageSettings(actor, "company sign-in settings are not permitted"); err != nil {
		return err
	}
	stored, err := s.store.GetCompanySignIn(ctx)
	if err != nil {
		return err
	}
	next := CompanySignInSettings{
		Address:               strings.TrimRight(strings.TrimSpace(in.Address), "/"),
		NewAccounts:           in.NewAccounts,
		PasswordSignInEnabled: in.PasswordSignInEnabled,
		Google: GoogleSignIn{
			Enabled:      in.Google.Enabled,
			ClientID:     strings.TrimSpace(in.Google.ClientID),
			SealedSecret: stored.Google.SealedSecret,
		},
		Microsoft: MicrosoftSignIn{
			Enabled:      in.Microsoft.Enabled,
			TenantID:     strings.ToLower(strings.TrimSpace(in.Microsoft.TenantID)),
			ClientID:     strings.TrimSpace(in.Microsoft.ClientID),
			SealedSecret: stored.Microsoft.SealedSecret,
		},
	}
	if next.NewAccounts != NewAccountsRequester && next.NewAccounts != NewAccountsPending {
		return &domain.ValidationError{Field: "company_signin_new_accounts", Message: "choose what a first sign-in creates", Key: "error.company_signin_new_accounts"}
	}
	if !next.PasswordSignInEnabled && !next.Google.Enabled && !next.Microsoft.Enabled {
		return &domain.ValidationError{Field: "password_signin_enabled", Message: "turn on Google or Microsoft sign-in before turning password sign-in off", Key: "error.password_signin_needs_provider"}
	}
	if (next.Google.Enabled || next.Microsoft.Enabled || next.Address != "") && !isPublicBaseURL(next.Address) {
		return &domain.ValidationError{Field: "company_signin_address", Message: "enter the public address of TKT, such as https://tkt.example.com", Key: "error.company_signin_address"}
	}

	// The domain list is validated whether Google is on or off: it is what the
	// administrator typed and is stored either way, so a bad line is refused
	// rather than silently dropped.
	domains, domainsOK := parseGoogleDomains(in.Google.Domains)
	googleSecret := strings.TrimSpace(in.Google.ClientSecret)
	switch {
	case !domainsOK || (next.Google.Enabled && len(domains) == 0):
		return &domain.ValidationError{Field: "google_domains", Message: "enter at least one domain, one per line, such as example.com", Key: "error.company_signin_domains"}
	case next.Google.Enabled && next.Google.ClientID == "":
		return &domain.ValidationError{Field: "google_client_id", Message: "enter the Google Client ID", Key: "error.company_signin_google_client_id"}
	case next.Google.Enabled && googleSecret == "" && next.Google.SealedSecret == "":
		return &domain.ValidationError{Field: "google_client_secret", Message: "enter the Google Client secret", Key: "error.company_signin_google_client_secret"}
	}
	next.Google.Domains = domains

	microsoftSecret := strings.TrimSpace(in.Microsoft.ClientSecret)
	if next.Microsoft.Enabled {
		switch {
		case !tenantIDPattern.MatchString(next.Microsoft.TenantID):
			return &domain.ValidationError{Field: "microsoft_tenant_id", Message: "enter the Directory (tenant) ID, a GUID such as 0f8fad5b-d9cb-469f-a165-70867728950e", Key: "error.company_signin_tenant_id"}
		case next.Microsoft.ClientID == "":
			return &domain.ValidationError{Field: "microsoft_client_id", Message: "enter the Microsoft Client ID", Key: "error.company_signin_microsoft_client_id"}
		case microsoftSecret == "" && next.Microsoft.SealedSecret == "":
			return &domain.ValidationError{Field: "microsoft_client_secret", Message: "enter the Microsoft Client secret", Key: "error.company_signin_microsoft_client_secret"}
		}
	}

	if googleSecret != "" {
		sealed, err := s.google.Seal([]byte(googleSecret))
		if err != nil {
			return err
		}
		next.Google.SealedSecret = sealed
	}
	if microsoftSecret != "" {
		sealed, err := s.microsoft.Seal([]byte(microsoftSecret))
		if err != nil {
			return err
		}
		next.Microsoft.SealedSecret = sealed
	}
	return s.store.SetCompanySignIn(ctx, next)
}

// tenantIDPattern accepts only a directory GUID. The multi-tenant authorities
// ("common", "organizations", "consumers") and domain names are refused: the
// tenant is the restriction, so it must name exactly one organization.
var tenantIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// domainPattern accepts a lowercase DNS name with at least two labels.
var domainPattern = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$`)

// parseGoogleDomains reads the one-per-line domain list: trimmed, lowercased,
// blank lines dropped, duplicates removed in first-seen order. Any line that
// is not a domain name refuses the whole list.
func parseGoogleDomains(text string) ([]string, bool) {
	var domains []string
	for line := range strings.Lines(text) {
		d := strings.ToLower(strings.TrimSpace(line))
		if d == "" {
			continue
		}
		if len(d) > 253 || !domainPattern.MatchString(d) {
			return nil, false
		}
		if !slices.Contains(domains, d) {
			domains = append(domains, d)
		}
	}
	return domains, true
}
