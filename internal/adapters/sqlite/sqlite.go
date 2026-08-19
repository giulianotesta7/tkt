// Package sqlite implements the application store ports over the modernc
// SQLite driver (D1): pure Go, CGO_ENABLED=0, FTS5 available. The single
// Open DSN carries the FK, WAL, busy-timeout, and immediate-txlock pragmas
// (design "SQLite Schema"), so every connection — including migrations and
// the unit-of-work — inherits the same safety properties.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/giulianotesta7/tkt/internal/application"
)

// pragmaDSN is the single DSN pragma fragment (D1, D8): foreign_keys ON,
// WAL journaling, 5s busy timeout, and _txlock=immediate, which makes every
// write transaction BEGIN IMMEDIATE — writers serialize, so the MAX+1
// ticket numbering is race-free by construction.
const pragmaDSN = "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate"

// Store owns the single *sql.DB and hands out the adapter-side store ports
// (hexagonal-lite: one adapter, one database, one wiring point).
type Store struct {
	db *sql.DB
}

// Open connects to the SQLite database at path with the single DSN
// (design "SQLite Schema"): file:<path>?_pragma=foreign_keys(1)&
// _pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate.
func Open(path string) (*Store, error) {
	return openDSN("file:" + path + pragmaDSN)
}

// openDSN opens a store from a full DSN. The tests use it for shared-cache
// in-memory databases; production goes through Open.
func openDSN(dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}
	s := &Store{db: db}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite: ping: %w", err)
	}
	return s, nil
}

// TicketStore returns the ticket read/list port (task 4.2).
func (s *Store) TicketStore() application.TicketStore { return newTicketStore(s.db) }

// TicketUnitOfWork returns the atomic ticket+audit mutation port (C1).
func (s *Store) TicketUnitOfWork() application.TicketUnitOfWork { return newUnitOfWork(s.db) }

// CommentStore returns the comment timeline port (task 4.3).
func (s *Store) CommentStore() application.CommentStore { return newCommentStore(s.db) }

// AuditStore returns the audit trail port (task 4.3).
func (s *Store) AuditStore() application.AuditStore { return newAuditStore(s.db) }

// UserStore returns the user port (task 4.4).
func (s *Store) UserStore() application.UserStore { return newUserStore(s.db) }

// SessionStore returns the session port (task 4.4).
func (s *Store) SessionStore() application.SessionStore { return newSessionStore(s.db) }

// SearchStore returns the FTS5 search port (task 4.5).
func (s *Store) SearchStore() application.SearchStore { return newSearchStore(s.db) }

// CategoryStore returns the category port (task 4.6). The accessor was
// deferred to the HTTP slice (4.6 kept newCategoryStore package-private);
// task 5.4 is its first consumer — ticket forms and filters list categories.
func (s *Store) CategoryStore() application.CategoryStore { return newCategoryStore(s.db) }

// DeskStore returns the desk and membership port.
func (s *Store) DeskStore() application.DeskStore { return newDeskStore(s.db) }

// Ping verifies the database connection is alive (SELECT 1). The
// composition root's -healthcheck flag uses it.
func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// Close releases the underlying *sql.DB (phase 6 composition root).
func (s *Store) Close() error {
	return s.db.Close()
}

// --- constraint and value helpers ---

// isConstraint reports whether err is a SQLite constraint failure.
// modernc's Error carries the extended result code (UNIQUE=2067,
// FOREIGN KEY=787), with the primary code in the low byte
// (SQLITE_CONSTRAINT=19); the errmsg text distinguishes UNIQUE from
// FOREIGN KEY violations.
func isConstraint(err error) bool {
	var se *sqlite.Error
	if !errors.As(err, &se) {
		return false
	}
	return se.Code()&0xFF == sqlite3.SQLITE_CONSTRAINT
}

// isUniqueViolation reports a UNIQUE constraint failure ("UNIQUE constraint
// failed: <table>.<column>").
func isUniqueViolation(err error) bool {
	return isConstraint(err) && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// isForeignKeyViolation reports a FOREIGN KEY constraint failure.
func isForeignKeyViolation(err error) bool {
	return isConstraint(err) && strings.Contains(err.Error(), "FOREIGN KEY constraint failed")
}

// isBusy reports whether err is a transient lock failure that a retry can
// clear: SQLITE_BUSY (5) or SQLITE_LOCKED (6), including the extended
// shared-cache form (262 = SQLITE_LOCKED_SHAREDCACHE) that the busy_timeout
// pragma does NOT cover. In-memory shared caches can return LOCKED instead
// of BUSY when two connections BEGIN IMMEDIATE at once.
func isBusy(err error) bool {
	var se *sqlite.Error
	if !errors.As(err, &se) {
		return false
	}
	code := se.Code() & 0xFF
	return code == sqlite3.SQLITE_BUSY || code == sqlite3.SQLITE_LOCKED
}

// beginImmediate starts a write transaction in immediate mode, retrying a
// bounded number of times while the database is transiently locked. The
// busy_timeout covers SQLITE_BUSY on file-backed WAL databases, but an
// in-memory shared cache can surface SQLITE_LOCKED_SHAREDCACHE (262) which
// the timeout does not handle — the retry closes that gap so concurrent
// bootstrap/recovery writers serialize instead of failing spuriously.
func beginImmediate(ctx context.Context, db *sql.DB, op string) (*sql.Tx, error) {
	const attempts = 5
	const sleep = 10 * time.Millisecond
	var tx *sql.Tx
	var err error
	for i := 0; i < attempts; i++ {
		tx, err = db.BeginTx(ctx, nil) // _txlock=immediate → BEGIN IMMEDIATE
		if err == nil || !isBusy(err) {
			return tx, err
		}
		time.Sleep(sleep)
	}
	return nil, fmt.Errorf("sqlite: begin %s: %w", op, err)
}

// retryUnique re-runs fn up to attempts times while it fails with a UNIQUE
// constraint violation (D8 belt-and-suspenders: MAX+1 inside an immediate
// transaction cannot collide, so this only fires on an unexpected race).
func retryUnique(attempts int, fn func() error) error {
	var err error
	for i := 0; i < attempts; i++ {
		err = fn()
		if !isUniqueViolation(err) {
			return err
		}
	}
	return err
}

// nullableInt64 binds a *int64 as NULL when nil.
func nullableInt64(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

// nullableString binds a *string as NULL when nil.
func nullableString(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}

// formatTime renders t in the persisted ISO-8601 UTC TEXT form (D7).
func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// formatTimePtr binds a *time.Time as NULL when nil.
func formatTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(timeLayout)
}
