package sqlite

import (
	"context"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
)

// Email settings (issue #362) live in the keyed settings table: no
// migration seeds them, so an untouched instance reads back a disabled
// configuration on port 25 with no encryption.

func TestEmailSettingsStoreDefaultsWithoutRows(t *testing.T) {
	s := newTestDB(t)
	got, err := s.EmailSettingsStore().GetEmailSettings(context.Background())
	if err != nil {
		t.Fatalf("GetEmailSettings: %v", err)
	}
	want := application.EmailSettings{Port: application.DefaultSMTPPort, Encryption: application.EmailEncryptionNone}
	if got != want {
		t.Fatalf("defaults = %+v, want %+v", got, want)
	}
}

func TestEmailSettingsStoreRoundTripsEveryField(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	want := application.EmailSettings{
		Enabled:        true,
		Host:           "smtp.example.com",
		Port:           465,
		Encryption:     application.EmailEncryptionTLS,
		SkipTLSVerify:  true,
		Username:       "tkt",
		SealedPassword: "v1:opaque",
		FromAddress:    "tkt@example.com",
		FromName:       "Help desk",
		BaseURL:        "https://tkt.example.com",
	}
	if err := s.EmailSettingsStore().SetEmailSettings(ctx, want); err != nil {
		t.Fatalf("SetEmailSettings: %v", err)
	}
	got, err := s.EmailSettingsStore().GetEmailSettings(ctx)
	if err != nil {
		t.Fatalf("GetEmailSettings: %v", err)
	}
	if got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}

	// Clearing values writes them back empty, not "left over".
	cleared := application.EmailSettings{Port: 25, Encryption: application.EmailEncryptionNone}
	if err := s.EmailSettingsStore().SetEmailSettings(ctx, cleared); err != nil {
		t.Fatalf("SetEmailSettings: %v", err)
	}
	if got, _ := s.EmailSettingsStore().GetEmailSettings(ctx); got != cleared {
		t.Fatalf("after clearing = %+v, want %+v", got, cleared)
	}
}

func TestEmailSettingsStoreNeverAcceptsThePlainPasswordField(t *testing.T) {
	s := newTestDB(t)
	// PasswordSet is a read-side projection; the store refuses a value that
	// carries it so no caller can mistake a Get result for a save payload.
	err := s.EmailSettingsStore().SetEmailSettings(context.Background(), application.EmailSettings{Port: 25, Encryption: application.EmailEncryptionNone, PasswordSet: true})
	if err == nil {
		t.Fatal("SetEmailSettings accepted a read-side value")
	}
}

func TestEmailSettingsStoreUnreadableValuesFailClosed(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES
		('email_enabled', 'yes'), ('email_port', 'abc'), ('email_encryption', 'ssl3'), ('email_skip_tls_verify', 'maybe')`); err != nil {
		t.Fatal(err)
	}
	got, err := s.EmailSettingsStore().GetEmailSettings(ctx)
	if err != nil {
		t.Fatalf("GetEmailSettings: %v", err)
	}
	if got.Enabled || got.SkipTLSVerify || got.Port != 25 || got.Encryption != application.EmailEncryptionNone {
		t.Fatalf("hand-edited rows read as %+v; want disabled, verified, port 25, none", got)
	}
}
