package application

import (
	"context"
	"fmt"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Password policy settings (issue #414, S1/S4). The two switches and the
// minimum length are ONE instance configuration: they are read on every page
// that offers a password path and on every write that stores a new password.
const (
	// DefaultPasswordMinLength is the minimum length a new instance enforces
	// until an administrator changes it (S4: "default de minimo 12
	// caracteres").
	DefaultPasswordMinLength = 12
	// MinPasswordMinLength and MaxPasswordMinLength bound the configurable
	// band (S4: "configurable desde la config de la instancia"), resolved as
	// 8-64. A value outside the band is refused before the store is touched.
	MinPasswordMinLength = 8
	MaxPasswordMinLength = 64
)

// PasswordSettings is the instance password policy as the settings section
// edits it and every password write reads it. Both switches start ON (S1).
type PasswordSettings struct {
	// ChangeEnabled allows a user to change their own password from
	// Preferences (S2).
	ChangeEnabled bool
	// RecoveryEnabled offers "Forgot your password?" on the sign-in page (S3).
	RecoveryEnabled bool
	// MinLength is the smallest accepted NEW password, in characters.
	MinLength int
}

// DefaultPasswordSettings is the policy of an instance that never saved the
// section: both paths ON and the documented default minimum.
func DefaultPasswordSettings() PasswordSettings {
	return PasswordSettings{
		ChangeEnabled:   true,
		RecoveryEnabled: true,
		MinLength:       DefaultPasswordMinLength,
	}
}

// PasswordSettingsInput is one submission of the passwords section.
type PasswordSettingsInput struct {
	ChangeEnabled   bool
	RecoveryEnabled bool
	MinLength       int
}

// PasswordSettingsService implements the self-service password configuration
// (issue #414, S1). Reads are ungated because every password surface asks for
// the switches; the write is admin/root only through CapManageSettings — the
// same instance-configuration capability the other settings sections use.
type PasswordSettingsService struct {
	store PasswordSettingsStore
}

// NewPasswordSettingsService wires the password-policy use cases against their
// own port.
func NewPasswordSettingsService(store PasswordSettingsStore) *PasswordSettingsService {
	return &PasswordSettingsService{store: store}
}

// Get answers the stored policy.
func (s *PasswordSettingsService) Get(ctx context.Context) (PasswordSettings, error) {
	return s.store.GetPasswordSettings(ctx)
}

// Update validates one submission and writes it. The actor must hold
// CapManageSettings (admin/root), and the minimum must sit inside the
// configurable band: both refusals happen BEFORE the store is touched, so a
// rejected submission leaves the previous policy standing.
func (s *PasswordSettingsService) Update(ctx context.Context, actor domain.User, in PasswordSettingsInput) error {
	if err := requireManageSettings(actor, "password settings are not permitted"); err != nil {
		return err
	}
	if in.MinLength < MinPasswordMinLength || in.MinLength > MaxPasswordMinLength {
		return PasswordMinLengthRangeError()
	}
	return s.store.SetPasswordSettings(ctx, PasswordSettings{
		ChangeEnabled:   in.ChangeEnabled,
		RecoveryEnabled: in.RecoveryEnabled,
		MinLength:       in.MinLength,
	})
}

// PasswordMinLengthRangeError builds the ONE refusal for a minimum outside the
// band (S4: 8-64). It is exported because the HTTP layer refuses a submission
// that is not a whole number before the service is ever reached, and both
// refusals must read as the same sentence.
func PasswordMinLengthRangeError() *domain.ValidationError {
	return &domain.ValidationError{
		Field:   "password_min_length",
		Message: fmt.Sprintf("the minimum password length must be between %d and %d characters", MinPasswordMinLength, MaxPasswordMinLength),
		Key:     "error.password_min_length",
		Args:    []any{MinPasswordMinLength, MaxPasswordMinLength},
	}
}
