package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	// Embedded IANA tz database: the sla_calendar_timezone key holds an
	// IANA name, and a resolvable timezone must not depend on the host's
	// zoneinfo being installed (slim containers have none).
	_ "time/tzdata"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
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

// settingsKeyInterfaceLanguage is the settings row key for the instance
// interface language (issue #335). No migration seeds it: an absent row reads
// back DefaultInterfaceLanguage, so the default is true without a stored row
// and the key/value table needs no new column or migration.
const settingsKeyInterfaceLanguage = "interface_language"

// SLA settings row keys (migration 0014, issue #211).
const (
	settingsKeySLAEnabled        = "sla_enabled"
	settingsKeySLAWarningPercent = "sla_warning_percent"
	settingsKeySLAEnabledAt      = "sla_enabled_at"
)

// settingsKeyHoldAutoCloseDays is the settings row key for the automatic hold
// expiry window (issue #353). No migration seeds it: an absent row reads back
// application.DefaultHoldAutoCloseWorkingDays (0, the feature off), so the
// default is true without a stored row and the key/value table needs neither a
// new column nor a migration.
const settingsKeyHoldAutoCloseDays = "hold_auto_close_working_days"

// SLA working-calendar row keys (migration 0017, issue #211) and their
// documented defaults: Monday-Friday, 09:00-18:00, UTC — the same values
// domain.DefaultSLACalendar answers, in their stored text form.
const (
	settingsKeySLACalendarDays        = "sla_calendar_days"
	settingsKeySLACalendarStartMinute = "sla_calendar_start_minute"
	settingsKeySLACalendarEndMinute   = "sla_calendar_end_minute"
	settingsKeySLACalendarTimezone    = "sla_calendar_timezone"
)

const (
	defaultSLACalendarDays        = "1,2,3,4,5"
	defaultSLACalendarStartMinute = 9 * 60
	defaultSLACalendarEndMinute   = 18 * 60
)

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

// GetInterfaceLanguage returns the instance interface language (issue #335).
// An absent, empty, or unknown value answers DefaultInterfaceLanguage, so the
// read never fails for configuration reasons and always answers a language
// the binary ships — the same degradation the internal-comment background read
// uses for a hand-edited database.
func (ss *settingsStore) GetInterfaceLanguage(ctx context.Context) (string, error) {
	value, err := ss.settingValue(ctx, settingsKeyInterfaceLanguage)
	if err != nil {
		return "", err
	}
	if !isShippedInterfaceLanguage(value) {
		return application.DefaultInterfaceLanguage, nil
	}
	return value, nil
}

// SetInterfaceLanguage upserts the interface-language row. A code no shipped
// catalogue answers is refused rather than persisted: this is the layer the
// NEXT read will answer from, so accepting it would leave the instance
// declaring a language it cannot render while both the write and the read
// reported success.
func (ss *settingsStore) SetInterfaceLanguage(ctx context.Context, language string) error {
	if !isShippedInterfaceLanguage(language) {
		return fmt.Errorf("sqlite: set %s: unsupported language %q", settingsKeyInterfaceLanguage, language)
	}
	_, err := ss.db.ExecContext(ctx, `
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		settingsKeyInterfaceLanguage, language)
	if err != nil {
		return fmt.Errorf("sqlite: set %s: %w", settingsKeyInterfaceLanguage, err)
	}
	return nil
}

// isShippedInterfaceLanguage reports whether code is one of the interface
// languages an administrator may select (application.AllowedInterfaceLanguages).
func isShippedInterfaceLanguage(code string) bool {
	for _, allowed := range application.AllowedInterfaceLanguages() {
		if code == allowed {
			return true
		}
	}
	return false
}

// GetSLAEnabled reports whether the per-category SLA is enabled. The row
// stores '0'/'1' text; an absent or unparseable value falls back to
// disabled (fail-safe: enabling is an explicit admin action).
func (ss *settingsStore) GetSLAEnabled(ctx context.Context) (bool, error) {
	var value string
	err := ss.db.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = ?`, settingsKeySLAEnabled).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("sqlite: get %s: %w", settingsKeySLAEnabled, err)
	}
	return value == "1", nil
}

// GetSLAWarningPercent returns the SLA warning threshold percentage, or
// DefaultSLAWarningPercent when the row is absent or unparseable.
func (ss *settingsStore) GetSLAWarningPercent(ctx context.Context) (int, error) {
	var value string
	err := ss.db.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = ?`, settingsKeySLAWarningPercent).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return application.DefaultSLAWarningPercent, nil
	}
	if err != nil {
		return 0, fmt.Errorf("sqlite: get %s: %w", settingsKeySLAWarningPercent, err)
	}
	n, parseErr := strconv.Atoi(value)
	if parseErr != nil {
		return application.DefaultSLAWarningPercent, nil
	}
	return n, nil
}

// GetHoldAutoCloseDays returns the automatic hold expiry window of issue #353,
// or DefaultHoldAutoCloseWorkingDays (0, off) when the row is absent,
// unparseable, or outside 0..60. A stored value the write path refuses is not
// trusted to close tickets: failing safe to "off" keeps a hand-edited database
// from silently closing held tickets on a window nobody configured.
func (ss *settingsStore) GetHoldAutoCloseDays(ctx context.Context) (int, error) {
	var value string
	err := ss.db.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = ?`, settingsKeyHoldAutoCloseDays).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return application.DefaultHoldAutoCloseWorkingDays, nil
	}
	if err != nil {
		return 0, fmt.Errorf("sqlite: get %s: %w", settingsKeyHoldAutoCloseDays, err)
	}
	days, parseErr := strconv.Atoi(value)
	if parseErr != nil || days < 0 || days > application.MaxHoldAutoCloseWorkingDays {
		return application.DefaultHoldAutoCloseWorkingDays, nil
	}
	return days, nil
}

// SetHoldAutoCloseDays upserts the hold auto-close window as text (the keyed
// settings table stores every value as text, like the SLA percent next to it).
func (ss *settingsStore) SetHoldAutoCloseDays(ctx context.Context, days int) error {
	_, err := ss.db.ExecContext(ctx, `
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		settingsKeyHoldAutoCloseDays, strconv.Itoa(days))
	if err != nil {
		return fmt.Errorf("sqlite: set %s: %w", settingsKeyHoldAutoCloseDays, err)
	}
	return nil
}

// SetSLAEnabledState persists sla_enabled and, when enabling, the
// activation instant, in ONE transaction: a failure cannot leave the
// feature switched on with no instant explaining it.
//
// The instant records when the feature was FIRST switched on. It is
// written with ON CONFLICT DO NOTHING, so an existing value survives both
// a second enable and a disable.
func (ss *settingsStore) SetSLAEnabledState(ctx context.Context, enabled bool, at time.Time) error {
	tx, err := beginImmediate(ctx, ss.db, "set sla enabled state")
	if err != nil {
		return err
	}
	defer tx.Rollback()

	value := "0"
	if enabled {
		value = "1"
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		settingsKeySLAEnabled, value); err != nil {
		return fmt.Errorf("sqlite: set %s: %w", settingsKeySLAEnabled, err)
	}
	if enabled {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO settings (key, value) VALUES (?, ?)
			ON CONFLICT(key) DO NOTHING`,
			settingsKeySLAEnabledAt, formatTime(at)); err != nil {
			return fmt.Errorf("sqlite: set %s: %w", settingsKeySLAEnabledAt, err)
		}
	}
	return tx.Commit()
}

// SetSLAConfiguration writes the whole instance SLA panel in ONE
// transaction: the enable flag and its activation instant, the warning
// percent, and all four default targets. An administrator's edit is one
// configuration, so a failure must leave none of it applied.
//
// The instant records the FIRST enable, so it is written with ON CONFLICT DO
// NOTHING and survives a re-enable and a disable.
func (ss *settingsStore) SetSLAConfiguration(ctx context.Context, enabled bool, at time.Time, warningPercent int, defaults []domain.SLAPolicy) error {
	tx, err := beginImmediate(ctx, ss.db, "set sla configuration")
	if err != nil {
		return err
	}
	defer tx.Rollback()

	value := "0"
	if enabled {
		value = "1"
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		settingsKeySLAEnabled, value); err != nil {
		return fmt.Errorf("sqlite: set %s: %w", settingsKeySLAEnabled, err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		settingsKeySLAWarningPercent, strconv.Itoa(warningPercent)); err != nil {
		return fmt.Errorf("sqlite: set %s: %w", settingsKeySLAWarningPercent, err)
	}
	if enabled {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO settings (key, value) VALUES (?, ?)
			ON CONFLICT(key) DO NOTHING`,
			settingsKeySLAEnabledAt, formatTime(at)); err != nil {
			return fmt.Errorf("sqlite: set %s: %w", settingsKeySLAEnabledAt, err)
		}
	}
	for _, p := range defaults {
		if err := upsertDefaultTx(ctx, tx, p); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetSLAWarningPercent persists the sla_warning_percent setting.
func (ss *settingsStore) SetSLAWarningPercent(ctx context.Context, percent int) error {
	_, err := ss.db.ExecContext(ctx, `
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		settingsKeySLAWarningPercent, strconv.Itoa(percent))
	if err != nil {
		return fmt.Errorf("sqlite: set %s: %w", settingsKeySLAWarningPercent, err)
	}
	return nil
}

// GetSLAEnabledAt returns the activation instant recorded when the SLA
// settings were first configured. Informational display only. An absent
// or unparseable row returns the zero time.
func (ss *settingsStore) GetSLAEnabledAt(ctx context.Context) (time.Time, error) {
	var value string
	err := ss.db.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = ?`, settingsKeySLAEnabledAt).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("sqlite: get %s: %w", settingsKeySLAEnabledAt, err)
	}
	ts, parseErr := time.Parse(timeLayout, value)
	if parseErr != nil {
		return time.Time{}, nil
	}
	return ts, nil
}

// GetSLACalendar assembles the instance working calendar from the four
// sla_calendar_* keys (migration 0017). Each key falls back to its
// documented default when its row is absent or unparseable, and an
// unknown timezone name falls back to UTC — the read never fails for
// configuration reasons, so the calendar a ticket freeze consumes is
// always usable. Only a real storage failure is an error.
//
// Per-key fallback alone does not make the WHOLE usable: independently
// defaulted values can compose into an invalid calendar (a stored start
// at or after the end, for instance). The ASSEMBLED value is validated
// too, and an invalid composition degrades to DefaultSLACalendar, so this
// read never answers a calendar whose Valid() is false.
func (ss *settingsStore) GetSLACalendar(ctx context.Context) (domain.SLACalendar, error) {
	days, err := ss.slaCalendarDays(ctx)
	if err != nil {
		return domain.SLACalendar{}, err
	}
	start, err := ss.slaCalendarMinute(ctx, settingsKeySLACalendarStartMinute, defaultSLACalendarStartMinute)
	if err != nil {
		return domain.SLACalendar{}, err
	}
	end, err := ss.slaCalendarMinute(ctx, settingsKeySLACalendarEndMinute, defaultSLACalendarEndMinute)
	if err != nil {
		return domain.SLACalendar{}, err
	}
	loc, err := ss.slaCalendarLocation(ctx)
	if err != nil {
		return domain.SLACalendar{}, err
	}
	calendar := domain.SLACalendar{
		WorkingDays: days,
		StartMinute: start,
		EndMinute:   end,
		Location:    loc,
	}
	if !calendar.Valid() {
		return domain.DefaultSLACalendar(), nil
	}
	return calendar, nil
}

// slaCalendarDays reads the working weekdays. An absent or unparseable
// row (any malformed or out-of-range entry makes the WHOLE value
// unparseable — a half-parsed working week would silently change when
// every future ticket is due) falls back to the documented default.
func (ss *settingsStore) slaCalendarDays(ctx context.Context) ([]time.Weekday, error) {
	value, err := ss.settingValue(ctx, settingsKeySLACalendarDays)
	if err != nil {
		return nil, err
	}
	if value == "" {
		value = defaultSLACalendarDays
	}
	days, ok := parseSLACalendarDays(value)
	if !ok {
		days, _ = parseSLACalendarDays(defaultSLACalendarDays)
	}
	return days, nil
}

// slaCalendarMinute reads one window bound in minutes past local
// midnight. An absent, unparseable, or out-of-range row falls back to the
// documented default.
func (ss *settingsStore) slaCalendarMinute(ctx context.Context, key string, fallback int) (int, error) {
	value, err := ss.settingValue(ctx, key)
	if err != nil {
		return 0, err
	}
	n, parseErr := strconv.Atoi(value)
	if parseErr != nil || n < 0 || n > 1439 {
		return fallback, nil
	}
	return n, nil
}

// slaCalendarLocation reads the IANA zone the window is interpreted in.
// An absent row or an unknown name falls back to UTC rather than failing
// the read (documented port behavior).
func (ss *settingsStore) slaCalendarLocation(ctx context.Context) (*time.Location, error) {
	value, err := ss.settingValue(ctx, settingsKeySLACalendarTimezone)
	if err != nil {
		return nil, err
	}
	loc, loadErr := time.LoadLocation(value)
	if loadErr != nil {
		return time.UTC, nil
	}
	return loc, nil
}

// settingValue reads one settings row's value. An absent row answers ""
// — indistinguishable from an empty stored value, which every caller
// treats as unparseable and falls back — and a storage failure is
// returned, never swallowed.
func (ss *settingsStore) settingValue(ctx context.Context, key string) (string, error) {
	var value string
	err := ss.db.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("sqlite: get %s: %w", key, err)
	}
	return value, nil
}

// SetSLACalendar writes the WHOLE working calendar — the four
// sla_calendar_* keys — in ONE transaction, all or nothing: a partially
// applied calendar would change when every future ticket is due, so a
// failing write rolls every key back and leaves the previous calendar
// standing. The calendar must be Valid (in particular its Location is
// non-nil): validation belongs to the application service, and this store
// refuses rather than panics on an unusable value.
func (ss *settingsStore) SetSLACalendar(ctx context.Context, calendar domain.SLACalendar) error {
	if !calendar.Valid() {
		return fmt.Errorf("sqlite: set sla calendar: invalid calendar (working days, window, and a timezone are required)")
	}
	// The zone is persisted by NAME and resolved back on every read with a
	// UTC fallback, so a name that does not read back as the same zone would
	// shift the whole working window while both the write and the read
	// reported success. Refuse it here too: this is the layer that will
	// answer the next read.
	if !domain.LocationIsPortable(calendar.Location) {
		return fmt.Errorf("sqlite: set sla calendar: the timezone name must read back as the same zone")
	}
	tx, err := beginImmediate(ctx, ss.db, "set sla calendar")
	if err != nil {
		return err
	}
	defer tx.Rollback()

	writes := []struct {
		key   string
		value string
	}{
		{settingsKeySLACalendarDays, formatSLACalendarDays(calendar.WorkingDays)},
		{settingsKeySLACalendarStartMinute, strconv.Itoa(calendar.StartMinute)},
		{settingsKeySLACalendarEndMinute, strconv.Itoa(calendar.EndMinute)},
		{settingsKeySLACalendarTimezone, calendar.Location.String()},
	}
	for _, w := range writes {
		if err := setSettingTx(ctx, tx, w.key, w.value); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// setSettingTx upserts one settings row inside the CALLER's transaction:
// exactly one SQL statement, so the transactional writers above can share
// it.
func setSettingTx(ctx context.Context, tx *sql.Tx, key, value string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("sqlite: set %s: %w", key, err)
	}
	return nil
}

// parseSLACalendarDays parses the stored comma-separated ISO weekday
// numbers (1=Monday..7=Sunday). Any malformed or out-of-range entry makes
// the whole value unparseable; the caller falls back to the default.
func parseSLACalendarDays(value string) ([]time.Weekday, bool) {
	parts := strings.Split(value, ",")
	days := make([]time.Weekday, 0, len(parts))
	for _, part := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 1 || n > 7 {
			return nil, false
		}
		days = append(days, isoToWeekday(n))
	}
	return days, true
}

// formatSLACalendarDays serializes working weekdays back to the stored
// comma-separated ISO weekday form.
func formatSLACalendarDays(days []time.Weekday) string {
	nums := make([]string, 0, len(days))
	for _, d := range days {
		nums = append(nums, strconv.Itoa(weekdayToISO(d)))
	}
	return strings.Join(nums, ",")
}

// weekdayToISO maps time.Weekday (Sunday=0) to the ISO numbering the
// setting stores (1=Monday..7=Sunday); isoToWeekday is its inverse.
func weekdayToISO(d time.Weekday) int {
	if d == time.Sunday {
		return 7
	}
	return int(d)
}

func isoToWeekday(n int) time.Weekday {
	if n == 7 {
		return time.Sunday
	}
	return time.Weekday(n)
}
