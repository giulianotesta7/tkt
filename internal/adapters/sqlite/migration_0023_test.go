package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Migration 0023 tests (notification kind on_hold — issue #352): 0021 pins the
// kind vocabulary with a CHECK, so adding a kind rebuilds the notifications
// table. These tests prove the rebuild changes ONLY the vocabulary: the
// columns, foreign keys and recipient index are identical to 0021, every
// existing row survives with its id and every column, and AUTOINCREMENT keeps
// refusing to reuse the id of a deleted row.

func pre0023Migrations(t *testing.T) fstest.MapFS {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	out := fstest.MapFS{}
	for _, entry := range entries {
		if strings.Compare(entry.Name(), "0023_notification_kind_on_hold.sql") >= 0 {
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

func openPre0023(t *testing.T) *Store {
	t.Helper()
	pre, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open pre-0023 db: %v", err)
	}
	pre.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pre.db.Close() })
	if err := migrate(context.Background(), pre.db, pre0023Migrations(t)); err != nil {
		t.Fatalf("migrate pre-0023 db: %v", err)
	}
	return pre
}

// notificationsSchema renders the table's columns, foreign keys and recipient
// index as rows of text, so two databases compare with one DeepEqual.
func notificationsSchema(t *testing.T, s *Store) []string {
	t.Helper()
	var out []string
	for _, q := range []string{
		`SELECT cid, name, type, "notnull", COALESCE(dflt_value, 'NULL'), pk FROM pragma_table_info('notifications') ORDER BY cid`,
		`SELECT "table", "from", "to", on_update, on_delete FROM pragma_foreign_key_list('notifications') ORDER BY "from"`,
		`SELECT seqno, name FROM pragma_index_info('idx_notifications_recipient') ORDER BY seqno`,
		`SELECT name FROM pragma_index_list('notifications') ORDER BY name`,
	} {
		rows, err := s.db.Query(q)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			out = append(out, fmt.Sprint(vals...))
		}
		rows.Close()
	}
	return out
}

// TestMigration0023KeepsTheNotificationsShape compares the table as 0023 left
// it with the table 0023 itself produces, so the claim is about THAT rebuild
// (a vocabulary-only change) rather than about the later ones. 0034 rebuilds
// the table again to add the ticketless account kind; that shape is pinned in
// migration_0034_notifications_test.go.
func TestMigration0023KeepsTheNotificationsShape(t *testing.T) {
	before := notificationsSchema(t, openPre0023(t))
	after := notificationsSchema(t, openThrough(t, "0023_notification_kind_on_hold.sql"))
	if len(before) == 0 {
		t.Fatal("pre-0023 notifications schema is empty")
	}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("notifications schema changed:\n before %v\n after  %v", before, after)
	}
}

func TestMigration0023AcceptsOnHoldAndStaysClosed(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	user := seedUser(t, s, "Carol", "carol@example.com", true)
	cat := seedCategory(t, s, "Bugs")
	tk := seedTicket(t, s, domain.Ticket{Number: 1, Title: "kind check", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateInProgress, CreatedAt: testClock, UpdatedAt: testClock})
	if _, err := s.db.ExecContext(ctx, `INSERT INTO notifications (user_id, ticket_id, kind, created_at) VALUES (?, ?, ?, ?)`,
		user, tk.ID, string(domain.NotificationOnHold), formatTime(testClock)); err != nil {
		t.Errorf("insert on_hold: %v, want it accepted", err)
	}
	for _, kind := range []string{"", "mentioned", "ON_HOLD", "on_hold "} {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO notifications (user_id, ticket_id, kind, created_at) VALUES (?, ?, ?, ?)`,
			user, tk.ID, kind, formatTime(testClock)); err == nil {
			t.Errorf("insert kind %q succeeded, want CHECK failure", kind)
		}
	}
}

// TestMigration0023UpgradePreservesRowsAndSequence seeds a pre-0023 database
// with read and unread rows (one carrying a comment, one a step index),
// deletes the newest row, upgrades, and proves every surviving row reads back
// identically and that the next id is NOT the deleted one: the rebuild keeps
// AUTOINCREMENT's never-reuse guarantee.
func TestMigration0023UpgradePreservesRowsAndSequence(t *testing.T) {
	ctx := context.Background()
	pre := openPre0023(t)
	user := seedUser(t, pre, "Carol", "carol@example.com", true)
	actor := seedUser(t, pre, "Bob", "bob@example.com", true)
	cat := seedCategory(t, pre, "pre-0023")
	tk := seedTicket(t, pre, domain.Ticket{Number: 7, Title: "before 0023", CategoryID: cat,
		Priority: domain.PriorityHigh, State: domain.StateInProgress, CreatedAt: testClock, UpdatedAt: testClock})
	res, err := pre.db.ExecContext(ctx, `INSERT INTO comments (ticket_id, author, body, created_at) VALUES (?, 'Bob', 'hi', ?)`, tk.ID, formatTime(testClock))
	if err != nil {
		t.Fatalf("seed comment: %v", err)
	}
	commentID, _ := res.LastInsertId()
	seed := []struct {
		kind             string
		actor, comment   any
		from, to, readAt any
		stepIndex        any
	}{
		{"state_changed", actor, nil, "new", "in_progress", "2026-08-06T11:00:00Z", nil},
		{"comment_public", actor, commentID, nil, nil, nil, nil},
		{"form_waiting", nil, nil, nil, nil, nil, 2},
		{"assigned", actor, nil, nil, nil, nil, nil}, // deleted below
	}
	for _, r := range seed {
		if _, err := pre.db.ExecContext(ctx, `INSERT INTO notifications (user_id, ticket_id, kind, actor_user_id, from_value, to_value, comment_id, step_index, created_at, read_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, user, tk.ID, r.kind, r.actor, r.from, r.to, r.comment, r.stepIndex, formatTime(testClock), r.readAt); err != nil {
			t.Fatalf("seed %s: %v", r.kind, err)
		}
	}
	var deletedID int64
	if err := pre.db.QueryRowContext(ctx, `SELECT MAX(id) FROM notifications`).Scan(&deletedID); err != nil {
		t.Fatal(err)
	}
	if _, err := pre.db.ExecContext(ctx, `DELETE FROM notifications WHERE id=?`, deletedID); err != nil {
		t.Fatal(err)
	}
	before := dumpNotifications(t, pre)

	if err := migrate(ctx, pre.db, migrationsFS); err != nil {
		t.Fatalf("upgrade pre-0023 db: %v", err)
	}
	if after := dumpNotifications(t, pre); !reflect.DeepEqual(before, after) {
		t.Errorf("rows changed by the rebuild:\n before %v\n after  %v", before, after)
	}
	res, err = pre.db.ExecContext(ctx, `INSERT INTO notifications (user_id, ticket_id, kind, created_at) VALUES (?, ?, 'on_hold', ?)`, user, tk.ID, formatTime(testClock))
	if err != nil {
		t.Fatalf("insert after upgrade: %v", err)
	}
	if next, _ := res.LastInsertId(); next <= deletedID {
		t.Errorf("next id = %d, want > %d (a deleted id must never be reused)", next, deletedID)
	}
	var tables int
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name='notifications_new'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Error("the rebuild left notifications_new behind")
	}
	var recorded int
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=23`).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded != 1 {
		t.Errorf("schema_migrations version=23 rows = %d, want 1", recorded)
	}
}

func dumpNotifications(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.db.Query(`SELECT id, user_id, ticket_id, kind, actor_user_id, from_value, to_value, comment_id, step_index, created_at, read_at FROM notifications ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var (
			id, userID, ticketID     int64
			kind, createdAt          string
			actorID, commentID, step sql.NullInt64
			from, to, readAt         sql.NullString
		)
		if err := rows.Scan(&id, &userID, &ticketID, &kind, &actorID, &from, &to, &commentID, &step, &createdAt, &readAt); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprint(id, userID, ticketID, kind, actorID, from, to, commentID, step, createdAt, readAt))
	}
	return out
}
