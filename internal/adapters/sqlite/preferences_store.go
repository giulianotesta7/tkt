package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/giulianotesta7/tkt/internal/application"
)

// preferencesStore implements application.PreferencesStore over the
// user_preferences table (0018_user_preferences.sql) and, on the same rows,
// application.EmailPreferences (issue #362, S5). The table is generic
// (user_id, key, value); the queue-order key is the first consumer and the
// email opt-out is the second. The store is deliberately dumb: it persists and
// reads back whatever it is given, because the closed-set validation and the
// fail-closed read normalization both live in the application service.
type preferencesStore struct {
	db *sql.DB
}

var (
	_ application.PreferencesStore = (*preferencesStore)(nil)
	_ application.EmailPreferences = (*preferencesStore)(nil)
)

// preferencesKeyQueueOrder is the user_preferences row key for the default
// queue order (issue #210).
const preferencesKeyQueueOrder = "queue_order"

// preferencesKeyEmailNotifications is the user_preferences row key for the
// email opt-out (issue #362, S5). Its stored values are exactly "on" and
// "off"; an absent row means on, and any other stored value reads as on.
const preferencesKeyEmailNotifications = "email_notifications"

func newPreferencesStore(db *sql.DB) *preferencesStore { return &preferencesStore{db: db} }

// PreferencesStore returns the per-user preferences port (issue #210).
func (s *Store) PreferencesStore() application.PreferencesStore { return newPreferencesStore(s.db) }

// EmailPreferences returns the per-user email opt-out store (issue #362, S5).
// It is a second view of the same user_preferences table, so the queue-order
// port stays unchanged. The concrete store is returned because the preference
// screen must both read and WRITE the toggle, while the background sender
// depends on the read-only application.EmailPreferences interface this value
// satisfies (asserted above).
func (s *Store) EmailPreferences() *preferencesStore { return newPreferencesStore(s.db) }

// GetQueueOrder returns the stored default queue order, or "" when the user
// has no row. An absent row is not an error — it is the "no preference yet"
// state the service normalizes to the default.
func (ps *preferencesStore) GetQueueOrder(ctx context.Context, userID int64) (string, error) {
	var value string
	err := ps.db.QueryRowContext(ctx,
		`SELECT value FROM user_preferences WHERE user_id = ? AND "key" = ?`,
		userID, preferencesKeyQueueOrder).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("sqlite: get %s for user %d: %w", preferencesKeyQueueOrder, userID, err)
	}
	return value, nil
}

// SetQueueOrder upserts the user's stored default queue order. The composite
// primary key makes the ON CONFLICT target unambiguous, so a second save
// updates the row instead of duplicating it.
func (ps *preferencesStore) SetQueueOrder(ctx context.Context, userID int64, order string) error {
	_, err := ps.db.ExecContext(ctx, `
		INSERT INTO user_preferences (user_id, "key", value) VALUES (?, ?, ?)
		ON CONFLICT(user_id, "key") DO UPDATE SET value = excluded.value`,
		userID, preferencesKeyQueueOrder, order)
	if err != nil {
		return fmt.Errorf("sqlite: set %s for user %d: %w", preferencesKeyQueueOrder, userID, err)
	}
	return nil
}

// preferencesEmailOn and preferencesEmailOff are the only two values this key
// stores. Anything else is treated as on by the read below, so a hand-edited
// or restored row can never silently stop a person's email.
const (
	preferencesEmailOn  = "on"
	preferencesEmailOff = "off"
)

// EmailNotificationsEnabled reports whether the user wants notification email
// (issue #362, S5). An absent row is the documented default — email is on —
// and only the exact stored value "off" turns it off; every other value falls
// back to on. The read writes nothing, so opening Preferences never creates a
// row.
func (ps *preferencesStore) EmailNotificationsEnabled(ctx context.Context, userID int64) (bool, error) {
	var value string
	err := ps.db.QueryRowContext(ctx,
		`SELECT value FROM user_preferences WHERE user_id = ? AND "key" = ?`,
		userID, preferencesKeyEmailNotifications).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("sqlite: get %s for user %d: %w", preferencesKeyEmailNotifications, userID, err)
	}
	return value != preferencesEmailOff, nil
}

// SetEmailNotificationsEnabled upserts the user's email choice as "on" or
// "off". The composite primary key makes the ON CONFLICT target unambiguous,
// so toggling the preference twice updates one row instead of accumulating
// one per save.
func (ps *preferencesStore) SetEmailNotificationsEnabled(ctx context.Context, userID int64, enabled bool) error {
	value := preferencesEmailOn
	if !enabled {
		value = preferencesEmailOff
	}
	_, err := ps.db.ExecContext(ctx, `
		INSERT INTO user_preferences (user_id, "key", value) VALUES (?, ?, ?)
		ON CONFLICT(user_id, "key") DO UPDATE SET value = excluded.value`,
		userID, preferencesKeyEmailNotifications, value)
	if err != nil {
		return fmt.Errorf("sqlite: set %s for user %d: %w", preferencesKeyEmailNotifications, userID, err)
	}
	return nil
}
