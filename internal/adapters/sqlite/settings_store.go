package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/giulianotesta7/tkt/internal/application"
)

// settingsStore implements application.SettingsStore over the keyed
// settings table (0005_instance_settings.sql). The internal-comment
// background row is seeded by the migration; a missing row reads back the
// default so a hand-edited database degrades to the known-good color.
type settingsStore struct {
	db *sql.DB
}

var _ application.SettingsStore = (*settingsStore)(nil)

func newSettingsStore(db *sql.DB) *settingsStore { return &settingsStore{db: db} }

// settingsKeyInternalCommentBg is the settings row key for the
// internal-comment background color.
const settingsKeyInternalCommentBg = "internal_comment_bg"

// GetInternalCommentBg returns the configured color, or the application
// default when the row is absent.
func (ss *settingsStore) GetInternalCommentBg(ctx context.Context) (string, error) {
	var value string
	err := ss.db.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = ?`, settingsKeyInternalCommentBg).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return application.DefaultInternalCommentBg, nil
	}
	if err != nil {
		return "", fmt.Errorf("sqlite: get %s: %w", settingsKeyInternalCommentBg, err)
	}
	return value, nil
}

// SetInternalCommentBg upserts the color row (single-row instance setting).
func (ss *settingsStore) SetInternalCommentBg(ctx context.Context, color string) error {
	_, err := ss.db.ExecContext(ctx, `
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		settingsKeyInternalCommentBg, color)
	if err != nil {
		return fmt.Errorf("sqlite: set %s: %w", settingsKeyInternalCommentBg, err)
	}
	return nil
}
