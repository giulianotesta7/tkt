package sqlite

import (
	"context"
	"database/sql"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// TestMigration0009 proves the ticket_manual_solutions contract (Amendment 2
// WA.1): the additive forward-only table keyed PRIMARY KEY(ticket_id,
// step_index), the 2,000-character CHECK mirroring the transport bound as
// defense in depth, the run/user foreign keys with ON DELETE CASCADE, the
// recorded schema version, and that a pre-0009 (already-migrated PR10-close)
// dev database gains ONLY this table with NO data backfill.
func TestMigration0009(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	// --- Column shape: every column NOT NULL, no defaults, composite PK. ---
	type col struct {
		notnull int
		dflt    *string
		pk      int
	}
	wantCols := map[string]col{
		"ticket_id":          {notnull: 1, dflt: nil, pk: 1},
		"step_index":         {notnull: 1, dflt: nil, pk: 2},
		"solution":           {notnull: 1, dflt: nil, pk: 0},
		"created_by_user_id": {notnull: 1, dflt: nil, pk: 0},
		"created_at":         {notnull: 1, dflt: nil, pk: 0},
	}
	for name, want := range wantCols {
		var got col
		err := s.db.QueryRow(`SELECT "notnull", dflt_value, pk FROM pragma_table_info(?) WHERE name = ?`,
			"ticket_manual_solutions", name).Scan(&got.notnull, &got.dflt, &got.pk)
		if err != nil {
			t.Fatalf("ticket_manual_solutions.%s missing: %v", name, err)
		}
		if got != want {
			t.Errorf("column %s = %+v, want %+v", name, got, want)
		}
	}

	// --- Foreign keys: run cascade + user reference. ---
	type fk struct {
		table, from, to, onDelete string
	}
	fks := map[string]fk{}
	rows, err := s.db.Query(`SELECT "table", "from", "to", on_delete FROM pragma_foreign_key_list(?)`, "ticket_manual_solutions")
	if err != nil {
		t.Fatalf("foreign key list: %v", err)
	}
	for rows.Next() {
		var f fk
		if err := rows.Scan(&f.table, &f.from, &f.to, &f.onDelete); err != nil {
			t.Fatalf("scan fk: %v", err)
		}
		fks[f.from] = f
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate fks: %v", err)
	}
	runFK, ok := fks["ticket_id"]
	if !ok || runFK.table != "ticket_workflow_runs" || runFK.to != "ticket_id" || runFK.onDelete != "CASCADE" {
		t.Fatalf("ticket_id FK = %+v (found %v), want ticket_workflow_runs.ticket_id ON DELETE CASCADE", runFK, ok)
	}
	userFK, ok := fks["created_by_user_id"]
	if !ok || userFK.table != "users" || userFK.onDelete != "NO ACTION" {
		t.Fatalf("created_by_user_id FK = %+v (found %v), want users(id)", userFK, ok)
	}

	// --- Write behavior against real seeded facts. ---
	cat := seedCategory(t, s, "Mig9Cat")
	agent := seedUserRole(t, s, "Agent", "mig9-agent@x", true, domain.RoleAgent)
	versionID := seedPublished(t, s, cat, domain.WorkflowDefinition{
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Rack the server"}},
	})
	now := testClock
	ticket := seedPinnedTicket(t, s, domain.Ticket{Number: 901, Title: "T", RequesterName: "Ag", RequesterEmail: "mig9-agent@x", CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &versionID})
	seedRun(t, s, ticket.ID, 1, "active", now)
	if _, err := s.db.ExecContext(ctx, `UPDATE ticket_workflow_runs SET status='completed', completed_at=? WHERE ticket_id=?`, formatTime(now), ticket.ID); err != nil {
		t.Fatalf("complete seeded run: %v", err)
	}

	exactly2000 := strings.Repeat("x", 2000)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO ticket_manual_solutions (ticket_id, step_index, solution, created_by_user_id, created_at) VALUES (?, ?, ?, ?, ?)`,
		ticket.ID, 1, exactly2000, agent, formatTime(now)); err != nil {
		t.Fatalf("insert 2,000-char solution (transport-bound mirror) rejected: %v", err)
	}

	// Composite PK: at most one solution per completed manual step.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO ticket_manual_solutions (ticket_id, step_index, solution, created_by_user_id, created_at) VALUES (?, ?, ?, ?, ?)`,
		ticket.ID, 1, "duplicate", agent, formatTime(now)); err == nil {
		t.Fatal("duplicate (ticket_id, step_index) must violate the composite primary key")
	}

	// CHECK(step_index >= 0).
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO ticket_manual_solutions (ticket_id, step_index, solution, created_by_user_id, created_at) VALUES (?, ?, ?, ?, ?)`,
		ticket.ID, -1, "negative", agent, formatTime(now)); err == nil {
		t.Fatal("negative step_index must violate the CHECK")
	}

	// CHECK(length(solution) <= 2000) mirrors the transport bound.
	tooLong := strings.Repeat("y", 2001)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO ticket_manual_solutions (ticket_id, step_index, solution, created_by_user_id, created_at) VALUES (?, ?, ?, ?, ?)`,
		ticket.ID, 2, tooLong, agent, formatTime(now)); err == nil {
		t.Fatal("2,001-character solution must violate the CHECK")
	}

	// FK to ticket_workflow_runs: a solution cannot exist without its run.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO ticket_manual_solutions (ticket_id, step_index, solution, created_by_user_id, created_at) VALUES (?, ?, ?, ?, ?)`,
		ticket.ID+100000, 0, "orphan", agent, formatTime(now)); err == nil {
		t.Fatal("solution for a nonexistent run must violate the foreign key")
	}

	// FK to users: the completion actor fact is mandatory and real.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO ticket_manual_solutions (ticket_id, step_index, solution, created_by_user_id, created_at) VALUES (?, ?, ?, ?, ?)`,
		ticket.ID, 3, "ghost actor", 999999, formatTime(now)); err == nil {
		t.Fatal("solution with a nonexistent actor must violate the foreign key")
	}

	// ON DELETE CASCADE: deleting the run removes its solution records.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM ticket_workflow_runs WHERE ticket_id=?`, ticket.ID); err != nil {
		t.Fatalf("delete run: %v", err)
	}
	var remaining int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ticket_manual_solutions WHERE ticket_id=?`, ticket.ID).Scan(&remaining); err != nil {
		t.Fatalf("count after cascade: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("solution rows survived run deletion: %d remain", remaining)
	}

	// --- Version bookkeeping: 0009 applied exactly once. ---
	var applied int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=9`).Scan(&applied); err != nil {
		t.Fatalf("schema_migrations read: %v", err)
	}
	if applied != 1 {
		t.Fatalf("schema_migrations version=9 rows = %d, want 1", applied)
	}

	// --- Already-migrated dev database: gains ONLY this table, NO backfill. ---
	testMigration0009_DevDatabaseUpgradeOnly(t)
}

// testMigration0009_DevDatabaseUpgradeOnly rebuilds a genuine pre-0009
// database (migrations 0001–0008 exactly as a PR10-close dev machine holds
// them), seeds a pre-amendment manual completion into it, snapshots the whole
// schema, then runs the REAL migration runner over the complete embedded set.
// The upgrade must add exactly one schema object — the ticket_manual_solutions
// table — record exactly version 9, keep every prior object byte-identical,
// leave the seeded pre-amendment completion untouched, and persist NO
// backfilled solution rows.
func testMigration0009_DevDatabaseUpgradeOnly(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	pre0009FS := fstest.MapFS{}
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	for _, e := range entries {
		// The dev machine predates 0009; later migrations (including any added
		// after 0009, e.g. 0010) must not leak into the pre-upgrade snapshot.
		if strings.Compare(e.Name(), "0009_ticket_manual_solutions.sql") >= 0 {
			continue
		}
		blob, err := fs.ReadFile(migrationsFS, "migrations/"+e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		pre0009FS["migrations/"+e.Name()] = &fstest.MapFile{Data: blob}
	}

	pre, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open pre-0009 db: %v", err)
	}
	pre.db.SetMaxOpenConns(1)
	t.Cleanup(func() { pre.db.Close() })
	if err := migrate(ctx, pre.db, pre0009FS); err != nil {
		t.Fatalf("migrate pre-0009 db: %v", err)
	}

	// A pre-amendment manual completion: pinned run advanced past a manual
	// task with its semantic audit — and NO solution storage exists yet.
	cat := seedCategory(t, pre, "Mig9DevCat")
	requester := seedUserRole(t, pre, "Requester", "mig9-dev@x", true, domain.RoleUser)
	versionID := seedPublished(t, pre, cat, domain.WorkflowDefinition{
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Rack the server"}},
	})
	now := time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)
	ticket := seedPinnedTicket(t, pre, domain.Ticket{Number: 777, Title: "Legacy solved", RequesterName: "Requester", RequesterEmail: "mig9-dev@x", RequesterUserID: &requester, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &versionID})
	seedRun(t, pre, ticket.ID, 1, "active", now)
	if _, err := pre.db.ExecContext(ctx, `UPDATE ticket_workflow_runs SET status='completed', completed_at=? WHERE ticket_id=?`, formatTime(now), ticket.ID); err != nil {
		t.Fatalf("complete legacy run: %v", err)
	}
	if _, err := pre.db.ExecContext(ctx,
		`INSERT INTO audit_events (ticket_id, actor, action, step_index, created_at) VALUES (?, 'Requester', ?, 0, ?)`,
		ticket.ID, domain.ActionWorkflowManualTask, formatTime(now)); err != nil {
		t.Fatalf("seed legacy manual audit: %v", err)
	}

	schemaBefore := dumpSchema(t, pre.db)

	// Upgrade the dev database with the REAL embedded migration set through
	// 0009.
	post0009FS := fstest.MapFS{}
	for _, entry := range entries {
		// The upgrade target is exactly 0009 — later migrations (0010+) are
		// outside this test's contract.
		if strings.Compare(entry.Name(), "0009_ticket_manual_solutions.sql") > 0 {
			continue
		}
		blob, err := fs.ReadFile(migrationsFS, "migrations/"+entry.Name())
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		post0009FS["migrations/"+entry.Name()] = &fstest.MapFile{Data: blob}
	}
	if err := migrate(ctx, pre.db, post0009FS); err != nil {
		t.Fatalf("upgrade dev db: %v", err)
	}

	schemaAfter := dumpSchema(t, pre.db)
	if len(schemaAfter) != len(schemaBefore)+1 {
		t.Fatalf("schema objects grew by %d, want exactly 1 (the new table)", len(schemaAfter)-len(schemaBefore))
	}
	added := map[string]bool{}
	for name, sql := range schemaAfter {
		before, existed := schemaBefore[name]
		if !existed {
			added[name] = true
			continue
		}
		if before != sql {
			t.Fatalf("upgraded schema modified existing object %q — forward-only migration must be purely additive", name)
		}
	}
	if len(added) != 1 || !added["ticket_manual_solutions"] {
		t.Fatalf("added objects = %v, want exactly {ticket_manual_solutions}", added)
	}

	var applied int
	if err := pre.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=9`).Scan(&applied); err != nil {
		t.Fatalf("post-upgrade version read: %v", err)
	}
	if applied != 1 {
		t.Fatalf("post-upgrade schema_migrations version=9 rows = %d, want 1", applied)
	}

	// NO backfill: the pre-amendment completion has no fabricated solution.
	var solutions int
	if err := pre.db.QueryRow(`SELECT COUNT(*) FROM ticket_manual_solutions`).Scan(&solutions); err != nil {
		t.Fatalf("count solutions after upgrade: %v", err)
	}
	if solutions != 0 {
		t.Fatalf("migration backfilled %d solution rows, want 0 (pre-amendment completions stay unsolved)", solutions)
	}
}

// dumpSchema captures every schema object (tables, indexes, triggers) with its
// defining SQL so an upgrade can be proven purely additive. Implicit
// sqlite_autoindex_* entries (e.g. the composite-PK backing index) are
// implementation artifacts of their table, not authored DDL.
func dumpSchema(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.Query(`SELECT name, COALESCE(sql, '') FROM sqlite_master WHERE name NOT LIKE 'sqlite_autoindex%' ORDER BY name`)
	if err != nil {
		t.Fatalf("dump schema: %v", err)
	}
	defer rows.Close()
	schema := map[string]string{}
	for rows.Next() {
		var name, ddl string
		if err := rows.Scan(&name, &ddl); err != nil {
			t.Fatalf("scan schema object: %v", err)
		}
		schema[name] = ddl
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate schema objects: %v", err)
	}
	return schema
}
