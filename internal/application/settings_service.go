package application

import (
	"context"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// DefaultInternalCommentBg is the seeded instance color (migration 0005)
// and the allowed-color default the store falls back to when the row is
// absent.
const DefaultInternalCommentBg = "#E8EEFF"

// DefaultSLAWarningPercent is the seeded SLA warning threshold percentage
// (migration 0014) and the fallback the store reads when the row is
// absent or unparseable (issue #211).
const DefaultSLAWarningPercent = 80

// Interface language settings (issue #335, S7): the interface language is an
// INSTANCE setting, chosen by an administrator, so one choice applies to
// every user of that instance. Both catalogues ship (internal/i18n) and
// English is the default, which keeps a default instance rendering exactly
// the copy it rendered before the catalogue existed.
const (
	// DefaultInterfaceLanguage is the interface language an instance renders
	// until an administrator chooses another one.
	DefaultInterfaceLanguage = "en"
	// InterfaceLanguageEnglish and InterfaceLanguageSpanish are the shipped
	// language codes. They are the stored values, so they never change with
	// display copy.
	InterfaceLanguageEnglish = "en"
	InterfaceLanguageSpanish = "es"
)

// AllowedInterfaceLanguages returns the interface languages an administrator
// may choose, in the order the settings screen offers them.
func AllowedInterfaceLanguages() []string {
	return []string{InterfaceLanguageEnglish, InterfaceLanguageSpanish}
}

// isAllowedInterfaceLanguage reports whether code is one of the shipped
// interface languages. The codes are exact: a case variant or a regional tag
// is refused rather than quietly normalized, so what an administrator saved
// is exactly what the instance renders with.
func isAllowedInterfaceLanguage(code string) bool {
	for _, allowed := range AllowedInterfaceLanguages() {
		if code == allowed {
			return true
		}
	}
	return false
}

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

// SettingsService implements the instance configuration use cases
// (appearance-settings spec): read the current internal-comment background
// and update it, and select the instance-wide interface language (issue
// #335). Updates are admin/root-only via CapManageSettings — the
// instance-configuration capability, deliberately separate from
// CapManageUsers — and validated against the allowed set BEFORE the
// store is reached, so a rejected value changes nothing.
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

// GetInterfaceLanguage returns the instance's active interface language.
// The store normalizes an absent row — and an unknown persisted value — to
// DefaultInterfaceLanguage, so this always answers a language the binary
// ships.
func (s *SettingsService) GetInterfaceLanguage(ctx context.Context) (string, error) {
	return s.settings.GetInterfaceLanguage(ctx)
}

// SetInterfaceLanguage selects the instance's interface language (issue
// #335). The actor must hold CapManageSettings (admin/root) — the same
// instance-configuration capability the appearance setting uses — and the
// language must be one of AllowedInterfaceLanguages; anything else is a
// ValidationError and the store is never touched (fail closed on invalid
// input).
func (s *SettingsService) SetInterfaceLanguage(ctx context.Context, actor domain.User, language string) error {
	if err := requireManageSettings(actor, "interface language settings are not permitted"); err != nil {
		return err
	}
	if !isAllowedInterfaceLanguage(language) {
		return &domain.ValidationError{Field: "interface_language", Message: "invalid interface language", Key: "error.invalid_interface_language"}
	}
	return s.settings.SetInterfaceLanguage(ctx, language)
}

// SetInternalCommentBg updates the internal-comment background color.
// The actor must hold CapManageSettings (admin/root); color must be one of
// AllowedInternalCommentBg — anything else is a ValidationError and the
// store is never touched (fail closed on invalid input).
func (s *SettingsService) SetInternalCommentBg(ctx context.Context, actor domain.User, color string) error {
	if err := requireManageSettings(actor, "appearance settings are not permitted"); err != nil {
		return err
	}
	if !isAllowedInternalCommentBg(color) {
		return &domain.ValidationError{Field: "internal_comment_bg", Message: "invalid internal comment background color", Key: "error.invalid_internal_comment_bg"}
	}
	return s.settings.SetInternalCommentBg(ctx, color)
}
