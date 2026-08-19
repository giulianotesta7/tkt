package application

import (
	"context"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// DefaultInternalCommentBg is the seeded instance color (migration 0005)
// and the allowed-color default the store falls back to when the row is
// absent.
const DefaultInternalCommentBg = "#E8EEFF"

// AllowedInternalCommentBg returns the instance colors an admin may
// choose for the internal-comment background (azul/violeta/amarillo).
// Green is intentionally reserved for a future "commented as solution"
// comment type, so it is not offered here.
func AllowedInternalCommentBg() []string {
	return []string{DefaultInternalCommentBg, "#EFE9FB", "#FFF6DC"}
}

// isAllowedInternalCommentBg reports whether color is one of the
// selectable instance colors.
func isAllowedInternalCommentBg(color string) bool {
	for _, c := range AllowedInternalCommentBg() {
		if c == color {
			return true
		}
	}
	return false
}

// SettingsService implements the instance appearance use cases
// (appearance-settings spec): read the current internal-comment background
// and update it. Updates are admin/root-only (CapManageUsers — the same
// capability that gates managed-user screens) and validated against the
// allowed color set BEFORE the store is reached, so a rejected value
// changes nothing.
type SettingsService struct {
	settings SettingsStore
}

// NewSettingsService wires the appearance use cases against the settings
// port.
func NewSettingsService(settings SettingsStore) *SettingsService {
	return &SettingsService{settings: settings}
}

// GetAppearance returns the current internal-comment background color.
func (s *SettingsService) GetAppearance(ctx context.Context) (string, error) {
	return s.settings.GetInternalCommentBg(ctx)
}

// SetInternalCommentBg updates the internal-comment background color.
// The actor must hold CapManageUsers (admin/root); color must be one of
// AllowedInternalCommentBg — anything else is a ValidationError and the
// store is never touched (fail closed on invalid input).
func (s *SettingsService) SetInternalCommentBg(ctx context.Context, actor domain.User, color string) error {
	if !NewPolicy().Capabilities(actor.Role).Require(CapManageUsers) {
		return domain.NewForbiddenError("appearance settings are not permitted")
	}
	if !isAllowedInternalCommentBg(color) {
		return &domain.ValidationError{Field: "internal_comment_bg", Message: "invalid internal comment background color"}
	}
	return s.settings.SetInternalCommentBg(ctx, color)
}
