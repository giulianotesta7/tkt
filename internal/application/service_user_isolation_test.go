package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Issue #488, S3: a service user is not a person. The people screens never list
// it, and no ordinary management route (edit, reactivate, role, password,
// delete) reaches it; each refusal leaves the account exactly as it was. Only
// the API keys screen manages a service user.
func TestServiceUserStaysOutOfPeopleManagement(t *testing.T) {
	ctx := context.Background()
	svc, users, _ := newUserService()
	ana := users.seedRole("Ana", "ana@example.com", domain.RoleAgent, true)
	bot := users.seedService("triage-bot", domain.RoleAgent)
	before := *users.users[bot.ID]

	list, err := svc.List(ctx, managerActor)
	if err != nil || len(list) != 1 || list[0].ID != ana.ID {
		t.Fatalf("List = %+v (err %v), want only the person", list, err)
	}
	assignable, err := svc.ListAssignable(ctx, managerActor)
	if err != nil {
		t.Fatalf("ListAssignable: %v", err)
	}
	for _, u := range assignable {
		if u.ID == bot.ID {
			t.Fatalf("ListAssignable offers the service user: %+v", assignable)
		}
	}

	_, err = svc.UpdateManagedUser(ctx, managerActor, bot.ID, application.UpdateManagedUserInput{
		Name: "Bot", Email: "bot@example.com", Role: domain.RoleUser, Active: false,
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("managed edit of a service user = %v, want ErrNotFound", err)
	}
	if _, err := svc.ChangeRole(ctx, managerActor, bot.ID, domain.RoleUser); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("role change of a service user = %v, want ErrNotFound", err)
	}
	if err := svc.ChangePassword(ctx, managerActor, bot.ID, "s3cret-pass-12"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("password of a service user = %v, want ErrNotFound", err)
	}
	if err := svc.Delete(ctx, managerActor, bot.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("delete of a service user = %v, want ErrNotFound", err)
	}
	if after := *users.users[bot.ID]; after != before {
		t.Errorf("a refused route changed the service user:\nbefore=%+v\nafter=%+v", before, after)
	}
	if len(users.roleChanges) != 0 {
		t.Errorf("role audits = %d, want 0", len(users.roleChanges))
	}
}
