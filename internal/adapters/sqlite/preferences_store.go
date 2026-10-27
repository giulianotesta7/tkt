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
	_ application.PreferencesStore              = (*preferencesStore)(nil)
	_ application.EmailPreferences              = (*preferencesStore)(nil)
	_ application.SidebarPreferencesStore       = (*preferencesStore)(nil)
	_ application.ThemePreferencesStore         = (*preferencesStore)(nil)
	_ application.TimelineOrderPreferencesStore = (*preferencesStore)(nil)
)

// preferencesKeyQueueOrder is the user_preferences row key for the default
// queue order (issue #210).
const preferencesKeyQueueOrder = "queue_order"

// preferencesKeyEmailNotifications is the user_preferences row key for the
// email opt-out (issue #362, S5). Its stored values are exactly "on" and
// "off"; an absent row means on, and any other stored value reads as on.
const preferencesKeyEmailNotifications = "email_notifications"

// preferencesKeySidebar is the user_preferences row key for the collapsed
// sidebar (issue #377, S2). Its stored values are exactly "expanded" and
// "collapsed"; an absent row means expanded, and any other stored value reads
// as expanded.
const preferencesKeySidebar = "sidebar"

// preferencesKeyTheme is the user_preferences row key for the interface theme
// (issue #378, S1). Its storable values are exactly "system", "light", and
// "dark"; an absent row means no choice yet, and the application service
// normalizes that — and any other stored value — to the system default.
const preferencesKeyTheme = "theme"

// preferencesKeyTimelineOrder is the user_preferences row key for the ticket
// activity order (issue #438, S1). Its storable values are exactly "newest" and
// "oldest"; an absent row means no choice yet, and the application service
// normalizes that — and any other stored value — to the newest-first default.
const preferencesKeyTimelineOrder = "timeline_order"

const preferencesKeyLanguage = "language"

func (ps *preferencesStore) GetLanguage(ctx context.Context, userID int64) (string, error) {
	var value string
	err := ps.db.QueryRowContext(ctx, `SELECT value FROM user_preferences WHERE user_id = ? AND "key" = ?`, userID, preferencesKeyLanguage).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("sqlite: get %s for user %d: %w", preferencesKeyLanguage, userID, err)
	}
	return value, nil
}

func (ps *preferencesStore) SetLanguage(ctx context.Context, userID int64, language string) error {
	_, err := ps.db.ExecContext(ctx, `INSERT INTO user_preferences (user_id, "key", value) VALUES (?, ?, ?) ON CONFLICT(user_id, "key") DO UPDATE SET value = excluded.value`, userID, preferencesKeyLanguage, language)
	if err != nil {
		return fmt.Errorf("sqlite: set %s for user %d: %w", preferencesKeyLanguage, userID, err)
	}
	return nil
}

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

// SidebarPreferences returns the per-user sidebar-state store (issue #377,
// S2). It is a third view of the same user_preferences table, so the
// queue-order and email ports stay unchanged. The port is enough for every
// caller — the middleware stamps the state and the toggle writes it — so the
// application interface is what this accessor answers.
func (s *Store) SidebarPreferences() application.SidebarPreferencesStore {
	return newPreferencesStore(s.db)
}

// ThemePreferences returns the per-user theme store (issue #378, S1). It is a
// fourth view of the same user_preferences table, so the queue-order, email,
// and sidebar ports stay unchanged. The port is enough for every caller — the
// middleware stamps the theme and the preferences form writes it — so the
// application interface is what this accessor answers.
func (s *Store) ThemePreferences() application.ThemePreferencesStore {
	return newPreferencesStore(s.db)
}

// TimelineOrderPreferences returns the per-user ticket-activity-order store
// (issue #438, S1). It is a fifth view of the same user_preferences table, so the
// queue-order, email, sidebar, and theme ports stay unchanged. The preferences
// service discovers it on the queue-order port it already receives, which is the
// same concrete store, so a wired deployment gains the preference without a
// second construction step; this accessor is what the adapter tests and any
// future explicit wiring use.
func (s *Store) TimelineOrderPreferences() application.TimelineOrderPreferencesStore {
	return newPreferencesStore(s.db)
}

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

// preferencesSidebarExpanded and preferencesSidebarCollapsed are the only two
// values this key stores. Anything else is treated as expanded by the read
// below, so a hand-edited or restored row always renders the shell a user can
// find their way out of.
const (
	preferencesSidebarExpanded  = "expanded"
	preferencesSidebarCollapsed = "collapsed"
)

// SidebarCollapsed reports whether the user's sidebar is collapsed (issue
// #377, S2). An absent row is the documented default — the sidebar is expanded
// — and only the exact stored value "collapsed" collapses it; every other
// value falls back to expanded. The read writes nothing, so rendering a page
// never creates a row.
func (ps *preferencesStore) SidebarCollapsed(ctx context.Context, userID int64) (bool, error) {
	var value string
	err := ps.db.QueryRowContext(ctx,
		`SELECT value FROM user_preferences WHERE user_id = ? AND "key" = ?`,
		userID, preferencesKeySidebar).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("sqlite: get %s for user %d: %w", preferencesKeySidebar, userID, err)
	}
	return value == preferencesSidebarCollapsed, nil
}

// SetSidebarCollapsed upserts the user's sidebar state as "collapsed" or
// "expanded". The composite primary key makes the ON CONFLICT target
// unambiguous, so toggling the sidebar twice updates one row instead of
// accumulating one per toggle.
func (ps *preferencesStore) SetSidebarCollapsed(ctx context.Context, userID int64, collapsed bool) error {
	value := preferencesSidebarExpanded
	if collapsed {
		value = preferencesSidebarCollapsed
	}
	_, err := ps.db.ExecContext(ctx, `
		INSERT INTO user_preferences (user_id, "key", value) VALUES (?, ?, ?)
		ON CONFLICT(user_id, "key") DO UPDATE SET value = excluded.value`,
		userID, preferencesKeySidebar, value)
	if err != nil {
		return fmt.Errorf("sqlite: set %s for user %d: %w", preferencesKeySidebar, userID, err)
	}
	return nil
}

// GetTheme returns the stored theme, or "" when the user has no row. An absent
// row is not an error — it is the "no choice yet" state the service normalizes
// to the system default. The value is returned verbatim: the closed set is the
// application's contract, and this adapter has no opinion about it.
func (ps *preferencesStore) GetTheme(ctx context.Context, userID int64) (string, error) {
	var value string
	err := ps.db.QueryRowContext(ctx,
		`SELECT value FROM user_preferences WHERE user_id = ? AND "key" = ?`,
		userID, preferencesKeyTheme).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("sqlite: get %s for user %d: %w", preferencesKeyTheme, userID, err)
	}
	return value, nil
}

// SetTheme upserts the user's stored theme. The composite primary key makes the
// ON CONFLICT target unambiguous, so choosing a theme twice updates one row
// instead of accumulating one per save. The value is written verbatim, exactly
// like the queue order: the service has already validated it.
func (ps *preferencesStore) SetTheme(ctx context.Context, userID int64, theme string) error {
	_, err := ps.db.ExecContext(ctx, `
		INSERT INTO user_preferences (user_id, "key", value) VALUES (?, ?, ?)
		ON CONFLICT(user_id, "key") DO UPDATE SET value = excluded.value`,
		userID, preferencesKeyTheme, theme)
	if err != nil {
		return fmt.Errorf("sqlite: set %s for user %d: %w", preferencesKeyTheme, userID, err)
	}
	return nil
}

// GetTimelineOrder returns the stored activity order, or "" when the user has no
// row. An absent row is not an error — it is the "no choice yet" state the
// service normalizes to the newest-first default. The value is returned
// verbatim: the closed set is the application's contract, and this adapter has no
// opinion about it.
func (ps *preferencesStore) GetTimelineOrder(ctx context.Context, userID int64) (string, error) {
	var value string
	err := ps.db.QueryRowContext(ctx,
		`SELECT value FROM user_preferences WHERE user_id = ? AND "key" = ?`,
		userID, preferencesKeyTimelineOrder).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("sqlite: get %s for user %d: %w", preferencesKeyTimelineOrder, userID, err)
	}
	return value, nil
}

// SetTimelineOrder upserts the user's stored activity order. The composite
// primary key makes the ON CONFLICT target unambiguous, so flipping the order
// twice updates one row instead of accumulating one per save. The value is
// written verbatim, exactly like the queue order and the theme: the service has
// already validated it.
func (ps *preferencesStore) SetTimelineOrder(ctx context.Context, userID int64, order string) error {
	_, err := ps.db.ExecContext(ctx, `
		INSERT INTO user_preferences (user_id, "key", value) VALUES (?, ?, ?)
		ON CONFLICT(user_id, "key") DO UPDATE SET value = excluded.value`,
		userID, preferencesKeyTimelineOrder, order)
	if err != nil {
		return fmt.Errorf("sqlite: set %s for user %d: %w", preferencesKeyTimelineOrder, userID, err)
	}
	return nil
}
