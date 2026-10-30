package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// settingsKeyAPIEnabled holds the instance API switch ('1' on, anything else or
// absent off — enabling is an explicit root action).
const settingsKeyAPIEnabled = "api_enabled"

// apiAccessStore implements application.APIAccessStore over migration 0037
// (issue #488). Every administrative mutation writes its api_admin_events row
// in the same immediate transaction, so an action and its history never part.
type apiAccessStore struct {
	db *sql.DB
}

var _ application.APIAccessStore = (*apiAccessStore)(nil)

func newAPIAccessStore(db *sql.DB) *apiAccessStore { return &apiAccessStore{db: db} }

// appendAPIAdminEventTx records one administrative action.
func appendAPIAdminEventTx(ctx context.Context, tx *sql.Tx, action string, actorID int64, serviceUserID, keyID *int64, at time.Time) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO api_admin_events (action, actor_user_id, service_user_id, api_key_id, created_at)
		VALUES (?, ?, ?, ?, ?)`, action, actorID, nullableInt64(serviceUserID), nullableInt64(keyID), formatTime(at)); err != nil {
		return fmt.Errorf("sqlite: audit %s: %w", action, err)
	}
	return nil
}

func (as *apiAccessStore) CreateServiceUser(ctx context.Context, u *domain.User, actorID int64) error {
	tx, err := beginImmediate(ctx, as.db, "create service user")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO users (name, email, password_hash, role, active, kind, created_at)
		VALUES (?, ?, '', ?, ?, 'service', ?)`, u.Name, u.Email, string(u.Role), u.Active, formatTime(u.CreatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return &domain.DuplicateError{Kind: "user", Name: u.Email}
		}
		return fmt.Errorf("sqlite: create service user: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("sqlite: service user id: %w", err)
	}
	if err := appendAPIAdminEventTx(ctx, tx, "service_user_created", actorID, &id, nil, u.CreatedAt); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit service user: %w", err)
	}
	u.ID = id
	u.Kind = domain.UserKindService
	return nil
}

func (as *apiAccessStore) ListServiceUsers(ctx context.Context) ([]domain.User, error) {
	return newUserStore(as.db).ListServiceUsers(ctx)
}

func (as *apiAccessStore) SetServiceUserActive(ctx context.Context, id int64, active bool, actorID int64, at time.Time) error {
	tx, err := beginImmediate(ctx, as.db, "set service user active")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE users SET active = ? WHERE id = ? AND kind = 'service'`, active, id)
	if err != nil {
		return fmt.Errorf("sqlite: set service user active: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("sqlite: set service user active rows: %w", err)
	} else if n == 0 {
		return &domain.NotFoundError{Kind: "service user", ID: id}
	}
	action := "service_user_reactivated"
	if !active {
		action = "service_user_deactivated"
		if _, err := tx.ExecContext(ctx, `UPDATE api_keys SET revoked_at = ?, revoked_by_user_id = ?
			WHERE user_id = ? AND revoked_at IS NULL`, formatTime(at), actorID, id); err != nil {
			return fmt.Errorf("sqlite: revoke service user keys: %w", err)
		}
	}
	if err := appendAPIAdminEventTx(ctx, tx, action, actorID, &id, nil, at); err != nil {
		return err
	}
	return tx.Commit()
}

func (as *apiAccessStore) CreateAPIKey(ctx context.Context, k *domain.APIKey) error {
	tx, err := beginImmediate(ctx, as.db, "create api key")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO api_keys (public_id, user_id, name, secret_hash, scopes, created_by_user_id, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		k.PublicID, k.UserID, k.Name, k.SecretHash, joinScopes(k.Scopes), k.CreatedByUserID, formatTime(k.CreatedAt), formatTimePtr(k.ExpiresAt))
	if err != nil {
		return fmt.Errorf("sqlite: create api key: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("sqlite: api key id: %w", err)
	}
	if err := appendAPIAdminEventTx(ctx, tx, "api_key_created", k.CreatedByUserID, &k.UserID, &id, k.CreatedAt); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit api key: %w", err)
	}
	k.ID = id
	return nil
}

const apiKeyColumns = `id, public_id, user_id, name, secret_hash, scopes, created_by_user_id, created_at, expires_at, last_used_at, revoked_at, revoked_by_user_id`

// ListAPIKeys answers every key, newest first.
func (as *apiAccessStore) ListAPIKeys(ctx context.Context) ([]domain.APIKey, error) {
	rows, err := as.db.QueryContext(ctx, `SELECT `+apiKeyColumns+` FROM api_keys ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list api keys: %w", err)
	}
	defer rows.Close()
	var out []domain.APIKey
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list api keys: %w", err)
	}
	return out, nil
}

func (as *apiAccessStore) APIKeyByPublicID(ctx context.Context, publicID string) (*domain.APIKey, error) {
	k, err := scanAPIKey(as.db.QueryRowContext(ctx, `SELECT `+apiKeyColumns+` FROM api_keys WHERE public_id = ?`, publicID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &domain.NotFoundError{Kind: "api key", ID: publicID}
	}
	return k, err
}

func (as *apiAccessStore) RevokeAPIKey(ctx context.Context, id, actorID int64, at time.Time) error {
	tx, err := beginImmediate(ctx, as.db, "revoke api key")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var owner int64
	err = tx.QueryRowContext(ctx, `SELECT user_id FROM api_keys WHERE id = ? AND revoked_at IS NULL`, id).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return &domain.NotFoundError{Kind: "api key", ID: id}
	}
	if err != nil {
		return fmt.Errorf("sqlite: read api key: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE api_keys SET revoked_at = ?, revoked_by_user_id = ? WHERE id = ?`, formatTime(at), actorID, id); err != nil {
		return fmt.Errorf("sqlite: revoke api key: %w", err)
	}
	if err := appendAPIAdminEventTx(ctx, tx, "api_key_revoked", actorID, &owner, &id, at); err != nil {
		return err
	}
	return tx.Commit()
}

func (as *apiAccessStore) TouchAPIKey(ctx context.Context, id int64, at time.Time) error {
	if _, err := as.db.ExecContext(ctx, `UPDATE api_keys SET last_used_at = ? WHERE id = ?`, formatTime(at), id); err != nil {
		return fmt.Errorf("sqlite: touch api key: %w", err)
	}
	return nil
}

func (as *apiAccessStore) APIEnabled(ctx context.Context) (bool, error) {
	var value string
	err := as.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, settingsKeyAPIEnabled).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("sqlite: get %s: %w", settingsKeyAPIEnabled, err)
	}
	return value == "1", nil
}

func (as *apiAccessStore) SetAPIEnabled(ctx context.Context, enabled bool, actorID int64, at time.Time) error {
	tx, err := beginImmediate(ctx, as.db, "set api enabled")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	value, action := "0", "api_disabled"
	if enabled {
		value, action = "1", "api_enabled"
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, settingsKeyAPIEnabled, value); err != nil {
		return fmt.Errorf("sqlite: set %s: %w", settingsKeyAPIEnabled, err)
	}
	if err := appendAPIAdminEventTx(ctx, tx, action, actorID, nil, nil, at); err != nil {
		return err
	}
	return tx.Commit()
}

func joinScopes(scopes []domain.APIKeyScope) string {
	parts := make([]string, len(scopes))
	for i, s := range scopes {
		parts[i] = string(s)
	}
	return strings.Join(parts, " ")
}

func scanAPIKey(scan rowScanner) (*domain.APIKey, error) {
	var (
		k                              domain.APIKey
		scopes, createdAt              string
		expiresAt, lastUsedAt, revoked sql.NullString
		revokedBy                      sql.NullInt64
	)
	if err := scan.Scan(&k.ID, &k.PublicID, &k.UserID, &k.Name, &k.SecretHash, &scopes, &k.CreatedByUserID,
		&createdAt, &expiresAt, &lastUsedAt, &revoked, &revokedBy); err != nil {
		return nil, err
	}
	for _, s := range strings.Fields(scopes) {
		k.Scopes = append(k.Scopes, domain.APIKeyScope(s))
	}
	var err error
	if k.CreatedAt, err = time.Parse(timeLayout, createdAt); err != nil {
		return nil, fmt.Errorf("sqlite: parse api key created_at %q: %w", createdAt, err)
	}
	for _, field := range []struct {
		raw  sql.NullString
		dest **time.Time
	}{{expiresAt, &k.ExpiresAt}, {lastUsedAt, &k.LastUsedAt}, {revoked, &k.RevokedAt}} {
		if !field.raw.Valid {
			continue
		}
		t, err := time.Parse(timeLayout, field.raw.String)
		if err != nil {
			return nil, fmt.Errorf("sqlite: parse api key time %q: %w", field.raw.String, err)
		}
		*field.dest = &t
	}
	if revokedBy.Valid {
		v := revokedBy.Int64
		k.RevokedByUserID = &v
	}
	return &k, nil
}
