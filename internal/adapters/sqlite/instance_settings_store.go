package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
)

const settingsKeyInstanceAddress = "instance_address"
const settingsKeyInstanceCompanyConflict = "instance_address_company_conflict"

func (s *Store) InstanceSettingsStore() application.InstanceSettingsStore {
	return newSettingsStore(s.db)
}

func (ss *settingsStore) GetInstanceAddress(ctx context.Context) (application.InstanceAddress, error) {
	values, err := ss.settingValues(ctx, settingsKeyInstanceAddress, settingsKeyInstanceCompanyConflict)
	if err != nil {
		return application.InstanceAddress{}, err
	}
	return application.InstanceAddress{Address: values[settingsKeyInstanceAddress], PreviousCompanyAddress: values[settingsKeyInstanceCompanyConflict]}, nil
}

func (ss *settingsStore) SetInstanceAddress(ctx context.Context, actorID int64, address string, at time.Time) error {
	tx, err := beginImmediate(ctx, ss.db, "set instance address")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if address == "" {
		var enabled int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM settings WHERE key IN ('email_enabled', 'google_signin_enabled', 'microsoft_signin_enabled', 'mcp_enabled') AND value = '1'`).Scan(&enabled); err != nil {
			return err
		}
		if enabled != 0 {
			return application.InstanceAddressInUseError()
		}
	}
	var previous string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT value FROM settings WHERE key = ?), '')`, settingsKeyInstanceAddress).Scan(&previous); err != nil {
		return err
	}
	if previous == address {
		return tx.Commit()
	}
	if err := setSettingTx(ctx, tx, settingsKeyInstanceAddress, address); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO instance_address_events (actor_user_id, previous_address, address, created_at) VALUES (?, ?, ?, ?)`, actorID, previous, address, formatTime(at)); err != nil {
		return err
	}
	return tx.Commit()
}

// checkInstanceAddressTx closes the race between enabling a consumer and
// clearing the address: both operations acquire the same immediate writer lock.
func checkInstanceAddressTx(ctx context.Context, tx *sql.Tx, enabled bool) error {
	if !enabled {
		return nil
	}
	var address string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT value FROM settings WHERE key = ?), '')`, settingsKeyInstanceAddress).Scan(&address); err != nil {
		return err
	}
	if address == "" {
		return application.InstanceAddressRequiredError()
	}
	return nil
}

func (ss *settingsStore) ListInstanceAddressEvents(ctx context.Context) ([]application.InstanceAddressEvent, error) {
	rows, err := ss.db.QueryContext(ctx, `SELECT actor_user_id, previous_address, address, created_at FROM instance_address_events ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []application.InstanceAddressEvent
	for rows.Next() {
		var event application.InstanceAddressEvent
		var at string
		if err := rows.Scan(&event.ActorUserID, &event.PreviousAddress, &event.Address, &at); err != nil {
			return nil, err
		}
		event.CreatedAt, err = time.Parse(timeLayout, at)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}
