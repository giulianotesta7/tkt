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

// settingsKeyMCPEnabled holds the instance MCP switch ('1' on, anything else or
// absent off), separate from the API switch.
const settingsKeyMCPEnabled = "mcp_enabled"

// oauthStore implements application.OAuthStore over migration 0042 (issue
// #493). Codes and tokens arrive already hashed; a reused code or refresh token
// revokes its grant in the same transaction that detects the reuse.
type oauthStore struct {
	db *sql.DB
}

var _ application.OAuthStore = (*oauthStore)(nil)

func newOAuthStore(db *sql.DB) *oauthStore { return &oauthStore{db: db} }

func (o *oauthStore) MCPEnabled(ctx context.Context) (bool, error) {
	var value string
	err := o.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, settingsKeyMCPEnabled).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("sqlite: get %s: %w", settingsKeyMCPEnabled, err)
	}
	return value == "1", nil
}

func (o *oauthStore) SetMCPEnabled(ctx context.Context, enabled bool, actorID int64, at time.Time) error {
	tx, err := beginImmediate(ctx, o.db, "set mcp enabled")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	value, action := "0", "mcp_disabled"
	if enabled {
		value, action = "1", "mcp_enabled"
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, settingsKeyMCPEnabled, value); err != nil {
		return fmt.Errorf("sqlite: set %s: %w", settingsKeyMCPEnabled, err)
	}
	if err := appendAPIAdminEventTx(ctx, tx, action, actorID, nil, nil, at); err != nil {
		return err
	}
	return tx.Commit()
}

func (o *oauthStore) CreateClient(ctx context.Context, c *domain.OAuthClient, pruneBefore time.Time) error {
	tx, err := beginImmediate(ctx, o.db, "create oauth client")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	const abandoned = `SELECT id FROM oauth_clients WHERE created_at < ?
		AND NOT EXISTS (SELECT 1 FROM oauth_grants g WHERE g.client_id = oauth_clients.id)`
	if _, err := tx.ExecContext(ctx, `DELETE FROM oauth_codes WHERE client_id IN (`+abandoned+`)`, formatTime(pruneBefore)); err != nil {
		return fmt.Errorf("sqlite: prune oauth codes: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM oauth_clients WHERE id IN (`+abandoned+`)`, formatTime(pruneBefore)); err != nil {
		return fmt.Errorf("sqlite: prune oauth clients: %w", err)
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO oauth_clients (client_id, name, redirect_uris, created_at) VALUES (?, ?, ?, ?)`,
		c.ClientID, c.Name, strings.Join(c.RedirectURIs, "\n"), formatTime(c.CreatedAt))
	if err != nil {
		return fmt.Errorf("sqlite: create oauth client: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("sqlite: oauth client id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit oauth client: %w", err)
	}
	c.ID = id
	return nil
}

func (o *oauthStore) ClientByClientID(ctx context.Context, clientID string) (*domain.OAuthClient, error) {
	var (
		c                  domain.OAuthClient
		redirects, created string
	)
	err := o.db.QueryRowContext(ctx, `SELECT id, client_id, name, redirect_uris, created_at FROM oauth_clients WHERE client_id = ?`, clientID).
		Scan(&c.ID, &c.ClientID, &c.Name, &redirects, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &domain.NotFoundError{Kind: "oauth client", ID: clientID}
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: read oauth client: %w", err)
	}
	c.RedirectURIs = strings.Split(redirects, "\n")
	if c.CreatedAt, err = time.Parse(timeLayout, created); err != nil {
		return nil, fmt.Errorf("sqlite: parse oauth client created_at %q: %w", created, err)
	}
	return &c, nil
}

func (o *oauthStore) CreateCode(ctx context.Context, c *domain.OAuthCode) error {
	tx, err := beginImmediate(ctx, o.db, "create oauth code")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM oauth_codes WHERE used_at IS NULL AND expires_at < ?`, formatTime(c.CreatedAt)); err != nil {
		return fmt.Errorf("sqlite: prune oauth codes: %w", err)
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO oauth_codes (code_hash, client_id, user_id, redirect_uri, code_challenge, scopes, resource, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.CodeHash, c.ClientID, c.UserID, c.RedirectURI, c.CodeChallenge, joinScopes(c.Scopes), c.Resource, formatTime(c.CreatedAt), formatTime(c.ExpiresAt))
	if err != nil {
		return fmt.Errorf("sqlite: create oauth code: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("sqlite: oauth code id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit oauth code: %w", err)
	}
	c.ID = id
	return nil
}

const oauthCodeColumns = `id, code_hash, client_id, user_id, redirect_uri, code_challenge, scopes, resource, created_at, expires_at, used_at, grant_id`

func (o *oauthStore) ConsumeCode(ctx context.Context, codeHash string, at time.Time) (*domain.OAuthCode, error) {
	tx, err := beginImmediate(ctx, o.db, "consume oauth code")
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	c, err := scanOAuthCode(tx.QueryRowContext(ctx, `SELECT `+oauthCodeColumns+` FROM oauth_codes WHERE code_hash = ?`, codeHash))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &domain.NotFoundError{Kind: "oauth code", ID: codeHash}
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: read oauth code: %w", err)
	}
	if c.UsedAt != nil {
		if c.GrantID != nil {
			if err := revokeGrantByTkt(ctx, tx, *c.GrantID, at); err != nil {
				return nil, err
			}
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("sqlite: commit oauth code reuse: %w", err)
		}
		return nil, application.ErrOAuthCodeReused
	}
	if _, err := tx.ExecContext(ctx, `UPDATE oauth_codes SET used_at = ? WHERE id = ?`, formatTime(at), c.ID); err != nil {
		return nil, fmt.Errorf("sqlite: spend oauth code: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("sqlite: commit oauth code: %w", err)
	}
	return c, nil
}

// revokeGrantByTkt revokes a live grant with no revoking person: tkt revoked
// it after detecting a reused code or refresh token.
func revokeGrantByTkt(ctx context.Context, tx *sql.Tx, grantID int64, at time.Time) error {
	if _, err := tx.ExecContext(ctx, `UPDATE oauth_grants SET revoked_at = ?, revoked_by_user_id = NULL
		WHERE id = ? AND revoked_at IS NULL`, formatTime(at), grantID); err != nil {
		return fmt.Errorf("sqlite: revoke oauth grant: %w", err)
	}
	return nil
}

func (o *oauthStore) CreateGrant(ctx context.Context, g *domain.OAuthGrant, codeID int64, tokens ...*domain.OAuthToken) error {
	tx, err := beginImmediate(ctx, o.db, "create oauth grant")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO oauth_grants (user_id, client_id, scopes, resource, created_at) VALUES (?, ?, ?, ?, ?)`,
		g.UserID, g.ClientID, joinScopes(g.Scopes), g.Resource, formatTime(g.CreatedAt))
	if err != nil {
		return fmt.Errorf("sqlite: create oauth grant: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("sqlite: oauth grant id: %w", err)
	}
	res, err = tx.ExecContext(ctx, `UPDATE oauth_codes SET grant_id = ? WHERE id = ?`, id, codeID)
	if err != nil {
		return fmt.Errorf("sqlite: link oauth code: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("sqlite: link oauth code rows: %w", err)
	} else if n == 0 {
		return &domain.NotFoundError{Kind: "oauth code", ID: codeID}
	}
	if err := insertOAuthTokensTx(ctx, tx, id, tokens); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit oauth grant: %w", err)
	}
	g.ID = id
	for _, t := range tokens {
		t.GrantID = id
	}
	return nil
}

// insertOAuthTokensTx stores tokens for grantID, assigning each ID. GrantID is
// assigned by the caller after commit, so a rolled-back transaction leaves the
// tokens untouched.
func insertOAuthTokensTx(ctx context.Context, tx *sql.Tx, grantID int64, tokens []*domain.OAuthToken) error {
	for _, t := range tokens {
		res, err := tx.ExecContext(ctx, `INSERT INTO oauth_tokens (grant_id, kind, token_hash, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
			grantID, string(t.Kind), t.TokenHash, formatTime(t.CreatedAt), formatTime(t.ExpiresAt))
		if err != nil {
			return fmt.Errorf("sqlite: create oauth token: %w", err)
		}
		if t.ID, err = res.LastInsertId(); err != nil {
			return fmt.Errorf("sqlite: oauth token id: %w", err)
		}
	}
	return nil
}

func (o *oauthStore) TokenByHash(ctx context.Context, hash string) (*domain.OAuthToken, error) {
	var (
		t                domain.OAuthToken
		kind             string
		created, expires string
		used             sql.NullString
	)
	err := o.db.QueryRowContext(ctx, `SELECT id, grant_id, kind, token_hash, created_at, expires_at, used_at FROM oauth_tokens WHERE token_hash = ?`, hash).
		Scan(&t.ID, &t.GrantID, &kind, &t.TokenHash, &created, &expires, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &domain.NotFoundError{Kind: "oauth token", ID: hash}
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: read oauth token: %w", err)
	}
	t.Kind = domain.OAuthTokenKind(kind)
	if t.CreatedAt, err = time.Parse(timeLayout, created); err != nil {
		return nil, fmt.Errorf("sqlite: parse oauth token created_at %q: %w", created, err)
	}
	if t.ExpiresAt, err = time.Parse(timeLayout, expires); err != nil {
		return nil, fmt.Errorf("sqlite: parse oauth token expires_at %q: %w", expires, err)
	}
	if t.UsedAt, err = parseOAuthTime(used); err != nil {
		return nil, err
	}
	return &t, nil
}

const oauthGrantColumns = `g.id, g.user_id, g.client_id, c.name, g.scopes, g.resource, g.created_at, g.last_used_at, g.revoked_at, g.revoked_by_user_id`

const oauthGrantFrom = ` FROM oauth_grants g JOIN oauth_clients c ON c.id = g.client_id`

func (o *oauthStore) GrantByID(ctx context.Context, id int64) (*domain.OAuthGrant, error) {
	g, err := scanOAuthGrant(o.db.QueryRowContext(ctx, `SELECT `+oauthGrantColumns+oauthGrantFrom+` WHERE g.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &domain.NotFoundError{Kind: "oauth grant", ID: id}
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: read oauth grant: %w", err)
	}
	return g, nil
}

func (o *oauthStore) RotateRefreshToken(ctx context.Context, tokenID int64, at time.Time, tokens ...*domain.OAuthToken) error {
	tx, err := beginImmediate(ctx, o.db, "rotate oauth refresh token")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var grantID int64
	err = tx.QueryRowContext(ctx, `SELECT grant_id FROM oauth_tokens WHERE id = ?`, tokenID).Scan(&grantID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("sqlite: read oauth token: %w", err)
	}
	res, err := tx.ExecContext(ctx, `UPDATE oauth_tokens SET used_at = ? WHERE id = ? AND kind = 'refresh' AND used_at IS NULL`, formatTime(at), tokenID)
	if err != nil {
		return fmt.Errorf("sqlite: spend oauth refresh token: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: spend oauth refresh token rows: %w", err)
	}
	if n == 0 {
		if grantID != 0 {
			if err := revokeGrantByTkt(ctx, tx, grantID, at); err != nil {
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("sqlite: commit oauth refresh reuse: %w", err)
		}
		return application.ErrOAuthRefreshReused
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM oauth_tokens WHERE grant_id = ? AND expires_at < ?`, grantID, formatTime(at)); err != nil {
		return fmt.Errorf("sqlite: prune oauth tokens: %w", err)
	}
	if err := insertOAuthTokensTx(ctx, tx, grantID, tokens); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit oauth refresh rotation: %w", err)
	}
	for _, t := range tokens {
		t.GrantID = grantID
	}
	return nil
}

func (o *oauthStore) TouchGrant(ctx context.Context, id int64, at time.Time) error {
	if _, err := o.db.ExecContext(ctx, `UPDATE oauth_grants SET last_used_at = ? WHERE id = ?`, formatTime(at), id); err != nil {
		return fmt.Errorf("sqlite: touch oauth grant: %w", err)
	}
	return nil
}

func (o *oauthStore) ListLiveGrants(ctx context.Context, userID int64, now time.Time) ([]domain.OAuthGrant, error) {
	rows, err := o.db.QueryContext(ctx, `SELECT `+oauthGrantColumns+oauthGrantFrom+`
		WHERE g.user_id = ? AND g.revoked_at IS NULL
		AND EXISTS (SELECT 1 FROM oauth_tokens t WHERE t.grant_id = g.id AND t.kind = 'refresh' AND t.used_at IS NULL AND t.expires_at > ?)
		ORDER BY g.created_at DESC, g.id DESC`, userID, formatTime(now))
	if err != nil {
		return nil, fmt.Errorf("sqlite: list oauth grants: %w", err)
	}
	defer rows.Close()
	var out []domain.OAuthGrant
	for rows.Next() {
		g, err := scanOAuthGrant(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: list oauth grants: %w", err)
		}
		out = append(out, *g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list oauth grants: %w", err)
	}
	return out, nil
}

func (o *oauthStore) RevokeGrant(ctx context.Context, id, userID int64, at time.Time) error {
	res, err := o.db.ExecContext(ctx, `UPDATE oauth_grants SET revoked_at = ?, revoked_by_user_id = ?
		WHERE id = ? AND user_id = ? AND revoked_at IS NULL`, formatTime(at), userID, id, userID)
	if err != nil {
		return fmt.Errorf("sqlite: revoke oauth grant: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("sqlite: revoke oauth grant rows: %w", err)
	} else if n == 0 {
		return &domain.NotFoundError{Kind: "oauth grant", ID: id}
	}
	return nil
}

func parseScopes(raw string) []domain.APIKeyScope {
	var out []domain.APIKeyScope
	for _, s := range strings.Fields(raw) {
		out = append(out, domain.APIKeyScope(s))
	}
	return out
}

func parseOAuthTime(raw sql.NullString) (*time.Time, error) {
	if !raw.Valid {
		return nil, nil
	}
	t, err := time.Parse(timeLayout, raw.String)
	if err != nil {
		return nil, fmt.Errorf("sqlite: parse oauth time %q: %w", raw.String, err)
	}
	return &t, nil
}

func scanOAuthCode(scan rowScanner) (*domain.OAuthCode, error) {
	var (
		c                        domain.OAuthCode
		scopes, created, expires string
		used                     sql.NullString
		grantID                  sql.NullInt64
	)
	if err := scan.Scan(&c.ID, &c.CodeHash, &c.ClientID, &c.UserID, &c.RedirectURI, &c.CodeChallenge, &scopes, &c.Resource,
		&created, &expires, &used, &grantID); err != nil {
		return nil, err
	}
	c.Scopes = parseScopes(scopes)
	var err error
	if c.CreatedAt, err = time.Parse(timeLayout, created); err != nil {
		return nil, fmt.Errorf("sqlite: parse oauth code created_at %q: %w", created, err)
	}
	if c.ExpiresAt, err = time.Parse(timeLayout, expires); err != nil {
		return nil, fmt.Errorf("sqlite: parse oauth code expires_at %q: %w", expires, err)
	}
	if c.UsedAt, err = parseOAuthTime(used); err != nil {
		return nil, err
	}
	if grantID.Valid {
		v := grantID.Int64
		c.GrantID = &v
	}
	return &c, nil
}

func scanOAuthGrant(scan rowScanner) (*domain.OAuthGrant, error) {
	var (
		g                 domain.OAuthGrant
		scopes, created   string
		lastUsed, revoked sql.NullString
		revokedBy         sql.NullInt64
	)
	if err := scan.Scan(&g.ID, &g.UserID, &g.ClientID, &g.ClientName, &scopes, &g.Resource, &created, &lastUsed, &revoked, &revokedBy); err != nil {
		return nil, err
	}
	g.Scopes = parseScopes(scopes)
	var err error
	if g.CreatedAt, err = time.Parse(timeLayout, created); err != nil {
		return nil, fmt.Errorf("sqlite: parse oauth grant created_at %q: %w", created, err)
	}
	if g.LastUsedAt, err = parseOAuthTime(lastUsed); err != nil {
		return nil, err
	}
	if g.RevokedAt, err = parseOAuthTime(revoked); err != nil {
		return nil, err
	}
	if revokedBy.Valid {
		v := revokedBy.Int64
		g.RevokedByUserID = &v
	}
	return &g, nil
}
