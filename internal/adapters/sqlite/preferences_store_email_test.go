package sqlite

import (
	"context"
	"testing"
)

// Email opt-out preference (issue #362, spec S5): one user_preferences row
// under the key email_notifications. No migration seeds it: an absent row is
// the documented default (the toggle is ON, S14), and the read normalizes ANY
// stored value other than "off" back to on, so a hand-edited or
// restored-from-backup row can never silently stop a person's email.

func userPreferenceRows(t *testing.T, s *Store, userID int64, key string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM user_preferences WHERE user_id = ? AND "key" = ?`, userID, key).Scan(&n); err != nil {
		t.Fatalf("count user_preferences rows: %v", err)
	}
	return n
}

func storedPreference(t *testing.T, s *Store, userID int64, key string) string {
	t.Helper()
	var value string
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT value FROM user_preferences WHERE user_id = ? AND "key" = ?`, userID, key).Scan(&value); err != nil {
		t.Fatalf("read user_preferences value: %v", err)
	}
	return value
}

// TestEmailPreferencesDefaultsToOnWithoutARow proves the default and that
// reading it writes nothing: a user who never touched Preferences receives
// email, and a GET leaves no row behind.
func TestEmailPreferencesDefaultsToOnWithoutARow(t *testing.T) {
	s := newTestDB(t)
	user := seedUser(t, s, "Ana", "ana@example.com", true)

	enabled, err := s.EmailPreferences().EmailNotificationsEnabled(context.Background(), user)
	if err != nil {
		t.Fatalf("EmailNotificationsEnabled: %v", err)
	}
	if !enabled {
		t.Fatal("EmailNotificationsEnabled = false without a stored row, want true (email is on by default)")
	}
	if n := userPreferenceRows(t, s, user, preferencesKeyEmailNotifications); n != 0 {
		t.Errorf("a read wrote %d preference rows, want none", n)
	}
}

// TestEmailPreferencesRoundTripsTheOptOut proves the toggle persists both
// directions as the documented "on"/"off" values, upserting the single row
// instead of accumulating one per save.
func TestEmailPreferencesRoundTripsTheOptOut(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	user := seedUser(t, s, "Ana", "ana@example.com", true)
	prefs := s.EmailPreferences()

	for _, step := range []struct {
		set  bool
		want bool
	}{{false, false}, {true, true}, {false, false}} {
		if err := prefs.SetEmailNotificationsEnabled(ctx, user, step.set); err != nil {
			t.Fatalf("SetEmailNotificationsEnabled(%v): %v", step.set, err)
		}
		got, err := prefs.EmailNotificationsEnabled(ctx, user)
		if err != nil {
			t.Fatalf("EmailNotificationsEnabled: %v", err)
		}
		if got != step.want {
			t.Fatalf("after setting %v: enabled = %v, want %v", step.set, got, step.want)
		}
		if n := userPreferenceRows(t, s, user, preferencesKeyEmailNotifications); n != 1 {
			t.Fatalf("after setting %v: %d stored rows, want exactly 1 (upsert)", step.set, n)
		}
		wantValue := "on"
		if !step.set {
			wantValue = "off"
		}
		if value := storedPreference(t, s, user, preferencesKeyEmailNotifications); value != wantValue {
			t.Errorf("stored value = %q, want %q", value, wantValue)
		}
	}
}

// TestEmailPreferencesTreatsAnyOtherValueAsOn proves the fail-open read: only
// the exact stored value "off" stops email. Anything else — an empty string, a
// different spelling, a value a future version wrote — keeps the default.
func TestEmailPreferencesTreatsAnyOtherValueAsOn(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	for i, value := range []string{"", "yes", "ON", "off ", "0", "false"} {
		user := seedUser(t, s, "User"+value, "user"+string(rune('a'+i))+"@example.com", true)
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO user_preferences (user_id, "key", value) VALUES (?, ?, ?)`,
			user, preferencesKeyEmailNotifications, value); err != nil {
			t.Fatalf("seed preference %q: %v", value, err)
		}
		enabled, err := s.EmailPreferences().EmailNotificationsEnabled(ctx, user)
		if err != nil {
			t.Fatalf("EmailNotificationsEnabled for %q: %v", value, err)
		}
		if !enabled {
			t.Errorf("stored value %q disabled email, want only the exact value \"off\" to disable it", value)
		}
	}
}

// TestEmailPreferencesIsPerUser proves one person's opt-out never reaches
// another's inbox.
func TestEmailPreferencesIsPerUser(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	optedOut := seedUser(t, s, "Ana", "ana@example.com", true)
	stillOn := seedUser(t, s, "Bea", "bea@example.com", true)
	prefs := s.EmailPreferences()

	if err := prefs.SetEmailNotificationsEnabled(ctx, optedOut, false); err != nil {
		t.Fatalf("SetEmailNotificationsEnabled: %v", err)
	}
	if got, err := prefs.EmailNotificationsEnabled(ctx, optedOut); err != nil || got {
		t.Fatalf("opted-out user enabled = %v (err %v), want false", got, err)
	}
	if got, err := prefs.EmailNotificationsEnabled(ctx, stillOn); err != nil || !got {
		t.Fatalf("other user enabled = %v (err %v), want true", got, err)
	}
}
