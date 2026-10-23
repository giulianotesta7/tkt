package sqlite

import (
	"context"
	"strconv"

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
func (ss *settingsStore) GetPasswordSettings(ctx context.Context) (application.PasswordSettings, error) {
	change, err := ss.passwordSwitch(ctx, settingsKeyPasswordChangeEnabled)
	if err != nil {
		return application.PasswordSettings{}, err
	}
	recovery, err := ss.passwordSwitch(ctx, settingsKeyPasswordRecoveryEnabled)
	if err != nil {
		return application.PasswordSettings{}, err
	}
	raw, err := ss.settingValue(ctx, settingsKeyPasswordMinLength)
	if err != nil {
		return application.PasswordSettings{}, err
	}
	return application.PasswordSettings{
		ChangeEnabled:   change,
		RecoveryEnabled: recovery,
		MinLength:       parsePasswordMinLength(raw),
	}, nil
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

// passwordSwitch reads one stored switch. Only the two canonical values are
// trusted; anything else — including an absent row — reads back ON, the
// documented default.
func (ss *settingsStore) passwordSwitch(ctx context.Context, key string) (bool, error) {
	value, err := ss.settingValue(ctx, key)
	if err != nil {
		return false, err
	}
	return value != "0", nil
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
