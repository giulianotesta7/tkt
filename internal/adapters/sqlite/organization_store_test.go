package sqlite

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// The organization identity store (issue #383, S3): the three text facts live
// in the keyed settings table and the logo lives in settings_blobs, because
// settings.value is TEXT. Absent rows answer the documented defaults, and one
// submission is one transaction — a refused write leaves nothing behind.

func TestOrganizationStoreAnswersTheDefaultsWhenNothingIsStored(t *testing.T) {
	s := newTestDB(t)

	got, err := s.OrganizationStore().GetOrganization(context.Background())
	if err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	want := domain.Organization{Name: domain.DefaultOrganizationName, HelpDesk: domain.DefaultOrganizationHelpDesk}
	if got.Name != want.Name || got.HelpDesk != want.HelpDesk {
		t.Errorf("organization = %+v, want the defaults %+v", got, want)
	}
	if got.SupportEmail != "" || got.HasLogo() {
		t.Errorf("organization = %+v, want no support email and no logo", got)
	}
}

func TestOrganizationStoreRoundTripsEveryField(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	logo := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 48)...)
	want := domain.Organization{Name: "Acme Support", HelpDesk: "Service desk", SupportEmail: "support@acme.test", Logo: logo, LogoMIME: "image/png"}

	if err := s.OrganizationStore().SetOrganization(ctx, want); err != nil {
		t.Fatalf("SetOrganization: %v", err)
	}
	got, err := s.OrganizationStore().GetOrganization(ctx)
	if err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	if got.Name != want.Name || got.HelpDesk != want.HelpDesk || got.SupportEmail != want.SupportEmail {
		t.Errorf("organization = %+v, want %+v", got, want)
	}
	if !bytes.Equal(got.Logo, logo) {
		t.Errorf("logo = %d bytes, want the %d stored bytes", len(got.Logo), len(logo))
	}
	if got.LogoMIME != domain.OrganizationLogoContentType {
		t.Errorf("logo mime = %q, want %q", got.LogoMIME, domain.OrganizationLogoContentType)
	}
}

// TestOrganizationStoreRemovalClearsTheLogo proves nil means "no logo": the
// blob row is deleted, so a later read answers no logo at all rather than the
// bytes of a previous save.
func TestOrganizationStoreRemovalClearsTheLogo(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	logo := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 16)...)
	if err := s.OrganizationStore().SetOrganization(ctx, domain.Organization{
		Name: "Acme", HelpDesk: "Help desk", Logo: logo, LogoMIME: domain.OrganizationLogoContentType,
	}); err != nil {
		t.Fatalf("SetOrganization: %v", err)
	}

	if err := s.OrganizationStore().SetOrganization(ctx, domain.Organization{Name: "Acme", HelpDesk: "Help desk"}); err != nil {
		t.Fatalf("SetOrganization without a logo: %v", err)
	}
	got, err := s.OrganizationStore().GetOrganization(ctx)
	if err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	if got.HasLogo() {
		t.Errorf("logo survived a save that carried none: %d bytes", len(got.Logo))
	}
	var rows int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM settings_blobs`).Scan(&rows); err != nil {
		t.Fatalf("count settings_blobs: %v", err)
	}
	if rows != 0 {
		t.Errorf("settings_blobs rows = %d, want the logo row deleted", rows)
	}
}

// TestOrganizationStoreWriteIsOneTransaction proves the atomicity the service
// promises: a logo the table refuses fails the WHOLE write, so the text
// fields of the same submission are not persisted either.
func TestOrganizationStoreWriteIsOneTransaction(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	if err := s.OrganizationStore().SetOrganization(ctx, domain.Organization{Name: "Before", HelpDesk: "Help desk"}); err != nil {
		t.Fatalf("seed organization: %v", err)
	}

	// 300 KiB is above the table's 256 KiB bound, so the blob insert fails
	// inside the transaction the text writes share.
	tooBig := make([]byte, 300<<10)
	err := s.OrganizationStore().SetOrganization(ctx, domain.Organization{
		Name: "After", HelpDesk: "Other desk", SupportEmail: "after@acme.test",
		Logo: tooBig, LogoMIME: domain.OrganizationLogoContentType,
	})
	if err == nil {
		t.Fatal("SetOrganization stored a logo above the table bound, want a failure")
	}

	got, readErr := s.OrganizationStore().GetOrganization(ctx)
	if readErr != nil {
		t.Fatalf("GetOrganization: %v", readErr)
	}
	if got.Name != "Before" || got.SupportEmail != "" {
		t.Errorf("organization = %+v, want the text fields rolled back with the refused logo", got)
	}
}

// TestOrganizationStoreUnreadableInterfaceLanguageDoesNotLeakIntoOrganization
// keeps the two instance reads apart: a hand-edited interface_language row
// must not change what the organization read answers.
func TestOrganizationStoreIsIndependentOfOtherSettingsRows(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `UPDATE settings SET value = 'fr' WHERE key = 'interface_language'`); err != nil {
		t.Fatalf("corrupt interface language: %v", err)
	}
	got, err := s.OrganizationStore().GetOrganization(ctx)
	if err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	if got.Name != domain.DefaultOrganizationName {
		t.Errorf("name = %q, want the default", got.Name)
	}
}

// TestOrganizationStoreEmptyNameFallsBackToTheDefault proves the read never
// answers an unusable identity: an empty stored name (a hand-edited row) reads
// back the documented default instead of an empty brand.
func TestOrganizationStoreEmptyNameFallsBackToTheDefault(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	if err := s.OrganizationStore().SetOrganization(ctx, domain.Organization{Name: "", HelpDesk: ""}); err != nil {
		t.Fatalf("SetOrganization: %v", err)
	}
	got, err := s.OrganizationStore().GetOrganization(ctx)
	if err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	if got.Name != domain.DefaultOrganizationName || got.HelpDesk != domain.DefaultOrganizationHelpDesk {
		t.Errorf("organization = %+v, want the documented defaults", got)
	}
}

// TestOrganizationStorePropagatesStorageFailures keeps an operational failure
// distinguishable from the defaults: a closed database is an error, not an
// empty identity.
func TestOrganizationStorePropagatesStorageFailures(t *testing.T) {
	s := newTestDB(t)
	store := s.OrganizationStore()
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	if _, err := store.GetOrganization(context.Background()); err == nil || errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetOrganization on a closed database = %v, want a storage failure", err)
	}
}
