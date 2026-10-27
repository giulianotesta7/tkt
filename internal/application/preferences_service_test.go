package application

import (
	"context"
	"errors"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Per-user sidebar state (issue #377, S2): every authenticated user owns
// exactly one stored value, the shell is EXPANDED when nothing is stored, and
// saving it touches neither the email opt-out nor the queue order.

// TestPreferencesSidebarDefaultsToExpanded proves the documented default: a
// user who never toggled the sidebar gets the expanded shell, which is what
// the first page render after login must show.
func TestPreferencesSidebarDefaultsToExpanded(t *testing.T) {
	fake := newFakePreferences()
	svc := NewPreferencesService(fake, fake, fake, fake)
	actor := domain.User{ID: 7, Email: "ana@tkt.test"}

	collapsed, err := svc.GetSidebarCollapsed(context.Background(), actor)
	if err != nil {
		t.Fatalf("GetSidebarCollapsed: %v", err)
	}
	if collapsed {
		t.Fatal("a user with no stored preference must get the expanded sidebar")
	}
}

// TestPreferencesSidebarRoundTripBothWays proves collapsing and expanding are
// both persisted, with no capability gate and no validation step to satisfy.
func TestPreferencesSidebarRoundTripBothWays(t *testing.T) {
	fake := newFakePreferences()
	svc := NewPreferencesService(fake, fake, fake, fake)
	actor := domain.User{ID: 7, Email: "ana@tkt.test"}
	ctx := context.Background()

	if err := svc.SetSidebarCollapsed(ctx, actor, true); err != nil {
		t.Fatalf("SetSidebarCollapsed(true): %v", err)
	}
	if collapsed, err := svc.GetSidebarCollapsed(ctx, actor); err != nil || !collapsed {
		t.Fatalf("after collapsing: collapsed = %v, %v; want true", collapsed, err)
	}

	if err := svc.SetSidebarCollapsed(ctx, actor, false); err != nil {
		t.Fatalf("SetSidebarCollapsed(false): %v", err)
	}
	if collapsed, err := svc.GetSidebarCollapsed(ctx, actor); err != nil || collapsed {
		t.Fatalf("after expanding: collapsed = %v, %v; want false", collapsed, err)
	}
}

// TestPreferencesSidebarBelongsToTheActor proves the stored value is keyed by
// the acting session: one user's collapsed shell never renders for another.
func TestPreferencesSidebarBelongsToTheActor(t *testing.T) {
	fake := newFakePreferences()
	svc := NewPreferencesService(fake, fake, fake, fake)
	ctx := context.Background()
	ana := domain.User{ID: 7, Email: "ana@tkt.test"}
	bruno := domain.User{ID: 8, Email: "bruno@tkt.test"}

	if err := svc.SetSidebarCollapsed(ctx, ana, true); err != nil {
		t.Fatalf("SetSidebarCollapsed(ana): %v", err)
	}
	if collapsed, err := svc.GetSidebarCollapsed(ctx, bruno); err != nil || collapsed {
		t.Fatalf("another actor's collapsed shell must not reach bruno: collapsed = %v, %v", collapsed, err)
	}
	if collapsed, err := svc.GetSidebarCollapsed(ctx, ana); err != nil || !collapsed {
		t.Fatalf("ana's own collapsed shell must hold: collapsed = %v, %v", collapsed, err)
	}
}

// TestPreferencesSidebarAndEmailStayIndependent proves the two preferences
// share the table but not a value: saving the sidebar never rewrites the email
// opt-out or the queue order, and switching email off never expands the shell.
func TestPreferencesSidebarAndEmailStayIndependent(t *testing.T) {
	fake := newFakePreferences()
	svc := NewPreferencesService(fake, fake, fake, fake)
	actor := domain.User{ID: 7, Email: "ana@tkt.test"}
	ctx := context.Background()

	if err := svc.SetDefaultQueueOrder(ctx, actor, QueueOrderUrgency); err != nil {
		t.Fatalf("SetDefaultQueueOrder: %v", err)
	}
	if err := svc.SetSidebarCollapsed(ctx, actor, true); err != nil {
		t.Fatalf("SetSidebarCollapsed: %v", err)
	}
	if enabled, err := svc.GetEmailNotifications(ctx, actor); err != nil || !enabled {
		t.Fatalf("saving the sidebar must leave email on: enabled = %v, %v", enabled, err)
	}
	if order, err := svc.GetDefaultQueueOrder(ctx, actor); err != nil || order != QueueOrderUrgency {
		t.Fatalf("saving the sidebar must leave the queue order alone: order = %q, %v", order, err)
	}

	if err := svc.SetEmailNotifications(ctx, actor, false); err != nil {
		t.Fatalf("SetEmailNotifications: %v", err)
	}
	if collapsed, err := svc.GetSidebarCollapsed(ctx, actor); err != nil || !collapsed {
		t.Fatalf("switching email off must leave the sidebar collapsed: collapsed = %v, %v", collapsed, err)
	}
}

// Per-user interface theme (issue #378, S1): every authenticated user owns
// exactly one stored value from a closed set of three, the SYSTEM default is
// what an absent or unknown value reads, and an out-of-set write is refused
// before the store is reached.

// TestPreferencesThemeDefaultsToSystem proves the documented default: a user
// who never chose a theme follows the system setting, which is what the first
// page render after login must apply and what an unknown stored value reads.
func TestPreferencesThemeDefaultsToSystem(t *testing.T) {
	fake := newFakePreferences()
	svc := NewPreferencesService(fake, fake, fake, fake)
	actor := domain.User{ID: 7, Email: "ana@tkt.test"}

	theme, err := svc.GetTheme(context.Background(), actor)
	if err != nil {
		t.Fatalf("GetTheme: %v", err)
	}
	if theme != ThemeSystem {
		t.Fatalf("a user with no stored preference got %q, want %q", theme, ThemeSystem)
	}
}

// TestPreferencesThemeNormalizesUnknownStoredValue proves the read fails
// closed: a hand-edited row, a value a future version wrote, or a differently
// spelled theme reads as the system default instead of reaching the shell.
func TestPreferencesThemeNormalizesUnknownStoredValue(t *testing.T) {
	for _, stored := range []string{"", "Dark", "dark ", "sepia", "true"} {
		fake := newFakePreferences()
		svc := NewPreferencesService(fake, fake, fake, fake)
		actor := domain.User{ID: 7, Email: "ana@tkt.test"}
		fake.themes[actor.ID] = stored

		theme, err := svc.GetTheme(context.Background(), actor)
		if err != nil {
			t.Fatalf("GetTheme for %q: %v", stored, err)
		}
		if theme != ThemeSystem {
			t.Errorf("stored value %q read %q, want the normalized %q", stored, theme, ThemeSystem)
		}
	}
}

// TestPreferencesThemeRoundTripsEveryAllowedValue proves each of the three
// storable themes is persisted verbatim and read back unchanged.
func TestPreferencesThemeRoundTripsEveryAllowedValue(t *testing.T) {
	fake := newFakePreferences()
	svc := NewPreferencesService(fake, fake, fake, fake)
	actor := domain.User{ID: 7, Email: "ana@tkt.test"}
	ctx := context.Background()

	for _, theme := range AllowedThemes() {
		if err := svc.SetTheme(ctx, actor, theme); err != nil {
			t.Fatalf("SetTheme(%q): %v", theme, err)
		}
		got, err := svc.GetTheme(ctx, actor)
		if err != nil {
			t.Fatalf("GetTheme: %v", err)
		}
		if got != theme {
			t.Fatalf("after saving %q: theme = %q", theme, got)
		}
		if stored := fake.themes[actor.ID]; stored != theme {
			t.Errorf("stored value = %q, want %q", stored, theme)
		}
	}
}

// TestPreferencesThemeRejectsUnknownValue proves the closed set is enforced
// BEFORE the store: an out-of-set theme is a validation failure on the "theme"
// field, and the previously stored value survives untouched.
func TestPreferencesThemeRejectsUnknownValue(t *testing.T) {
	fake := newFakePreferences()
	svc := NewPreferencesService(fake, fake, fake, fake)
	actor := domain.User{ID: 7, Email: "ana@tkt.test"}
	ctx := context.Background()

	if err := svc.SetTheme(ctx, actor, ThemeDark); err != nil {
		t.Fatalf("SetTheme(%q): %v", ThemeDark, err)
	}

	for _, rejected := range []string{"", "Dark", "dark ", "sepia", "auto"} {
		err := svc.SetTheme(ctx, actor, rejected)
		var validation *domain.ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("SetTheme(%q) = %v, want a *domain.ValidationError", rejected, err)
		}
		if validation.Field != "theme" || validation.Message != "invalid theme" || validation.Key != "error.invalid_theme" {
			t.Errorf("SetTheme(%q) validation = %+v, want field %q message %q key %q",
				rejected, validation, "theme", "invalid theme", "error.invalid_theme")
		}
		if stored := fake.themes[actor.ID]; stored != ThemeDark {
			t.Errorf("SetTheme(%q) stored %q, want the previous value %q untouched", rejected, stored, ThemeDark)
		}
	}
}

// TestPreferencesThemeBelongsToTheActor proves the stored value is keyed by
// the acting session: one user's dark theme never renders for another.
func TestPreferencesThemeBelongsToTheActor(t *testing.T) {
	fake := newFakePreferences()
	svc := NewPreferencesService(fake, fake, fake, fake)
	ctx := context.Background()
	ana := domain.User{ID: 7, Email: "ana@tkt.test"}
	bruno := domain.User{ID: 8, Email: "bruno@tkt.test"}

	if err := svc.SetTheme(ctx, ana, ThemeDark); err != nil {
		t.Fatalf("SetTheme(ana): %v", err)
	}
	if theme, err := svc.GetTheme(ctx, bruno); err != nil || theme != ThemeSystem {
		t.Fatalf("another actor's theme must not reach bruno: theme = %q, %v", theme, err)
	}
	if theme, err := svc.GetTheme(ctx, ana); err != nil || theme != ThemeDark {
		t.Fatalf("ana's own theme must hold: theme = %q, %v", theme, err)
	}
}

// TestPreferencesThemeAndOtherPreferencesStayIndependent proves the four
// preferences share the table but not a value: saving the theme never rewrites
// the queue order, the email opt-out, or the sidebar state, and the reverse
// holds too.
func TestPreferencesThemeAndOtherPreferencesStayIndependent(t *testing.T) {
	fake := newFakePreferences()
	svc := NewPreferencesService(fake, fake, fake, fake)
	actor := domain.User{ID: 7, Email: "ana@tkt.test"}
	ctx := context.Background()

	if err := svc.SetDefaultQueueOrder(ctx, actor, QueueOrderUrgency); err != nil {
		t.Fatalf("SetDefaultQueueOrder: %v", err)
	}
	if err := svc.SetEmailNotifications(ctx, actor, false); err != nil {
		t.Fatalf("SetEmailNotifications: %v", err)
	}
	if err := svc.SetSidebarCollapsed(ctx, actor, true); err != nil {
		t.Fatalf("SetSidebarCollapsed: %v", err)
	}
	if err := svc.SetTheme(ctx, actor, ThemeLight); err != nil {
		t.Fatalf("SetTheme: %v", err)
	}

	if order, err := svc.GetDefaultQueueOrder(ctx, actor); err != nil || order != QueueOrderUrgency {
		t.Errorf("the theme save changed the queue order: %q, %v", order, err)
	}
	if enabled, err := svc.GetEmailNotifications(ctx, actor); err != nil || enabled {
		t.Errorf("the theme save changed the email preference: %v, %v", enabled, err)
	}
	if collapsed, err := svc.GetSidebarCollapsed(ctx, actor); err != nil || !collapsed {
		t.Errorf("the theme save changed the sidebar state: %v, %v", collapsed, err)
	}

	if err := svc.SetTheme(ctx, actor, ThemeDark); err != nil {
		t.Fatalf("SetTheme again: %v", err)
	}
	if theme, err := svc.GetTheme(ctx, actor); err != nil || theme != ThemeDark {
		t.Fatalf("the theme must round-trip on its own key: %q, %v", theme, err)
	}
	if order, err := svc.GetDefaultQueueOrder(ctx, actor); err != nil || order != QueueOrderUrgency {
		t.Errorf("a second theme save changed the queue order: %q, %v", order, err)
	}
}

// Per-user activity order (issue #438, S1): every authenticated user owns
// exactly one stored value from a closed set of two, NEWEST FIRST is what an
// absent or unknown value reads, and an out-of-set write is refused before the
// store is reached. The preference decides where the detail renders the
// composer, so its default is part of the product contract, not a taste.

// fakeOrderPreferences is the in-memory user_preferences table seen through the
// timeline-order port on top of the fake every other preference already uses
// (issue #438, S1). It counts writes so a refused submission can prove it
// reached no persistence at all.
type fakeOrderPreferences struct {
	*fakePreferences
	timelines map[int64]string
	writes    int
}

var (
	_ PreferencesStore              = (*fakeOrderPreferences)(nil)
	_ TimelineOrderPreferencesStore = (*fakeOrderPreferences)(nil)
)

func newFakeOrderPreferences() *fakeOrderPreferences {
	return &fakeOrderPreferences{fakePreferences: newFakePreferences(), timelines: map[int64]string{}}
}

// GetTimelineOrder answers the stored value verbatim and "" for an absent row,
// exactly like the adapter: normalization is the service's job.
func (f *fakeOrderPreferences) GetTimelineOrder(_ context.Context, userID int64) (string, error) {
	return f.timelines[userID], nil
}

func (f *fakeOrderPreferences) SetTimelineOrder(_ context.Context, userID int64, order string) error {
	f.writes++
	f.timelines[userID] = order
	return nil
}

// TestPreferencesTimelineOrderDefaultsToNewest proves the documented default:
// a user who never chose reads the newest-first feed, and reading it writes
// nothing.
func TestPreferencesTimelineOrderDefaultsToNewest(t *testing.T) {
	fake := newFakeOrderPreferences()
	svc := NewPreferencesService(fake, fake, fake, fake)
	actor := domain.User{ID: 7, Email: "ana@tkt.test"}

	order, err := svc.GetTimelineOrder(context.Background(), actor)
	if err != nil {
		t.Fatalf("GetTimelineOrder: %v", err)
	}
	if order != TimelineOrderNewest {
		t.Fatalf("a user with no stored preference got %q, want %q", order, TimelineOrderNewest)
	}
	if _, stored := fake.timelines[actor.ID]; stored {
		t.Error("reading the preference must not create a row")
	}
	if fake.writes != 0 {
		t.Errorf("a read performed %d writes, want none", fake.writes)
	}
}

// TestPreferencesTimelineOrderDefaultsToNewestWithoutTheStore proves the read
// fails closed when the wired store does not answer the port: the documented
// newest-first default is still what the detail renders, so a store that
// predates the preference can never blank the feed or guess an order.
func TestPreferencesTimelineOrderDefaultsToNewestWithoutTheStore(t *testing.T) {
	// A store that answers the queue order only, like an adapter built before
	// issue #438: the narrow port is absent, the read answers the default.
	svc := NewPreferencesService(newFakePreferences(), newFakePreferences(), newFakePreferences(), newFakePreferences())
	actor := domain.User{ID: 7, Email: "ana@tkt.test"}

	order, err := svc.GetTimelineOrder(context.Background(), actor)
	if err != nil {
		t.Fatalf("GetTimelineOrder without the port: %v", err)
	}
	if order != TimelineOrderNewest {
		t.Fatalf("GetTimelineOrder without the port = %q, want %q", order, TimelineOrderNewest)
	}
	// A WRITE is refused instead of silently discarded: a user who chose the
	// other order must never be told it was saved when nothing can store it.
	if err := svc.SetTimelineOrder(context.Background(), actor, TimelineOrderOldest); err == nil {
		t.Fatal("SetTimelineOrder without the port succeeded, want a refusal")
	}
}

// TestPreferencesTimelineOrderRoundTripsEveryAllowedValue proves both storable
// orders persist verbatim and read back unchanged.
func TestPreferencesTimelineOrderRoundTripsEveryAllowedValue(t *testing.T) {
	fake := newFakeOrderPreferences()
	svc := NewPreferencesService(fake, fake, fake, fake)
	actor := domain.User{ID: 7, Email: "ana@tkt.test"}
	ctx := context.Background()

	for _, order := range AllowedTimelineOrders() {
		if err := svc.SetTimelineOrder(ctx, actor, order); err != nil {
			t.Fatalf("SetTimelineOrder(%q): %v", order, err)
		}
		got, err := svc.GetTimelineOrder(ctx, actor)
		if err != nil {
			t.Fatalf("GetTimelineOrder: %v", err)
		}
		if got != order {
			t.Fatalf("after saving %q: order = %q", order, got)
		}
		if stored := fake.timelines[actor.ID]; stored != order {
			t.Errorf("stored value = %q, want %q", stored, order)
		}
	}
}

// TestPreferencesTimelineOrderNormalizesUnknownStoredValue proves the read fails
// closed: a hand-edited row, a future value, or a differently spelled order
// reads as the newest-first default instead of reaching the detail.
func TestPreferencesTimelineOrderNormalizesUnknownStoredValue(t *testing.T) {
	for _, stored := range []string{"", "Newest", "newest ", "asc", "descending", "oldest_first"} {
		fake := newFakeOrderPreferences()
		svc := NewPreferencesService(fake, fake, fake, fake)
		actor := domain.User{ID: 7, Email: "ana@tkt.test"}
		fake.timelines[actor.ID] = stored

		order, err := svc.GetTimelineOrder(context.Background(), actor)
		if err != nil {
			t.Fatalf("GetTimelineOrder for %q: %v", stored, err)
		}
		if order != TimelineOrderNewest {
			t.Errorf("stored value %q read %q, want the normalized %q", stored, order, TimelineOrderNewest)
		}
	}
}

// TestPreferencesTimelineOrderRejectsUnknownValue proves the closed set is
// enforced BEFORE the store: an out-of-set order is a validation failure on the
// "timeline_order" field, no write happens, and the previously stored value
// survives untouched.
func TestPreferencesTimelineOrderRejectsUnknownValue(t *testing.T) {
	fake := newFakeOrderPreferences()
	svc := NewPreferencesService(fake, fake, fake, fake)
	actor := domain.User{ID: 7, Email: "ana@tkt.test"}
	ctx := context.Background()

	if err := svc.SetTimelineOrder(ctx, actor, TimelineOrderOldest); err != nil {
		t.Fatalf("SetTimelineOrder(%q): %v", TimelineOrderOldest, err)
	}
	writes := fake.writes

	for _, rejected := range []string{"", "Newest", "newest ", "asc", "desc", "oldest first"} {
		err := svc.SetTimelineOrder(ctx, actor, rejected)
		var validation *domain.ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("SetTimelineOrder(%q) = %v, want a *domain.ValidationError", rejected, err)
		}
		if validation.Field != "timeline_order" || validation.Message != "invalid timeline order" || validation.Key != "error.invalid_timeline_order" {
			t.Errorf("SetTimelineOrder(%q) validation = %+v, want field %q message %q key %q",
				rejected, validation, "timeline_order", "invalid timeline order", "error.invalid_timeline_order")
		}
		if stored := fake.timelines[actor.ID]; stored != TimelineOrderOldest {
			t.Errorf("SetTimelineOrder(%q) stored %q, want the previous value %q untouched", rejected, stored, TimelineOrderOldest)
		}
	}
	if fake.writes != writes {
		t.Errorf("rejected submissions performed %d writes, want 0", fake.writes-writes)
	}
}

// TestPreferencesTimelineOrderBelongsToTheActor proves the stored value is keyed
// by the acting session: one user's oldest-first feed never renders for another.
func TestPreferencesTimelineOrderBelongsToTheActor(t *testing.T) {
	fake := newFakeOrderPreferences()
	svc := NewPreferencesService(fake, fake, fake, fake)
	ctx := context.Background()
	ana := domain.User{ID: 7, Email: "ana@tkt.test"}
	bruno := domain.User{ID: 8, Email: "bruno@tkt.test"}

	if err := svc.SetTimelineOrder(ctx, ana, TimelineOrderOldest); err != nil {
		t.Fatalf("SetTimelineOrder(ana): %v", err)
	}
	if order, err := svc.GetTimelineOrder(ctx, bruno); err != nil || order != TimelineOrderNewest {
		t.Fatalf("another actor's order must not reach bruno: order = %q, %v", order, err)
	}
	if order, err := svc.GetTimelineOrder(ctx, ana); err != nil || order != TimelineOrderOldest {
		t.Fatalf("ana's own order must hold: order = %q, %v", order, err)
	}
	if err := svc.SetTimelineOrder(ctx, bruno, TimelineOrderNewest); err != nil {
		t.Fatalf("SetTimelineOrder(bruno): %v", err)
	}
	if order, err := svc.GetTimelineOrder(ctx, ana); err != nil || order != TimelineOrderOldest {
		t.Fatalf("bruno's save must not rewrite ana's order: order = %q, %v", order, err)
	}
}

// TestPreferencesTimelineOrderAndOtherPreferencesStayIndependent proves the
// five preferences share the table but not a value: saving the activity order
// rewrites none of the others, and their saves never move it.
func TestPreferencesTimelineOrderAndOtherPreferencesStayIndependent(t *testing.T) {
	fake := newFakeOrderPreferences()
	svc := NewPreferencesService(fake, fake, fake, fake)
	actor := domain.User{ID: 7, Email: "ana@tkt.test"}
	ctx := context.Background()

	if err := svc.SetDefaultQueueOrder(ctx, actor, QueueOrderUrgency); err != nil {
		t.Fatalf("SetDefaultQueueOrder: %v", err)
	}
	if err := svc.SetEmailNotifications(ctx, actor, false); err != nil {
		t.Fatalf("SetEmailNotifications: %v", err)
	}
	if err := svc.SetSidebarCollapsed(ctx, actor, true); err != nil {
		t.Fatalf("SetSidebarCollapsed: %v", err)
	}
	if err := svc.SetTheme(ctx, actor, ThemeDark); err != nil {
		t.Fatalf("SetTheme: %v", err)
	}

	if err := svc.SetTimelineOrder(ctx, actor, TimelineOrderOldest); err != nil {
		t.Fatalf("SetTimelineOrder: %v", err)
	}
	if order, err := svc.GetDefaultQueueOrder(ctx, actor); err != nil || order != QueueOrderUrgency {
		t.Errorf("the activity-order save changed the queue order: %q, %v", order, err)
	}
	if enabled, err := svc.GetEmailNotifications(ctx, actor); err != nil || enabled {
		t.Errorf("the activity-order save changed the email preference: %v, %v", enabled, err)
	}
	if collapsed, err := svc.GetSidebarCollapsed(ctx, actor); err != nil || !collapsed {
		t.Errorf("the activity-order save changed the sidebar state: %v, %v", collapsed, err)
	}
	if theme, err := svc.GetTheme(ctx, actor); err != nil || theme != ThemeDark {
		t.Errorf("the activity-order save changed the theme: %q, %v", theme, err)
	}

	if err := svc.SetTheme(ctx, actor, ThemeLight); err != nil {
		t.Fatalf("SetTheme again: %v", err)
	}
	if err := svc.SetDefaultQueueOrder(ctx, actor, QueueOrderPriority); err != nil {
		t.Fatalf("SetDefaultQueueOrder again: %v", err)
	}
	if order, err := svc.GetTimelineOrder(ctx, actor); err != nil || order != TimelineOrderOldest {
		t.Fatalf("other preference saves changed the activity order: %q, %v", order, err)
	}
}
