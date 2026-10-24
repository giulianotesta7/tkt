package sqlite

import (
	"context"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Migration 0030 tests (what changed since the last visit — issue #415): one
// additive table, ticket_visits, keyed by (user, ticket), with two
// non-negative history marks and cascades from both parents. An upgraded
// database keeps every existing row and starts with no visits.

func pre0030Migrations(t *testing.T) fstest.MapFS {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	out := fstest.MapFS{}
	for _, entry := range entries {
		if strings.Compare(entry.Name(), "0030_ticket_visits.sql") >= 0 {
			continue
		}
		blob, err := fs.ReadFile(migrationsFS, "migrations/"+entry.Name())
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		out["migrations/"+entry.Name()] = &fstest.MapFile{Data: blob}
	}
	return out
}

func TestMigration0030TicketVisitsShape(t *testing.T) {
	s := newTestDB(t)
	rows, err := s.db.Query(`SELECT name, type, "notnull", pk FROM pragma_table_info('ticket_visits') ORDER BY cid`)
	if err != nil {
		t.Fatalf("pragma_table_info: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name, typ string
		var notNull, pk int
		if err := rows.Scan(&name, &typ, &notNull, &pk); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		got = append(got, strings.Join([]string{name, typ, map[int]string{0: "null", 1: "notnull"}[notNull], map[int]string{0: "-", 1: "pk1", 2: "pk2"}[pk]}, " "))
	}
	want := []string{
		"user_id INTEGER notnull pk1",
		"ticket_id INTEGER notnull pk2",
		"audit_seen_id INTEGER notnull -",
		"comment_seen_id INTEGER notnull -",
		"visited_at TEXT notnull -",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ticket_visits columns = %v, want %v", got, want)
	}

	fks, err := s.db.Query(`SELECT "from", "table", "to", on_delete FROM pragma_foreign_key_list('ticket_visits') ORDER BY "from"`)
	if err != nil {
		t.Fatalf("pragma_foreign_key_list: %v", err)
	}
	defer fks.Close()
	var gotFK []string
	for fks.Next() {
		var from, table, to, onDelete string
		if err := fks.Scan(&from, &table, &to, &onDelete); err != nil {
			t.Fatalf("scan fk: %v", err)
		}
		gotFK = append(gotFK, from+"->"+table+"."+to+" "+onDelete)
	}
	wantFK := []string{"ticket_id->tickets.id CASCADE", "user_id->users.id CASCADE"}
	if !reflect.DeepEqual(gotFK, wantFK) {
		t.Fatalf("ticket_visits foreign keys = %v, want %v", gotFK, wantFK)
	}
}

func TestMigration0030RefusesNegativeMarksAndCascades(t *testing.T) {
	f := newVisitFixture(t, 1)
	ctx := context.Background()
	for _, col := range []string{"audit_seen_id", "comment_seen_id"} {
		other := map[string]string{"audit_seen_id": "comment_seen_id", "comment_seen_id": "audit_seen_id"}[col]
		if _, err := f.s.db.ExecContext(ctx,
			`INSERT INTO ticket_visits (user_id, ticket_id, `+col+`, `+other+`, visited_at) VALUES (?, ?, -1, 0, 'x')`,
			f.ana, f.tickets[0]); err == nil {
			t.Fatalf("negative %s accepted", col)
		}
	}

	f.visit(t, f.ana, f.tickets[0])
	f.visit(t, f.bob, f.tickets[0])
	count := func() int {
		var n int
		if err := f.s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ticket_visits`).Scan(&n); err != nil {
			t.Fatalf("count visits: %v", err)
		}
		return n
	}
	if _, err := f.s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, f.bob); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if got := count(); got != 1 {
		t.Fatalf("visits after deleting a user = %d, want 1", got)
	}
	if _, err := f.s.db.ExecContext(ctx, `DELETE FROM tickets WHERE id = ?`, f.tickets[0]); err != nil {
		t.Fatalf("delete ticket: %v", err)
	}
	if got := count(); got != 0 {
		t.Fatalf("visits after deleting the ticket = %d, want 0", got)
	}
}

func TestMigration0030UpgradesAnExistingDatabase(t *testing.T) {
	ctx := context.Background()
	pre, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open pre-0030 db: %v", err)
	}
	pre.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pre.db.Close() })
	if err := migrate(ctx, pre.db, pre0030Migrations(t)); err != nil {
		t.Fatalf("migrate pre-0030 db: %v", err)
	}
	ana := seedUser(t, pre, "Ana", "ana@example.com", true)
	cat := seedCategory(t, pre, "Bugs")
	tk := seedTicket(t, pre, domain.Ticket{Number: 1, Title: "kept", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: testClock, UpdatedAt: testClock})

	if err := migrate(ctx, pre.db, migrationsFS); err != nil {
		t.Fatalf("upgrade to 0030: %v", err)
	}
	var recorded, visits, tickets int
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 30`).Scan(&recorded); err != nil {
		t.Fatalf("schema_migrations: %v", err)
	}
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ticket_visits`).Scan(&visits); err != nil {
		t.Fatalf("ticket_visits: %v", err)
	}
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tickets WHERE id = ?`, tk.ID).Scan(&tickets); err != nil {
		t.Fatalf("tickets: %v", err)
	}
	if recorded != 1 || visits != 0 || tickets != 1 {
		t.Fatalf("after upgrade: version 30 rows %d, visits %d, ticket kept %d; want 1, 0, 1", recorded, visits, tickets)
	}
	// The upgraded database answers the new port: a never-opened ticket with
	// no history is not changed.
	got, err := pre.TicketVisitStore().Changed(ctx, ana, []int64{tk.ID}, true)
	if err != nil || len(got) != 0 {
		t.Fatalf("Changed after upgrade = %v, %v; want empty", got, err)
	}
}
