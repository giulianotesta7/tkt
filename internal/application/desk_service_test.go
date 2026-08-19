package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestDeskServiceAllowsOnlyAdminAndRootToManageDesks(t *testing.T) {
	store := newFakeDeskStore()
	svc := application.NewDeskService(store, newFakeUserStore(), fixedClock())
	ctx := context.Background()

	for _, actor := range []domain.User{{ID: 1, Role: domain.RoleAdmin}, {ID: 2, Role: domain.RoleRoot}} {
		desk, err := svc.Create(ctx, actor, "Support")
		if err != nil {
			t.Fatalf("%s Create: %v", actor.Role, err)
		}
		if desk.Name != "Support" || desk.ID == 0 {
			t.Fatalf("%s Create = %+v, want stored desk", actor.Role, desk)
		}
		if err := svc.Delete(ctx, actor, desk.ID); err != nil {
			t.Fatalf("%s Delete: %v", actor.Role, err)
		}
	}

	for _, role := range []domain.Role{domain.RoleUser, domain.RoleAgent} {
		actor := domain.User{ID: 3, Role: role}
		desk, err := svc.Create(ctx, adminActorForDeskTest(), "Protected-"+string(role))
		if err != nil {
			t.Fatalf("setup desk: %v", err)
		}
		for _, operation := range []struct {
			name string
			run  func() error
		}{
			{"create", func() error { _, err := svc.Create(ctx, actor, "Forbidden"); return err }},
			{"rename", func() error { _, err := svc.Rename(ctx, actor, desk.ID, "Renamed"); return err }},
			{"delete", func() error { return svc.Delete(ctx, actor, desk.ID) }},
		} {
			before := store.mutations
			err := operation.run()
			var forbidden *domain.ForbiddenError
			if !errors.As(err, &forbidden) {
				t.Errorf("%s %s error = %v, want ErrForbidden", role, operation.name, err)
			}
			if store.mutations != before {
				t.Errorf("%s %s called the store despite denial", role, operation.name)
			}
		}
	}
}

func adminActorForDeskTest() domain.User { return domain.User{ID: 1, Role: domain.RoleAdmin} }

func TestDeskServiceRejectsUserMembersBeforeStoreMutation(t *testing.T) {
	store := newFakeDeskStore()
	users := newFakeUserStore()
	user := users.seedRole("Customer", "customer@example.com", domain.RoleUser, true)
	svc := application.NewDeskService(store, users, fixedClock())
	admin := domain.User{ID: 1, Role: domain.RoleAdmin}

	desk, err := svc.Create(context.Background(), admin, "Support")
	if err != nil {
		t.Fatal(err)
	}
	before := store.mutations
	err = svc.AddMember(context.Background(), admin, desk.ID, user.ID)
	if err == nil {
		t.Fatal("user-role membership must be rejected")
	}
	if store.mutations != before {
		t.Fatal("rejected user-role membership must not mutate the store")
	}
}

type fakeDeskStore struct {
	desks     map[int64]*domain.Desk
	members   map[int64]map[int64]bool
	nextID    int64
	mutations int
}

func newFakeDeskStore() *fakeDeskStore {
	return &fakeDeskStore{desks: map[int64]*domain.Desk{}, members: map[int64]map[int64]bool{}, nextID: 1}
}

func (s *fakeDeskStore) Create(_ context.Context, desk *domain.Desk) error {
	for _, existing := range s.desks {
		if existing.Name == desk.Name {
			return &domain.DuplicateError{Kind: "desk", Name: desk.Name}
		}
	}
	desk.ID = s.nextID
	s.nextID++
	cp := *desk
	s.desks[desk.ID] = &cp
	s.mutations++
	return nil
}
func (s *fakeDeskStore) Update(_ context.Context, desk *domain.Desk) error {
	s.desks[desk.ID] = desk
	s.mutations++
	return nil
}
func (s *fakeDeskStore) Delete(_ context.Context, id int64) error {
	delete(s.desks, id)
	s.mutations++
	return nil
}
func (s *fakeDeskStore) GetByID(_ context.Context, id int64) (*domain.Desk, error) {
	return s.desks[id], nil
}
func (s *fakeDeskStore) List(_ context.Context) ([]domain.Desk, error) { return nil, nil }
func (s *fakeDeskStore) AddMember(_ context.Context, deskID, userID int64, _ time.Time) error {
	if s.members[deskID] == nil {
		s.members[deskID] = map[int64]bool{}
	}
	s.members[deskID][userID] = true
	s.mutations++
	return nil
}
func (s *fakeDeskStore) RemoveMember(_ context.Context, deskID, userID int64) error {
	delete(s.members[deskID], userID)
	s.mutations++
	return nil
}
func (s *fakeDeskStore) ListMembers(_ context.Context, _ int64) ([]domain.User, error) {
	return nil, nil
}
