package sqlite

import (
	"context"
	"testing"
)

// Ticket activity order preference (issue #438, S1): one user_preferences row
// under the key "timeline_order", holding one of the two values the application
// service allows ("newest", "oldest"). No migration seeds it: an absent row is
// the documented "no choice yet" state, which the service normalizes to
// "newest". Like the queue order and the theme, the store persists whatever it
// is given — the closed set is the service's contract, and the read path there
// normalizes an unknown stored value — so this file proves the storage facts
// alone, on the same real embedded migrations as every other preference.

// TestTimelineOrderPreferencesDefaultsToNoRow proves the default and that
// reading it writes nothing: a user who never chose an order stores no row at
// all, and rendering a ticket leaves none behind.
func TestTimelineOrderPreferencesDefaultsToNoRow(t *testing.T) {
	s := newTestDB(t)
	user := seedUser(t, s, "Ana", "ana@example.com", true)

	order, err := s.TimelineOrderPreferences().GetTimelineOrder(context.Background(), user)
	if err != nil {
		t.Fatalf("GetTimelineOrder: %v", err)
	}
	if order != "" {
		t.Fatalf("GetTimelineOrder = %q without a stored row, want \"\" (the service normalizes it to newest first)", order)
	}
	if n := userPreferenceRows(t, s, user, preferencesKeyTimelineOrder); n != 0 {
		t.Errorf("a read wrote %d preference rows, want none", n)
	}
}

// TestTimelineOrderPreferencesRoundTripsEveryValue proves both storable orders
// persist as documented, upserting the single row instead of accumulating one
// per save — the Activity toggle can flip the order many times per session.
func TestTimelineOrderPreferencesRoundTripsEveryValue(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	user := seedUser(t, s, "Ana", "ana@example.com", true)
	prefs := s.TimelineOrderPreferences()

	for _, order := range []string{"newest", "oldest", "newest", "oldest"} {
		if err := prefs.SetTimelineOrder(ctx, user, order); err != nil {
			t.Fatalf("SetTimelineOrder(%q): %v", order, err)
		}
		got, err := prefs.GetTimelineOrder(ctx, user)
		if err != nil {
			t.Fatalf("GetTimelineOrder: %v", err)
		}
		if got != order {
			t.Fatalf("after saving %q: order = %q", order, got)
		}
		if n := userPreferenceRows(t, s, user, preferencesKeyTimelineOrder); n != 1 {
			t.Fatalf("after saving %q: %d stored rows, want exactly 1 (upsert)", order, n)
		}
		if value := storedPreference(t, s, user, preferencesKeyTimelineOrder); value != order {
			t.Errorf("stored value = %q, want %q", value, order)
		}
	}
}

// TestTimelineOrderPreferencesStoresWhateverItIsGiven proves the store has no
// opinion about the closed set: a value outside it is persisted verbatim and
// read back verbatim, because normalization belongs to the application service
// (the same division the queue order and the theme follow).
func TestTimelineOrderPreferencesStoresWhateverItIsGiven(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	user := seedUser(t, s, "Ana", "ana@example.com", true)
	prefs := s.TimelineOrderPreferences()

	for _, order := range []string{"asc", "", "Newest"} {
		if err := prefs.SetTimelineOrder(ctx, user, order); err != nil {
			t.Fatalf("SetTimelineOrder(%q): %v", order, err)
		}
		got, err := prefs.GetTimelineOrder(ctx, user)
		if err != nil {
			t.Fatalf("GetTimelineOrder: %v", err)
		}
		if got != order {
			t.Errorf("the store changed %q into %q, want it persisted verbatim", order, got)
		}
	}
}

// TestTimelineOrderPreferencesIsPerUser proves one person's feed order never
// renders for another user.
func TestTimelineOrderPreferencesIsPerUser(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	oldestUser := seedUser(t, s, "Ana", "ana@example.com", true)
	untouchedUser := seedUser(t, s, "Bea", "bea@example.com", true)
	prefs := s.TimelineOrderPreferences()

	if err := prefs.SetTimelineOrder(ctx, oldestUser, "oldest"); err != nil {
		t.Fatalf("SetTimelineOrder: %v", err)
	}
	if got, err := prefs.GetTimelineOrder(ctx, oldestUser); err != nil || got != "oldest" {
		t.Fatalf("oldest user = %q (err %v), want %q", got, err, "oldest")
	}
	if got, err := prefs.GetTimelineOrder(ctx, untouchedUser); err != nil || got != "" {
		t.Fatalf("other user = %q (err %v), want \"\" (no stored choice)", got, err)
	}
	if n := userPreferenceRows(t, s, untouchedUser, preferencesKeyTimelineOrder); n != 0 {
		t.Errorf("the other user has %d activity-order rows, want none", n)
	}
}

// TestTimelineOrderPreferencesSharesTheTableWithoutTouchingOtherKeys proves the
// new key reuses the generic table and no neighbour: saving the activity order
// rewrites neither the queue order, nor the email opt-out, nor the sidebar, nor
// the theme, and each of those saves leaves the activity order alone.
func TestTimelineOrderPreferencesSharesTheTableWithoutTouchingOtherKeys(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	user := seedUser(t, s, "Ana", "ana@example.com", true)

	if err := s.PreferencesStore().SetQueueOrder(ctx, user, "priority"); err != nil {
		t.Fatalf("SetQueueOrder: %v", err)
	}
	if err := s.EmailPreferences().SetEmailNotificationsEnabled(ctx, user, false); err != nil {
		t.Fatalf("SetEmailNotificationsEnabled: %v", err)
	}
	if err := s.SidebarPreferences().SetSidebarCollapsed(ctx, user, true); err != nil {
		t.Fatalf("SetSidebarCollapsed: %v", err)
	}
	if err := s.ThemePreferences().SetTheme(ctx, user, "dark"); err != nil {
		t.Fatalf("SetTheme: %v", err)
	}

	if err := s.TimelineOrderPreferences().SetTimelineOrder(ctx, user, "oldest"); err != nil {
		t.Fatalf("SetTimelineOrder: %v", err)
	}
	if order, err := s.PreferencesStore().GetQueueOrder(ctx, user); err != nil || order != "priority" {
		t.Errorf("queue order = %q (err %v), want it untouched", order, err)
	}
	if enabled, err := s.EmailPreferences().EmailNotificationsEnabled(ctx, user); err != nil || enabled {
		t.Errorf("email enabled = %v (err %v), want the stored opt-out untouched", enabled, err)
	}
	if collapsed, err := s.SidebarPreferences().SidebarCollapsed(ctx, user); err != nil || !collapsed {
		t.Errorf("sidebar collapsed = %v (err %v), want it untouched", collapsed, err)
	}
	if theme, err := s.ThemePreferences().GetTheme(ctx, user); err != nil || theme != "dark" {
		t.Errorf("theme = %q (err %v), want it untouched", theme, err)
	}

	// The reverse: a neighbour's save never rewrites the activity order.
	if err := s.ThemePreferences().SetTheme(ctx, user, "light"); err != nil {
		t.Fatalf("SetTheme again: %v", err)
	}
	if err := s.PreferencesStore().SetQueueOrder(ctx, user, "urgency"); err != nil {
		t.Fatalf("SetQueueOrder again: %v", err)
	}
	if order, err := s.TimelineOrderPreferences().GetTimelineOrder(ctx, user); err != nil || order != "oldest" {
		t.Errorf("activity order = %q (err %v), want it left at %q", order, err, "oldest")
	}
	if n := userPreferenceRows(t, s, user, preferencesKeyTimelineOrder); n != 1 {
		t.Errorf("the user has %d activity-order rows, want exactly 1", n)
	}
}
