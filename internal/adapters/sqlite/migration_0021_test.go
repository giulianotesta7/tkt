package sqlite

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Migration 0021 tests (notification storage — issue #344, slice B1): the
// notifications table shape, its closed kind vocabulary, the users/tickets/
// comments foreign keys and cascades, the recipient index, the pre-0021
// upgrade path leaving existing rows untouched (S18), and the version
// bookkeeping. Written against the numbered-series conventions of
// migration_0020_test.go.

// pre0021Migrations returns the embedded migrations strictly before 0021, so
// the upgrade-path test can seed a pre-0021 database and then apply the real
// full set (pattern of pre0020Migrations).
func pre0021Migrations(t *testing.T) fstest.MapFS {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	out := fstest.MapFS{}
	for _, entry := range entries {
		if strings.Compare(entry.Name(), "0021_notifications.sql") >= 0 {
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

// migrationsThrough returns the embedded migrations up to and including last,
// so a test can assert what a HISTORICAL migration itself produced. The
// notifications table has been rebuilt several times, so the shape an earlier
// migration declared only exists inside this window.
func migrationsThrough(t *testing.T, last string) fstest.MapFS {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	out := fstest.MapFS{}
	for _, entry := range entries {
		if strings.Compare(entry.Name(), last) > 0 {
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

// openThrough applies the migrations up to and including last and returns the
// store, so a historical shape can be asserted on the schema that migration
// actually produced rather than on the one the whole set produces today.
func openThrough(t *testing.T, last string) *Store {
	t.Helper()
	s, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open through %s: %v", last, err)
	}
	s.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = s.db.Close() })
	if err := migrate(context.Background(), s.db, migrationsThrough(t, last)); err != nil {
		t.Fatalf("migrate through %s: %v", last, err)
	}
	return s
}

// TestMigration0021NotificationsTableShape proves the notifications columns
// exist with the nullability the domain relies on: recipient, ticket, kind and
// created_at are required; everything else is a fact that may be absent (an
// automatic action has no actor, a state change has no comment, an unread row
// has no read_at). It asserts the table 0021 itself creates; 0034 later
// rebuilds it so the bell can also carry a ticketless account announcement,
// and that shape is pinned in migration_0034_notifications_test.go.
func TestMigration0021NotificationsTableShape(t *testing.T) {
	s := openThrough(t, "0021_notifications.sql")

	type columnShape struct {
		column  string
		wantTyp string
		notNull int
		pk      int
	}
	want := []columnShape{
		{"id", "INTEGER", 0, 1},
		{"user_id", "INTEGER", 1, 0},
		{"ticket_id", "INTEGER", 1, 0},
		{"kind", "TEXT", 1, 0},
		{"actor_user_id", "INTEGER", 0, 0},
		{"from_value", "TEXT", 0, 0},
		{"to_value", "TEXT", 0, 0},
		{"comment_id", "INTEGER", 0, 0},
		{"step_index", "INTEGER", 0, 0},
		{"created_at", "TEXT", 1, 0},
		{"read_at", "TEXT", 0, 0},
	}

	rows, err := s.db.Query(`SELECT name, type, "notnull", pk, dflt_value FROM pragma_table_info('notifications')`)
	if err != nil {
		t.Fatalf("pragma_table_info('notifications'): %v", err)
	}
	defer rows.Close()
	got := map[string]columnShape{}
	for rows.Next() {
		var (
			name, colType string
			notNull, pk   int
			dflt          *string
		)
		if err := rows.Scan(&name, &colType, &notNull, &pk, &dflt); err != nil {
			t.Fatal(err)
		}
		if dflt != nil {
			t.Errorf("%s default = %v, want none (absent facts stay NULL, never defaulted)", name, *dflt)
		}
		got[name] = columnShape{name, colType, notNull, pk}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Errorf("notifications has %d columns, want %d: %v", len(got), len(want), got)
	}
	for _, w := range want {
		g, ok := got[w.column]
		if !ok {
			t.Errorf("column %s missing after 0021", w.column)
			continue
		}
		if g.wantTyp != w.wantTyp {
			t.Errorf("%s type = %q, want %q", w.column, g.wantTyp, w.wantTyp)
		}
		if g.notNull != w.notNull {
			t.Errorf("%s notnull = %d, want %d", w.column, g.notNull, w.notNull)
		}
		if g.pk != w.pk {
			t.Errorf("%s pk = %d, want %d", w.column, g.pk, w.pk)
		}
	}
}

// TestMigration0021KindVocabularyIsClosed proves the column's CHECK accepts
// exactly the nine S10 kinds the domain can render, and rejects anything else
// — the SQL and Go halves of the closed vocabulary cannot drift.
func TestMigration0021KindVocabularyIsClosed(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	user := seedUser(t, s, "Ana", "ana@example.com", true)
	cat := seedCategory(t, s, "Bugs")
	tk := seedTicket(t, s, domain.Ticket{Number: 1, Title: "kind check", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: testClock, UpdatedAt: testClock})

	accepted := []domain.NotificationKind{
		domain.NotificationAssigned,
		domain.NotificationStateChanged,
		domain.NotificationResolvedAwaitingConfirmation,
		domain.NotificationResolutionConfirmed,
		domain.NotificationResolutionRejected,
		domain.NotificationPriorityChanged,
		domain.NotificationCommentPublic,
		domain.NotificationCommentInternal,
		domain.NotificationFormWaiting,
	}
	for _, kind := range accepted {
		var commentID any
		if kind.IsComment() {
			res, err := s.db.ExecContext(ctx,
				`INSERT INTO comments (ticket_id, author, body, created_at) VALUES (?, 'Ana', 'body', ?)`,
				tk.ID, formatTime(testClock))
			if err != nil {
				t.Fatalf("seed comment for %s: %v", kind, err)
			}
			id, err := res.LastInsertId()
			if err != nil {
				t.Fatal(err)
			}
			commentID = id
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO notifications (user_id, ticket_id, kind, comment_id, created_at) VALUES (?, ?, ?, ?, ?)`,
			user, tk.ID, string(kind), commentID, formatTime(testClock)); err != nil {
			t.Errorf("insert kind %q: %v, want it accepted", kind, err)
		}
	}

	for _, kind := range []string{"", "mentioned", "ASSIGNED", "assigned "} {
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO notifications (user_id, ticket_id, kind, created_at) VALUES (?, ?, ?, ?)`,
			user, tk.ID, kind, formatTime(testClock)); err == nil {
			t.Errorf("insert kind %q succeeded, want CHECK failure", kind)
		}
	}
}

// TestMigration0021ForeignKeys proves the three ownership edges and their
// cascade behavior: a notification belongs to a recipient, a ticket and
// (for comment kinds) a comment, and it disappears with any of them. The
// actor edge carries no cascade — the actor is history, not an owner. It
// asserts the edges 0021 declared; 0034 adds the pending-account edge on the
// rebuilt table, and that full set is pinned in
// migration_0034_notifications_test.go.
func TestMigration0021ForeignKeys(t *testing.T) {
	s := openThrough(t, "0021_notifications.sql")

	type fk struct {
		table, to, onDelete string
	}
	want := map[string]fk{
		"user_id":       {"users", "id", "CASCADE"},
		"ticket_id":     {"tickets", "id", "CASCADE"},
		"comment_id":    {"comments", "id", "CASCADE"},
		"actor_user_id": {"users", "id", "NO ACTION"},
	}

	rows, err := s.db.Query(`SELECT "from", "table", "to", on_delete FROM pragma_foreign_key_list('notifications')`)
	if err != nil {
		t.Fatalf("foreign key list: %v", err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var from, table, to, onDelete string
		if err := rows.Scan(&from, &table, &to, &onDelete); err != nil {
			t.Fatal(err)
		}
		seen[from] = true
		w, ok := want[from]
		if !ok {
			t.Errorf("unexpected foreign key on notifications.%s", from)
			continue
		}
		if table != w.table || to != w.to {
			t.Errorf("%s FK = %s(%s), want %s(%s)", from, table, to, w.table, w.to)
		}
		if onDelete != w.onDelete {
			t.Errorf("%s ON DELETE = %q, want %q", from, onDelete, w.onDelete)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for from := range want {
		if !seen[from] {
			t.Errorf("notifications.%s has no foreign key", from)
		}
	}
}

// TestMigration0021RecipientIndex proves the unread-count/newest-first index
// exists over exactly (user_id, read_at, created_at), the three columns the
// bell count and the panel filter and order by (S13, S14).
func TestMigration0021RecipientIndex(t *testing.T) {
	s := newTestDB(t)

	rows, err := s.db.Query(`SELECT name FROM pragma_index_info('idx_notifications_recipient') ORDER BY seqno`)
	if err != nil {
		t.Fatalf("pragma_index_info: %v", err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(columns, ",")
	if got != "user_id,read_at,created_at" {
		t.Errorf("idx_notifications_recipient columns = %q, want %q", got, "user_id,read_at,created_at")
	}
}

// TestMigration0021UpgradePathPreservesExistingRows proves a pre-0021
// database upgrades cleanly and rewrites nothing (S18): the table is absent
// before, and an existing ticket, comment, and audit event survive the
// upgrade byte for byte.
func TestMigration0021UpgradePathPreservesExistingRows(t *testing.T) {
	ctx := context.Background()
	pre, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open pre-0021 db: %v", err)
	}
	pre.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pre.db.Close() })
	if err := migrate(ctx, pre.db, pre0021Migrations(t)); err != nil {
		t.Fatalf("migrate pre-0021 db: %v", err)
	}

	var n int
	if err := pre.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='notifications'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("notifications existed before 0021, want it added by the migration")
	}

	cat := seedCategory(t, pre, "pre-0021")
	tk := seedTicket(t, pre, domain.Ticket{Number: 77, Title: "written before 0021", CategoryID: cat,
		Priority: domain.PriorityHigh, State: domain.StateInProgress, CreatedAt: testClock, UpdatedAt: testClock})
	if _, err := pre.db.ExecContext(ctx,
		`INSERT INTO comments (ticket_id, author, body, created_at) VALUES (?, 'Ana', 'before', ?)`,
		tk.ID, formatTime(testClock)); err != nil {
		t.Fatalf("seed pre-0021 comment: %v", err)
	}
	if _, err := pre.db.ExecContext(ctx,
		`INSERT INTO audit_events (ticket_id, actor, action, created_at) VALUES (?, 'Ana', 'transition', ?)`,
		tk.ID, formatTime(testClock)); err != nil {
		t.Fatalf("seed pre-0021 audit: %v", err)
	}

	if err := migrate(ctx, pre.db, migrationsFS); err != nil {
		t.Fatalf("upgrade pre-0021 db: %v", err)
	}

	var (
		title    string
		comments int
		audits   int
	)
	if err := pre.db.QueryRowContext(ctx, `SELECT title FROM tickets WHERE id=?`, tk.ID).Scan(&title); err != nil {
		t.Fatalf("read upgraded ticket: %v", err)
	}
	if title != "written before 0021" {
		t.Errorf("upgraded ticket title = %q, want it untouched", title)
	}
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM comments WHERE ticket_id=?`, tk.ID).Scan(&comments); err != nil {
		t.Fatal(err)
	}
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE ticket_id=?`, tk.ID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if comments != 1 || audits != 1 {
		t.Errorf("after upgrade comments=%d audits=%d, want 1 and 1 — 0021 rewrites no existing row", comments, audits)
	}
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications`).Scan(&n); err != nil {
		t.Fatalf("read upgraded notifications: %v", err)
	}
	if n != 0 {
		t.Errorf("notifications rows after upgrade = %d, want 0 — 0021 backfills nothing", n)
	}

	var recorded int
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=21`).Scan(&recorded); err != nil {
		t.Fatalf("schema_migrations read: %v", err)
	}
	if recorded != 1 {
		t.Errorf("schema_migrations version=21 rows = %d, want 1", recorded)
	}
}

// TestMigration0021VersionBookkeeping proves the applied version is 21.
func TestMigration0021VersionBookkeeping(t *testing.T) {
	s := newTestDB(t)
	var applied int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=21`).Scan(&applied); err != nil {
		t.Fatalf("schema_migrations read: %v", err)
	}
	if applied != 1 {
		t.Fatalf("schema_migrations version=21 rows = %d, want 1", applied)
	}
}
