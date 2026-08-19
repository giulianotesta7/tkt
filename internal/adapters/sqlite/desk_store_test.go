package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Desk persistence covers the desk-management CRUD and N:N membership
// contract over the real 0003 schema.
func TestDeskStoreCRUDAndMembership(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	store := newDeskStore(s.db)

	desk := &domain.Desk{Name: "Support", CreatedAt: testClock}
	if err := store.Create(ctx, desk); err != nil {
		t.Fatalf("create desk: %v", err)
	}
	if desk.ID == 0 {
		t.Fatal("create desk did not assign an ID")
	}

	agentID := seedUser(t, s, "Agent", "agent@example.com", true)
	adminID := seedUserRaw(t, s, "Admin", "admin@example.com", "admin")
	if err := store.AddMember(ctx, desk.ID, agentID, testClock); err != nil {
		t.Fatalf("add agent member: %v", err)
	}
	if err := store.AddMember(ctx, desk.ID, adminID, testClock); err != nil {
		t.Fatalf("add admin member: %v", err)
	}

	members, err := store.ListMembers(ctx, desk.ID)
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if len(members) != 2 || members[0].ID != agentID || members[1].ID != adminID {
		t.Fatalf("members = %+v, want agent then admin", members)
	}

	desk.Name = "Customer Support"
	if err := store.Update(ctx, desk); err != nil {
		t.Fatalf("rename desk: %v", err)
	}
	desks, err := store.List(ctx)
	if err != nil {
		t.Fatalf("list desks: %v", err)
	}
	if len(desks) != 1 || desks[0].Name != "Customer Support" {
		t.Fatalf("desks = %+v, want renamed desk", desks)
	}

	if err := store.RemoveMember(ctx, desk.ID, agentID); err != nil {
		t.Fatalf("remove member: %v", err)
	}
	members, err = store.ListMembers(ctx, desk.ID)
	if err != nil {
		t.Fatalf("list members after remove: %v", err)
	}
	if len(members) != 1 || members[0].ID != adminID {
		t.Fatalf("members after remove = %+v, want only admin", members)
	}

	if err := store.Delete(ctx, desk.ID); err != nil {
		t.Fatalf("delete desk: %v", err)
	}
	if _, err := store.GetByID(ctx, desk.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get deleted desk = %v, want ErrNotFound", err)
	}
}

func TestDeskStoreRejectsDuplicateNameAndUserMember(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	store := newDeskStore(s.db)

	desk := &domain.Desk{Name: "Support", CreatedAt: testClock}
	if err := store.Create(ctx, desk); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(ctx, &domain.Desk{Name: "Support", CreatedAt: testClock}); !errors.Is(err, domain.ErrDuplicate) {
		t.Fatalf("duplicate create = %v, want ErrDuplicate", err)
	}

	userID := seedUserRaw(t, s, "Customer", "customer@example.com", "user")
	err := store.AddMember(ctx, desk.ID, userID, testClock)
	if err == nil {
		t.Fatal("adding a user-role member must be rejected")
	}
	members, err := store.ListMembers(ctx, desk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 0 {
		t.Fatalf("rejected user member must not persist, got %+v", members)
	}
}
