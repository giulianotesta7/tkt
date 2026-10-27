package sqlite

import (
	"context"
	"strings"

	"github.com/giulianotesta7/tkt/internal/application"
)

// The settings rows of the company sign-in configuration (issue #419). No
// migration seeds them: an absent row reads back the documented default, so a
// fresh instance offers password sign-in only.
const (
	settingsKeyCompanySignInAddress     = "company_signin_address"
	settingsKeyCompanySignInNewAccounts = "company_signin_new_accounts"
	settingsKeyPasswordSignInEnabled    = "password_signin_enabled"
	settingsKeyGoogleSignInEnabled      = "google_signin_enabled"
	settingsKeyGoogleSignInClientID     = "google_signin_client_id"
	settingsKeyGoogleSignInSecret       = "google_signin_client_secret"
	settingsKeyGoogleSignInDomains      = "google_signin_domains"
	settingsKeyMicrosoftSignInEnabled   = "microsoft_signin_enabled"
	settingsKeyMicrosoftSignInTenantID  = "microsoft_signin_tenant_id"
	settingsKeyMicrosoftSignInClientID  = "microsoft_signin_client_id"
	settingsKeyMicrosoftSignInSecret    = "microsoft_signin_client_secret"
)

var companySignInKeys = []string{
	settingsKeyCompanySignInAddress, settingsKeyCompanySignInNewAccounts, settingsKeyPasswordSignInEnabled,
	settingsKeyGoogleSignInEnabled, settingsKeyGoogleSignInClientID, settingsKeyGoogleSignInSecret, settingsKeyGoogleSignInDomains,
	settingsKeyMicrosoftSignInEnabled, settingsKeyMicrosoftSignInTenantID, settingsKeyMicrosoftSignInClientID, settingsKeyMicrosoftSignInSecret,
}

var _ application.CompanySignInSettingsStore = (*settingsStore)(nil)

// GetCompanySignIn answers the configuration from ONE read of its rows, so a
// concurrent save is never seen half applied. Only the canonical values are
// trusted: a provider is on only when its row says "1", password sign-in is
// off only when its row says "0", and a policy other than pending creates
// requesters.
func (ss *settingsStore) GetCompanySignIn(ctx context.Context) (application.CompanySignInSettings, error) {
	v, err := ss.settingValues(ctx, companySignInKeys...)
	if err != nil {
		return application.CompanySignInSettings{}, err
	}
	newAccounts := application.NewAccountsRequester
	if v[settingsKeyCompanySignInNewAccounts] == string(application.NewAccountsPending) {
		newAccounts = application.NewAccountsPending
	}
	var domains []string
	for line := range strings.Lines(v[settingsKeyGoogleSignInDomains]) {
		if d := strings.TrimSpace(line); d != "" {
			domains = append(domains, d)
		}
	}
	return application.CompanySignInSettings{
		Address:               v[settingsKeyCompanySignInAddress],
		NewAccounts:           newAccounts,
		PasswordSignInEnabled: v[settingsKeyPasswordSignInEnabled] != "0",
		Google: application.GoogleSignIn{
			Enabled:      v[settingsKeyGoogleSignInEnabled] == "1",
			ClientID:     v[settingsKeyGoogleSignInClientID],
			SealedSecret: v[settingsKeyGoogleSignInSecret],
			Domains:      domains,
		},
		Microsoft: application.MicrosoftSignIn{
			Enabled:      v[settingsKeyMicrosoftSignInEnabled] == "1",
			TenantID:     v[settingsKeyMicrosoftSignInTenantID],
			ClientID:     v[settingsKeyMicrosoftSignInClientID],
			SealedSecret: v[settingsKeyMicrosoftSignInSecret],
		},
	}, nil
}

// SetCompanySignIn writes every row in ONE transaction.
func (ss *settingsStore) SetCompanySignIn(ctx context.Context, c application.CompanySignInSettings) error {
	tx, err := beginImmediate(ctx, ss.db, "set company sign-in settings")
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, row := range []struct{ key, value string }{
		{settingsKeyCompanySignInAddress, c.Address},
		{settingsKeyCompanySignInNewAccounts, string(c.NewAccounts)},
		{settingsKeyPasswordSignInEnabled, formatSettingBool(c.PasswordSignInEnabled)},
		{settingsKeyGoogleSignInEnabled, formatSettingBool(c.Google.Enabled)},
		{settingsKeyGoogleSignInClientID, c.Google.ClientID},
		{settingsKeyGoogleSignInSecret, c.Google.SealedSecret},
		{settingsKeyGoogleSignInDomains, strings.Join(c.Google.Domains, "\n")},
		{settingsKeyMicrosoftSignInEnabled, formatSettingBool(c.Microsoft.Enabled)},
		{settingsKeyMicrosoftSignInTenantID, c.Microsoft.TenantID},
		{settingsKeyMicrosoftSignInClientID, c.Microsoft.ClientID},
		{settingsKeyMicrosoftSignInSecret, c.Microsoft.SealedSecret},
	} {
		if err := setSettingTx(ctx, tx, row.key, row.value); err != nil {
			return err
		}
	}
	return tx.Commit()
}
