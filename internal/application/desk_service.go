package application

import (
	"context"
	"strings"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// DeskService implements admin/root-managed desks. Authorization happens
// before every store read or mutation so direct HTTP access cannot expose or
// alter desk data.
type DeskService struct {
	desks DeskStore
	users UserStore
	clock domain.Clock
}

func NewDeskService(desks DeskStore, users UserStore, clock domain.Clock) *DeskService {
	return &DeskService{desks: desks, users: users, clock: clock}
}

func (s *DeskService) Create(ctx context.Context, actor domain.User, name string) (*domain.Desk, error) {
	return s.create(ctx, actor, name, "", nil)
}

// CreateWithDescription creates a Desk while preserving the legacy
// compatibility operation's optional Department behavior.
func (s *DeskService) CreateWithDescription(ctx context.Context, actor domain.User, name, description string) (*domain.Desk, error) {
	return s.create(ctx, actor, name, description, nil)
}

// CreateInDepartment creates a Desk in the selected catalog Department. The
// legacy Create operation remains available for workflow and compatibility
// callers and intentionally leaves DepartmentID nil.
func (s *DeskService) CreateInDepartment(ctx context.Context, actor domain.User, departmentID int64, name string) (*domain.Desk, error) {
	if departmentID <= 0 {
		return nil, &domain.ValidationError{Field: "department_id", Message: "department is required"}
	}
	return s.create(ctx, actor, name, "", &departmentID)
}

func (s *DeskService) create(ctx context.Context, actor domain.User, name, description string, departmentID *int64) (*domain.Desk, error) {
	if err := manageDesks(actor); err != nil {
		return nil, err
	}
	name, err := deskName(name)
	if err != nil {
		return nil, err
	}
	g := &domain.Desk{Name: name, Description: strings.TrimSpace(description), DepartmentID: departmentID, CreatedAt: s.clock.Now()}
	if err := s.desks.Create(ctx, g); err != nil {
		return nil, err
	}
	return g, nil
}

func (s *DeskService) Rename(ctx context.Context, actor domain.User, id int64, name string) (*domain.Desk, error) {
	return s.update(ctx, actor, id, name, nil, nil, false)
}

// RenameWithDescription updates a Desk's name and description while preserving
// its existing Department.
func (s *DeskService) RenameWithDescription(ctx context.Context, actor domain.User, id int64, name, description string) (*domain.Desk, error) {
	description = strings.TrimSpace(description)
	return s.update(ctx, actor, id, name, &description, nil, false)
}

// UpdateInDepartment renames and/or moves a Desk while retaining the same
// Desk record, members, workflows, and stable ID.
func (s *DeskService) UpdateInDepartment(ctx context.Context, actor domain.User, id, departmentID int64, name string) (*domain.Desk, error) {
	if departmentID <= 0 {
		return nil, &domain.ValidationError{Field: "department_id", Message: "department is required"}
	}
	return s.update(ctx, actor, id, name, nil, &departmentID, true)
}

func (s *DeskService) update(ctx context.Context, actor domain.User, id int64, name string, description *string, departmentID *int64, replaceDepartment bool) (*domain.Desk, error) {
	if err := manageDesks(actor); err != nil {
		return nil, err
	}
	name, err := deskName(name)
	if err != nil {
		return nil, err
	}
	g, err := s.desks.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	g.Name = name
	if description != nil {
		g.Description = *description
	}
	if replaceDepartment {
		g.DepartmentID = departmentID
	}
	if err := s.desks.Update(ctx, g); err != nil {
		return nil, err
	}
	return g, nil
}

func (s *DeskService) Delete(ctx context.Context, actor domain.User, id int64) error {
	if err := manageDesks(actor); err != nil {
		return err
	}
	return s.desks.Delete(ctx, id)
}

func (s *DeskService) GetByID(ctx context.Context, actor domain.User, id int64) (*domain.Desk, error) {
	if err := manageDesks(actor); err != nil {
		return nil, err
	}
	return s.desks.GetByID(ctx, id)
}

func (s *DeskService) List(ctx context.Context, actor domain.User) ([]domain.Desk, error) {
	if err := manageDesks(actor); err != nil {
		return nil, err
	}
	return s.desks.List(ctx)
}

func (s *DeskService) AddMember(ctx context.Context, actor domain.User, deskID, userID int64) error {
	if err := manageDesks(actor); err != nil {
		return err
	}
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if !u.Role.AtLeast(domain.RoleAgent) {
		return &domain.ValidationError{Field: "user", Message: "desk members must have agent, admin, or root role"}
	}
	return s.desks.AddMember(ctx, deskID, userID, s.clock.Now())
}

func (s *DeskService) RemoveMember(ctx context.Context, actor domain.User, deskID, userID int64) error {
	if err := manageDesks(actor); err != nil {
		return err
	}
	return s.desks.RemoveMember(ctx, deskID, userID)
}

func (s *DeskService) ListMembers(ctx context.Context, actor domain.User, deskID int64) ([]domain.User, error) {
	if err := manageDesks(actor); err != nil {
		return nil, err
	}
	return s.desks.ListMembers(ctx, deskID)
}

// ListEligibleMembers returns active agent-plus accounts for the membership
// form; user-role accounts are never offered as desk members.
func (s *DeskService) ListEligibleMembers(ctx context.Context, actor domain.User) ([]domain.User, error) {
	if err := manageDesks(actor); err != nil {
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

func manageDesks(actor domain.User) error {
	if !NewPolicy().Capabilities(actor.Role).Require(CapManageDesks) {
		return domain.NewForbiddenError("desk management not allowed")
	}
	return nil
}

func deskName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", &domain.ValidationError{Field: "name", Message: "desk name is required"}
	}
	return name, nil
}
