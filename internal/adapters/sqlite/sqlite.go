// Package sqlite implements the application store ports over the modernc
// SQLite driver (D1): pure Go, CGO_ENABLED=0, FTS5 available. The single
// Open DSN carries the FK, WAL, busy-timeout, and immediate-txlock pragmas
// (design "SQLite Schema"), so every connection — including migrations and
// the unit-of-work — inherits the same safety properties.
package sqlite

import (
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
