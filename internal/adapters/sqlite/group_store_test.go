package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Group persistence covers the group-management CRUD and N:N membership
// contract over the real 0003 schema.
func TestGroupStoreCRUDAndMembership(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	store := newGroupStore(s.db)

	group := &domain.Group{Name: "Support", CreatedAt: testClock}
	if err := store.Create(ctx, group); err != nil {
		t.Fatalf("create group: %v", err)
	}
	if group.ID == 0 {
		t.Fatal("create group did not assign an ID")
	}

	agentID := seedUser(t, s, "Agent", "agent@example.com", true)
	adminID := seedUserRaw(t, s, "Admin", "admin@example.com", "admin")
	if err := store.AddMember(ctx, group.ID, agentID, testClock); err != nil {
		t.Fatalf("add agent member: %v", err)
	}
	if err := store.AddMember(ctx, group.ID, adminID, testClock); err != nil {
		t.Fatalf("add admin member: %v", err)
	}

	members, err := store.ListMembers(ctx, group.ID)
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if len(members) != 2 || members[0].ID != agentID || members[1].ID != adminID {
		t.Fatalf("members = %+v, want agent then admin", members)
	}

	group.Name = "Customer Support"
	if err := store.Update(ctx, group); err != nil {
		t.Fatalf("rename group: %v", err)
	}
	groups, err := store.List(ctx)
	if err != nil {
		t.Fatalf("list groups: %v", err)
	}
	if len(groups) != 1 || groups[0].Name != "Customer Support" {
		t.Fatalf("groups = %+v, want renamed group", groups)
	}

	if err := store.RemoveMember(ctx, group.ID, agentID); err != nil {
		t.Fatalf("remove member: %v", err)
	}
	members, err = store.ListMembers(ctx, group.ID)
	if err != nil {
		t.Fatalf("list members after remove: %v", err)
	}
	if len(members) != 1 || members[0].ID != adminID {
		t.Fatalf("members after remove = %+v, want only admin", members)
	}

	if err := store.Delete(ctx, group.ID); err != nil {
		t.Fatalf("delete group: %v", err)
	}
	if _, err := store.GetByID(ctx, group.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get deleted group = %v, want ErrNotFound", err)
	}
}

func TestGroupStoreRejectsDuplicateNameAndUserMember(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	store := newGroupStore(s.db)

	group := &domain.Group{Name: "Support", CreatedAt: testClock}
	if err := store.Create(ctx, group); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(ctx, &domain.Group{Name: "Support", CreatedAt: testClock}); !errors.Is(err, domain.ErrDuplicate) {
		t.Fatalf("duplicate create = %v, want ErrDuplicate", err)
	}

	userID := seedUserRaw(t, s, "Customer", "customer@example.com", "user")
	err := store.AddMember(ctx, group.ID, userID, testClock)
	if err == nil {
		t.Fatal("adding a user-role member must be rejected")
	}
	members, err := store.ListMembers(ctx, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 0 {
		t.Fatalf("rejected user member must not persist, got %+v", members)
	}
}
