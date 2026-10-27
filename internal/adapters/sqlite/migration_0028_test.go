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

// Migration 0028 tests (operational alert kinds — issue #408): adding the four
// SLA alert kinds and the inactivity kind rebuilds the notifications table
// again, for the same reason 0023 and 0027 did. These tests prove the rebuild
// changes ONLY the vocabulary: the shape is identical to the 0027 table, every
// stored row survives with its id and every column, the email delivery rows
// that point at them survive, and AUTOINCREMENT keeps refusing to reuse the id
// of a deleted row.

func pre0028Migrations(t *testing.T) fstest.MapFS {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	out := fstest.MapFS{}
	for _, entry := range entries {
		if strings.Compare(entry.Name(), "0028_notification_kinds_alerts.sql") >= 0 {
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

func openPre0028(t *testing.T) *Store {
	t.Helper()
	pre, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open pre-0028 db: %v", err)
	}
	pre.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pre.db.Close() })
	if err := migrate(context.Background(), pre.db, pre0028Migrations(t)); err != nil {
		t.Fatalf("migrate pre-0028 db: %v", err)
	}
	return pre
}

// TestMigration0028KeepsTheNotificationsShape compares the table as 0028
// itself leaves it with the table it found, so a column, foreign key or index
// that drifts during THAT vocabulary-only rebuild is a red test. 0034 rebuilds
// the table again to add the ticketless account kind; that shape is pinned in
// migration_0034_notifications_test.go.
func TestMigration0028KeepsTheNotificationsShape(t *testing.T) {
	before := notificationsSchema(t, openPre0028(t))
	after := notificationsSchema(t, openThrough(t, "0028_notification_kinds_alerts.sql"))
	if len(before) == 0 {
		t.Fatal("pre-0028 notifications schema is empty")
	}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("notifications schema changed:\n before %v\n after  %v", before, after)
	}
}

// TestMigration0028AcceptsTheAlertKindsAndStaysClosed proves the CHECK carries
// the five new kinds, still carries the twelve older ones, and still refuses
// anything outside the closed vocabulary.
func TestMigration0028AcceptsTheAlertKindsAndStaysClosed(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	user := seedUser(t, s, "Carol", "carol@example.com", true)
	cat := seedCategory(t, s, "Bugs")
	tk := seedTicket(t, s, domain.Ticket{Number: 1, Title: "kind check", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateInProgress, CreatedAt: testClock, UpdatedAt: testClock})
	for _, kind := range []domain.NotificationKind{
		domain.NotificationSLAFirstResponseAtRisk,
		domain.NotificationSLAFirstResponseBreached,
		domain.NotificationSLAResolutionAtRisk,
		domain.NotificationSLAResolutionBreached,
		domain.NotificationNoMovement,
		// The kinds the earlier rebuilds added must still be accepted.
		domain.NotificationHoldReminder,
		domain.NotificationHoldClosed,
		domain.NotificationOnHold,
	} {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO notifications (user_id, ticket_id, kind, created_at) VALUES (?, ?, ?, ?)`,
			user, tk.ID, string(kind), formatTime(testClock)); err != nil {
			t.Errorf("insert %q: %v, want it accepted", kind, err)
		}
	}
	for _, kind := range []string{"", "stalled", "NO_MOVEMENT", "no_movement ", "sla_at_risk"} {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO notifications (user_id, ticket_id, kind, created_at) VALUES (?, ?, ?, ?)`,
			user, tk.ID, kind, formatTime(testClock)); err == nil {
			t.Errorf("insert kind %q succeeded, want CHECK failure", kind)
		}
	}
}

// TestMigration0028UpgradePreservesRowsAndSequence seeds a pre-0028 database,
// deletes the newest notification, upgrades, and proves every surviving row
// reads back identically, that the delivery row pointing at one of them
// survived, and that the next id is NOT the deleted one.
func TestMigration0028UpgradePreservesRowsAndSequence(t *testing.T) {
	ctx := context.Background()
	pre := openPre0028(t)
	user := seedUser(t, pre, "Carol", "carol@example.com", true)
	actor := seedUser(t, pre, "Bob", "bob@example.com", true)
	cat := seedCategory(t, pre, "pre-0028")
	tk := seedTicket(t, pre, domain.Ticket{Number: 7, Title: "before 0028", CategoryID: cat,
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
		{"hold_reminder", nil, nil, nil, nil, nil, nil},
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
	if err := pre.db.QueryRowContext(ctx, `SELECT MIN(id) FROM notifications WHERE kind='hold_reminder'`).Scan(&deliveredID); err != nil {
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
		t.Fatalf("upgrade pre-0028 db: %v", err)
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
	res, err = pre.db.ExecContext(ctx, `INSERT INTO notifications (user_id, ticket_id, kind, created_at) VALUES (?, ?, 'no_movement', ?)`, user, tk.ID, formatTime(testClock))
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
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 28`).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded != 1 {
		t.Errorf("schema_migrations version = 28 rows = %d, want 1", recorded)
	}
	var sequence int64
	if err := pre.db.QueryRowContext(ctx, `SELECT seq FROM sqlite_sequence WHERE name='notifications'`).Scan(&sequence); err != nil {
		t.Fatalf("read notifications sequence: %v", err)
	}
	if sequence < deletedID+1 {
		t.Errorf("sqlite_sequence(notifications) = %d, want at least %d", sequence, deletedID+1)
	}
}
