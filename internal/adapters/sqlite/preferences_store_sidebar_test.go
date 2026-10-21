package sqlite

import (
	"context"
	"testing"
)

// Sidebar state preference (issue #377, S2): one user_preferences row under the
// key "sidebar", with exactly two storable values, "expanded" and "collapsed".
// No migration seeds it: an absent row is the documented default (the sidebar
// is expanded), and the read treats ANY stored value other than the exact
// "collapsed" as expanded, so a hand-edited or restored-from-backup row can
// never collapse a sidebar nobody collapsed.

// TestSidebarPreferencesDefaultsToExpandedWithoutARow proves the default and
// that reading it writes nothing: a user who never toggled the sidebar gets the
// expanded shell, and a page render leaves no row behind.
func TestSidebarPreferencesDefaultsToExpandedWithoutARow(t *testing.T) {
	s := newTestDB(t)
	user := seedUser(t, s, "Ana", "ana@example.com", true)

	collapsed, err := s.SidebarPreferences().SidebarCollapsed(context.Background(), user)
	if err != nil {
		t.Fatalf("SidebarCollapsed: %v", err)
	}
	if collapsed {
		t.Fatal("SidebarCollapsed = true without a stored row, want false (expanded is the default)")
	}
	if n := userPreferenceRows(t, s, user, preferencesKeySidebar); n != 0 {
		t.Errorf("a read wrote %d preference rows, want none", n)
	}
}

// TestSidebarPreferencesRoundTripsBothStates proves the toggle persists both
// directions as the documented "collapsed"/"expanded" values, upserting the
// single row instead of accumulating one per save.
func TestSidebarPreferencesRoundTripsBothStates(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	user := seedUser(t, s, "Ana", "ana@example.com", true)
	prefs := s.SidebarPreferences()

	for _, step := range []struct {
		set  bool
		want bool
	}{{true, true}, {false, false}, {true, true}} {
		if err := prefs.SetSidebarCollapsed(ctx, user, step.set); err != nil {
			t.Fatalf("SetSidebarCollapsed(%v): %v", step.set, err)
		}
		got, err := prefs.SidebarCollapsed(ctx, user)
		if err != nil {
			t.Fatalf("SidebarCollapsed: %v", err)
		}
		if got != step.want {
			t.Fatalf("after setting %v: collapsed = %v, want %v", step.set, got, step.want)
		}
		if n := userPreferenceRows(t, s, user, preferencesKeySidebar); n != 1 {
			t.Fatalf("after setting %v: %d stored rows, want exactly 1 (upsert)", step.set, n)
		}
		wantValue := preferencesSidebarExpanded
		if step.set {
			wantValue = preferencesSidebarCollapsed
		}
		if value := storedPreference(t, s, user, preferencesKeySidebar); value != wantValue {
			t.Errorf("stored value = %q, want %q", value, wantValue)
		}
	}
}

// TestSidebarPreferencesTreatsAnyOtherValueAsExpanded proves the fail-closed
// read: only the exact stored value "collapsed" collapses the shell. Anything
// else — an empty string, a different spelling or case, a value a future
// version wrote — keeps the expanded default.
func TestSidebarPreferencesTreatsAnyOtherValueAsExpanded(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	for i, value := range []string{"", "true", "Collapsed", "collapsed ", "0", "expanded"} {
		user := seedUser(t, s, "User"+value, "user"+string(rune('a'+i))+"@example.com", true)
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO user_preferences (user_id, "key", value) VALUES (?, ?, ?)`,
			user, preferencesKeySidebar, value); err != nil {
			t.Fatalf("seed preference %q: %v", value, err)
		}
		collapsed, err := s.SidebarPreferences().SidebarCollapsed(ctx, user)
		if err != nil {
			t.Fatalf("SidebarCollapsed for %q: %v", value, err)
		}
		if collapsed {
			t.Errorf("stored value %q collapsed the sidebar, want only the exact value %q to collapse it", value, preferencesSidebarCollapsed)
		}
	}
}

// TestSidebarPreferencesIsPerUser proves one person's collapsed shell never
// renders for another user.
func TestSidebarPreferencesIsPerUser(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	collapsedUser := seedUser(t, s, "Ana", "ana@example.com", true)
	expandedUser := seedUser(t, s, "Bea", "bea@example.com", true)
	prefs := s.SidebarPreferences()

	if err := prefs.SetSidebarCollapsed(ctx, collapsedUser, true); err != nil {
		t.Fatalf("SetSidebarCollapsed: %v", err)
	}
	if got, err := prefs.SidebarCollapsed(ctx, collapsedUser); err != nil || !got {
		t.Fatalf("collapsed user = %v (err %v), want true", got, err)
	}
	if got, err := prefs.SidebarCollapsed(ctx, expandedUser); err != nil || got {
		t.Fatalf("other user = %v (err %v), want false (expanded)", got, err)
	}
}
