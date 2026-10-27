package sqlite

import (
	"context"
	"reflect"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
)

// Company sign-in configuration (issue #419): keyed settings rows, so no
// migration is needed for it — an absent row reads back the default.

func TestCompanySignInStoreDefaultsWithoutRows(t *testing.T) {
	s := newTestDB(t)
	got, err := s.CompanySignInSettingsStore().GetCompanySignIn(context.Background())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if want := application.DefaultCompanySignInSettings(); !reflect.DeepEqual(got, want) {
		t.Fatalf("fresh configuration = %+v, want the default %+v", got, want)
	}
}

func TestCompanySignInStoreRoundTrip(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	want := application.CompanySignInSettings{
		Address:               "https://tkt.example.com",
		NewAccounts:           application.NewAccountsPending,
		PasswordSignInEnabled: false,
		Google: application.GoogleSignIn{
			Enabled: true, ClientID: "g-client", SealedSecret: "v1:google", Domains: []string{"example.com", "example.org"},
		},
		Microsoft: application.MicrosoftSignIn{
			Enabled: true, TenantID: "0f8fad5b-d9cb-469f-a165-70867728950e", ClientID: "m-client", SealedSecret: "v1:microsoft",
		},
	}
	if err := s.CompanySignInSettingsStore().SetCompanySignIn(ctx, want); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := s.CompanySignInSettingsStore().GetCompanySignIn(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("read back %+v, want %+v", got, want)
	}

	// Turning everything off and clearing the domains reads back exactly that.
	want = application.DefaultCompanySignInSettings()
	if err := s.CompanySignInSettingsStore().SetCompanySignIn(ctx, want); err != nil {
		t.Fatalf("set default: %v", err)
	}
	got, err = s.CompanySignInSettingsStore().GetCompanySignIn(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("read back %+v, want %+v", got, want)
	}
}

// A hand-edited row degrades to the safe reading: an unknown policy creates
// requesters (never pending by accident is fine, never anything else), and an
// unparseable password switch keeps password sign-in on.
func TestCompanySignInStoreReadsNonsenseRowsAsDefaults(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	for key, value := range map[string]string{
		settingsKeyCompanySignInNewAccounts: "admin",
		settingsKeyPasswordSignInEnabled:    "maybe",
		settingsKeyGoogleSignInEnabled:      "yes",
	} {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)`, key, value); err != nil {
			t.Fatalf("insert %s: %v", key, err)
		}
	}
	got, err := s.CompanySignInSettingsStore().GetCompanySignIn(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.NewAccounts != application.NewAccountsRequester || !got.PasswordSignInEnabled || got.Google.Enabled {
		t.Fatalf("nonsense rows read back %+v; want requester, password on, Google off", got)
	}
}
