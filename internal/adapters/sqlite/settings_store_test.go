package sqlite

import (
	"context"
	"testing"
)

// Instance appearance settings store (0005_instance_settings.sql): the
// migration seeds the default, writes round-trip, and an absent row falls
// back to the same default (fail-open read, service-level validation).

func TestSettingsStoreDefaultSeededByMigration(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	got, err := s.SettingsStore().GetInternalCommentBg(ctx)
	if err != nil {
		t.Fatalf("get default: %v", err)
	}
	if got != "#E8EEFF" {
		t.Errorf("default internal comment bg = %q, want %q", got, "#E8EEFF")
	}
}

func TestSettingsStoreRoundTrip(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	if err := s.SettingsStore().SetInternalCommentBg(ctx, "#EFE9FB"); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := s.SettingsStore().GetInternalCommentBg(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != "#EFE9FB" {
		t.Errorf("internal comment bg = %q, want %q", got, "#EFE9FB")
	}
}

func TestSettingsStoreAbsentRowFallsBackToDefault(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	// Remove the seeded row: the store must still answer the default.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key = 'internal_comment_bg'`); err != nil {
		t.Fatalf("delete settings row: %v", err)
	}
	got, err := s.SettingsStore().GetInternalCommentBg(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != "#E8EEFF" {
		t.Errorf("internal comment bg = %q, want default %q", got, "#E8EEFF")
	}
}
