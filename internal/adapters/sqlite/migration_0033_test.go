package sqlite

import (
	"context"
	"fmt"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Migration 0033 tests (canned replies, ticket followers and the mention /
// assignee-change kinds — issue #418).
//
// Issue #418 adds two values to the notification vocabulary, so this is the FIFTH
// application of the recipe 0021 documented (after 0023, 0027 and 0028): the
// kind list is pinned by a CHECK, SQLite cannot alter a CHECK in place, so
// adding kinds rebuilds the table. The rebuild changes ONLY that list: the
// columns, their types, defaults, foreign keys and cascades, the sqlite_sequence
// step and idx_notifications_recipient are copied from the 0028 table
// unchanged, and every existing row is copied with its id, so a stored
// notification reads back exactly as before.
//
// The migration also creates canned_replies (a personal or per-desk reply) and
// ticket_followers; both are new tables, so they are asserted by shape,
// constraint and cascade instead of by a before/after comparison.

func pre0033Migrations(t *testing.T) fstest.MapFS {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	out := fstest.MapFS{}
	for _, entry := range entries {
		if strings.Compare(entry.Name(), "0033_replies_followers_mentions.sql") >= 0 {
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

func openPre0033(t *testing.T) *Store {
	t.Helper()
	pre, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open pre-0033 db: %v", err)
	}
	pre.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pre.db.Close() })
	if err := migrate(context.Background(), pre.db, pre0033Migrations(t)); err != nil {
		t.Fatalf("migrate pre-0033 db: %v", err)
	}
	return pre
}

// TestMigration0033KeepsTheNotificationsShape compares the table as 0028 left
// it with the table the whole migration set produces, so a column, foreign key
// or index that drifts during the rebuild is a red test.
func TestMigration0033KeepsTheNotificationsShape(t *testing.T) {
	before := notificationsSchema(t, openPre0033(t))
	after := notificationsSchema(t, newTestDB(t))
	if len(before) == 0 {
		t.Fatal("pre-0033 notifications schema is empty")
	}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("notifications schema changed:\n before %v\n after  %v", before, after)
	}
}

// TestMigration0033AcceptsTheNewKindsAndStaysClosed proves the CHECK carries
// the two kinds issue #418 adds, still carries every older one, and still
// refuses anything outside the closed vocabulary.
func TestMigration0033AcceptsTheNewKindsAndStaysClosed(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	user := seedUser(t, s, "Carol", "carol@example.com", true)
	cat := seedCategory(t, s, "Bugs")
	tk := seedTicket(t, s, domain.Ticket{Number: 1, Title: "kind check", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateInProgress, CreatedAt: testClock, UpdatedAt: testClock})
	res, err := s.db.ExecContext(ctx, `INSERT INTO comments (ticket_id, author, body, created_at) VALUES (?, 'Carol', 'hi', ?)`, tk.ID, formatTime(testClock))
	if err != nil {
		t.Fatalf("seed comment: %v", err)
	}
	commentID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	owner := "Bob Ray"
	for _, kind := range []domain.NotificationKind{
		// The kinds the earlier rebuilds added must still be accepted.
		domain.NotificationAssigned,
		domain.NotificationStateChanged,
		domain.NotificationResolvedAwaitingConfirmation,
		domain.NotificationResolutionConfirmed,
		domain.NotificationResolutionRejected,
		domain.NotificationPriorityChanged,
		domain.NotificationCommentPublic,
		domain.NotificationCommentInternal,
		domain.NotificationFormWaiting,
		domain.NotificationOnHold,
		domain.NotificationHoldReminder,
		domain.NotificationHoldClosed,
		domain.NotificationSLAFirstResponseAtRisk,
		domain.NotificationSLAFirstResponseBreached,
		domain.NotificationSLAResolutionAtRisk,
		domain.NotificationSLAResolutionBreached,
		domain.NotificationNoMovement,
		// Issue #418 adds these two.
		domain.NotificationMentioned,
		domain.NotificationAssigneeChanged,
	} {
		var comment any
		var toValue any
		switch kind {
		case domain.NotificationMentioned:
			comment = commentID
		case domain.NotificationAssigneeChanged:
			toValue = owner
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO notifications (user_id, ticket_id, kind, to_value, comment_id, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			user, tk.ID, string(kind), toValue, comment, formatTime(testClock)); err != nil {
			t.Errorf("insert %q: %v, want it accepted", kind, err)
		}
	}
	for _, kind := range []string{"", "mention", "assignee", "ASSIGNED", "mentioned ", "assignee changed"} {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO notifications (user_id, ticket_id, kind, created_at) VALUES (?, ?, ?, ?)`,
			user, tk.ID, kind, formatTime(testClock)); err == nil {
			t.Errorf("insert kind %q succeeded, want CHECK failure", kind)
		}
	}
}

// TestMigration0033UpgradePreservesRowsAndSequence seeds a pre-0033 database,
// deletes the newest notification, upgrades, and proves every surviving row
// reads back identically, that the delivery row pointing at one of them
// survived, and that the next id is NOT the deleted one.
func TestMigration0033UpgradePreservesRowsAndSequence(t *testing.T) {
	ctx := context.Background()
	pre := openPre0033(t)
	user := seedUser(t, pre, "Carol", "carol@example.com", true)
	actor := seedUser(t, pre, "Bob", "bob@example.com", true)
	cat := seedCategory(t, pre, "pre-0033")
	tk := seedTicket(t, pre, domain.Ticket{Number: 7, Title: "before 0033", CategoryID: cat,
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
		{"no_movement", nil, nil, nil, nil, nil, nil},
		{"form_waiting", nil, nil, nil, nil, nil, 2},
		{"priority_changed", actor, nil, "medium", "high", nil, nil}, // deleted below
	}
	for _, r := range seed {
		if _, err := pre.db.ExecContext(ctx, `INSERT INTO notifications (user_id, ticket_id, kind, actor_user_id, from_value, to_value, comment_id, step_index, created_at, read_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, user, tk.ID, r.kind, r.actor, r.from, r.to, r.comment, r.stepIndex, formatTime(testClock), r.readAt); err != nil {
			t.Fatalf("seed %s: %v", r.kind, err)
		}
	}
	var deliveredID int64
	if err := pre.db.QueryRowContext(ctx, `SELECT MIN(id) FROM notifications WHERE kind='comment_public'`).Scan(&deliveredID); err != nil {
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
		t.Fatalf("upgrade pre-0033 db: %v", err)
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
	res, err = pre.db.ExecContext(ctx, `INSERT INTO notifications (user_id, ticket_id, kind, created_at) VALUES (?, ?, 'mentioned', ?)`, user, tk.ID, formatTime(testClock))
	if err != nil {
		t.Fatalf("insert after upgrade: %v", err)
	}
	if next, _ := res.LastInsertId(); next <= deletedID {
		t.Errorf("next id = %d, want > %d (a deleted id must never be reused)", next, deletedID)
	}
	if leftovers := countRows(t, pre, `SELECT COUNT(*) FROM sqlite_master WHERE name='notifications_new'`); leftovers != 0 {
		t.Error("the rebuild left notifications_new behind")
	}
	if recorded := countRows(t, pre, `SELECT COUNT(*) FROM schema_migrations WHERE version = 33`); recorded != 1 {
		t.Errorf("schema_migrations version = 33 rows = %d, want 1", recorded)
	}
	var sequence int64
	if err := pre.db.QueryRowContext(ctx, `SELECT seq FROM sqlite_sequence WHERE name='notifications'`).Scan(&sequence); err != nil {
		t.Fatalf("read notifications sequence: %v", err)
	}
	if sequence < deletedID+1 {
		t.Errorf("sqlite_sequence(notifications) = %d, want at least %d", sequence, deletedID+1)
	}
}

// replyColumnShape is one column of pragma_table_info.
type replyColumnShape struct {
	Name    string
	Type    string
	NotNull int
	PK      int
}

func replyColumns(t *testing.T, s *Store, table string) []replyColumnShape {
	t.Helper()
	rows, err := s.db.Query(fmt.Sprintf(`SELECT name, type, "notnull", pk FROM pragma_table_info('%s') ORDER BY cid`, table))
	if err != nil {
		t.Fatalf("pragma_table_info(%s): %v", table, err)
	}
	defer rows.Close()
	var out []replyColumnShape
	for rows.Next() {
		var c replyColumnShape
		if err := rows.Scan(&c.Name, &c.Type, &c.NotNull, &c.PK); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// replyForeignKeys renders each foreign key of a table as
// "from->table.to ON DELETE action", ordered by the referencing column.
func replyForeignKeys(t *testing.T, s *Store, table string) []string {
	t.Helper()
	rows, err := s.db.Query(fmt.Sprintf(`SELECT "from", "table", "to", on_delete FROM pragma_foreign_key_list('%s') ORDER BY "from"`, table))
	if err != nil {
		t.Fatalf("pragma_foreign_key_list(%s): %v", table, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var from, target, to, onDelete string
		if err := rows.Scan(&from, &target, &to, &onDelete); err != nil {
			t.Fatal(err)
		}
		out = append(out, from+"->"+target+"."+to+" ON DELETE "+onDelete)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// replyIndexColumns lists the columns of one index, in index order.
func replyIndexColumns(t *testing.T, s *Store, index string) []string {
	t.Helper()
	rows, err := s.db.Query(fmt.Sprintf(`SELECT name FROM pragma_index_info('%s') ORDER BY seqno`, index))
	if err != nil {
		t.Fatalf("pragma_index_info(%s): %v", index, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// replyIndexes lists the index names a table carries, ordered by name.
func replyIndexes(t *testing.T, s *Store, table string) []string {
	t.Helper()
	rows, err := s.db.Query(fmt.Sprintf(`SELECT name FROM pragma_index_list('%s') ORDER BY name`, table))
	if err != nil {
		t.Fatalf("pragma_index_list(%s): %v", table, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestMigration0033CannedRepliesShape pins the saved-reply table: one nullable owner
// edge and one nullable desk edge, both cascading, a bounded non-empty title
// and body, and the two lookup indexes the picker reads.
func TestMigration0033CannedRepliesShape(t *testing.T) {
	s := newTestDB(t)
	wantColumns := []replyColumnShape{
		{"id", "INTEGER", 0, 1},
		{"owner_user_id", "INTEGER", 0, 0},
		{"desk_id", "INTEGER", 0, 0},
		{"title", "TEXT", 1, 0},
		{"body", "TEXT", 1, 0},
		{"created_at", "TEXT", 1, 0},
		{"updated_at", "TEXT", 1, 0},
	}
	if got := replyColumns(t, s, "canned_replies"); !reflect.DeepEqual(got, wantColumns) {
		t.Errorf("canned_replies columns = %v, want %v", got, wantColumns)
	}
	wantFKs := []string{
		"desk_id->desks.id ON DELETE CASCADE",
		"owner_user_id->users.id ON DELETE CASCADE",
	}
	if got := replyForeignKeys(t, s, "canned_replies"); !reflect.DeepEqual(got, wantFKs) {
		t.Errorf("canned_replies foreign keys = %v, want %v", got, wantFKs)
	}
	wantIndexes := []string{"idx_canned_replies_desk", "idx_canned_replies_owner"}
	if got := replyIndexes(t, s, "canned_replies"); !reflect.DeepEqual(got, wantIndexes) {
		t.Errorf("canned_replies indexes = %v, want %v", got, wantIndexes)
	}
	for index, column := range map[string]string{
		"idx_canned_replies_owner": "owner_user_id",
		"idx_canned_replies_desk":  "desk_id",
	} {
		if got := replyIndexColumns(t, s, index); !reflect.DeepEqual(got, []string{column}) {
			t.Errorf("%s columns = %v, want [%s]", index, got, column)
		}
	}
}

// insertCannedReply writes one reply through raw SQL: the arrange path for a
// table whose store is not under test here, so the CHECK is what is
// under test here.
func insertCannedReply(t *testing.T, s *Store, owner, desk any, title, body string) error {
	t.Helper()
	_, err := s.db.Exec(`INSERT INTO canned_replies (owner_user_id, desk_id, title, body, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`, owner, desk, title, body, formatTime(testClock), formatTime(testClock))
	return err
}

// TestMigration0033CannedReplyNeedsExactlyOneOwner proves the personal/shared
// split cannot be ambiguous: a reply belongs to a person or to a desk, never
// to both and never to nobody.
func TestMigration0033CannedReplyNeedsExactlyOneOwner(t *testing.T) {
	s := newTestDB(t)
	agent := seedUser(t, s, "Ana", "ana@example.com", true)
	desk := seedDesk(t, s, "Support")
	for _, tc := range []struct {
		name        string
		owner, desk any
		ok          bool
	}{
		{"personal", agent, nil, true},
		{"desk", nil, desk, true},
		{"nobody", nil, nil, false},
		{"both", agent, desk, false},
	} {
		if err := insertCannedReply(t, s, tc.owner, tc.desk, "Greeting", "Hello there"); (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

// TestMigration0033CannedReplyTitleAndBodyBounds proves the lengths the schema
// promises: a non-empty title of at most 120 characters and a non-empty body
// of at most 10000, both measured after trimming.
func TestMigration0033CannedReplyTitleAndBodyBounds(t *testing.T) {
	s := newTestDB(t)
	agent := seedUser(t, s, "Ana", "ana@example.com", true)
	for _, tc := range []struct {
		name, title, body string
		ok                bool
	}{
		{"shortest", "Hi", "Hello", true},
		{"title at the bound", strings.Repeat("t", 120), "Hello", true},
		{"title past the bound", strings.Repeat("t", 121), "Hello", false},
		{"empty title", "", "Hello", false},
		{"blank title", strings.Repeat(" ", 4), "Hello", false},
		{"body at the bound", "Hi", strings.Repeat("b", 10000), true},
		{"body past the bound", "Hi", strings.Repeat("b", 10001), false},
		{"empty body", "Hi", "", false},
		{"blank body", "Hi", strings.Repeat(" ", 4), false},
	} {
		if err := insertCannedReply(t, s, agent, nil, tc.title, tc.body); (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

// TestMigration0033CannedRepliesCascade proves a reply never outlives the
// person or the desk it belongs to.
func TestMigration0033CannedRepliesCascade(t *testing.T) {
	ctx := context.Background()
	s := newTestDB(t)
	agent := seedUser(t, s, "Ana", "ana@example.com", true)
	desk := seedDesk(t, s, "Support")
	if err := insertCannedReply(t, s, agent, nil, "Personal", "mine"); err != nil {
		t.Fatalf("insert personal reply: %v", err)
	}
	if err := insertCannedReply(t, s, nil, desk, "Shared", "ours"); err != nil {
		t.Fatalf("insert desk reply: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id=?`, agent); err != nil {
		t.Fatalf("delete owner: %v", err)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM canned_replies WHERE owner_user_id IS NOT NULL`); n != 0 {
		t.Errorf("personal replies after deleting their owner = %d, want 0", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM canned_replies WHERE desk_id IS NOT NULL`); n != 1 {
		t.Errorf("desk replies after deleting the owner = %d, want 1 (the desk reply is untouched)", n)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM desks WHERE id=?`, desk); err != nil {
		t.Fatalf("delete desk: %v", err)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM canned_replies`); n != 0 {
		t.Errorf("replies after deleting their desk = %d, want 0", n)
	}
}

// TestMigration0033TicketFollowersShape pins the follower table: one row per
// (ticket, follower), both edges cascading, and the follower index the fan-out
// reads.
func TestMigration0033TicketFollowersShape(t *testing.T) {
	s := newTestDB(t)
	wantColumns := []replyColumnShape{
		{"ticket_id", "INTEGER", 1, 1},
		{"user_id", "INTEGER", 1, 2},
		{"created_at", "TEXT", 1, 0},
	}
	if got := replyColumns(t, s, "ticket_followers"); !reflect.DeepEqual(got, wantColumns) {
		t.Errorf("ticket_followers columns = %v, want %v", got, wantColumns)
	}
	wantFKs := []string{
		"ticket_id->tickets.id ON DELETE CASCADE",
		"user_id->users.id ON DELETE CASCADE",
	}
	if got := replyForeignKeys(t, s, "ticket_followers"); !reflect.DeepEqual(got, wantFKs) {
		t.Errorf("ticket_followers foreign keys = %v, want %v", got, wantFKs)
	}
	if got := replyIndexColumns(t, s, "idx_ticket_followers_user"); !reflect.DeepEqual(got, []string{"user_id"}) {
		t.Errorf("idx_ticket_followers_user columns = %v, want [user_id]", got)
	}
}

func addFollower(t *testing.T, s *Store, ticketID, userID int64) error {
	t.Helper()
	_, err := s.db.Exec(`INSERT INTO ticket_followers (ticket_id, user_id, created_at) VALUES (?, ?, ?)`,
		ticketID, userID, formatTime(testClock))
	return err
}

// TestMigration0033TicketFollowersPrimaryKeyAndCascades proves following is
// idempotent (one row per pair) and that a follower row never outlives its
// ticket or its person.
func TestMigration0033TicketFollowersPrimaryKeyAndCascades(t *testing.T) {
	ctx := context.Background()
	s := newTestDB(t)
	follower := seedUser(t, s, "Ana", "ana@example.com", true)
	other := seedUser(t, s, "Bob", "bob@example.com", true)
	cat := seedCategory(t, s, "Bugs")
	tk := seedTicket(t, s, domain.Ticket{Number: 1, Title: "followed", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: testClock, UpdatedAt: testClock})
	if err := addFollower(t, s, tk.ID, follower); err != nil {
		t.Fatalf("follow: %v", err)
	}
	if err := addFollower(t, s, tk.ID, follower); err == nil {
		t.Error("following the same ticket twice succeeded, want a primary-key failure")
	}
	if err := addFollower(t, s, tk.ID, other); err != nil {
		t.Fatalf("second follower: %v", err)
	}

	if _, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id=?`, follower); err != nil {
		t.Fatalf("delete follower: %v", err)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM ticket_followers WHERE user_id=?`, follower); n != 0 {
		t.Errorf("rows for the deleted follower = %d, want 0", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM ticket_followers WHERE ticket_id=?`, tk.ID); n != 1 {
		t.Errorf("rows for the ticket after one follower left = %d, want 1", n)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM tickets WHERE id=?`, tk.ID); err != nil {
		t.Fatalf("delete ticket: %v", err)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM ticket_followers`); n != 0 {
		t.Errorf("follower rows after deleting the ticket = %d, want 0", n)
	}
}

// TestMigration0033NotificationStoreRoundTripsTheNewKinds proves the two new
// kinds travel through the real store path — validation included — and read
// back with their facts: a mention keeps its comment id, and an assignee
// change keeps the new assignee's name (NULL meaning unassigned).
func TestMigration0033NotificationStoreRoundTripsTheNewKinds(t *testing.T) {
	ctx := context.Background()
	s := newTestDB(t)
	ana, ticketID := notificationFixture(t, s)
	res, err := s.db.ExecContext(ctx, `INSERT INTO comments (ticket_id, author, body, created_at) VALUES (?, 'Ana', 'hi', ?)`,
		ticketID, formatTime(testClock))
	if err != nil {
		t.Fatalf("seed comment: %v", err)
	}
	commentID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	assignee := "Bob Ray"
	insertNotifications(t, s,
		domain.Notification{UserID: ana, TicketID: ticketID, Kind: domain.NotificationMentioned,
			CommentID: ptr(commentID), CreatedAt: testClock},
		domain.Notification{UserID: ana, TicketID: ticketID, Kind: domain.NotificationAssigneeChanged,
			ToValue: &assignee, CreatedAt: testClock.Add(time.Minute)},
		domain.Notification{UserID: ana, TicketID: ticketID, Kind: domain.NotificationAssigneeChanged,
			CreatedAt: testClock.Add(2 * time.Minute)},
	)
	rows, err := s.NotificationStore().ListForUser(ctx, ana, 10)
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("ListForUser = %d rows, want 3", len(rows))
	}
	if got := rows[0]; got.Kind != domain.NotificationAssigneeChanged || got.ToValue != nil {
		t.Errorf("newest row = %+v, want an unassigned assignee change with a NULL to_value", got)
	}
	if got := rows[1]; got.Kind != domain.NotificationAssigneeChanged || got.ToValue == nil || *got.ToValue != assignee {
		t.Errorf("second row = %+v, want an assignee change to %q", got, assignee)
	}
	if got := rows[2]; got.Kind != domain.NotificationMentioned || got.CommentID == nil || *got.CommentID != commentID {
		t.Errorf("oldest row = %+v, want a mention of comment %d", got, commentID)
	}
}
