package application

import (
	"context"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Per-user email opt-out (issue #362, S5/S14): every authenticated user owns
// exactly one stored value, it is ON when nothing is stored, and saving it
// touches neither another actor's value nor the queue-order preference.

// fakePreferences is ONE in-memory user_preferences table serving all three
// preference ports, the way the SQLite adapter serves the queue order, the
// email opt-out, and the sidebar state from the same rows. It stores exactly
// what it is handed and answers an absent queue order with "", an absent email
// row with ON, and an absent sidebar row with expanded (false) — the
// documented defaults — so the service cannot hide a normalization bug behind
// a helpful fake.
type fakePreferences struct {
	orders   map[int64]string
	emails   map[int64]bool
	sidebars map[int64]bool
}

var (
	_ PreferencesStore        = (*fakePreferences)(nil)
	_ EmailPreferencesStore   = (*fakePreferences)(nil)
	_ SidebarPreferencesStore = (*fakePreferences)(nil)
)

func newFakePreferences() *fakePreferences {
	return &fakePreferences{
		orders:   map[int64]string{},
		emails:   map[int64]bool{},
		sidebars: map[int64]bool{},
	}
}

func (f *fakePreferences) GetQueueOrder(_ context.Context, userID int64) (string, error) {
	return f.orders[userID], nil
}

func (f *fakePreferences) SetQueueOrder(_ context.Context, userID int64, order string) error {
	f.orders[userID] = order
	return nil
}

// EmailNotificationsEnabled answers the stored choice and defaults an absent
// row to true, exactly like the adapter.
func (f *fakePreferences) EmailNotificationsEnabled(_ context.Context, userID int64) (bool, error) {
	enabled, ok := f.emails[userID]
	if !ok {
		return true, nil
	}
	return enabled, nil
}

func (f *fakePreferences) SetEmailNotificationsEnabled(_ context.Context, userID int64, enabled bool) error {
	f.emails[userID] = enabled
	return nil
}

// SidebarCollapsed answers the stored state and defaults an absent row to
// false (expanded), exactly like the adapter.
func (f *fakePreferences) SidebarCollapsed(_ context.Context, userID int64) (bool, error) {
	collapsed, ok := f.sidebars[userID]
	if !ok {
		return false, nil
	}
	return collapsed, nil
}

func (f *fakePreferences) SetSidebarCollapsed(_ context.Context, userID int64, collapsed bool) error {
	f.sidebars[userID] = collapsed
	return nil
}

// TestPreferencesEmailNotificationsDefaultOn proves the documented default:
// a user with no stored row receives email, so the toggle starts checked.
func TestPreferencesEmailNotificationsDefaultOn(t *testing.T) {
	svc := NewPreferencesService(newFakePreferences(), newFakePreferences(), newFakePreferences())
	actor := domain.User{ID: 7, Email: "ana@tkt.test"}

	enabled, err := svc.GetEmailNotifications(context.Background(), actor)
	if err != nil {
		t.Fatalf("GetEmailNotifications: %v", err)
	}
	if !enabled {
		t.Fatal("a user with no stored preference must have email on")
	}
}

// TestPreferencesEmailNotificationsRoundTripBothWays proves turning email off
// is persisted and turning it back on is persisted, with no capability gate
// and no validation step to satisfy.
func TestPreferencesEmailNotificationsRoundTripBothWays(t *testing.T) {
	fake := newFakePreferences()
	svc := NewPreferencesService(fake, fake, fake)
	actor := domain.User{ID: 7, Email: "ana@tkt.test"}
	ctx := context.Background()

	if err := svc.SetEmailNotifications(ctx, actor, false); err != nil {
		t.Fatalf("SetEmailNotifications(false): %v", err)
	}
	if enabled, err := svc.GetEmailNotifications(ctx, actor); err != nil || enabled {
		t.Fatalf("after turning it off: enabled = %v, %v; want false", enabled, err)
	}

	if err := svc.SetEmailNotifications(ctx, actor, true); err != nil {
		t.Fatalf("SetEmailNotifications(true): %v", err)
	}
	if enabled, err := svc.GetEmailNotifications(ctx, actor); err != nil || !enabled {
		t.Fatalf("after turning it back on: enabled = %v, %v; want true", enabled, err)
	}
}

// TestPreferencesEmailNotificationsBelongToTheActor proves the stored value is
// keyed by the acting session: one user's choice never reaches another's, in
// either direction.
func TestPreferencesEmailNotificationsBelongToTheActor(t *testing.T) {
	fake := newFakePreferences()
	svc := NewPreferencesService(fake, fake, fake)
	ctx := context.Background()
	ana := domain.User{ID: 7, Email: "ana@tkt.test"}
	bruno := domain.User{ID: 8, Email: "bruno@tkt.test"}

	if err := svc.SetEmailNotifications(ctx, ana, false); err != nil {
		t.Fatalf("SetEmailNotifications(ana): %v", err)
	}
	if enabled, err := svc.GetEmailNotifications(ctx, bruno); err != nil || !enabled {
		t.Fatalf("another actor's opt-out must not reach bruno: enabled = %v, %v", enabled, err)
	}
	if enabled, err := svc.GetEmailNotifications(ctx, ana); err != nil || enabled {
		t.Fatalf("ana's own opt-out must hold: enabled = %v, %v", enabled, err)
	}
}

// TestPreferencesEmailAndQueueOrderStayIndependent proves the two preferences
// share the table but not a value: saving one never rewrites the other.
func TestPreferencesEmailAndQueueOrderStayIndependent(t *testing.T) {
	fake := newFakePreferences()
	svc := NewPreferencesService(fake, fake, fake)
	ctx := context.Background()
	actor := domain.User{ID: 7, Email: "ana@tkt.test"}

	if err := svc.SetDefaultQueueOrder(ctx, actor, QueueOrderPriority); err != nil {
		t.Fatalf("SetDefaultQueueOrder: %v", err)
	}
	if err := svc.SetEmailNotifications(ctx, actor, false); err != nil {
		t.Fatalf("SetEmailNotifications: %v", err)
	}
	if order, err := svc.GetDefaultQueueOrder(ctx, actor); err != nil || order != QueueOrderPriority {
		t.Fatalf("the email save changed the queue order: %q, %v", order, err)
	}

	if err := svc.SetDefaultQueueOrder(ctx, actor, QueueOrderUrgency); err != nil {
		t.Fatalf("SetDefaultQueueOrder again: %v", err)
	}
	if enabled, err := svc.GetEmailNotifications(ctx, actor); err != nil || enabled {
		t.Fatalf("the queue-order save changed the email preference: enabled = %v, %v", enabled, err)
	}
}
