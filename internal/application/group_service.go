package application

import (
	"context"
	"strings"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// GroupService implements admin/root-managed groups. Authorization happens
// before every store read or mutation so direct HTTP access cannot expose or
// alter group data.
type GroupService struct {
	groups GroupStore
	users  UserStore
	clock  domain.Clock
}

func NewGroupService(groups GroupStore, users UserStore, clock domain.Clock) *GroupService {
	return &GroupService{groups: groups, users: users, clock: clock}
}

func (s *GroupService) Create(ctx context.Context, actor domain.User, name string) (*domain.Group, error) {
	if err := manageGroups(actor); err != nil {
		return nil, err
	}
	name, err := groupName(name)
	if err != nil {
		return nil, err
	}
	g := &domain.Group{Name: name, CreatedAt: s.clock.Now()}
	if err := s.groups.Create(ctx, g); err != nil {
		return nil, err
	}
	return g, nil
}

func (s *GroupService) Rename(ctx context.Context, actor domain.User, id int64, name string) (*domain.Group, error) {
	if err := manageGroups(actor); err != nil {
		return nil, err
	}
	name, err := groupName(name)
	if err != nil {
		return nil, err
	}
	g, err := s.groups.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	g.Name = name
	if err := s.groups.Update(ctx, g); err != nil {
		return nil, err
	}
	return g, nil
}

func (s *GroupService) Delete(ctx context.Context, actor domain.User, id int64) error {
	if err := manageGroups(actor); err != nil {
		return err
	}
	return s.groups.Delete(ctx, id)
}

func (s *GroupService) GetByID(ctx context.Context, actor domain.User, id int64) (*domain.Group, error) {
	if err := manageGroups(actor); err != nil {
		return nil, err
	}
	return s.groups.GetByID(ctx, id)
}

func (s *GroupService) List(ctx context.Context, actor domain.User) ([]domain.Group, error) {
	if err := manageGroups(actor); err != nil {
		return nil, err
	}
	return s.groups.List(ctx)
}

func (s *GroupService) AddMember(ctx context.Context, actor domain.User, groupID, userID int64) error {
	if err := manageGroups(actor); err != nil {
		return err
	}
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if !u.Role.AtLeast(domain.RoleAgent) {
		return &domain.ValidationError{Field: "user", Message: "group members must have agent, admin, or root role"}
	}
	return s.groups.AddMember(ctx, groupID, userID, s.clock.Now())
}

func (s *GroupService) RemoveMember(ctx context.Context, actor domain.User, groupID, userID int64) error {
	if err := manageGroups(actor); err != nil {
		return err
	}
	return s.groups.RemoveMember(ctx, groupID, userID)
}

func (s *GroupService) ListMembers(ctx context.Context, actor domain.User, groupID int64) ([]domain.User, error) {
	if err := manageGroups(actor); err != nil {
		return nil, err
	}
	return s.groups.ListMembers(ctx, groupID)
}

// ListEligibleMembers returns active agent-plus accounts for the membership
// form; user-role accounts are never offered as group members.
func (s *GroupService) ListEligibleMembers(ctx context.Context, actor domain.User) ([]domain.User, error) {
	if err := manageGroups(actor); err != nil {
		return nil, err
	}
	users, err := s.users.ListActive(ctx)
	if err != nil {
		return nil, err
	}
	eligible := make([]domain.User, 0, len(users))
	for _, user := range users {
		if user.Role.AtLeast(domain.RoleAgent) {
			eligible = append(eligible, user)
		}
	}
	return eligible, nil
}

func manageGroups(actor domain.User) error {
	if !NewPolicy().Capabilities(actor.Role).Require(CapManageGroups) {
		return domain.NewForbiddenError("group management not allowed")
	}
	return nil
}

func groupName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", &domain.ValidationError{Field: "name", Message: "group name is required"}
	}
	return name, nil
}
