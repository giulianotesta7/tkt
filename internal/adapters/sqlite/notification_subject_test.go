package sqlite

import (
	"context"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Issue #419, S3/S12: the notification center renders the account a pending
// notice names from THIS read. It differs from the Pending tab's read in the one
// way that matters: a stored notice outlives the state that produced it, so the
// read answers the account whatever its state is today — waiting, approved, or
// deactivated — and never reads the identity's issuer, subject or provider
// address.

// newNotificationSubjectTestStore opens a migrated database and the subject
// read over it.
func newNotificationSubjectTestStore(t *testing.T) (*Store, application.NotificationSubjectStore) {
	t.Helper()
	s := newTestDB(t)
	return s, newNotificationSubjectStore(s.db)
}

// TestNotificationSubjectStoreAnswersTheStoredAccountAndItsProviders is the
// authorized read: the account's own name and address (never the identity's),
// its pending state, and the provider identities it holds in display order.
func TestNotificationSubjectStoreAnswersTheStoredAccountAndItsProviders(t *testing.T) {
	s, subjects := newNotificationSubjectTestStore(t)
	ctx := context.Background()
	waiting := seedPendingUser(t, s, "Ana Pérez", "ana.perez@example.com")
	settled := seedApprovalActor(t, s)
	seedPendingIdentity(t, s, waiting, application.ProviderMicrosoft)
	seedPendingIdentity(t, s, waiting, application.ProviderGoogle)
	seedPendingIdentity(t, s, settled, application.ProviderGoogle)

	rows, err := subjects.ListNotificationSubjects(ctx, []int64{settled, waiting, 999})
	if err != nil {
		t.Fatalf("ListNotificationSubjects: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want the two existing accounts in request order", rows)
	}
	if rows[0].UserID != settled || rows[0].Pending || rows[0].Name != "Ada" {
		t.Errorf("first row = %+v, want the settled account in request order", rows[0])
	}
	if len(rows[0].Providers) != 1 || rows[0].Providers[0] != application.ProviderGoogle {
		t.Errorf("settled providers = %v, want its stored Google identity", rows[0].Providers)
	}
	if rows[1].UserID != waiting || !rows[1].Pending {
		t.Errorf("second row = %+v, want the waiting account", rows[1])
	}
	if rows[1].Name != "Ana Pérez" || rows[1].Email != "ana.perez@example.com" {
		t.Errorf("waiting row = %+v, want the account's own name and address", rows[1])
	}
	if len(rows[1].Providers) != 2 || rows[1].Providers[0] != application.ProviderGoogle || rows[1].Providers[1] != application.ProviderMicrosoft {
		t.Errorf("waiting providers = %v, want both identities ordered by provider", rows[1].Providers)
	}
}

// TestNotificationSubjectStoreStillAnswersAfterApproval is the notice-outlives-
// state rule: once the account is approved the same read answers it as settled
// with the identity it signed in with, so the row can say the request was
// already answered instead of claiming the account still waits — or vanishing.
func TestNotificationSubjectStoreStillAnswersAfterApproval(t *testing.T) {
	s, subjects := newNotificationSubjectTestStore(t)
	ctx := context.Background()
	waiting := seedPendingUser(t, s, "Bruno Salas", "bruno.salas@example.com")
	seedPendingIdentity(t, s, waiting, application.ProviderMicrosoft)
	actor := seedApprovalActor(t, s)

	if _, err := s.PendingUserStore().ApprovePending(ctx, []application.PendingApproval{{ID: waiting, Role: domain.RoleAgent}}, actor, testClock); err != nil {
		t.Fatalf("ApprovePending: %v", err)
	}

	rows, err := subjects.ListNotificationSubjects(ctx, []int64{waiting})
	if err != nil {
		t.Fatalf("ListNotificationSubjects: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want the approved account", rows)
	}
	if rows[0].Pending {
		t.Errorf("the approved account is still reported as waiting: %+v", rows[0])
	}
	if rows[0].Name != "Bruno Salas" || rows[0].Email != "bruno.salas@example.com" {
		t.Errorf("approved row = %+v, want the account's stored facts", rows[0])
	}
	if len(rows[0].Providers) != 1 || rows[0].Providers[0] != application.ProviderMicrosoft {
		t.Errorf("approved providers = %v, want the identity it signed in with", rows[0].Providers)
	}

	// A deactivated account is settled too, and still answered: the notice was
	// already addressed to its recipient.
	settled, err := s.UserStore().GetByID(ctx, waiting)
	if err != nil {
		t.Fatalf("read the approved account: %v", err)
	}
	settled.Active = false
	if err := s.UserStore().Update(ctx, settled); err != nil {
		t.Fatalf("deactivate the approved account: %v", err)
	}
	rows, err = subjects.ListNotificationSubjects(ctx, []int64{waiting})
	if err != nil {
		t.Fatalf("ListNotificationSubjects after deactivation: %v", err)
	}
	if len(rows) != 1 || rows[0].Pending {
		t.Errorf("deactivated account rows = %+v, want one settled row", rows)
	}
}

// TestNotificationSubjectStoreAnswersNothingForAnEmptyOrUnknownRequest keeps the
// panel's cost at zero and never invents an account.
func TestNotificationSubjectStoreAnswersNothingForAnEmptyOrUnknownRequest(t *testing.T) {
	s, subjects := newNotificationSubjectTestStore(t)
	ctx := context.Background()

	rows, err := subjects.ListNotificationSubjects(ctx, nil)
	if err != nil || rows != nil {
		t.Errorf("empty request = %+v, %v, want nothing read", rows, err)
	}
	rows, err = subjects.ListNotificationSubjects(ctx, []int64{404})
	if err != nil || len(rows) != 0 {
		t.Errorf("unknown id = %+v, %v, want no row", rows, err)
	}
	// An account with no stored identity is answered without a provider: the
	// display layer says so instead of guessing a method.
	unlinked := seedPendingUser(t, s, "Nora Sin", "nora.sin@example.com")
	rows, err = subjects.ListNotificationSubjects(ctx, []int64{unlinked})
	if err != nil {
		t.Fatalf("ListNotificationSubjects: %v", err)
	}
	if len(rows) != 1 || len(rows[0].Providers) != 0 {
		t.Errorf("unlinked account = %+v, want one row with no provider", rows)
	}
}
