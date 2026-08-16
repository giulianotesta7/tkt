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

// userStore implements application.UserStore (task 4.4): UNIQUE email →
// DuplicateError, delete blocked by ticket references → ReferencedError,
// inactive users remain readable (historical display), sessions of a
// hard-deleted user go with them (the sessions FK would block the delete).
type userStore struct {
	db *sql.DB
}

var _ application.UserStore = (*userStore)(nil)

func newUserStore(db *sql.DB) *userStore { return &userStore{db: db} }

// Create stores u, assigning u.ID. A duplicate email is a DuplicateError.
// u.Role is persisted when set; a zero Role omits the column so the
// migration DEFAULT ('agent') applies — legacy callers that never set a
// role keep working unchanged (S2 role round-trip). SQLite applies a column
// DEFAULT only when the column is omitted from the statement, never for an
// explicit NULL, hence the conditional column list.
func (us *userStore) Create(ctx context.Context, u *domain.User) error {
	query := `INSERT INTO users (name, email, password_hash, role, active, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`
	args := []any{u.Name, u.Email, u.PasswordHash, string(u.Role), u.Active, formatTime(u.CreatedAt)}
	if u.Role == "" {
		query = `INSERT INTO users (name, email, password_hash, active, created_at)
			VALUES (?, ?, ?, ?, ?)`
		args = []any{u.Name, u.Email, u.PasswordHash, u.Active, formatTime(u.CreatedAt)}
	}
	res, err := us.db.ExecContext(ctx, query, args...)
	if err != nil {
		if isUniqueViolation(err) {
			return &domain.DuplicateError{Kind: "user", Name: u.Email}
		}
		return fmt.Errorf("sqlite: create user: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("sqlite: user id: %w", err)
	}
	u.ID = id
	return nil
}

// Update persists the user's fields, including deactivation (Active). A
// rename onto an existing email is a DuplicateError. A zero Role leaves the
// stored role untouched (the service always populates Role from GetByID
// before updating; direct store callers keep the existing row's role). Root
// rows are protected by the DB trigger regardless of what the caller sends.
func (us *userStore) Update(ctx context.Context, u *domain.User) error {
	query := `UPDATE users SET name = ?, email = ?, password_hash = ?, role = ?, active = ?
		WHERE id = ?`
	args := []any{u.Name, u.Email, u.PasswordHash, string(u.Role), u.Active, u.ID}
	if u.Role == "" {
		query = `UPDATE users SET name = ?, email = ?, password_hash = ?, active = ?
			WHERE id = ?`
		args = []any{u.Name, u.Email, u.PasswordHash, u.Active, u.ID}
	}
	res, err := us.db.ExecContext(ctx, query, args...)
	if err != nil {
		if isUniqueViolation(err) {
			return &domain.DuplicateError{Kind: "user", Name: u.Email}
		}
		return fmt.Errorf("sqlite: update user: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: update user rows: %w", err)
	}
	if n == 0 {
		return &domain.NotFoundError{Kind: "user", ID: u.ID}
	}
	return nil
}

// Delete removes an unreferenced user and their sessions in one
// transaction. A user assigned to tickets is a ReferencedError — the
// tickets FK fires after the session cleanup and rolls the whole delete
// back (deactivation is the removal path then, user-management spec).
func (us *userStore) Delete(ctx context.Context, id int64) error {
	tx, err := us.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin delete user: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
		tx.Rollback()
		return fmt.Errorf("sqlite: delete user sessions: %w", err)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		tx.Rollback()
		if isForeignKeyViolation(err) {
			return &domain.ReferencedError{Kind: "user", ID: id}
		}
		return fmt.Errorf("sqlite: delete user: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("sqlite: delete user rows: %w", err)
	}
	if n == 0 {
		tx.Rollback()
		return &domain.NotFoundError{Kind: "user", ID: id}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit delete user: %w", err)
	}
	return nil
}

// GetByID returns the user, including inactive ones (historical display);
// ErrNotFound when absent.
func (us *userStore) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	u, err := scanUserFrom(us.db.QueryRowContext(ctx, `SELECT id, name, email, password_hash, role, active, created_at FROM users WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &domain.NotFoundError{Kind: "user", ID: id}
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// GetByEmail returns the user by email; ErrNotFound when absent.
func (us *userStore) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	u, err := scanUserFrom(us.db.QueryRowContext(ctx, `SELECT id, name, email, password_hash, role, active, created_at FROM users WHERE email = ?`, email))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &domain.NotFoundError{Kind: "user", ID: email}
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// Count returns the number of users (first-user bootstrap check, D16).
func (us *userStore) Count(ctx context.Context) (int, error) {
	var n int
	if err := us.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("sqlite: count users: %w", err)
	}
	return n, nil
}

// List returns all users ordered by id.
func (us *userStore) List(ctx context.Context) ([]domain.User, error) {
	return us.listWhere(ctx, "")
}

// ListActive returns only active users.
func (us *userStore) ListActive(ctx context.Context) ([]domain.User, error) {
	return us.listWhere(ctx, "WHERE active = 1")
}

func (us *userStore) listWhere(ctx context.Context, where string) ([]domain.User, error) {
	rows, err := us.db.QueryContext(ctx, `SELECT id, name, email, password_hash, role, active, created_at FROM users `+where+` ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list users: %w", err)
	}
	defer rows.Close()
	var out []domain.User
	for rows.Next() {
		u, err := scanUserFrom(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan user: %w", err)
		}
		out = append(out, *u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list users: %w", err)
	}
	return out, nil
}

// BootstrapRoot creates the very first user with role root ATOMICALLY
// (role-authorization "First-User Root Bootstrap"). The count check and the
// insert share one immediate transaction (the DSN's _txlock=immediate):
// concurrent bootstrap calls serialize on the write lock, the loser's count
// check observes the winner's committed row, and exactly one root exists.
// Bootstrap is unavailable once ANY user exists (ErrBootstrapUnavailable) —
// recovery and backfill are the only other root-creating paths.
func (us *userStore) BootstrapRoot(ctx context.Context, u *domain.User) error {
	tx, err := beginImmediate(ctx, us.db, "bootstrap")
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return fmt.Errorf("sqlite: bootstrap count users: %w", err)
	}
	if n > 0 {
		return domain.NewBootstrapUnavailableError()
	}

	res, err := tx.ExecContext(ctx, `INSERT INTO users (name, email, password_hash, role, active, created_at)
		VALUES (?, ?, ?, 'root', ?, ?)`,
		u.Name, u.Email, u.PasswordHash, u.Active, formatTime(u.CreatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return &domain.DuplicateError{Kind: "user", Name: u.Email}
		}
		return fmt.Errorf("sqlite: bootstrap insert: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("sqlite: bootstrap id: %w", err)
	}
	u.ID = id
	u.Role = domain.RoleRoot
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit bootstrap: %w", err)
	}
	return nil
}

// scanUserFrom projects one row into a domain.User. active is stored as
// INTEGER 0/1 (CHECK) and converted to bool; the role column is guaranteed
// valid by the CHECK constraint, and an unknown role would fail closed in
// the policy layer anyway (the zero Role grants nothing).
func scanUserFrom(scan rowScanner) (*domain.User, error) {
	var (
		u         domain.User
		role      string
		active    int64
		createdAt string
	)
	if err := scan.Scan(&u.ID, &u.Name, &u.Email, &u.PasswordHash, &role, &active, &createdAt); err != nil {
		return nil, err
	}
	u.Role = domain.Role(role)
	u.Active = active == 1
	var err error
	if u.CreatedAt, err = time.Parse(timeLayout, createdAt); err != nil {
		return nil, fmt.Errorf("sqlite: parse user created_at %q: %w", createdAt, err)
	}
	return &u, nil
}
