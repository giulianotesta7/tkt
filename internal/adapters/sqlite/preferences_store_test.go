package sqlite

import (
	"context"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
)

func TestLanguagePreferencesStore(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	a := seedUser(t, s, "Ana", "language-ana@example.com", true)
	b := seedUser(t, s, "Beto", "language-beto@example.com", true)
	prefs := s.PreferencesStore().(application.LanguagePreferencesStore)
	if got, err := prefs.GetLanguage(ctx, a); err != nil || got != "" {
		t.Fatalf("absent: %q %v", got, err)
	}
	if n := userPreferenceRows(t, s, a, preferencesKeyLanguage); n != 0 {
		t.Fatalf("read created %d rows", n)
	}
	for _, value := range []string{"en", "es", "instance", "future", ""} {
		if err := prefs.SetLanguage(ctx, a, value); err != nil {
			t.Fatal(err)
		}
		if got, err := prefs.GetLanguage(ctx, a); err != nil || got != value {
			t.Fatalf("%q: %q %v", value, got, err)
		}
		if n := userPreferenceRows(t, s, a, preferencesKeyLanguage); n != 1 {
			t.Fatalf("rows: %d", n)
		}
	}
	if got, err := prefs.GetLanguage(ctx, b); err != nil || got != "" {
		t.Fatalf("other user: %q %v", got, err)
	}
	if err := s.ThemePreferences().SetTheme(ctx, a, "dark"); err != nil {
		t.Fatal(err)
	}
	if err := prefs.SetLanguage(ctx, a, "en"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ThemePreferences().GetTheme(ctx, a); err != nil || got != "dark" {
		t.Fatalf("theme: %q %v", got, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := prefs.GetLanguage(ctx, a); err == nil {
		t.Fatal("read from closed store succeeded")
	}
	if err := prefs.SetLanguage(ctx, a, "es"); err == nil {
		t.Fatal("write to closed store succeeded")
	}
}
