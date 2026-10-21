package application

import (
	"context"
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
	svc := NewPreferencesService(fake, fake, fake)
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
	svc := NewPreferencesService(fake, fake, fake)
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
	svc := NewPreferencesService(fake, fake, fake)
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
	svc := NewPreferencesService(fake, fake, fake)
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
