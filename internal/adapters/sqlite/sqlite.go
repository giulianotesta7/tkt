// Package sqlite implements the application store ports over the modernc
// SQLite driver (D1): pure Go, CGO_ENABLED=0, FTS5 available. Production opens
// through Open, whose DSN carries the FK, WAL, synchronous=FULL, busy-timeout,
// and immediate-txlock pragmas (design "SQLite Schema"), so every connection —
// including migrations and the unit-of-work — inherits the same safety
// properties. The test-only OpenForTests entry point composes the same pragma
// fragment with synchronous=NORMAL; see its documentation for why that one
// divergence is safe.
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

// pragmaDSNHead and pragmaDSNTail bracket the one token that may differ
// between the production DSN and the fast test DSN. Both variants below are
// assembled as pragmaDSNHead + <synchronous token> + pragmaDSNTail, so the two
// are structurally incapable of drifting apart in foreign_keys, journal_mode,
// busy_timeout or _txlock.
//
// This is not the package's only DSN. testDSN in sqlite_test.go builds a
// shared-cache in-memory DSN whose pragma set genuinely differs — no
// journal_mode, because an in-memory database has no WAL — so it deliberately
// does not share this fragment.
const (
	pragmaDSNHead = "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous("
	pragmaDSNTail = ")&_pragma=busy_timeout(5000)&_txlock=immediate"
)

// synchronousFull and synchronousNormal are the only two tokens substituted
// into the DSN. FULL fsyncs the WAL on every commit and is what production
// uses; NORMAL is the fast, non-durable variant reserved for tests.
const (
	synchronousFull   = "FULL"
	synchronousNormal = "NORMAL"
)

// pragmaDSN is the production DSN pragma fragment (D1, D8): foreign_keys ON,
// WAL journaling, synchronous FULL, 5s busy timeout, and _txlock=immediate,
// which makes every write transaction BEGIN IMMEDIATE — writers serialize, so
// the MAX+1 ticket numbering is race-free by construction.
//
// synchronous=FULL is deliberate: it fsyncs the WAL on every commit, so a
// committed ticket or audit event survives a power failure or hard reset.
// NORMAL is the usual WAL companion and is faster, but SQLite documents that
// with it "transactions are no longer durable and might rollback following a
// power failure or hard reset" (sqlite.org/wal.html, Performance
// Considerations), and the exposure is every commit since the last checkpoint.
// That checkpoint is size-triggered rather than time-triggered, so on a
// low-write deployment the window spans weeks instead of seconds. Measured on
// this repository's hardware, FULL costs ~2.1ms per commit against ~0.09ms for
// NORMAL: invisible on a single form post, and ~11% on the largest test
// package. Durability was worth more than that latency.
const pragmaDSN = pragmaDSNHead + synchronousFull + pragmaDSNTail

// pragmaTestDSN is the test-only variant: byte-identical to pragmaDSN except
// for the synchronous token, which is NORMAL instead of FULL. It is composed
// from the same head and tail constants as pragmaDSN, so the two DSNs cannot
// diverge in foreign_keys, journal_mode, busy_timeout, or _txlock.
const pragmaTestDSN = pragmaDSNHead + synchronousNormal + pragmaDSNTail

// defaultMaxOpenConns bounds the production pool that openDSN configures.
// WAL allows concurrent readers and _txlock=immediate already serializes
// writers, so the ceiling exists to cap file descriptors and SQLite page
// cache, not to serialize work. It is deliberately greater than one: a pool
// of 1 would queue every read behind the single writer and regress read
// throughput. Tests that need another budget (the shared-cache memory DSN
// needs exactly 1) override it after opening.
const defaultMaxOpenConns = 4

// defaultMaxIdleConns keeps a full budget of warm connections so a burst of
// requests reuses pooled connections instead of paying a new-connection cost
// per request.
const defaultMaxIdleConns = defaultMaxOpenConns

// Store owns the single *sql.DB and hands out the adapter-side store ports
// (hexagonal-lite: one adapter, one database, one wiring point).
type Store struct {
	db *sql.DB
}

// Open connects to the SQLite database at path with the production DSN
// (design "SQLite Schema"): file:<path>?_pragma=foreign_keys(1)&
// _pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&
// _pragma=busy_timeout(5000)&_txlock=immediate.
func Open(path string) (*Store, error) {
	return openDSN("file:" + path + pragmaDSN)
}

// OpenForTests opens a database whose DSN is identical to the one Open builds
// except for the synchronous pragma, which is NORMAL instead of FULL. It
// exists so the test harnesses stop paying a durable fsync on every commit for
// a property they never assert.
//
// What differs from production: exactly one token — synchronous goes from
// FULL to NORMAL. Every other pragma (foreign_keys ON, WAL journaling, 5s
// busy_timeout, _txlock=immediate) comes from the same shared fragment Open
// uses, so the test path cannot drift from production there.
//
// Why that is safe for tests: their databases are created per test under
// t.TempDir() and discarded, so a rollback following power loss or a hard
// reset is not a test failure, and power-loss durability is not what these
// tests prove. NORMAL still commits atomically and cleanly; it only drops the
// guarantee that survives the machine losing power mid-write.
//
// Accepted cost: the function is exported because the http harness lives in a
// different package, so it is compiled into the production binary even though
// only tests call it.
func OpenForTests(path string) (*Store, error) {
	return openDSN("file:" + path + pragmaTestDSN)
}

// openDSN opens a store from a full DSN. The tests use it for shared-cache
// in-memory databases; production goes through Open.
func openDSN(dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}
	db.SetMaxOpenConns(defaultMaxOpenConns)
	db.SetMaxIdleConns(defaultMaxIdleConns)
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

// TicketMetricsStore returns the isolated administrative metrics read port.
func (s *Store) TicketMetricsStore() application.TicketMetricsStore {
	return newTicketMetricsStore(s.db)
}

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

// CatalogStore returns the Department/Desk/Category hierarchy port.
func (s *Store) CatalogStore() application.CatalogStore { return newCatalogStore(s.db) }

// DeskStore returns the desk and membership port.
func (s *Store) DeskStore() application.DeskStore { return newDeskStore(s.db) }

// WorkflowStore returns the category workflow draft/version port.
func (s *Store) WorkflowStore() application.WorkflowStore { return newWorkflowStore(s.db) }

// WorkflowVersionStore returns the current-version resolution port for
// ticket creation (design S5).
func (s *Store) WorkflowVersionStore() application.WorkflowVersionStore {
	return newWorkflowStore(s.db)
}

// WorkflowResponseStore returns the pinned-definition form-response projection.
func (s *Store) WorkflowResponseStore() application.WorkflowResponseStore {
	return newWorkflowResponseStore(s.db)
}

// WorkflowRunStore returns the ticket workflow-execution snapshot port (PR9).
func (s *Store) WorkflowRunStore() application.WorkflowRunStore {
	return newWorkflowRunStore(s.db)
}

// WorkflowUnitOfWork returns the atomic fixed-plan workflow mutation port
// (design S5).
func (s *Store) WorkflowUnitOfWork() application.WorkflowUnitOfWork {
	return newWorkflowUnitOfWork(s.db)
}

// SettingsStore returns the instance appearance settings port.
func (s *Store) SettingsStore() application.SettingsStore { return newSettingsStore(s.db) }

// SLAStore returns the SLA port (issue #211): global defaults,
// materialized category matrices, observed milestone instants, and the
// commitments frozen onto tickets.
func (s *Store) SLAStore() application.SLAStore { return newSLAStore(s.db) }

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
