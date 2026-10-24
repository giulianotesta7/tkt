package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Issue #419, S3/S8: a pending account is not a managed user. It is inactive
// and waits for an administrator's approval, so the Users list never shows it
// and no ordinary management route — edit, reactivate, password, delete —
// reaches it. Every refusal below also proves nothing was written: the account
// keeps waiting, with the role and password it had.
func TestPendingAccountStaysOutOfManagedUserManagement(t *testing.T) {
	ctx := context.Background()
	svc, users, _ := newUserService()
	users.seedRole("Ana", "ana@example.com", domain.RoleUser, true)
	waiting := users.seedPending("Wanda", "wanda@example.com")

	list, err := svc.List(ctx, managerActor)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Email != "ana@example.com" {
		t.Fatalf("List = %+v, want only the managed account", list)
	}

	// Reactivate / edit: the pending account must not be activated, and the
	// refusal must leave it exactly as it was.
	_, err = svc.UpdateManagedUser(ctx, managerActor, waiting.ID, application.UpdateManagedUserInput{
		Name: "Wanda", Email: "wanda@example.com", Role: domain.RoleAgent, Active: true,
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("edit/reactivate of a pending account = %v, want ErrNotFound", err)
	}
	assertStillWaiting(t, users, waiting)

	// The retained non-HTTP entry point must not route around the rule.
	active := true
	_, err = svc.Update(ctx, managerActor, waiting.ID, application.UpdateUserInput{Active: &active})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("legacy update of a pending account = %v, want ErrNotFound", err)
	}
	assertStillWaiting(t, users, waiting)

	if err := svc.ChangePassword(ctx, managerActor, waiting.ID, "s3cret-pass-12"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("password change of a pending account = %v, want ErrNotFound", err)
	}
	assertStillWaiting(t, users, waiting)

	if err := svc.Delete(ctx, managerActor, waiting.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("delete of a pending account = %v, want ErrNotFound", err)
	}
	assertStillWaiting(t, users, waiting)

	if len(users.roleChanges) != 0 {
		t.Fatalf("role audits = %d, want 0: a refused pending action grants nothing", len(users.roleChanges))
	}

	// PRESERVE: an ordinary deactivated account still reactivates through the
	// same managed route, so the refusals above are about pending accounts only.
	bob := users.seedRole("Bob", "bob@example.com", domain.RoleUser, false)
	reactivated, err := svc.UpdateManagedUser(ctx, managerActor, bob.ID, application.UpdateManagedUserInput{
		Name: "Bob", Email: "bob@example.com", Role: domain.RoleUser, Active: true,
	})
	if err != nil {
		t.Fatalf("reactivating a normal inactive account: %v", err)
	}
	if !reactivated.Active || reactivated.Pending {
		t.Fatalf("reactivated account = %+v, want it active and never pending", reactivated)
	}
}

// assertStillWaiting proves a refused action changed nothing: the account is
// still pending, inactive, a requester, and keeps an empty password hash (a
// company sign-in account has none).
func assertStillWaiting(t *testing.T, users *fakeUserStore, want domain.User) {
	t.Helper()
	got, err := users.GetByID(context.Background(), want.ID)
	if err != nil {
		t.Fatalf("pending account %d disappeared: %v", want.ID, err)
	}
	if !got.Pending || got.Active || got.Role != domain.RoleUser || got.PasswordHash != "" {
		t.Fatalf("pending account after a refused action = %+v, want it unchanged", got)
	}
}
