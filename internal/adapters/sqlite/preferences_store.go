package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/giulianotesta7/tkt/internal/application"
)

// preferencesStore implements application.PreferencesStore over the
// user_preferences table (0018_user_preferences.sql). The table is generic
// (user_id, key, value); the queue-order key is the first consumer. The
// store is deliberately dumb: it persists and reads back whatever it is
// given, because the closed-set validation and the fail-closed read
// normalization both live in the application service.
type preferencesStore struct {
	db *sql.DB
}

var _ application.PreferencesStore = (*preferencesStore)(nil)

// preferencesKeyQueueOrder is the user_preferences row key for the default
// queue order (issue #210).
const preferencesKeyQueueOrder = "queue_order"

func newPreferencesStore(db *sql.DB) *preferencesStore { return &preferencesStore{db: db} }

// PreferencesStore returns the per-user preferences port (issue #210).
func (s *Store) PreferencesStore() application.PreferencesStore { return newPreferencesStore(s.db) }

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
