// Package sqlite implements the application store ports over the modernc
// SQLite driver (D1): pure Go, CGO_ENABLED=0, FTS5 available. The single
// Open DSN carries the FK, WAL, busy-timeout, and immediate-txlock pragmas
// (design "SQLite Schema"), so every connection — including migrations and
// the unit-of-work — inherits the same safety properties.
package sqlite

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// pragmaDSN is the single DSN pragma fragment (D1, D8): foreign_keys ON,
// WAL journaling, 5s busy timeout, and _txlock=immediate, which makes every
// write transaction BEGIN IMMEDIATE — writers serialize, so the MAX+1
// ticket numbering is race-free by construction.
const pragmaDSN = "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate"

// Store implements every adapter-side store port over one *sql.DB. The
// concrete store types in this package are methods on Store; each store
// file carries a compile-time port assertion.
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
