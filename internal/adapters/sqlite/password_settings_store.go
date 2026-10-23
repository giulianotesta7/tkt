package sqlite

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/giulianotesta7/tkt/internal/application"
)

// settingsKeyPasswordChangeEnabled, settingsKeyPasswordRecoveryEnabled and
// settingsKeyPasswordMinLength are the settings rows of the instance password
// policy (issue #414, S1/S4). No migration seeds them: an absent row reads back
// the documented default, so a fresh instance offers both self-service paths
// with the default minimum without a stored row, and the key/value table needs
// no new column.
const (
	settingsKeyPasswordChangeEnabled   = "password_change_enabled"
	settingsKeyPasswordRecoveryEnabled = "password_recovery_enabled"
	settingsKeyPasswordMinLength       = "password_min_length"
)

var _ application.PasswordSettingsStore = (*settingsStore)(nil)

// GetPasswordSettings answers the instance password policy (issue #414,
// S1/S4). It never fails for configuration reasons: an absent or unparseable
// switch reads back ON (the shipped default, so a hand-edited database never
// silently removes a password path), and an absent, unparseable, or
// out-of-band minimum reads back the default 12 — a stored value the write path
// refuses is not trusted to weaken every password rule. Only a real storage
// failure is an error.
//
// The three rows are read in ONE statement, so the answer is always one saved
// policy: SetPasswordSettings commits all three together, and three separate
// reads could otherwise interleave with a concurrent save and return the
// minimum of one configuration next to the switches of another.
func (ss *settingsStore) GetPasswordSettings(ctx context.Context) (application.PasswordSettings, error) {
	values, err := ss.settingValues(ctx, settingsKeyPasswordChangeEnabled, settingsKeyPasswordRecoveryEnabled, settingsKeyPasswordMinLength)
	if err != nil {
		return application.PasswordSettings{}, err
	}
	return application.PasswordSettings{
		ChangeEnabled:   passwordSwitch(values[settingsKeyPasswordChangeEnabled]),
		RecoveryEnabled: passwordSwitch(values[settingsKeyPasswordRecoveryEnabled]),
		MinLength:       parsePasswordMinLength(values[settingsKeyPasswordMinLength]),
	}, nil
}

// settingValues reads the named settings rows in ONE query and answers the
// values that exist. A missing key is simply absent from the map, so each
// caller applies its own documented default per row.
func (ss *settingsStore) settingValues(ctx context.Context, keys ...string) (map[string]string, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",")
	args := make([]any, len(keys))
	for i, key := range keys {
		args[i] = key
	}
	rows, err := ss.db.QueryContext(ctx,
		`SELECT key, value FROM settings WHERE key IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: get settings %s: %w", strings.Join(keys, ", "), err)
	}
	defer rows.Close()

	values := make(map[string]string, len(keys))
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("sqlite: get settings %s: %w", strings.Join(keys, ", "), err)
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: get settings %s: %w", strings.Join(keys, ", "), err)
	}
	return values, nil
}

// SetPasswordSettings writes the two switches and the minimum in ONE
// transaction: an administrator's save is one configuration, so a failure can
// never leave the policy half applied — a switch turned off while the minimum
// it was saved with stayed behind.
func (ss *settingsStore) SetPasswordSettings(ctx context.Context, settings application.PasswordSettings) error {
	tx, err := beginImmediate(ctx, ss.db, "set password settings")
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, row := range []struct{ key, value string }{
		{settingsKeyPasswordChangeEnabled, formatSettingBool(settings.ChangeEnabled)},
		{settingsKeyPasswordRecoveryEnabled, formatSettingBool(settings.RecoveryEnabled)},
		{settingsKeyPasswordMinLength, strconv.Itoa(settings.MinLength)},
	} {
		if err := setSettingTx(ctx, tx, row.key, row.value); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// passwordSwitch reads one stored switch value. Only the two canonical values
// are trusted; anything else — including an absent row, which contributes no
// value at all — reads back ON, the documented default.
func passwordSwitch(value string) bool {
	return value != "0"
}

// parsePasswordMinLength reads the stored minimum. An unparseable value, or one
// outside the configurable band, answers the documented default: this is the
// number every NEW password write obeys, so a nonsense row must never tighten
// the rule past what an administrator could have saved, nor drop it to nothing.
func parsePasswordMinLength(value string) int {
	minimum, err := strconv.Atoi(value)
	if err != nil || minimum < application.MinPasswordMinLength || minimum > application.MaxPasswordMinLength {
		return application.DefaultPasswordMinLength
	}
	return minimum
}

// formatSettingBool renders a switch in the '1'/'0' text form the keyed
// settings table stores (the same form the SLA flag uses).
func formatSettingBool(value bool) string {
	if value {
		return "1"
	}
	return "0"
}
