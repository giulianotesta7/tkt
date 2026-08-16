package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestGroupServiceAllowsOnlyAdminAndRootToManageGroups(t *testing.T) {
	store := newFakeGroupStore()
	svc := application.NewGroupService(store, newFakeUserStore(), fixedClock())
	ctx := context.Background()

	for _, actor := range []domain.User{{ID: 1, Role: domain.RoleAdmin}, {ID: 2, Role: domain.RoleRoot}} {
		group, err := svc.Create(ctx, actor, "Support")
		if err != nil {
			t.Fatalf("%s Create: %v", actor.Role, err)
		}
		if group.Name != "Support" || group.ID == 0 {
			t.Fatalf("%s Create = %+v, want stored group", actor.Role, group)
		}
		if err := svc.Delete(ctx, actor, group.ID); err != nil {
			t.Fatalf("%s Delete: %v", actor.Role, err)
		}
	}

	for _, role := range []domain.Role{domain.RoleUser, domain.RoleAgent} {
		before := store.mutations
		_, err := svc.Create(ctx, domain.User{ID: 3, Role: role}, "Forbidden")
		var forbidden *domain.ForbiddenError
		if !errors.As(err, &forbidden) {
			t.Errorf("%s Create error = %v, want ErrForbidden", role, err)
		}
		if store.mutations != before {
			t.Errorf("%s Create called the store despite denial", role)
		}
	}
}

func TestGroupServiceRejectsUserMembersBeforeStoreMutation(t *testing.T) {
	store := newFakeGroupStore()
	users := newFakeUserStore()
	user := users.seedRole("Customer", "customer@example.com", domain.RoleUser, true)
	svc := application.NewGroupService(store, users, fixedClock())
	admin := domain.User{ID: 1, Role: domain.RoleAdmin}

	group, err := svc.Create(context.Background(), admin, "Support")
	if err != nil {
		t.Fatal(err)
	}
	before := store.mutations
	err = svc.AddMember(context.Background(), admin, group.ID, user.ID)
	if err == nil {
		t.Fatal("user-role membership must be rejected")
	}
	if store.mutations != before {
		t.Fatal("rejected user-role membership must not mutate the store")
	}
}

type fakeGroupStore struct {
	groups    map[int64]*domain.Group
	members   map[int64]map[int64]bool
	nextID    int64
	mutations int
}

func newFakeGroupStore() *fakeGroupStore {
	return &fakeGroupStore{groups: map[int64]*domain.Group{}, members: map[int64]map[int64]bool{}, nextID: 1}
}

func (s *fakeGroupStore) Create(_ context.Context, group *domain.Group) error {
	for _, existing := range s.groups {
		if existing.Name == group.Name {
			return &domain.DuplicateError{Kind: "group", Name: group.Name}
		}
	}
	group.ID = s.nextID
	s.nextID++
	cp := *group
	s.groups[group.ID] = &cp
	s.mutations++
	return nil
}
func (s *fakeGroupStore) Update(_ context.Context, group *domain.Group) error {
	s.groups[group.ID] = group
	s.mutations++
	return nil
}
func (s *fakeGroupStore) Delete(_ context.Context, id int64) error {
	delete(s.groups, id)
	s.mutations++
	return nil
}
func (s *fakeGroupStore) GetByID(_ context.Context, id int64) (*domain.Group, error) {
	return s.groups[id], nil
}
func (s *fakeGroupStore) List(_ context.Context) ([]domain.Group, error) { return nil, nil }
func (s *fakeGroupStore) AddMember(_ context.Context, groupID, userID int64, _ time.Time) error {
	if s.members[groupID] == nil {
		s.members[groupID] = map[int64]bool{}
	}
	s.members[groupID][userID] = true
	s.mutations++
	return nil
}
func (s *fakeGroupStore) RemoveMember(_ context.Context, groupID, userID int64) error {
	delete(s.members[groupID], userID)
	s.mutations++
	return nil
}
func (s *fakeGroupStore) ListMembers(_ context.Context, _ int64) ([]domain.User, error) {
	return nil, nil
}
