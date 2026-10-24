package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// newPendingUsersService wires the Pending tab use cases over the shared user
// fake and the injected clock.
func newPendingUsersService() (*application.PendingUsersService, *fakeUserStore, *fakeClock) {
	clock := fixedClock()
	users := newFakeUserStore()
	return application.NewPendingUsersService(users, clock), users, clock
}

// Issue #419, S3: the Pending tab answers exactly the accounts waiting for
// approval, and only to an actor who may manage users.
func TestPendingUsersListAnswersOnlyWaitingAccountsToAManager(t *testing.T) {
	ctx := context.Background()
	svc, users, _ := newPendingUsersService()
	users.seedRole("Ana", "ana@example.com", domain.RoleUser, true)
	users.seedRole("Bob", "bob@example.com", domain.RoleUser, false)
	waiting := users.seedPending("Wanda", "wanda@example.com")

	got, err := svc.List(ctx, managerActor)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != waiting.ID {
		t.Fatalf("List = %+v, want only the pending account %d", got, waiting.ID)
	}
	for _, role := range []domain.Role{domain.RoleUser, domain.RoleAgent} {
		if _, err := svc.List(ctx, domain.User{ID: 7, Role: role}); err == nil {
			t.Errorf("List as %q must be denied", role)
		}
	}
}

// Issue #419, S3/S9: approval activates the account, clears the pending flag,
// and grants exactly the chosen role — nothing more.
func TestPendingUsersApproveActivatesWithTheChosenRole(t *testing.T) {
	ctx := context.Background()
	svc, users, _ := newPendingUsersService()
	waiting := users.seedPending("Wanda", "wanda@example.com")

	approved, err := svc.Approve(ctx, managerActor, []application.PendingApproval{{ID: waiting.ID, Role: domain.RoleAgent}})
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if len(approved) != 1 || approved[0].ID != waiting.ID || !approved[0].Active || approved[0].Pending || approved[0].Role != domain.RoleAgent {
		t.Fatalf("Approve = %+v, want the account activated as agent", approved)
	}
	got, err := users.GetByID(ctx, waiting.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Active || got.Pending || got.Role != domain.RoleAgent {
		t.Fatalf("stored account = %+v, want active agent with no pending flag", got)
	}
	if len(users.roleChanges) != 1 || users.roleChanges[0].actorID != managerActor.ID {
		t.Fatalf("role audits = %+v, want one by the approving actor", users.roleChanges)
	}
}

// Issue #419, S3: approving with the role the waiting account already holds
// grants nothing and records no role change; the account still becomes active.
func TestPendingUsersApproveKeepingTheRequesterRoleAuditsNothing(t *testing.T) {
	ctx := context.Background()
	svc, users, _ := newPendingUsersService()
	waiting := users.seedPending("Wanda", "wanda@example.com")

	if _, err := svc.Approve(ctx, managerActor, []application.PendingApproval{{ID: waiting.ID, Role: domain.RoleUser}}); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if len(users.roleChanges) != 0 {
		t.Fatalf("role audits = %d, want 0 for an unchanged role", len(users.roleChanges))
	}
	got, err := users.GetByID(ctx, waiting.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Active || got.Pending {
		t.Fatalf("stored account = %+v, want it active and no longer pending", got)
	}
}

// Issue #419, S9: approval grants no privilege of its own. It reuses the
// existing managed-user grant matrix, so an agent or a requester cannot approve
// at all, an admin cannot approve an admin, and root can.
func TestPendingUsersApproveReusesTheManagedUserGrantMatrix(t *testing.T) {
	ctx := context.Background()
	refused := []struct {
		name  string
		actor domain.User
		role  domain.Role
	}{
		{"requester", domain.User{ID: 5, Role: domain.RoleUser}, domain.RoleUser},
		{"agent", domain.User{ID: 6, Role: domain.RoleAgent}, domain.RoleAgent},
		{"admin granting admin", managerActor, domain.RoleAdmin},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			svc, users, _ := newPendingUsersService()
			waiting := users.seedPending("Wanda", "wanda@example.com")

			_, err := svc.Approve(ctx, tc.actor, []application.PendingApproval{{ID: waiting.ID, Role: tc.role}})
			var forbidden *domain.ForbiddenError
			if !errors.As(err, &forbidden) {
				t.Fatalf("Approve = %v, want ForbiddenError", err)
			}
			assertStillWaiting(t, users, waiting)
			if len(users.roleChanges) != 0 {
				t.Fatalf("role audits = %d, want 0", len(users.roleChanges))
			}
		})
	}

	svc, users, _ := newPendingUsersService()
	waiting := users.seedPending("Wanda", "wanda@example.com")
	root := domain.User{ID: 1, Role: domain.RoleRoot}
	approved, err := svc.Approve(ctx, root, []application.PendingApproval{{ID: waiting.ID, Role: domain.RoleAdmin}})
	if err != nil {
		t.Fatalf("root Approve as admin: %v", err)
	}
	if len(approved) != 1 || approved[0].Role != domain.RoleAdmin {
		t.Fatalf("Approve as root = %+v, want the admin role granted", approved)
	}
}

// Issue #419, S9: root is never an approvable role — approval never creates or
// promotes a root, whatever the actor — and an unknown role is refused.
func TestPendingUsersApproveRefusesRootAndUnknownRoles(t *testing.T) {
	ctx := context.Background()
	root := domain.User{ID: 1, Role: domain.RoleRoot}
	for _, tc := range []struct {
		name string
		role domain.Role
		want *domain.RootProtectedError
	}{
		{"root", domain.RoleRoot, domain.NewRootProtectedError()},
		{"unknown", domain.Role("superuser"), nil},
		{"empty", domain.Role(""), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, users, _ := newPendingUsersService()
			waiting := users.seedPending("Wanda", "wanda@example.com")

			_, err := svc.Approve(ctx, root, []application.PendingApproval{{ID: waiting.ID, Role: tc.role}})
			if tc.want != nil {
				var protected *domain.RootProtectedError
				if !errors.As(err, &protected) {
					t.Fatalf("Approve = %v, want RootProtectedError", err)
				}
			} else {
				var validation *domain.ValidationError
				if !errors.As(err, &validation) || validation.Field != "role" {
					t.Fatalf("Approve = %v, want a role ValidationError", err)
				}
			}
			assertStillWaiting(t, users, waiting)
		})
	}
}

// Issue #419, S3: a bulk approval is one decision. A stale id, an account that
// is no longer pending, a repeated id, or an empty selection refuses the whole
// batch and leaves every named account waiting.
func TestPendingUsersApproveChangesNothingWhenAnyEntryIsRefused(t *testing.T) {
	cases := []struct {
		name  string
		build func(first, second, settled domain.User) []application.PendingApproval
	}{
		{"unknown id", func(first, _, _ domain.User) []application.PendingApproval {
			return []application.PendingApproval{{ID: first.ID, Role: domain.RoleAgent}, {ID: 9999, Role: domain.RoleAgent}}
		}},
		{"already approved", func(first, _, settled domain.User) []application.PendingApproval {
			return []application.PendingApproval{{ID: first.ID, Role: domain.RoleAgent}, {ID: settled.ID, Role: domain.RoleAgent}}
		}},
		{"repeated id", func(first, _, _ domain.User) []application.PendingApproval {
			return []application.PendingApproval{{ID: first.ID, Role: domain.RoleAgent}, {ID: first.ID, Role: domain.RoleAgent}}
		}},
		{"empty selection", func(_, _, _ domain.User) []application.PendingApproval {
			return nil
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			svc, users, _ := newPendingUsersService()
			first := users.seedPending("Wanda", "wanda@example.com")
			second := users.seedPending("Vic", "vic@example.com")
			settled := users.seedRole("Ana", "ana@example.com", domain.RoleUser, true)

			_, err := svc.Approve(ctx, managerActor, tc.build(first, second, settled))
			if err == nil {
				t.Fatal("Approve must refuse the batch")
			}
			assertStillWaiting(t, users, first)
			assertStillWaiting(t, users, second)
			got, getErr := users.GetByID(ctx, settled.ID)
			if getErr != nil {
				t.Fatal(getErr)
			}
			if !got.Active || got.Pending {
				t.Fatalf("settled account = %+v, want it untouched", got)
			}
			if len(users.roleChanges) != 0 {
				t.Fatalf("role audits = %d, want 0", len(users.roleChanges))
			}
		})
	}
}
