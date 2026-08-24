package application

import (
	"context"
	"strings"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// UserService implements the managed-user use cases (user-management spec):
// create/update/deactivate/delete. Deletion of referenced users is rejected
// (the store enforces it); deactivation preserves historical assignments.
type UserService struct {
	users UserStore
	clock domain.Clock
}

// NewUserService wires the user use cases against the given ports.
func NewUserService(users UserStore, clock domain.Clock) *UserService {
	return &UserService{users: users, clock: clock}
}

// CreateUserInput is the creation payload (user-management create-user).
type CreateUserInput struct {
	Name     string
	Email    string
	Password string
}

// prepareUser validates the create payload, hashes the password (D15), and
// builds an active user carrying the given role, stamped by the injected
// clock. Create and BootstrapRoot share it so both use cases enforce the
// exact same input contract (non-empty name/email, bcrypt-only storage).
func (s *UserService) prepareUser(in CreateUserInput, role domain.Role) (*domain.User, error) {
	name := strings.TrimSpace(in.Name)
	email := strings.TrimSpace(in.Email)
	if name == "" {
		return nil, &domain.ValidationError{Field: "name", Message: domain.ErrMsgUserNameRequired}
	}
	if email == "" {
		return nil, &domain.ValidationError{Field: "email", Message: domain.ErrMsgUserEmailRequired}
	}
	hash, err := HashPassword(in.Password)
	if err != nil {
		return nil, err
	}
	return &domain.User{
		Name:         name,
		Email:        email,
		PasswordHash: hash,
		Role:         role,
		Active:       true,
		CreatedAt:    s.clock.Now(),
	}, nil
}

// Create validates an authorized management actor, then validates name, email,
// and password, stores the bcrypt hash (D15),
// and returns the new active user with the user role. Root is only ever
// created by BootstrapRoot, never through user creation.
func (s *UserService) Create(ctx context.Context, actor domain.User, in CreateUserInput) (*domain.User, error) {
	if !NewPolicy().Capabilities(actor.Role).Require(CapManageUsers) {
		return nil, domain.NewForbiddenError("user management is not permitted")
	}
	u, err := s.prepareUser(in, domain.RoleUser)
	if err != nil {
		return nil, err
	}
	if err := s.users.Create(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

// BootstrapRoot creates the very first user with role root (role-authorization
// "First-User Root Bootstrap"): validates like Create, hashes, and hands an
// active root to the store's atomic conditional insert. It is the ONLY use
// case that may create a root — every other creation/role-grant path must
// reject the root role. Concurrent setup submissions produce exactly one
// root; the loser gets ErrBootstrapUnavailable and creates nothing.
func (s *UserService) BootstrapRoot(ctx context.Context, in CreateUserInput) (*domain.User, error) {
	u, err := s.prepareUser(in, domain.RoleRoot)
	if err != nil {
		return nil, err
	}
	if err := s.users.BootstrapRoot(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

// UpdateManagedUserInput is the complete non-password edit submitted by the
// managed-user form. Password changes intentionally use ChangePassword.
type UpdateManagedUserInput struct {
	Name   string
	Email  string
	Role   domain.Role
	Active bool
}

// UpdateManagedUser applies identity, role, and active state as one guarded
// store transaction. The root and peer-admin protections are enforced before
// the store is reached; admins cannot grant or manage admin accounts.
func (s *UserService) UpdateManagedUser(ctx context.Context, actor domain.User, id int64, in UpdateManagedUserInput) (*domain.User, error) {
	policy := NewPolicy()
	if !policy.Capabilities(actor.Role).Require(CapManageUsers) {
		return nil, domain.NewForbiddenError("user management is not permitted")
	}
	if strings.TrimSpace(in.Name) == "" {
		return nil, &domain.ValidationError{Field: "name", Message: domain.ErrMsgUserNameRequired}
	}
	if strings.TrimSpace(in.Email) == "" {
		return nil, &domain.ValidationError{Field: "email", Message: domain.ErrMsgUserEmailRequired}
	}
	if !in.Role.Valid() {
		return nil, &domain.ValidationError{Field: "role", Message: "invalid role"}
	}
	if in.Role == domain.RoleRoot {
		return nil, domain.NewRootProtectedError()
	}
	if !policy.CanGrantUserRole(actor.Role, in.Role) {
		return nil, domain.NewForbiddenError("admin accounts require root")
	}

	u, err := s.users.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	targetRole := u.Role
	if targetRole == "" {
		targetRole = domain.RoleUser
	}
	if targetRole == domain.RoleRoot {
		return nil, domain.NewRootProtectedError()
	}
	if !policy.CanManageUser(actor.Role, targetRole) {
		return nil, domain.NewForbiddenError("admin accounts require root")
	}
	expectedRole := u.Role
	u.Name = strings.TrimSpace(in.Name)
	u.Email = strings.TrimSpace(in.Email)
	u.Role = in.Role
	u.Active = in.Active
	if err := s.users.UpdateManagedUser(ctx, u, expectedRole, actor.ID, s.clock.Now()); err != nil {
		return nil, err
	}
	return u, nil
}

// ChangePassword hashes a non-empty secret then replaces only the target's
// password hash. It shares the managed-user authorization and protections.
func (s *UserService) ChangePassword(ctx context.Context, actor domain.User, id int64, password string) error {
	policy := NewPolicy()
	if !policy.Capabilities(actor.Role).Require(CapManageUsers) {
		return domain.NewForbiddenError("user management is not permitted")
	}
	if strings.TrimSpace(password) == "" {
		return &domain.ValidationError{Field: "password", Message: domain.ErrMsgPasswordRequired}
	}
	u, err := s.users.GetByID(ctx, id)
	if err != nil {
		return err
	}
	targetRole := u.Role
	if targetRole == "" {
		targetRole = domain.RoleUser
	}
	if targetRole == domain.RoleRoot {
		return domain.NewRootProtectedError()
	}
	if !policy.CanManageUser(actor.Role, targetRole) {
		return domain.NewForbiddenError("admin accounts require root")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	return s.users.UpdatePasswordHash(ctx, id, hash)
}

// UpdateUserInput and Update are retained for non-HTTP compatibility. New
// managed-user flows must use UpdateManagedUser and ChangePassword directly.
type UpdateUserInput struct {
	Name     *string
	Email    *string
	Password *string
	Active   *bool
}

func (s *UserService) Update(ctx context.Context, actor domain.User, id int64, in UpdateUserInput) (*domain.User, error) {
	u, err := s.users.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		u.Name = *in.Name
	}
	if in.Email != nil {
		u.Email = *in.Email
	}
	if in.Active != nil {
		u.Active = *in.Active
	}
	if u.Role == "" {
		u.Role = domain.RoleUser
	}
	updated, err := s.UpdateManagedUser(ctx, actor, id, UpdateManagedUserInput{Name: u.Name, Email: u.Email, Role: u.Role, Active: u.Active})
	if err != nil || in.Password == nil {
		return updated, err
	}
	if err := s.ChangePassword(ctx, actor, id, *in.Password); err != nil {
		return nil, err
	}
	return s.users.GetByID(ctx, id)
}

// ChangeRole is retained for non-HTTP compatibility. New flows use
// UpdateManagedUser so role changes are atomic with the form edit.
func (s *UserService) ChangeRole(ctx context.Context, actor domain.User, id int64, to domain.Role) (*domain.User, error) {
	u, err := s.users.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.UpdateManagedUser(ctx, actor, id, UpdateManagedUserInput{Name: u.Name, Email: u.Email, Role: to, Active: u.Active})
}

// Delete removes an unreferenced user; a referenced user is rejected with a
// ReferencedError (deactivation is the removal path). The root account is
// never deletable by any actor (role-authorization root invariants) — the
// guard rejects it before the store is reached; the DB trigger is the
// hard backstop for any other path.
func (s *UserService) Delete(ctx context.Context, actor domain.User, id int64) error {
	policy := NewPolicy()
	if !policy.Capabilities(actor.Role).Require(CapManageUsers) {
		return domain.NewForbiddenError("user management is not permitted")
	}
	u, err := s.users.GetByID(ctx, id)
	if err != nil {
		return err
	}
	targetRole := u.Role
	if targetRole == "" {
		targetRole = domain.RoleUser
	}
	if targetRole == domain.RoleRoot {
		return domain.NewRootProtectedError()
	}
	if !policy.CanManageUser(actor.Role, targetRole) {
		return domain.NewForbiddenError("admin accounts require root")
	}
	return s.users.Delete(ctx, id)
}

// GetByID returns the user, including inactive ones (historical display).
func (s *UserService) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	return s.users.GetByID(ctx, id)
}

// List returns all managed users to an authorized management actor.
func (s *UserService) List(ctx context.Context, actor domain.User) ([]domain.User, error) {
	if !NewPolicy().Capabilities(actor.Role).Require(CapManageUsers) {
		return nil, domain.NewForbiddenError("user management is not permitted")
	}
	return s.users.List(ctx)
}

// ListAssignable returns active agent-plus users for assignment controls. A
// user-role actor receives no candidates, preventing managed-user disclosure.
func (s *UserService) ListAssignable(ctx context.Context, actor domain.User) ([]domain.User, error) {
	if !NewPolicy().Capabilities(actor.Role).Require(CapAssignTicket) {
		return nil, nil
	}
	users, err := s.users.ListActive(ctx)
	if err != nil {
		return nil, err
	}
	assignable := make([]domain.User, 0, len(users))
	for _, u := range users {
		if u.Role.AtLeast(domain.RoleAgent) {
			assignable = append(assignable, u)
		}
	}
	return assignable, nil
}
