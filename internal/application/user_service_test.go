package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func newUserService() (*application.UserService, *fakeUserStore, *fakeClock) {
	clock := fixedClock()
	users := newFakeUserStore()
	return application.NewUserService(users, clock), users, clock
}

func TestCreateUserStoresActiveWithHashedPassword(t *testing.T) {
	svc, users, clock := newUserService()

	u, err := svc.Create(context.Background(), application.CreateUserInput{
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

	_, err := svc.Create(context.Background(), application.CreateUserInput{
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
			_, err := svc.Create(context.Background(), tc.in)
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
	created, err := svc.Create(context.Background(), application.CreateUserInput{
		Name: "Ana", Email: "ana@example.com", Password: "old-pass",
	})
	if err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}
	oldHash := created.PasswordHash

	newName := "Ana Maria"
	newEmail := "ana.maria@example.com"
	newPassword := "new-pass"
	updated, err := svc.Update(context.Background(), created.ID, application.UpdateUserInput{
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
	_, err := svc.Update(context.Background(), beto.ID, application.UpdateUserInput{Email: &newEmail})
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
	created, err := svc.Create(context.Background(), application.CreateUserInput{
		Name: "Ana", Email: "ana@example.com", Password: "x",
	})
	if err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}

	blank := "   "
	_, err = svc.Update(context.Background(), created.ID, application.UpdateUserInput{Name: &blank})
	var verr *domain.ValidationError
	if !errors.As(err, &verr) || verr.Field != "name" {
		t.Fatalf("Update: blank name must be a ValidationError on field name, got %v", err)
	}
}

func TestDeactivateUserKeepsHistoricalData(t *testing.T) {
	svc, users, _ := newUserService()
	created, err := svc.Create(context.Background(), application.CreateUserInput{
		Name: "Ana", Email: "ana@example.com", Password: "x",
	})
	if err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}

	inactive := false
	deactivated, err := svc.Update(context.Background(), created.ID, application.UpdateUserInput{Active: &inactive})
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

	err := svc.Delete(context.Background(), ana.ID)
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

	if err := svc.Delete(context.Background(), ana.ID); err != nil {
		t.Fatalf("Delete: unreferenced user must be deletable, got %v", err)
	}
	if _, err := users.GetByID(context.Background(), ana.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Delete: user must be removed from the managed list, got %v", err)
	}
}

func TestUserServiceList(t *testing.T) {
	svc, _, _ := newUserService()
	svc.Create(context.Background(), application.CreateUserInput{Name: "Ana", Email: "ana@example.com", Password: "x"})
	svc.Create(context.Background(), application.CreateUserInput{Name: "Beto", Email: "beto@example.com", Password: "x"})

	list, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List: unexpected error: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List: 2 users expected, got %d", len(list))
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
