package sqlite

import (
	"context"
	"testing"
)

// Interface theme preference (issue #378, S1): one user_preferences row under
// the key "theme", holding one of the three values the application service
// allows ("system", "light", "dark"). No migration seeds it: an absent row is
// the documented "no choice yet" state, which the service normalizes to
// "system". Like the queue order, the store persists whatever it is given — the
// closed set is the service's contract, and the read path there normalizes an
// unknown stored value.

// TestThemePreferencesDefaultsToNoRow proves the default and that reading it
// writes nothing: a user who never chose a theme stores no row at all, and a
// page render leaves none behind.
func TestThemePreferencesDefaultsToNoRow(t *testing.T) {
	s := newTestDB(t)
	user := seedUser(t, s, "Ana", "ana@example.com", true)

	theme, err := s.ThemePreferences().GetTheme(context.Background(), user)
	if err != nil {
		t.Fatalf("GetTheme: %v", err)
	}
	if theme != "" {
		t.Fatalf("GetTheme = %q without a stored row, want \"\" (the service normalizes it to the system default)", theme)
	}
	if n := userPreferenceRows(t, s, user, preferencesKeyTheme); n != 0 {
		t.Errorf("a read wrote %d preference rows, want none", n)
	}
}

// TestThemePreferencesRoundTripsEveryValue proves all three storable themes
// persist as documented, upserting the single row instead of accumulating one
// per save.
func TestThemePreferencesRoundTripsEveryValue(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	user := seedUser(t, s, "Ana", "ana@example.com", true)
	prefs := s.ThemePreferences()

	for _, theme := range []string{"dark", "light", "system", "dark"} {
		if err := prefs.SetTheme(ctx, user, theme); err != nil {
			t.Fatalf("SetTheme(%q): %v", theme, err)
		}
		got, err := prefs.GetTheme(ctx, user)
		if err != nil {
			t.Fatalf("GetTheme: %v", err)
		}
		if got != theme {
			t.Fatalf("after saving %q: theme = %q", theme, got)
		}
		if n := userPreferenceRows(t, s, user, preferencesKeyTheme); n != 1 {
			t.Fatalf("after saving %q: %d stored rows, want exactly 1 (upsert)", theme, n)
		}
		if value := storedPreference(t, s, user, preferencesKeyTheme); value != theme {
			t.Errorf("stored value = %q, want %q", value, theme)
		}
	}
}

// TestThemePreferencesStoresWhateverItIsGiven proves the store has no opinion
// about the closed set: a value outside it is persisted verbatim and read back
// verbatim, because normalization belongs to the application service (the same
// division the queue order follows).
func TestThemePreferencesStoresWhateverItIsGiven(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	user := seedUser(t, s, "Ana", "ana@example.com", true)
	prefs := s.ThemePreferences()

	for _, theme := range []string{"sepia", "", "Dark"} {
		if err := prefs.SetTheme(ctx, user, theme); err != nil {
			t.Fatalf("SetTheme(%q): %v", theme, err)
		}
		got, err := prefs.GetTheme(ctx, user)
		if err != nil {
			t.Fatalf("GetTheme: %v", err)
		}
		if got != theme {
			t.Errorf("the store changed %q into %q, want it persisted verbatim", theme, got)
		}
	}
}

// TestThemePreferencesIsPerUser proves one person's theme never renders for
// another user.
func TestThemePreferencesIsPerUser(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	darkUser := seedUser(t, s, "Ana", "ana@example.com", true)
	untouchedUser := seedUser(t, s, "Bea", "bea@example.com", true)
	prefs := s.ThemePreferences()

	if err := prefs.SetTheme(ctx, darkUser, "dark"); err != nil {
		t.Fatalf("SetTheme: %v", err)
	}
	if got, err := prefs.GetTheme(ctx, darkUser); err != nil || got != "dark" {
		t.Fatalf("dark user = %q (err %v), want \"dark\"", got, err)
	}
	if got, err := prefs.GetTheme(ctx, untouchedUser); err != nil || got != "" {
		t.Fatalf("other user = %q (err %v), want \"\" (no stored choice)", got, err)
	}
	if n := userPreferenceRows(t, s, untouchedUser, preferencesKeyTheme); n != 0 {
		t.Errorf("the other user has %d theme rows, want none", n)
	}
}
