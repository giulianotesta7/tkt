package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// ErrRecoverRootRequired is returned by the legacy backfill when users exist
// without a provable root (the reliable id=1 setup user is absent): startup
// must FAIL CLOSED and the operator must select the root via
// -recover-root=<user-id> (design "Persistence and Recovery"; role-
// authorization "Operator-Selected Root Recovery"). The command layer
// tolerates exactly this error when the flag is set — it is the situation
// the flag exists to resolve.
var ErrRecoverRootRequired = errors.New("users exist without a root and the legacy setup user id=1 is absent; run -recover-root=<user-id>")

// Migrate applies the embedded versioned migrations (D10) that are not yet
// recorded in schema_migrations. Each migration runs in its own immediate
// transaction (the DSN's _txlock=immediate); a failure rolls the whole
// migration back, and a rerun is a no-op. Errors are fatal to startup (the
// composition root decides), never silently skipped.
func (s *Store) Migrate(ctx context.Context) error {
	return migrate(ctx, s.db, migrationsFS)
}

// migrate applies the .sql files under migrations/ from fsys in version
// order. It bootstraps schema_migrations (the runner needs the table before
// the first version check), skips applied versions, and records each
// applied version inside the same transaction as the migration itself.
func migrate(ctx context.Context, db *sql.DB, fsys fs.FS) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("sqlite: bootstrap schema_migrations: %w", err)
	}

	entries, err := fs.Glob(fsys, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("sqlite: list migrations: %w", err)
	}
	sort.Strings(entries)

	for _, name := range entries {
		version, err := migrationVersion(name)
		if err != nil {
			return err
		}
		applied, err := migrationApplied(ctx, db, version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}

		script, err := fs.ReadFile(fsys, name)
		if err != nil {
			return fmt.Errorf("sqlite: read %s: %w", name, err)
		}

		tx, err := beginImmediate(ctx, db, name)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(script)); err != nil {
			tx.Rollback()
			return fmt.Errorf("sqlite: apply %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			version, time.Now().UTC().Format(time.RFC3339)); err != nil {
			tx.Rollback()
			return fmt.Errorf("sqlite: record version %d: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("sqlite: commit %s: %w", name, err)
		}
	}

	// After every run, normalize legacy rows once 0003 exists: promote the
	// reliable id=1 setup user to root and backfill requester ownership
	// from reliable audit evidence. Idempotent, fail-closed (see
	// backfillRolesAndRequesters).
	if err := backfillRolesAndRequesters(ctx, db); err != nil {
		return err
	}
	return nil
}

// migrationVersion parses the leading zero-padded version from an
// NNNN_name.sql filename ("0001_init.sql" → 1).
func migrationVersion(name string) (int, error) {
	base := path.Base(name)
	rest := strings.TrimLeft(base, "0123456789")
	if rest == base {
		return 0, fmt.Errorf("sqlite: migration %q has no leading version", name)
	}
	v, err := strconv.Atoi(base[:len(base)-len(rest)])
	if err != nil {
		return 0, fmt.Errorf("sqlite: migration %q version: %w", name, err)
	}
	return v, nil
}

// migrationApplied reports whether the version is already recorded.
func migrationApplied(ctx context.Context, db *sql.DB, version int) (bool, error) {
	var exists int
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = ?)`, version).Scan(&exists); err != nil {
		return false, fmt.Errorf("sqlite: check version %d: %w", version, err)
	}
	return exists == 1, nil
}

// backfillRolesAndRequesters normalizes legacy rows once migration 0003 is
// recorded (design "Persistence and Recovery"; role-authorization and
// ticket-access "Legacy Ownership Backfill"). It is a no-op on the
// pre-0003 schema and idempotent on rerun. The whole backfill runs in one
// immediate transaction, so a failure rolls every promotion back.
//
// Root: when no root exists, the reliable legacy setup user (id=1 under
// AUTOINCREMENT — never MIN(id)) is promoted to root and the promotion is
// audited in role_changes. When users exist but id=1 is absent the setup
// user cannot be proven, so migration FAILS CLOSED and the operator must
// select a root via -recover-root.
//
// Requester: a ticket with NULL requester_user_id is backfilled ONLY when
// exactly one 'created' audit event exists, exactly one surviving user
// matches the ticket's requester_name/requester_email snapshot, and that
// user's name equals the creation event's actor. Any ambiguity leaves NULL
// — the ticket stays visible to roles agent+ only, never attributed to a
// guessed user.
func backfillRolesAndRequesters(ctx context.Context, db *sql.DB) error {
	applied, err := migrationApplied(ctx, db, 3)
	if err != nil {
		return err
	}
	if !applied {
		return nil // pre-0003 schema: nothing to normalize
	}

	tx, err := beginImmediate(ctx, db, "backfill")
	if err != nil {
		return err
	}
	if err := backfillRoot(ctx, tx); err != nil {
		tx.Rollback()
		return err
	}
	if err := backfillRequesters(ctx, tx); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit backfill: %w", err)
	}
	return nil
}

// backfillRoot promotes the reliable id=1 legacy setup user to root, or
// fails closed when the setup user cannot be proven.
func backfillRoot(ctx context.Context, tx *sql.Tx) error {
	var rootCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role = 'root'`).Scan(&rootCount); err != nil {
		return fmt.Errorf("sqlite: backfill count roots: %w", err)
	}
	if rootCount > 0 {
		return nil // a root exists (fresh bootstrap or a prior backfill)
	}
	var userCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&userCount); err != nil {
		return fmt.Errorf("sqlite: backfill count users: %w", err)
	}
	if userCount == 0 {
		return nil // fresh database; /setup creates the root
	}
	var hasID1 int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id = 1)`).Scan(&hasID1); err != nil {
		return fmt.Errorf("sqlite: backfill check id 1: %w", err)
	}
	if hasID1 == 0 {
		return fmt.Errorf("sqlite: %w", ErrRecoverRootRequired)
	}
	// Legacy id=1 is the original setup user under AUTOINCREMENT: promote
	// AND ACTIVATE it and audit (an inactive root would be unusable and
	// unrecoverable — the root account is immutable and recovery refuses
	// when a root exists — R3-001; the root-immutable triggers fire only
	// when OLD.role is already 'root', so this update is unaffected).
	if _, err := tx.ExecContext(ctx, `UPDATE users SET role = 'root', active = 1 WHERE id = 1`); err != nil {
		return fmt.Errorf("sqlite: backfill promote root: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO role_changes (user_id, from_role, to_role, actor_user_id, reason, created_at)
		 VALUES (1, 'agent', 'root', NULL, 'legacy setup user backfill', ?)`,
		time.Now().UTC().Format(timeLayout)); err != nil {
		return fmt.Errorf("sqlite: backfill audit root promotion: %w", err)
	}
	return nil
}

// backfillRequesters attributes legacy tickets to their creator only from
// reliable audit evidence; anything ambiguous stays NULL (agent+-only).
func backfillRequesters(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT t.id, t.requester_name, t.requester_email,
		       (SELECT COUNT(*) FROM audit_events a WHERE a.ticket_id = t.id AND a.action = 'created')
		FROM tickets t WHERE t.requester_user_id IS NULL`)
	if err != nil {
		return fmt.Errorf("sqlite: backfill list tickets: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id             int64
			requesterName  string
			requesterEmail string
			createdEvents  int
		)
		if err := rows.Scan(&id, &requesterName, &requesterEmail, &createdEvents); err != nil {
			return fmt.Errorf("sqlite: backfill scan ticket: %w", err)
		}
		// Exactly one creation event, or the creator is unprovable.
		if createdEvents != 1 {
			continue
		}
		// Exactly one surviving user matching the ticket's snapshot.
		var n int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM users WHERE name = ? AND email = ?`, requesterName, requesterEmail).Scan(&n); err != nil {
			return fmt.Errorf("sqlite: backfill count requester matches: %w", err)
		}
		if n != 1 {
			continue
		}
		var userID int64
		if err := tx.QueryRowContext(ctx,
			`SELECT id FROM users WHERE name = ? AND email = ?`, requesterName, requesterEmail).Scan(&userID); err != nil {
			return fmt.Errorf("sqlite: backfill requester id: %w", err)
		}
		// The creation event must name that same user — a renamed or
		// guessed identity never earns attribution.
		var actor string
		if err := tx.QueryRowContext(ctx,
			`SELECT actor FROM audit_events WHERE ticket_id = ? AND action = 'created'`, id).Scan(&actor); err != nil {
			return fmt.Errorf("sqlite: backfill created actor: %w", err)
		}
		if actor != requesterName {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE tickets SET requester_user_id = ? WHERE id = ?`, userID, id); err != nil {
			return fmt.Errorf("sqlite: backfill requester ticket %d: %w", id, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("sqlite: backfill iterate tickets: %w", err)
	}
	return nil
}
