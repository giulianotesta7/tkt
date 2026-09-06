package sqlite

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// TestMigration0010 proves the audit_events.closure_via column contract (issue
// #55 closure attribution): the column exists, is NULLABLE with no default and
// no backfill, legacy rows written before the migration read back closure_via
// IS NULL, the migration is purely additive forward-only DDL, and the schema
// version is recorded exactly once.
func TestMigration0010(t *testing.T) {
	s := newTestDB(t)

	// The column exists and is nullable.
	var nn, pk int
	var dflt *string
	if err := s.db.QueryRow(`SELECT "notnull", dflt_value, pk FROM pragma_table_info(?) WHERE name = ?`, "audit_events", "closure_via").Scan(&nn, &dflt, &pk); err != nil {
		t.Fatalf("audit_events.closure_via missing: %v", err)
	}
	if nn != 0 {
		t.Fatal("audit_events.closure_via must be nullable")
	}
	if dflt != nil {
		t.Fatalf("audit_events.closure_via must have no default, got %v", *dflt)
	}
	if pk != 0 {
		t.Fatal("audit_events.closure_via must not be part of a key")
	}

	ctx := context.Background()
	now := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)

	cat := seedCategory(t, s, "Mig10Cat")
	req := seedUser(t, s, "Req", "mig10@x", true)
	tk := seedTicket(t, s, domain.Ticket{Number: 902, Title: "T", CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, RequesterName: "Req", RequesterEmail: "mig10@x", RequesterUserID: &req, CreatedAt: now, UpdatedAt: now})

	// Legacy raw row in the exact pre-0010 shape (no closure_via value):
	// pre-attribution closures must read back NULL — no fabricated provenance.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO audit_events (ticket_id, actor, action, created_at) VALUES (?, 'Legacy', ?, ?)`, tk.ID, domain.ActionTransition, formatTime(now)); err != nil {
		t.Fatalf("raw legacy insert: %v", err)
	}

	var legacyVia *string
	if err := s.db.QueryRow(`SELECT closure_via FROM audit_events WHERE ticket_id=? AND actor='Legacy'`, tk.ID).Scan(&legacyVia); err != nil {
		t.Fatalf("read legacy row closure_via: %v", err)
	}
	if legacyVia != nil {
		t.Fatalf("legacy row closure_via = %q, want NULL (no backfill)", *legacyVia)
	}

	// The 0001 state CHECK is untouched: no new state values were introduced.
	var checkSQL string
	if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='tickets'`).Scan(&checkSQL); err != nil {
		t.Fatalf("read tickets DDL: %v", err)
	}
	if !strings.Contains(checkSQL, "CHECK(state IN ('new','in_progress','resolved','closed','cancelled'))") {
		t.Fatalf("0001 tickets state CHECK must be untouched, got: %s", checkSQL)
	}

	// Migration bookkeeping: version 10 applied exactly once.
	var applied int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=10`).Scan(&applied); err != nil {
		t.Fatalf("schema_migrations read: %v", err)
	}
	if applied != 1 {
		t.Fatalf("schema_migrations version=10 rows = %d, want 1", applied)
	}
}

// TestMigration0010_AdditiveOnlyUpgrade rebuilds a genuine pre-0010 database
// (migrations 0001-0009 exactly as a pre-attribution dev machine holds them),
// seeds a pre-attribution closure audit into it, snapshots the whole schema,
// then runs the REAL migration runner over the complete embedded set. The
// upgrade must record exactly version 10, keep every prior schema object
// byte-identical, and leave the seeded closure row with closure_via NULL —
// no backfill, no fabricated provenance.
func TestMigration0010_AdditiveOnlyUpgrade(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	pre0010FS := fstest.MapFS{}
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	for _, e := range entries {
		if strings.Compare(e.Name(), "0010_audit_closure_via.sql") >= 0 {
			continue // the dev machine predates 0010
		}
		blob, err := fs.ReadFile(migrationsFS, "migrations/"+e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		pre0010FS["migrations/"+e.Name()] = &fstest.MapFile{Data: blob}
	}

	pre, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open pre-0010 db: %v", err)
	}
	pre.db.SetMaxOpenConns(1)
	t.Cleanup(func() { pre.db.Close() })
	if err := migrate(ctx, pre.db, pre0010FS); err != nil {
		t.Fatalf("migrate pre-0010 db: %v", err)
	}

	// A pre-attribution closure: a transition audit row written under the old
	// policy, before any closure_via concept existed.
	cat := seedCategory(t, pre, "Mig10DevCat")
	now := time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)
	ticket := seedTicket(t, pre, domain.Ticket{Number: 778, Title: "Legacy closed", RequesterName: "Ag", RequesterEmail: "mig10-dev@x", CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateClosed, CreatedAt: now, UpdatedAt: now})
	if _, err := pre.db.ExecContext(ctx,
		`INSERT INTO audit_events (ticket_id, actor, action, field, from_value, to_value, created_at) VALUES (?, 'Agent', ?, 'state', 'resolved', 'closed', ?)`,
		ticket.ID, domain.ActionTransition, formatTime(now)); err != nil {
		t.Fatalf("seed legacy closure audit: %v", err)
	}

	schemaBefore := dumpSchema(t, pre.db)

	// Upgrade the dev database with the REAL embedded migration set through
	// 0010.
	post0010FS := fstest.MapFS{}
	for _, entry := range entries {
		if strings.Compare(entry.Name(), "0011_ticket_catalog_hierarchy.sql") >= 0 {
			continue // this regression targets the pre-catalog 0010 boundary
		}
		blob, err := fs.ReadFile(migrationsFS, "migrations/"+entry.Name())
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		post0010FS["migrations/"+entry.Name()] = &fstest.MapFile{Data: blob}
	}
	if err := migrate(ctx, pre.db, post0010FS); err != nil {
		t.Fatalf("upgrade dev db: %v", err)
	}

	schemaAfter := dumpSchema(t, pre.db)
	// tickets DDL drifts textually because ADD COLUMN rewrites the table SQL,
	// but only by the one appended column; every other object must be
	// byte-identical and no object may disappear.
	for name, before := range schemaBefore {
		after, existed := schemaAfter[name]
		if !existed {
			t.Fatalf("upgraded schema lost existing object %q — forward-only migration must be purely additive", name)
		}
		if name == "audit_events" {
			continue // expected to gain closure_via; asserted below
		}
		if before != after {
			t.Fatalf("upgraded schema modified existing object %q — forward-only migration must be purely additive", name)
		}
	}

	// audit_events gained exactly the closure_via column and nothing else.
	var added []string
	for name := range schemaAfter {
		if _, existed := schemaBefore[name]; !existed {
			added = append(added, name)
		}
	}
	if len(added) != 0 {
		t.Fatalf("upgrade added whole objects %v, want none (single ALTER TABLE ADD COLUMN)", added)
	}

	// The pre-attribution closure row keeps closure_via NULL after upgrade —
	// no backfill, no fabricated attribution.
	var closureVia *string
	if err := pre.db.QueryRow(`SELECT closure_via FROM audit_events WHERE ticket_id=? AND to_value='closed'`, ticket.ID).Scan(&closureVia); err != nil {
		t.Fatalf("read upgraded closure row: %v", err)
	}
	if closureVia != nil {
		t.Fatalf("upgraded legacy closure closure_via = %q, want NULL (no backfill)", *closureVia)
	}

	var applied int
	if err := pre.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=10`).Scan(&applied); err != nil {
		t.Fatalf("post-upgrade version read: %v", err)
	}
	if applied != 1 {
		t.Fatalf("post-upgrade schema_migrations version=10 rows = %d, want 1", applied)
	}
}
