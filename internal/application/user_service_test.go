package application_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func newUserService() (*application.UserService, *fakeUserStore, *fakeClock) {
	clock := fixedClock()
	users := newFakeUserStore()
	return application.NewUserService(users, clock), users, clock
}

var managerActor = domain.User{ID: 999, Role: domain.RoleAdmin}

func TestCreateUserStoresActiveWithHashedPassword(t *testing.T) {
	svc, users, clock := newUserService()

	u, err := svc.Create(context.Background(), managerActor, application.CreateUserInput{
		Name: "Ana", Email: "ana@example.com", Password: "s3cret-pass",
	})
	if err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}
	if u.ID == 0 {
		t.Fatal("Create: user must receive a unique ID")
	}
	if !u.Active {
		t.Fatal("Create: new users must be active by default")
	}
	if u.PasswordHash == "s3cret-pass" || u.PasswordHash == "" {
		t.Fatal("Create: only the bcrypt hash must be stored, never the plaintext")
	}
	if !application.VerifyPassword(u.PasswordHash, "s3cret-pass") {
		t.Fatal("Create: stored hash must verify against the password")
	}
	if !u.CreatedAt.Equal(clock.now) {
		t.Fatalf("Create: timestamp must come from the injected clock, got %v", u.CreatedAt)
	}
	if len(users.users) != 1 {
		t.Fatalf("Create: user must be stored, got %d users", len(users.users))
	}
}

func TestCreateUserRejectsDuplicateEmail(t *testing.T) {
	svc, users, _ := newUserService()
	users.seed("Ana", "ana@example.com", true)

	_, err := svc.Create(context.Background(), managerActor, application.CreateUserInput{
		Name: "Ana 2", Email: "ana@example.com", Password: "s3cret-pass",
	})
	var derr *domain.DuplicateError
	if !errors.As(err, &derr) || derr.Kind != "user" {
		t.Fatalf("Create: duplicate email must be a DuplicateError(kind=user), got %v", err)
	}
	if len(users.users) != 1 {
		t.Fatal("Create: duplicate email must not store another user")
	}
}

func TestCreateUserRejectsMissingFields(t *testing.T) {
	svc, users, _ := newUserService()

	cases := []struct {
		name  string
		in    application.CreateUserInput
		field string
	}{
		{"missing name", application.CreateUserInput{Email: "ana@example.com", Password: "x"}, "name"},
		{"missing email", application.CreateUserInput{Name: "Ana", Password: "x"}, "email"},
		{"missing password", application.CreateUserInput{Name: "Ana", Email: "ana@example.com"}, "password"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Create(context.Background(), managerActor, tc.in)
			var verr *domain.ValidationError
			if !errors.As(err, &verr) || verr.Field != tc.field {
				t.Fatalf("Create: must be a ValidationError on field %q, got %v", tc.field, err)
			}
		})
	}
	if len(users.users) != 0 {
		t.Fatal("Create: rejected inputs must not store users")
	}
}

func TestUpdateUserReplacesValuesAndRehashesPassword(t *testing.T) {
	svc, _, _ := newUserService()
	created, err := svc.Create(context.Background(), managerActor, application.CreateUserInput{
		Name: "Ana", Email: "ana@example.com", Password: "old-pass",
	})
	if err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}
	oldHash := created.PasswordHash

	newName := "Ana Maria"
	newEmail := "ana.maria@example.com"
	newPassword := "new-pass"
	updated, err := svc.Update(context.Background(), managerActor, created.ID, application.UpdateUserInput{
		Name: &newName, Email: &newEmail, Password: &newPassword,
	})
	if err != nil {
		t.Fatalf("Update: unexpected error: %v", err)
	}
	if updated.Name != newName || updated.Email != newEmail {
		t.Fatalf("Update: values must be stored, got name=%q email=%q", updated.Name, updated.Email)
	}
	if updated.PasswordHash == oldHash {
		t.Fatal("Update: password change must store a new hash")
	}
	if !application.VerifyPassword(updated.PasswordHash, "new-pass") {
		t.Fatal("Update: new password must verify")
	}
	if application.VerifyPassword(updated.PasswordHash, "old-pass") {
		t.Fatal("Update: old password must no longer verify")
	}
}

func TestUpdateUserRejectsDuplicateEmail(t *testing.T) {
	svc, users, _ := newUserService()
	users.seed("Ana", "ana@example.com", true)
	beto := users.seed("Beto", "beto@example.com", true)

	newEmail := "ana@example.com"
	_, err := svc.Update(context.Background(), managerActor, beto.ID, application.UpdateUserInput{Email: &newEmail})
	var derr *domain.DuplicateError
	if !errors.As(err, &derr) || derr.Kind != "user" {
		t.Fatalf("Update: rename to duplicate email must be a DuplicateError(kind=user), got %v", err)
	}
	stored, _ := users.GetByID(context.Background(), beto.ID)
	if stored.Email != "beto@example.com" {
		t.Fatalf("Update: rejected rename must not change the stored email, got %q", stored.Email)
	}
}

func TestUpdateUserRejectsBlankValues(t *testing.T) {
	svc, _, _ := newUserService()
	created, err := svc.Create(context.Background(), managerActor, application.CreateUserInput{
		Name: "Ana", Email: "ana@example.com", Password: "x",
	})
	if err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}

	blank := "   "
	_, err = svc.Update(context.Background(), managerActor, created.ID, application.UpdateUserInput{Name: &blank})
	var verr *domain.ValidationError
	if !errors.As(err, &verr) || verr.Field != "name" {
		t.Fatalf("Update: blank name must be a ValidationError on field name, got %v", err)
	}
}

func TestDeactivateUserKeepsHistoricalData(t *testing.T) {
	svc, users, _ := newUserService()
	created, err := svc.Create(context.Background(), managerActor, application.CreateUserInput{
		Name: "Ana", Email: "ana@example.com", Password: "x",
	})
	if err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}

	inactive := false
	deactivated, err := svc.Update(context.Background(), managerActor, created.ID, application.UpdateUserInput{Active: &inactive})
	if err != nil {
		t.Fatalf("Update: deactivate: unexpected error: %v", err)
	}
	if deactivated.Active {
		t.Fatal("Update: user must be deactivated")
	}
	// Historical data preserved: the user is still retrievable.
	got, err := users.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID: deactivated user must still be retrievable: %v", err)
	}
	if got.Active {
		t.Fatal("GetByID: stored user must remain inactive")
	}
}

func TestDeleteUserReferencedRejected(t *testing.T) {
	svc, users, _ := newUserService()
	ana := users.seed("Ana", "ana@example.com", true)
	users.markReferenced(ana.ID) // assigned to tickets

	err := svc.Delete(context.Background(), managerActor, ana.ID)
	var rerr *domain.ReferencedError
	if !errors.As(err, &rerr) || rerr.Kind != "user" {
		t.Fatalf("Delete: referenced user must be a ReferencedError(kind=user), got %v", err)
	}
	if _, err := users.GetByID(context.Background(), ana.ID); err != nil {
		t.Fatal("Delete: referenced user must not be removed")
	}
}

func TestDeleteUserUnreferencedRemoves(t *testing.T) {
	svc, users, _ := newUserService()
	ana := users.seed("Ana", "ana@example.com", true)

	if err := svc.Delete(context.Background(), managerActor, ana.ID); err != nil {
		t.Fatalf("Delete: unreferenced user must be deletable, got %v", err)
	}
	if _, err := users.GetByID(context.Background(), ana.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Delete: user must be removed from the managed list, got %v", err)
	}
}

func TestUserServiceList(t *testing.T) {
	svc, _, _ := newUserService()
	svc.Create(context.Background(), managerActor, application.CreateUserInput{Name: "Ana", Email: "ana@example.com", Password: "x"})
	svc.Create(context.Background(), managerActor, application.CreateUserInput{Name: "Beto", Email: "beto@example.com", Password: "x"})

	list, err := svc.List(context.Background(), managerActor)
	if err != nil {
		t.Fatalf("List: unexpected error: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List: 2 users expected, got %d", len(list))
	}
}

// TestManagedUserOperationsRequireAnAdminActor proves user management is
// authorized at the application boundary before any user-store mutation.
func TestManagedUserOperationsRequireAnAdminActor(t *testing.T) {
	svc, users, _ := newUserService()
	admin := domain.User{ID: 1, Role: domain.RoleAdmin}
	agent := domain.User{ID: 2, Role: domain.RoleAgent}
	target := users.seedRole("Target", "target@example.com", domain.RoleUser, true)

	if _, err := svc.Create(context.Background(), agent, application.CreateUserInput{Name: "Denied", Email: "denied@example.com", Password: "secret"}); err == nil {
		t.Fatal("agent Create must be denied")
	}
	if _, err := svc.List(context.Background(), agent); err == nil {
		t.Fatal("agent List must be denied")
	}
	name := "Changed"
	if _, err := svc.Update(context.Background(), agent, target.ID, application.UpdateUserInput{Name: &name}); err == nil {
		t.Fatal("agent Update must be denied")
	}
	if err := svc.Delete(context.Background(), agent, target.ID); err == nil {
		t.Fatal("agent Delete must be denied")
	}
	if _, err := svc.Create(context.Background(), admin, application.CreateUserInput{Name: "Allowed", Email: "allowed@example.com", Password: "secret"}); err != nil {
		t.Fatalf("admin Create: %v", err)
	}
}

// TestAdminCannotDeactivateOrDeleteAnotherAdmin proves the role boundary is
// enforced by the use case, not merely the HTTP handler.
func TestAdminCannotDeactivateOrDeleteAnotherAdmin(t *testing.T) {
	svc, users, _ := newUserService()
	actor := domain.User{ID: 1, Role: domain.RoleAdmin}
	peer := users.seedRole("Peer", "peer@example.com", domain.RoleAdmin, true)
	inactive := false

	if _, err := svc.Update(context.Background(), actor, peer.ID, application.UpdateUserInput{Active: &inactive}); err == nil {
		t.Fatal("admin must not deactivate a peer admin")
	}
	if err := svc.Delete(context.Background(), actor, peer.ID); err == nil {
		t.Fatal("admin must not delete a peer admin")
	}
	stored, err := users.GetByID(context.Background(), peer.ID)
	if err != nil || !stored.Active {
		t.Fatalf("peer admin must remain active, user=%+v err=%v", stored, err)
	}
}

// S2 bootstrap use case RED: BootstrapRoot is the ONLY use case that creates
// a root (role-authorization first-user bootstrap). Written before the
// method exists — compile-error RED.

func TestBootstrapRootCreatesRootWithHashedPassword(t *testing.T) {
	svc, users, clock := newUserService()

	u, err := svc.BootstrapRoot(context.Background(), application.CreateUserInput{
		Name: "Root", Email: "root@example.com", Password: "s3cret-pass",
	})
	if err != nil {
		t.Fatalf("BootstrapRoot: unexpected error: %v", err)
	}
	if u.ID == 0 {
		t.Fatal("BootstrapRoot: user must receive a unique ID")
	}
	if u.Role != domain.RoleRoot {
		t.Fatalf("BootstrapRoot: role = %q, want %q", u.Role, domain.RoleRoot)
	}
	if !u.Active {
		t.Fatal("BootstrapRoot: the first user must be active")
	}
	if u.PasswordHash == "s3cret-pass" || u.PasswordHash == "" {
		t.Fatal("BootstrapRoot: only the bcrypt hash must be stored")
	}
	if !application.VerifyPassword(u.PasswordHash, "s3cret-pass") {
		t.Fatal("BootstrapRoot: stored hash must verify against the password")
	}
	if !u.CreatedAt.Equal(clock.now) {
		t.Fatalf("BootstrapRoot: timestamp must come from the injected clock")
	}
	// The same user object visible through the store carries the root role.
	got, err := users.GetByID(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != domain.RoleRoot {
		t.Errorf("stored role = %q, want %q", got.Role, domain.RoleRoot)
	}
}

func TestBootstrapRootRejectsWhenUsersExist(t *testing.T) {
	svc, users, _ := newUserService()
	users.seed("Ana", "ana@example.com", true)

	_, err := svc.BootstrapRoot(context.Background(), application.CreateUserInput{
		Name: "Other", Email: "other@example.com", Password: "s3cret-pass",
	})
	if !errors.Is(err, domain.ErrBootstrapUnavailable) {
		t.Fatalf("BootstrapRoot with users present = %v, want ErrBootstrapUnavailable", err)
	}
	if len(users.users) != 1 {
		t.Fatalf("BootstrapRoot must not create an account, got %d users", len(users.users))
	}
}

func TestBootstrapRootRejectsInvalidInput(t *testing.T) {
	svc, users, _ := newUserService()

	_, err := svc.BootstrapRoot(context.Background(), application.CreateUserInput{
		Name: "  ", Email: "root@example.com", Password: "s3cret-pass",
	})
	var verr *domain.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("BootstrapRoot blank name = %v, want ValidationError", err)
	}
	if len(users.users) != 0 {
		t.Fatal("BootstrapRoot must not store a user on validation failure")
	}
}

// S2 root invariants RED (task 2.4; role-authorization "Root Invariants"):
// no actor — including root itself — may edit, deactivate, or delete the
// root account, and user creation never yields a root. Written before the
// UserService guards exist: these fail until Update/Delete reject the root
// account with RootProtectedError.

// seedRoot puts a root user (and a regular user) into the fake store.
func seedRoot(t *testing.T, svc *application.UserService, users *fakeUserStore) (rootID, regularID int64) {
	t.Helper()
	ctx := context.Background()
	u := users.seed("Root", "root@example.com", true)
	u.Role = domain.RoleRoot
	if err := users.Update(ctx, &u); err != nil {
		t.Fatalf("seed root: %v", err)
	}
	r := users.seed("Ana", "ana@example.com", true)
	r.Role = domain.RoleAgent
	if err := users.Update(ctx, &r); err != nil {
		t.Fatalf("seed regular: %v", err)
	}
	return u.ID, r.ID
}

func TestUpdateRootRejected(t *testing.T) {
	svc, users, _ := newUserService()
	rootID, _ := seedRoot(t, svc, users)
	ctx := context.Background()

	name := "Hacker"
	email := "hack@example.com"
	_, err := svc.Update(ctx, managerActor, rootID, application.UpdateUserInput{Name: &name, Email: &email})
	if !errors.Is(err, domain.ErrRootProtected) {
		t.Fatalf("Update root = %v, want RootProtectedError", err)
	}
	var rpe *domain.RootProtectedError
	if !errors.As(err, &rpe) {
		t.Errorf("err = %v, want *RootProtectedError", err)
	}

	// The root account is untouched.
	got, err := users.GetByID(ctx, rootID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Root" || got.Email != "root@example.com" {
		t.Errorf("root mutated = %+v, want unchanged", got)
	}
	if got.Role != domain.RoleRoot {
		t.Errorf("root role = %q, want %q", got.Role, domain.RoleRoot)
	}
}

func TestDeactivateRootRejected(t *testing.T) {
	svc, users, _ := newUserService()
	rootID, _ := seedRoot(t, svc, users)
	ctx := context.Background()

	active := false
	_, err := svc.Update(ctx, managerActor, rootID, application.UpdateUserInput{Active: &active})
	if !errors.Is(err, domain.ErrRootProtected) {
		t.Fatalf("Deactivate root = %v, want RootProtectedError", err)
	}
	got, err := users.GetByID(ctx, rootID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Active {
		t.Error("root must remain active")
	}
}

func TestDeleteRootRejected(t *testing.T) {
	svc, users, _ := newUserService()
	rootID, _ := seedRoot(t, svc, users)
	ctx := context.Background()

	err := svc.Delete(ctx, managerActor, rootID)
	if !errors.Is(err, domain.ErrRootProtected) {
		t.Fatalf("Delete root = %v, want RootProtectedError", err)
	}
	if _, err := users.GetByID(ctx, rootID); err != nil {
		t.Fatalf("root must remain after rejected delete: %v", err)
	}
}

// TestUpdateAndDeleteRegularUserStillWork guards the non-root path: the
// invariants protect ONLY the root account, never the rest of the list.
func TestUpdateAndDeleteRegularUserStillWork(t *testing.T) {
	svc, users, _ := newUserService()
	_, regularID := seedRoot(t, svc, users)
	ctx := context.Background()

	name := "Ana Maria"
	_, err := svc.Update(ctx, managerActor, regularID, application.UpdateUserInput{Name: &name})
	if err != nil {
		t.Fatalf("Update regular user = %v, want success", err)
	}
	if err := svc.Delete(ctx, managerActor, regularID); err != nil {
		t.Fatalf("Delete regular user = %v, want success", err)
	}
}

// TestCreateUserIsNeverRoot proves the root role is not grantable through
// user creation (role-authorization "Root role not grantable"): Create
// assigns no role at all, so the created user can never carry root.
func TestCreateUserIsNeverRoot(t *testing.T) {
	svc, _, _ := newUserService()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		u, err := svc.Create(ctx, managerActor, application.CreateUserInput{
			Name: "U" + fmt.Sprint(i), Email: fmt.Sprintf("u%d@example.com", i), Password: "secret",
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if u.Role == domain.RoleRoot {
			t.Fatalf("Create produced role root for user %d, want never root", u.ID)
		}
	}
}

// S7.1 RED: role changes are authorized at the application boundary. Admins
// may move only between user and agent; root additionally grants/removes
// admin. This test is intentionally written before ChangeRole exists.
func TestChangeRoleEnforcesAdminAndRootMatrix(t *testing.T) {
	ctx := context.Background()
	svc, users, _ := newUserService()

	admin := users.seed("Admin", "admin@example.com", true)
	admin.Role = domain.RoleAdmin
	if err := users.Update(ctx, &admin); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	root := users.seed("Root", "root@example.com", true)
	root.Role = domain.RoleRoot
	if err := users.Update(ctx, &root); err != nil {
		t.Fatalf("seed root: %v", err)
	}
	member := users.seed("Member", "member@example.com", true)
	member.Role = domain.RoleUser
	if err := users.Update(ctx, &member); err != nil {
		t.Fatalf("seed member: %v", err)
	}

	if _, err := svc.ChangeRole(ctx, admin, member.ID, domain.RoleAgent); err != nil {
		t.Fatalf("admin user->agent: %v", err)
	}
	if _, err := svc.ChangeRole(ctx, admin, member.ID, domain.RoleAdmin); err == nil {
		t.Fatal("admin agent->admin must be forbidden")
	}
	if _, err := svc.ChangeRole(ctx, root, member.ID, domain.RoleAdmin); err != nil {
		t.Fatalf("root agent->admin: %v", err)
	}
	if _, err := svc.ChangeRole(ctx, root, member.ID, domain.RoleRoot); !errors.Is(err, domain.ErrRootProtected) {
		t.Fatalf("root grant root = %v, want root protected", err)
	}

	stored, err := users.GetByID(ctx, member.ID)
	if err != nil {
		t.Fatalf("get changed member: %v", err)
	}
	if stored.Role != domain.RoleAdmin {
		t.Fatalf("member role = %q, want %q", stored.Role, domain.RoleAdmin)
	}
}

func TestRoleChangesRoundTripWithActorAudit(t *testing.T) {
	ctx := context.Background()
	svc, users, _ := newUserService()
	admin := users.seedRole("Admin", "admin@example.com", domain.RoleAdmin, true)
	root := users.seedRole("Root", "root@example.com", domain.RoleRoot, true)
	member := users.seedRole("Member", "member@example.com", domain.RoleUser, true)

	for _, role := range []domain.Role{domain.RoleAgent, domain.RoleUser} {
		if _, err := svc.ChangeRole(ctx, admin, member.ID, role); err != nil {
			t.Fatalf("admin role round trip to %s: %v", role, err)
		}
	}
	for _, role := range []domain.Role{domain.RoleAdmin, domain.RoleAgent} {
		if _, err := svc.ChangeRole(ctx, root, member.ID, role); err != nil {
			t.Fatalf("root role round trip to %s: %v", role, err)
		}
	}
	if len(users.roleChanges) != 4 {
		t.Fatalf("role audits = %d, want 4", len(users.roleChanges))
	}
	for _, change := range users.roleChanges {
		if change.actorID != admin.ID && change.actorID != root.ID {
			t.Errorf("audit actor = %d, want admin or root", change.actorID)
		}
	}
}

// S3.1 RED: managed edits combine identity, role, and account status in one
// use case. A rejected role transition must leave every field unchanged.
func TestUpdateManagedUserIsAtomicAndAuditsRoleChanges(t *testing.T) {
	ctx := context.Background()
	svc, users, _ := newUserService()
	admin := users.seedRole("Admin", "admin@example.com", domain.RoleAdmin, true)
	member := users.seedRole("Member", "member@example.com", domain.RoleUser, true)

	updated, err := svc.UpdateManagedUser(ctx, admin, member.ID, application.UpdateManagedUserInput{
		Name:   "Member Updated",
		Email:  "member.updated@example.com",
		Role:   domain.RoleAgent,
		Active: false,
	})
	if err != nil {
		t.Fatalf("UpdateManagedUser: %v", err)
	}
	if updated.Name != "Member Updated" || updated.Email != "member.updated@example.com" || updated.Role != domain.RoleAgent || updated.Active {
		t.Fatalf("updated user = %+v, want combined identity, role, and inactive state", updated)
	}
	if len(users.roleChanges) != 1 || users.roleChanges[0].actorID != admin.ID {
		t.Fatalf("role audit = %+v, want one change by %d", users.roleChanges, admin.ID)
	}

	_, err = svc.UpdateManagedUser(ctx, admin, member.ID, application.UpdateManagedUserInput{
		Name:   "Forbidden Change",
		Email:  "forbidden@example.com",
		Role:   domain.RoleAdmin,
		Active: true,
	})
	if err == nil {
		t.Fatal("admin promotion must be rejected")
	}
	stored, err := users.GetByID(ctx, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != "Member Updated" || stored.Email != "member.updated@example.com" || stored.Role != domain.RoleAgent || stored.Active {
		t.Fatalf("rejected edit changed stored user = %+v", stored)
	}
}

// S3.1 RED: passwords use their own hash-only use case and cannot be changed
// through UpdateManagedUser.
func TestChangePasswordUpdatesOnlyTheHash(t *testing.T) {
	ctx := context.Background()
	svc, users, _ := newUserService()
	member := users.seedRole("Member", "member@example.com", domain.RoleUser, true)
	member.PasswordHash = "old-hash"
	if err := users.Update(ctx, &member); err != nil {
		t.Fatal(err)
	}

	if err := svc.ChangePassword(ctx, managerActor, member.ID, "new-secret"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	stored, err := users.GetByID(ctx, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != "Member" || stored.Email != "member@example.com" || stored.Role != domain.RoleUser || !stored.Active {
		t.Fatalf("password change mutated non-password fields: %+v", stored)
	}
	if !application.VerifyPassword(stored.PasswordHash, "new-secret") {
		t.Fatal("new password hash must verify")
	}
	if err := svc.ChangePassword(ctx, managerActor, member.ID, " "); err == nil {
		t.Fatal("blank password must be rejected")
	}
}
