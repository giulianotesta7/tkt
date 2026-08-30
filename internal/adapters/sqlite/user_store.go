package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
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

// UpdateManagedUser writes the non-password form fields and, when needed,
// appends the role audit in one immediate transaction.
func (us *userStore) UpdateManagedUser(ctx context.Context, u *domain.User, expectedRole domain.Role, actorID int64, at time.Time) error {
	tx, err := beginImmediate(ctx, us.db, "update managed user")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE users SET name = ?, email = ?, role = ?, active = ? WHERE id = ? AND role = ?`, u.Name, u.Email, string(u.Role), u.Active, u.ID, string(expectedRole))
	if err != nil {
		if isUniqueViolation(err) {
			return &domain.DuplicateError{Kind: "user", Name: u.Email}
		}
		return fmt.Errorf("sqlite: update managed user: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: update managed user rows: %w", err)
	}
	if n == 0 {
		return &domain.NotFoundError{Kind: "user", ID: u.ID}
	}
	if u.Role != expectedRole {
		if _, err := tx.ExecContext(ctx, `INSERT INTO role_changes (user_id, from_role, to_role, actor_user_id, reason, created_at) VALUES (?, ?, ?, ?, ?, ?)`, u.ID, string(expectedRole), string(u.Role), actorID, "role change", formatTime(at)); err != nil {
			return fmt.Errorf("sqlite: audit role change: %w", err)
		}
	}
	return tx.Commit()
}

// downgradeHandoffReason is the audit reason identifying the automatic
// ticket handoff performed by an agent-to-user role downgrade (issue #47).
var downgradeHandoffReason = "role downgrade handoff"

// DowngradeToUser applies the agent-to-user downgrade lifecycle (issue #47)
// as ONE immediate transaction: desk memberships are removed first (which
// satisfies trg_users_no_desk_member_downgrade and re-scopes the least-loaded
// pool so the downgraded account is never its own replacement), every open
// (new/in_progress) assigned ticket is handed off to the deterministic
// least-loaded eligible member of its resolved desk (or left unassigned), the
// guarded role update persists, one handoff audit event rides per affected
// ticket, and the role_changes row records the flip. Any failure rolls the
// whole lifecycle back. An account with no memberships and no open tickets
// degenerates to the plain managed update.
func (us *userStore) DowngradeToUser(ctx context.Context, u *domain.User, expectedRole domain.Role, actorID int64, at time.Time) (*domain.User, error) {
	tx, err := beginImmediate(ctx, us.db, "downgrade to user")
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM desk_members WHERE user_id = ?`, u.ID); err != nil {
		return nil, fmt.Errorf("sqlite: downgrade delete memberships: %w", err)
	}

	actorName, err := downgradeActorNameTx(ctx, tx, actorID)
	if err != nil {
		return nil, err
	}

	type handoffTicket struct {
		id                int64
		assigneeID        sql.NullInt64
		workflowVersionID sql.NullInt64
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, user_id, workflow_version_id FROM tickets
		WHERE user_id = ? AND state IN ('new', 'in_progress') ORDER BY id ASC`, u.ID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: downgrade select tickets: %w", err)
	}
	var open []handoffTicket
	for rows.Next() {
		var ht handoffTicket
		if err := rows.Scan(&ht.id, &ht.assigneeID, &ht.workflowVersionID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("sqlite: downgrade scan ticket: %w", err)
		}
		open = append(open, ht)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("sqlite: downgrade tickets rows: %w", err)
	}
	rows.Close()

	field := "user"
	var events []domain.AuditEvent
	for _, ht := range open {
		deskID, err := downgradeDeskTx(ctx, tx, ht.id, ht.workflowVersionID)
		if err != nil {
			return nil, err
		}
		var replacement sql.NullInt64
		if deskID != nil {
			id, err := leastLoadedAssigneeTx(ctx, tx, *deskID)
			if err != nil {
				return nil, err
			}
			if id != 0 {
				replacement = sql.NullInt64{Int64: id, Valid: true}
			}
		}
		var replacementPtr *int64
		if replacement.Valid {
			replacementPtr = &replacement.Int64
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tickets SET user_id = ?, updated_at = ? WHERE id = ?`,
			nullableInt64(replacementPtr), formatTime(at), ht.id); err != nil {
			return nil, fmt.Errorf("sqlite: downgrade handoff ticket: %w", err)
		}
		from := ""
		if ht.assigneeID.Valid {
			from = strconv.FormatInt(ht.assigneeID.Int64, 10)
		}
		to := ""
		if replacement.Valid {
			to = strconv.FormatInt(replacement.Int64, 10)
		}
		events = append(events, domain.AuditEvent{
			TicketID:    ht.id,
			Actor:       actorName,
			ActorUserID: &actorID,
			Action:      domain.ActionUpdate,
			Field:       &field,
			FromValue:   &from,
			ToValue:     &to,
			Reason:      downgradeReasonPtr(),
			DeskID:      deskID,
			CreatedAt:   at,
		})
	}

	res, err := tx.ExecContext(ctx, `UPDATE users SET name = ?, email = ?, role = ?, active = ? WHERE id = ? AND role = ?`,
		u.Name, u.Email, string(u.Role), u.Active, u.ID, string(expectedRole))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, &domain.DuplicateError{Kind: "user", Name: u.Email}
		}
		return nil, fmt.Errorf("sqlite: downgrade user: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("sqlite: downgrade user rows: %w", err)
	}
	if n == 0 {
		return nil, &domain.NotFoundError{Kind: "user", ID: u.ID}
	}

	if len(events) > 0 {
		if err := appendAuditEventsTx(ctx, tx, events...); err != nil {
			return nil, err
		}
	}
	if u.Role != expectedRole {
		if _, err := tx.ExecContext(ctx, `INSERT INTO role_changes (user_id, from_role, to_role, actor_user_id, reason, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			u.ID, string(expectedRole), string(u.Role), actorID, "role change", formatTime(at)); err != nil {
			return nil, fmt.Errorf("sqlite: audit role change: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("sqlite: commit downgrade: %w", err)
	}
	cp := *u
	return &cp, nil
}

// downgradeReasonPtr returns the shared downgrade handoff reason pointer.
func downgradeReasonPtr() *string { return &downgradeHandoffReason }

// downgradeActorNameTx reads the initiating actor's display name for the
// handoff audit events (the manual-assignment convention stores the session
// actor's name alongside the actor user id).
func downgradeActorNameTx(ctx context.Context, tx *sql.Tx, actorID int64) (string, error) {
	var name string
	err := tx.QueryRowContext(ctx, `SELECT name FROM users WHERE id = ?`, actorID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("sqlite: downgrade actor name: %w", err)
	}
	return name, nil
}

// downgradeDeskTx resolves the handoff desk context of one ticket, in
// priority order: the latest audit event carrying a desk_id (assignment
// context snapshot), else the first assign_to_desk step by step order in the
// ticket's pinned workflow version, else nil (unresolvable — the ticket
// becomes unassigned, issue #47 fallback).
func downgradeDeskTx(ctx context.Context, tx *sql.Tx, ticketID int64, workflowVersionID sql.NullInt64) (*int64, error) {
	var desk sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT desk_id FROM audit_events WHERE ticket_id = ? AND desk_id IS NOT NULL ORDER BY id DESC LIMIT 1`, ticketID).Scan(&desk)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("sqlite: downgrade desk audit: %w", err)
	}
	if desk.Valid {
		id := desk.Int64
		return &id, nil
	}
	if !workflowVersionID.Valid {
		return nil, nil
	}
	var steps string
	err = tx.QueryRowContext(ctx, `SELECT steps_json FROM workflow_versions WHERE id = ?`, workflowVersionID.Int64).Scan(&steps)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: downgrade desk workflow: %w", err)
	}
	err = tx.QueryRowContext(ctx, `SELECT CAST(json_extract(je.value, '$.assign_to_desk.desk_id') AS INTEGER)
		FROM json_each(?, '$') je
		WHERE json_extract(je.value, '$.type') = 'assign_to_desk'
		ORDER BY CAST(je.key AS INTEGER) ASC
		LIMIT 1`, steps).Scan(&desk)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("sqlite: downgrade desk step: %w", err)
	}
	if !desk.Valid {
		return nil, nil
	}
	id := desk.Int64
	return &id, nil
}

// UpdatePasswordHash changes only the password hash for one existing user.
func (us *userStore) UpdatePasswordHash(ctx context.Context, id int64, passwordHash string) error {
	res, err := us.db.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, passwordHash, id)
	if err != nil {
		return fmt.Errorf("sqlite: update password hash: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: update password hash rows: %w", err)
	}
	if n == 0 {
		return &domain.NotFoundError{Kind: "user", ID: id}
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
