package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// companyIdentityStore implements application.CompanyIdentityStore over
// migration 0034 (issue #419): the provider identity links a company sign-in
// resolves on (S7), and the single-use authorization attempts the callback
// consumes.
//
// Two read choices are worth knowing about. Every query here reads the account's
// pending column, because the sign-in decision needs it; the shared user scanner
// in user_store.go reads the column set of its callers, which do not.
// UsersByEmail compares with lower() on both sides, since the account is matched
// whatever case the provider reports and two accounts differing only in case are
// an ambiguity the flow refuses rather than a choice it makes.
type companyIdentityStore struct {
	db *sql.DB
}

var _ application.CompanyIdentityStore = (*companyIdentityStore)(nil)

func newCompanyIdentityStore(db *sql.DB) *companyIdentityStore {
	return &companyIdentityStore{db: db}
}

// CreateAttempt stores one authorization in flight. The state hash is the
// primary key, so a state can address one attempt only; the state itself is
// never stored.
func (cs *companyIdentityStore) CreateAttempt(ctx context.Context, attempt *application.CompanySignInAttempt) error {
	_, err := cs.db.ExecContext(ctx,
		`INSERT INTO oidc_sign_in_attempts (state_hash, provider, nonce, code_verifier, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		attempt.StateHash, string(attempt.Provider), attempt.Nonce, attempt.CodeVerifier,
		formatTime(attempt.CreatedAt), formatTime(attempt.ExpiresAt))
	if err != nil {
		return fmt.Errorf("sqlite: create sign-in attempt: %w", err)
	}
	return nil
}

// ConsumeAttempt reads the attempt for stateHash and deletes it in one immediate
// transaction, which is what makes a callback answerable exactly once: the
// second consume finds no row.
func (cs *companyIdentityStore) ConsumeAttempt(ctx context.Context, stateHash string) (*application.CompanySignInAttempt, error) {
	tx, err := beginImmediate(ctx, cs.db, "consume sign-in attempt")
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var (
		attempt            application.CompanySignInAttempt
		provider           string
		createdAt, expirAt string
	)
	err = tx.QueryRowContext(ctx,
		`SELECT provider, nonce, code_verifier, created_at, expires_at FROM oidc_sign_in_attempts WHERE state_hash = ?`,
		stateHash).Scan(&provider, &attempt.Nonce, &attempt.CodeVerifier, &createdAt, &expirAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &domain.NotFoundError{Kind: "sign-in attempt", ID: stateHash}
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: read sign-in attempt: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM oidc_sign_in_attempts WHERE state_hash = ?`, stateHash); err != nil {
		return nil, fmt.Errorf("sqlite: delete sign-in attempt: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("sqlite: commit consumed attempt: %w", err)
	}

	attempt.StateHash = stateHash
	attempt.Provider = application.CompanyProvider(provider)
	if attempt.CreatedAt, err = time.Parse(timeLayout, createdAt); err != nil {
		return nil, fmt.Errorf("sqlite: parse attempt created_at %q: %w", createdAt, err)
	}
	if attempt.ExpiresAt, err = time.Parse(timeLayout, expirAt); err != nil {
		return nil, fmt.Errorf("sqlite: parse attempt expires_at %q: %w", expirAt, err)
	}
	return &attempt, nil
}

// PurgeExpiredAttempts deletes the attempts that expired before the given
// instant. The comparison goes through the persisted ISO-8601 UTC form, which
// sorts chronologically, and the index on expires_at serves it.
func (cs *companyIdentityStore) PurgeExpiredAttempts(ctx context.Context, before time.Time) (int, error) {
	res, err := cs.db.ExecContext(ctx, `DELETE FROM oidc_sign_in_attempts WHERE expires_at < ?`, formatTime(before))
	if err != nil {
		return 0, fmt.Errorf("sqlite: purge expired attempts: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlite: purge expired attempts rows: %w", err)
	}
	return int(n), nil
}

// ResolveIdentity answers the identity linked to (provider, issuer, subject) and
// the account it belongs to, with the pending state the decision reads. A
// vanished identity and a vanished account are both ErrNotFound: neither can
// sign anyone in.
func (cs *companyIdentityStore) ResolveIdentity(ctx context.Context, provider application.CompanyProvider, issuer, subject string) (*application.CompanyIdentity, *domain.User, error) {
	var (
		identity   application.CompanyIdentity
		providerDB string
		user       domain.User
		role       string
		active     int64
		pending    int64
		createdAt  string
		identAt    string
		signInAt   string
	)
	err := cs.db.QueryRowContext(ctx,
		`SELECT i.id, i.user_id, i.provider, i.issuer, i.subject, i.email, i.created_at, i.last_sign_in_at,
		        u.name, u.email, u.password_hash, u.role, u.active, u.pending, u.created_at
		 FROM user_identities i JOIN users u ON u.id = i.user_id
		 WHERE i.provider = ? AND i.issuer = ? AND i.subject = ?`,
		string(provider), issuer, subject).Scan(
		&identity.ID, &identity.UserID, &providerDB, &identity.Issuer, &identity.Subject, &identity.Email, &createdAt, &signInAt,
		&user.Name, &user.Email, &user.PasswordHash, &role, &active, &pending, &identAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, &domain.NotFoundError{Kind: "user identity", ID: subject}
	}
	if err != nil {
		return nil, nil, fmt.Errorf("sqlite: read user identity: %w", err)
	}
	identity.Provider = application.CompanyProvider(providerDB)
	identity.UserID = user.ID
	identity.CreatedAt, err = time.Parse(timeLayout, createdAt)
	if err != nil {
		return nil, nil, fmt.Errorf("sqlite: parse identity created_at %q: %w", createdAt, err)
	}
	identity.LastSignInAt, err = time.Parse(timeLayout, signInAt)
	if err != nil {
		return nil, nil, fmt.Errorf("sqlite: parse identity last_sign_in_at %q: %w", signInAt, err)
	}
	user.Role = domain.Role(role)
	user.Active = active == 1
	user.Pending = pending == 1
	if user.CreatedAt, err = time.Parse(timeLayout, identAt); err != nil {
		return nil, nil, fmt.Errorf("sqlite: parse user created_at %q: %w", identAt, err)
	}
	return &identity, &user, nil
}

// UsersByEmail answers every account whose email matches case-insensitively,
// ordered by id, so the caller can tell one match from an ambiguity.
func (cs *companyIdentityStore) UsersByEmail(ctx context.Context, email string) ([]domain.User, error) {
	rows, err := cs.db.QueryContext(ctx,
		`SELECT id, name, email, password_hash, role, active, pending, created_at
		 FROM users WHERE lower(email) = lower(?) ORDER BY id ASC`, email)
	if err != nil {
		return nil, fmt.Errorf("sqlite: users by email: %w", err)
	}
	defer rows.Close()
	var out []domain.User
	for rows.Next() {
		u, err := scanCompanyUser(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan user: %w", err)
		}
		out = append(out, *u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: users by email: %w", err)
	}
	return out, nil
}

// CreateUserWithIdentity stores the account and its provider link in ONE
// immediate transaction. A UNIQUE failure — the account's email or the
// (issuer, subject) pair — rolls the whole pair back and answers
// ErrCompanyIdentityTaken, which the flow reads as a lost race and answers by
// repeating the lookup.
func (cs *companyIdentityStore) CreateUserWithIdentity(ctx context.Context, user *domain.User, identity *application.CompanyIdentity) error {
	tx, err := beginImmediate(ctx, cs.db, "create company account")
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`INSERT INTO users (name, email, password_hash, role, active, pending, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		user.Name, user.Email, user.PasswordHash, string(user.Role), user.Active, user.Pending, formatTime(user.CreatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return application.ErrCompanyIdentityTaken
		}
		return fmt.Errorf("sqlite: create company account: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("sqlite: company account id: %w", err)
	}
	user.ID = id
	identity.UserID = id
	if err := insertIdentityTx(ctx, tx, identity); err != nil {
		if isUniqueViolation(err) {
			return application.ErrCompanyIdentityTaken
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit company account: %w", err)
	}
	return nil
}

// LinkIdentity attaches a provider identity to an existing account. Either
// unique rule broken — the pair, or the account's one-identity-per-provider
// slot — answers ErrCompanyIdentityTaken: the account already reaches another
// identity of this provider, and the flow refuses instead of replacing it.
func (cs *companyIdentityStore) LinkIdentity(ctx context.Context, identity *application.CompanyIdentity) error {
	err := insertIdentityTx(ctx, cs.db, identity)
	if err != nil {
		if isUniqueViolation(err) {
			return application.ErrCompanyIdentityTaken
		}
		return err
	}
	return nil
}

// TouchIdentity refreshes the email last seen at the provider and the instant of
// the sign-in. It never rewrites created_at: the link is not made twice.
func (cs *companyIdentityStore) TouchIdentity(ctx context.Context, identityID int64, email string, at time.Time) error {
	res, err := cs.db.ExecContext(ctx,
		`UPDATE user_identities SET email = ?, last_sign_in_at = ? WHERE id = ?`, email, formatTime(at), identityID)
	if err != nil {
		return fmt.Errorf("sqlite: touch user identity: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: touch user identity rows: %w", err)
	}
	if n == 0 {
		return &domain.NotFoundError{Kind: "user identity", ID: identityID}
	}
	return nil
}

// execer is the one write method the identity insert needs, so it runs inside a
// transaction or directly on the pool with the same code.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// insertIdentityTx inserts one identity row, taking the id from the insert so a
// caller can use it afterwards. A UNIQUE failure is left to the caller: what it
// means differs between creating an account and linking one.
func insertIdentityTx(ctx context.Context, ex execer, identity *application.CompanyIdentity) error {
	res, err := ex.ExecContext(ctx,
		`INSERT INTO user_identities (user_id, provider, issuer, subject, email, created_at, last_sign_in_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		identity.UserID, string(identity.Provider), identity.Issuer, identity.Subject, identity.Email,
		formatTime(identity.CreatedAt), formatTime(identity.LastSignInAt))
	if err != nil {
		return fmt.Errorf("sqlite: insert user identity: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("sqlite: user identity id: %w", err)
	}
	identity.ID = id
	return nil
}

// scanCompanyUser projects one account row into a domain.User, including the
// pending state (issue #419, S3).
func scanCompanyUser(scan rowScanner) (*domain.User, error) {
	var (
		u         domain.User
		role      string
		active    int64
		pending   int64
		createdAt string
	)
	if err := scan.Scan(&u.ID, &u.Name, &u.Email, &u.PasswordHash, &role, &active, &pending, &createdAt); err != nil {
		return nil, err
	}
	u.Role = domain.Role(role)
	u.Active = active == 1
	u.Pending = pending == 1
	var err error
	if u.CreatedAt, err = time.Parse(timeLayout, createdAt); err != nil {
		return nil, fmt.Errorf("sqlite: parse user created_at %q: %w", createdAt, err)
	}
	return &u, nil
}
