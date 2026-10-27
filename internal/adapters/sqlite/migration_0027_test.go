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

// Migration 0027 tests (notification kinds hold_reminder and hold_closed —
// issue #353): 0021 pins the kind vocabulary with a CHECK and 0023 already
// rebuilt the table once to add on_hold, so adding the two automatic
// hold-expiry kinds rebuilds it again. These tests prove the rebuild changes
// ONLY the vocabulary: the columns, foreign keys and recipient index are
// identical to the 0023 table, every existing row survives with its id and
// every column, the email delivery rows that point at those notifications
// survive too, and AUTOINCREMENT keeps refusing to reuse the id of a deleted
// row.

func pre0027Migrations(t *testing.T) fstest.MapFS {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	out := fstest.MapFS{}
	for _, entry := range entries {
		if strings.Compare(entry.Name(), "0027_notification_kinds_hold_expiry.sql") >= 0 {
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

func openPre0027(t *testing.T) *Store {
	t.Helper()
	pre, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open pre-0027 db: %v", err)
	}
	pre.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pre.db.Close() })
	if err := migrate(context.Background(), pre.db, pre0027Migrations(t)); err != nil {
		t.Fatalf("migrate pre-0027 db: %v", err)
	}
	return pre
}

// TestMigration0027KeepsTheNotificationsShape compares the table as the
// 0027 migration itself leaves it with the table it found, so a column, foreign
// key, or index that drifts during THAT vocabulary-only rebuild is a red test.
// 0034 rebuilds the table again to add the ticketless account kind; that shape
// is pinned in migration_0034_notifications_test.go.
func TestMigration0027KeepsTheNotificationsShape(t *testing.T) {
	before := notificationsSchema(t, openPre0027(t))
	after := notificationsSchema(t, openThrough(t, "0027_notification_kinds_hold_expiry.sql"))
	if len(before) == 0 {
		t.Fatal("pre-0027 notifications schema is empty")
	}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("notifications schema changed:\n before %v\n after  %v", before, after)
	}
}

// TestMigration0027AcceptsTheHoldKindsAndStaysClosed proves the CHECK carries
// the two new kinds and still refuses anything outside the closed vocabulary
// (a case variant, a trailing space, an empty kind).
func TestMigration0027AcceptsTheHoldKindsAndStaysClosed(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	user := seedUser(t, s, "Carol", "carol@example.com", true)
	cat := seedCategory(t, s, "Bugs")
	tk := seedTicket(t, s, domain.Ticket{Number: 1, Title: "kind check", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateInProgress, CreatedAt: testClock, UpdatedAt: testClock})
	for _, kind := range []domain.NotificationKind{domain.NotificationHoldReminder, domain.NotificationHoldClosed} {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO notifications (user_id, ticket_id, kind, created_at) VALUES (?, ?, ?, ?)`,
			user, tk.ID, string(kind), formatTime(testClock)); err != nil {
			t.Errorf("insert %q: %v, want it accepted", kind, err)
		}
	}
	for _, kind := range []string{"", "mention", "HOLD_REMINDER", "hold_closed ", "hold-expired"} {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO notifications (user_id, ticket_id, kind, created_at) VALUES (?, ?, ?, ?)`,
			user, tk.ID, kind, formatTime(testClock)); err == nil {
			t.Errorf("insert kind %q succeeded, want CHECK failure", kind)
		}
	}
}

// TestMigration0027UpgradePreservesRowsAndSequence seeds a pre-0027 database
// with read and unread rows (one carrying a comment, one a step index, one the
// on_hold kind 0023 added) plus the email delivery row that points at one of
// them, deletes the newest notification, upgrades, and proves every surviving
// row reads back identically, that the delivery row survived a rebuild of the
// table it references without a foreign key, and that the next id is NOT the
// deleted one.
func TestMigration0027UpgradePreservesRowsAndSequence(t *testing.T) {
	ctx := context.Background()
	pre := openPre0027(t)
	user := seedUser(t, pre, "Carol", "carol@example.com", true)
	actor := seedUser(t, pre, "Bob", "bob@example.com", true)
	cat := seedCategory(t, pre, "pre-0027")
	tk := seedTicket(t, pre, domain.Ticket{Number: 7, Title: "before 0027", CategoryID: cat,
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
		{"on_hold", nil, nil, nil, nil, nil, nil},
		{"form_waiting", nil, nil, nil, nil, nil, 2},
		{"assigned", actor, nil, nil, nil, nil, nil}, // deleted below
	}
	for _, r := range seed {
		if _, err := pre.db.ExecContext(ctx, `INSERT INTO notifications (user_id, ticket_id, kind, actor_user_id, from_value, to_value, comment_id, step_index, created_at, read_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, user, tk.ID, r.kind, r.actor, r.from, r.to, r.comment, r.stepIndex, formatTime(testClock), r.readAt); err != nil {
			t.Fatalf("seed %s: %v", r.kind, err)
		}
	}
	var deliveredID int64
	if err := pre.db.QueryRowContext(ctx, `SELECT MIN(id) FROM notifications WHERE kind='on_hold'`).Scan(&deliveredID); err != nil {
		t.Fatal(err)
	}
	if _, err := pre.db.ExecContext(ctx, `INSERT INTO notification_emails (notification_id, status, attempts, next_attempt_at, created_at, updated_at)
		VALUES (?, 'sent', 1, ?, ?, ?)`, deliveredID, formatTime(testClock), formatTime(testClock), formatTime(testClock)); err != nil {
		t.Fatalf("seed notification email: %v", err)
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
		t.Fatalf("upgrade pre-0027 db: %v", err)
	}
	if after := dumpNotifications(t, pre); !reflect.DeepEqual(before, after) {
		t.Errorf("rows changed by the rebuild:\n before %v\n after  %v", before, after)
	}
	var delivered int
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_emails WHERE notification_id=?`, deliveredID).Scan(&delivered); err != nil {
		t.Fatal(err)
	}
	if delivered != 1 {
		t.Errorf("notification_emails rows for notification %d = %d after the rebuild, want 1", deliveredID, delivered)
	}
	res, err = pre.db.ExecContext(ctx, `INSERT INTO notifications (user_id, ticket_id, kind, created_at) VALUES (?, ?, 'hold_reminder', ?)`, user, tk.ID, formatTime(testClock))
	if err != nil {
		t.Fatalf("insert after upgrade: %v", err)
	}
	if next, _ := res.LastInsertId(); next <= deletedID {
		t.Errorf("next id = %d, want > %d (a deleted id must never be reused)", next, deletedID)
	}
	var leftovers int
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name='notifications_new'`).Scan(&leftovers); err != nil {
		t.Fatal(err)
	}
	if leftovers != 0 {
		t.Error("the rebuild left notifications_new behind")
	}
	var recorded int
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 27`).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded != 1 {
		t.Errorf("schema_migrations version = 27 rows = %d, want 1", recorded)
	}
	// The counter of the rebuilt table is the old table's counter, so the
	// never-reuse promise holds for the deleted id above.
	var sequence int64
	if err := pre.db.QueryRowContext(ctx, `SELECT seq FROM sqlite_sequence WHERE name='notifications'`).Scan(&sequence); err != nil {
		t.Fatalf("read notifications sequence: %v", err)
	}
	if sequence < deletedID+1 {
		t.Errorf("sqlite_sequence(notifications) = %d, want at least %d", sequence, deletedID+1)
	}
}
