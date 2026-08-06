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

// Create validates name, email, and password, stores the bcrypt hash (D15),
// and returns the new active user.
func (s *UserService) Create(ctx context.Context, in CreateUserInput) (*domain.User, error) {
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
	u := &domain.User{
		Name:         name,
		Email:        email,
		PasswordHash: hash,
		Active:       true,
		CreatedAt:    s.clock.Now(),
	}
	if err := s.users.Create(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

// UpdateUserInput describes optional user edits (name, email, password,
// active). A nil pointer means "not provided".
type UpdateUserInput struct {
	Name     *string
	Email    *string
	Password *string
	Active   *bool
}

// Update applies the provided fields. Email uniqueness applies to the new
// email (store); a password change stores a new bcrypt hash; Active=false
// deactivates without touching historical ticket assignments.
func (s *UserService) Update(ctx context.Context, id int64, in UpdateUserInput) (*domain.User, error) {
	if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
		return nil, &domain.ValidationError{Field: "name", Message: domain.ErrMsgUserNameRequired}
	}
	if in.Email != nil && strings.TrimSpace(*in.Email) == "" {
		return nil, &domain.ValidationError{Field: "email", Message: domain.ErrMsgUserEmailRequired}
	}
	if in.Password != nil && strings.TrimSpace(*in.Password) == "" {
		return nil, &domain.ValidationError{Field: "password", Message: domain.ErrMsgPasswordRequired}
	}

	u, err := s.users.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		u.Name = strings.TrimSpace(*in.Name)
	}
	if in.Email != nil {
		u.Email = strings.TrimSpace(*in.Email)
	}
	if in.Password != nil {
		hash, err := HashPassword(*in.Password)
		if err != nil {
			return nil, err
		}
		u.PasswordHash = hash
	}
	if in.Active != nil {
		u.Active = *in.Active
	}
	if err := s.users.Update(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

// Delete removes an unreferenced user; a referenced user is rejected with a
// ReferencedError (deactivation is the removal path).
func (s *UserService) Delete(ctx context.Context, id int64) error {
	return s.users.Delete(ctx, id)
}

// GetByID returns the user, including inactive ones (historical display).
func (s *UserService) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	return s.users.GetByID(ctx, id)
}

// List returns all managed users.
func (s *UserService) List(ctx context.Context) ([]domain.User, error) {
	return s.users.List(ctx)
}
