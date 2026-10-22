package sqlite

import (
	"context"
	"fmt"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// settingsKeyOrganizationName, settingsKeyOrganizationHelpDesk and
// settingsKeyOrganizationSupportEmail are the settings rows of the instance
// identity (issue #383, S3). No migration seeds them: an absent row reads back
// the documented default, so a fresh instance renders a usable brand without a
// stored row and the key/value table needs no new column.
const (
	settingsKeyOrganizationName         = "org_name"
	settingsKeyOrganizationHelpDesk     = "org_helpdesk_name"
	settingsKeyOrganizationSupportEmail = "org_support_email"
	// settingsKeyOrganizationLogo is the settings_blobs key holding the logo
	// bytes and the type they are served as (migration 0026).
	settingsKeyOrganizationLogo = "org_logo"
)

// GetOrganization answers the instance identity (issue #383, S3). It never
// fails for configuration reasons: an absent or empty name reads back the
// documented default, an absent help desk name reads back its default, and the
// optional email and logo stay empty — a hand-edited database degrades to the
// brand the interface shipped with instead of dropping every page. Only a real
// storage failure is an error.
func (ss *settingsStore) GetOrganization(ctx context.Context) (domain.Organization, error) {
	name, err := ss.settingValue(ctx, settingsKeyOrganizationName)
	if err != nil {
		return domain.Organization{}, err
	}
	helpDesk, err := ss.settingValue(ctx, settingsKeyOrganizationHelpDesk)
	if err != nil {
		return domain.Organization{}, err
	}
	supportEmail, err := ss.settingValue(ctx, settingsKeyOrganizationSupportEmail)
	if err != nil {
		return domain.Organization{}, err
	}
	org := domain.Organization{
		Name:         name,
		HelpDesk:     helpDesk,
		SupportEmail: supportEmail,
	}
	if org.Name == "" {
		org.Name = domain.DefaultOrganizationName
	}
	if org.HelpDesk == "" {
		org.HelpDesk = domain.DefaultOrganizationHelpDesk
	}
	logo, mime, err := ss.blobValue(ctx, settingsKeyOrganizationLogo)
	if err != nil {
		return domain.Organization{}, err
	}
	org.Logo, org.LogoMIME = logo, mime
	return org, nil
}

// SetOrganization writes the whole identity — the three text rows and the
// logo — in ONE transaction, so one submission lands entirely or not at all: a
// refused logo must not leave the name that travelled with it stored. A nil
// Logo deletes the stored logo, which is how the remove route and a save
// without a file both end with no logo.
func (ss *settingsStore) SetOrganization(ctx context.Context, org domain.Organization) error {
	tx, err := beginImmediate(ctx, ss.db, "set organization")
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, row := range []struct{ key, value string }{
		{settingsKeyOrganizationName, org.Name},
		{settingsKeyOrganizationHelpDesk, org.HelpDesk},
		{settingsKeyOrganizationSupportEmail, org.SupportEmail},
	} {
		if err := setSettingTx(ctx, tx, row.key, row.value); err != nil {
			return err
		}
	}
	if len(org.Logo) == 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM settings_blobs WHERE key = ?`, settingsKeyOrganizationLogo); err != nil {
			return fmt.Errorf("sqlite: delete %s: %w", settingsKeyOrganizationLogo, err)
		}
	} else {
		mime := org.LogoMIME
		if mime == "" {
			mime = domain.OrganizationLogoContentType
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO settings_blobs (key, value, mime) VALUES (?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, mime = excluded.mime`,
			settingsKeyOrganizationLogo, org.Logo, mime); err != nil {
			return fmt.Errorf("sqlite: set %s: %w", settingsKeyOrganizationLogo, err)
		}
	}
	return tx.Commit()
}

var _ application.OrganizationStore = (*settingsStore)(nil)
