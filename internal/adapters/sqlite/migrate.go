package sqlite

import (
	"context"
	"database/sql"
	"embed"
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

		tx, err := db.BeginTx(ctx, nil) // _txlock=immediate → BEGIN IMMEDIATE
		if err != nil {
			return fmt.Errorf("sqlite: begin %s: %w", name, err)
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
