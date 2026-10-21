package sqlite

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/giulianotesta7/tkt/internal/application"
)

// Email settings row keys (issue #362). No migration seeds them: an absent
// row reads back the disabled default, so the keyed settings table needs no
// new column. The password row holds the secret box output, never the
// password.
const (
	settingsKeyEmailEnabled        = "email_enabled"
	settingsKeyEmailHost           = "email_host"
	settingsKeyEmailPort           = "email_port"
	settingsKeyEmailEncryption     = "email_encryption"
	settingsKeyEmailSkipTLSVerify  = "email_skip_tls_verify"
	settingsKeyEmailUsername       = "email_username"
	settingsKeyEmailSealedPassword = "email_password_sealed"
	settingsKeyEmailFromAddress    = "email_from_address"
	settingsKeyEmailFromName       = "email_from_name"
	settingsKeyEmailBaseURL        = "email_base_url"
)

var _ application.EmailSettingsStore = (*settingsStore)(nil)

// GetEmailSettings assembles the email configuration from its rows in one
// read. Every row falls back to its default when absent, and an unparseable
// flag, port, or encryption fails closed: disabled, certificate verified,
// port 25, no encryption.
func (ss *settingsStore) GetEmailSettings(ctx context.Context) (application.EmailSettings, error) {
	rows, err := ss.db.QueryContext(ctx, `SELECT key, value FROM settings WHERE key LIKE 'email\_%' ESCAPE '\'`)
	if err != nil {
		return application.EmailSettings{}, fmt.Errorf("sqlite: get email settings: %w", err)
	}
	defer rows.Close()
	values := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return application.EmailSettings{}, fmt.Errorf("sqlite: scan email settings: %w", err)
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return application.EmailSettings{}, fmt.Errorf("sqlite: get email settings: %w", err)
	}

	out := application.EmailSettings{
		Enabled:        values[settingsKeyEmailEnabled] == "1",
		Host:           values[settingsKeyEmailHost],
		Port:           application.DefaultSMTPPort,
		Encryption:     application.EmailEncryptionNone,
		SkipTLSVerify:  values[settingsKeyEmailSkipTLSVerify] == "1",
		Username:       values[settingsKeyEmailUsername],
		SealedPassword: values[settingsKeyEmailSealedPassword],
		FromAddress:    values[settingsKeyEmailFromAddress],
		FromName:       values[settingsKeyEmailFromName],
		BaseURL:        values[settingsKeyEmailBaseURL],
	}
	if port, err := strconv.Atoi(values[settingsKeyEmailPort]); err == nil && port >= 1 && port <= 65535 {
		out.Port = port
	}
	for _, e := range application.AllowedEmailEncryptions() {
		if string(e) == values[settingsKeyEmailEncryption] {
			out.Encryption = e
		}
	}
	return out, nil
}

// SetEmailSettings writes every email row in ONE transaction, so a save is
// never half-applied. The service validated the values; the store refuses
// only a read-side value (PasswordSet), which would mean a Get result is
// being written back.
func (ss *settingsStore) SetEmailSettings(ctx context.Context, s application.EmailSettings) error {
	if s.PasswordSet {
		return errors.New("sqlite: set email settings: PasswordSet is read-only; pass SealedPassword")
	}
	tx, err := beginImmediate(ctx, ss.db, "set email settings")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	writes := []struct {
		key   string
		value string
	}{
		{settingsKeyEmailEnabled, boolSetting(s.Enabled)},
		{settingsKeyEmailHost, s.Host},
		{settingsKeyEmailPort, strconv.Itoa(s.Port)},
		{settingsKeyEmailEncryption, string(s.Encryption)},
		{settingsKeyEmailSkipTLSVerify, boolSetting(s.SkipTLSVerify)},
		{settingsKeyEmailUsername, s.Username},
		{settingsKeyEmailSealedPassword, s.SealedPassword},
		{settingsKeyEmailFromAddress, s.FromAddress},
		{settingsKeyEmailFromName, s.FromName},
		{settingsKeyEmailBaseURL, s.BaseURL},
	}
	for _, w := range writes {
		if err := setSettingTx(ctx, tx, w.key, w.value); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func boolSetting(v bool) string {
	if v {
		return "1"
	}
	return "0"
}
