package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Migration 0034 notification tests (issue #419, S3): the bell has to carry an
// announcement that is not about a ticket — an account created by a company
// sign-in that waits for approval. That needs the notifications table rebuilt
// for the first time with a nullable ticket and an account subject, while every
// kind the earlier rebuilds added, every stored row and id, and every email
// delivery row survive. These tests pin the new shape, the closed vocabulary,
// the ownership edges, and the upgrade path.

// priorNotificationKinds is the vocabulary 0033 left (0028 plus the mention and
// the follower's assignee change), in the order the migrations introduced it. The upgrade test seeds one row of each so the
// rebuild proves it accepts all of them after the change.
var priorNotificationKinds = []domain.NotificationKind{
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
	domain.NotificationMentioned,
	domain.NotificationAssigneeChanged,
}

// TestMigration0034NotificationsShape pins the rebuilt columns: the ticket is
// now one of two subjects, so it is nullable, and the pending account sits
// next to it. Every other column keeps the definition 0021 gave it.
func TestMigration0034NotificationsShape(t *testing.T) {
	s := newTestDB(t)
	got := readColumnShapes(t, s, "notifications")
	assertColumnShapes(t, "notifications", got, []columnShape{
		{"id", "INTEGER", 0, 1},
		{"user_id", "INTEGER", 1, 0},
		{"ticket_id", "INTEGER", 0, 0},
		{"pending_user_id", "INTEGER", 0, 0},
		{"kind", "TEXT", 1, 0},
		{"actor_user_id", "INTEGER", 0, 0},
		{"from_value", "TEXT", 0, 0},
		{"to_value", "TEXT", 0, 0},
		{"comment_id", "INTEGER", 0, 0},
		{"step_index", "INTEGER", 0, 0},
		{"created_at", "TEXT", 1, 0},
		{"read_at", "TEXT", 0, 0},
	})

	rows, err := s.db.Query(`SELECT name FROM pragma_table_info('notifications') WHERE dflt_value IS NOT NULL`)
	if err != nil {
		t.Fatalf("read column defaults: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		t.Errorf("notifications.%s has a default, want none (an absent subject stays NULL)", name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	// The recipient index is the one 0021 created, unchanged: the rebuild
	// copies it rather than inventing a new read path.
	indexRows, err := s.db.Query(`SELECT name FROM pragma_index_info('idx_notifications_recipient') ORDER BY seqno`)
	if err != nil {
		t.Fatalf("pragma_index_info: %v", err)
	}
	defer indexRows.Close()
	var columns []string
	for indexRows.Next() {
		var name string
		if err := indexRows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, name)
	}
	if err := indexRows.Err(); err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(columns, ","); joined != "user_id,read_at,created_at" {
		t.Errorf("idx_notifications_recipient columns = %q, want %q", joined, "user_id,read_at,created_at")
	}
}

// TestMigration0034NotificationShapeIsClosed proves the columns accept exactly
// two shapes: a ticket notification (a ticket, no account) and the account
// announcement (an account, no ticket). Everything else is refused, including
// the old vocabulary misused with the new column.
func TestMigration0034NotificationShapeIsClosed(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	recipient := seedUser(t, s, "Ada", "ada@example.com", true)
	waiting := seedPendingUser(t, s, "Wanda", "wanda@example.com")
	cat := seedCategory(t, s, "Bugs")
	tk := seedTicket(t, s, domain.Ticket{Number: 1, Title: "shape", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: testClock, UpdatedAt: testClock})
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO comments (ticket_id, author, body, created_at) VALUES (?, 'Ada', 'body', ?)`,
		tk.ID, formatTime(testClock))
	if err != nil {
		t.Fatalf("seed comment: %v", err)
	}
	commentID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}

	insert := func(ticketID, pendingID, actorID, commentID any, kind string, from, to, step any) error {
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO notifications (user_id, ticket_id, pending_user_id, kind, actor_user_id, from_value, to_value, comment_id, step_index, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			recipient, ticketID, pendingID, kind, actorID, from, to, commentID, step, formatTime(testClock))
		return err
	}

	// Every kind 0028 left still needs its ticket and refuses an account
	// subject, so an older writer keeps working and a mixed row is impossible.
	for _, kind := range priorNotificationKinds {
		var comment any
		if kind.IsComment() {
			comment = commentID
		}
		if err := insert(tk.ID, nil, nil, comment, string(kind), nil, nil, nil); err != nil {
			t.Errorf("prior kind %q with its ticket = %v, want it accepted", kind, err)
		}
		if err := insert(nil, nil, nil, comment, string(kind), nil, nil, nil); !isConstraint(err) {
			t.Errorf("prior kind %q without a ticket = %v, want a CHECK failure", kind, err)
		}
		if err := insert(tk.ID, waiting, nil, comment, string(kind), nil, nil, nil); !isConstraint(err) {
			t.Errorf("prior kind %q with an account subject = %v, want a CHECK failure", kind, err)
		}
	}

	// The account announcement: an account subject, no ticket, and no ticket
	// facts. An actor is allowed — an administrator can be recorded as the
	// person whose action caused it.
	account := string(domain.NotificationCompanyAccountPending)
	if err := insert(nil, waiting, recipient, nil, account, nil, nil, nil); err != nil {
		t.Errorf("account announcement with an actor = %v, want it accepted", err)
	}
	if err := insert(nil, waiting, nil, nil, account, nil, nil, nil); err != nil {
		t.Errorf("account announcement = %v, want it accepted", err)
	}
	for name, err := range map[string]error{
		"with a ticket":          insert(tk.ID, waiting, nil, nil, account, nil, nil, nil),
		"without a subject":      insert(nil, nil, nil, nil, account, nil, nil, nil),
		"with a missing subject": insert(nil, 999, nil, nil, account, nil, nil, nil),
		"with a comment":         insert(nil, waiting, nil, commentID, account, nil, nil, nil),
		"with a from value":      insert(nil, waiting, nil, nil, account, "new", nil, nil),
		"with a to value":        insert(nil, waiting, nil, nil, account, nil, "closed", nil),
		"with a step index":      insert(nil, waiting, nil, nil, account, nil, nil, 2),
	} {
		if !isConstraint(err) {
			t.Errorf("account announcement %s = %v, want a CHECK failure", name, err)
		}
	}

	// The account subject must be a real, positive account. The foreign key
	// alone would accept 0 or a negative id when a row with that id exists, so
	// create those rows and prove the CHECK is what refuses them.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO users (id, name, email, password_hash, active, created_at) VALUES
		 (0, 'Zero', 'zero@example.com', '', 1, ?), (-3, 'Negative', 'negative@example.com', '', 1, ?)`,
		formatTime(testClock), formatTime(testClock)); err != nil {
		t.Fatalf("seed non-positive account ids: %v", err)
	}
	for _, subject := range []int{0, -3} {
		if err := insert(nil, subject, nil, nil, account, nil, nil, nil); !isConstraint(err) {
			t.Errorf("account announcement for subject %d = %v, want a CHECK failure", subject, err)
		}
	}

	for _, kind := range []string{"", "not_a_kind", "COMPANY_ACCOUNT_PENDING", "company_account_pending "} {
		if err := insert(tk.ID, nil, nil, nil, kind, nil, nil, nil); err == nil {
			t.Errorf("kind %q accepted, want CHECK rejection", kind)
		}
	}
}

// TestMigration0034NotificationForeignKeys pins the ownership edges after the
// rebuild: the ticket, the account subject, the recipient and the comment all
// cascade, and the actor edge never dangles with a cascade (the actor is
// history, not an owner).
func TestMigration0034NotificationForeignKeys(t *testing.T) {
	s := newTestDB(t)
	type fk struct {
		table, to, onDelete string
	}
	want := map[string]fk{
		"user_id":         {"users", "id", "CASCADE"},
		"ticket_id":       {"tickets", "id", "CASCADE"},
		"pending_user_id": {"users", "id", "CASCADE"},
		"comment_id":      {"comments", "id", "CASCADE"},
		"actor_user_id":   {"users", "id", "NO ACTION"},
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
	if len(seen) != len(want) {
		t.Errorf("notifications has %d foreign keys, want %d", len(seen), len(want))
	}
}

// TestMigration0034NotificationsUpgradeKeepsRowsIDsAndDelivery seeds a
// pre-0034 instance with one row of every prior kind under explicit HIGH ids,
// one delivery row in each settled state, and a deleted high id, then upgrades.
// Every row reads back with its id and facts, no delivery row is cascaded away
// by the rebuild, the id of the deleted row is never reused, and the new
// ticketless kind is accepted and read back through the store afterwards.
func TestMigration0034NotificationsUpgradeKeepsRowsIDsAndDelivery(t *testing.T) {
	ctx := context.Background()
	pre, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open pre-0034 db: %v", err)
	}
	defer pre.db.Close()
	pre.db.SetMaxOpenConns(1)
	if err := migrate(ctx, pre.db, pre0034Migrations(t)); err != nil {
		t.Fatalf("migrate pre-0034 db: %v", err)
	}
	recipient := seedUser(t, pre, "Ada", "ada@example.com", true)
	actor := seedUser(t, pre, "Bob", "bob@example.com", true)
	cat := seedCategory(t, pre, "Upgrade")
	tk := seedTicket(t, pre, domain.Ticket{Number: 7, Title: "before 0034", CategoryID: cat,
		Priority: domain.PriorityHigh, State: domain.StateInProgress, CreatedAt: testClock, UpdatedAt: testClock})
	res, err := pre.db.ExecContext(ctx,
		`INSERT INTO comments (ticket_id, author, body, created_at) VALUES (?, 'Bob', 'hi', ?)`,
		tk.ID, formatTime(testClock))
	if err != nil {
		t.Fatalf("seed comment: %v", err)
	}
	commentID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}

	const firstID = 4001
	for i, kind := range priorNotificationKinds {
		var comment, from, to, step any
		if kind.IsComment() {
			comment = commentID
		}
		switch kind {
		case domain.NotificationStateChanged:
			from, to = "new", "in_progress"
		case domain.NotificationPriorityChanged:
			from, to = "high", "critical"
		case domain.NotificationFormWaiting:
			step = 2
		}
		if _, err := pre.db.ExecContext(ctx,
			`INSERT INTO notifications (id, user_id, ticket_id, kind, actor_user_id, from_value, to_value, comment_id, step_index, created_at, read_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			int64(firstID+i), recipient, tk.ID, string(kind), actor, from, to, comment, step,
			formatTime(testClock), formatTime(testClock.Add(time.Hour))); err != nil {
			t.Fatalf("seed %s: %v", kind, err)
		}
	}
	// A delivery row per settled state proves the rebuild does not cascade
	// them away: notification_emails deliberately carries no foreign key.
	deliveries := []struct {
		id       int64
		status   string
		attempts int
		reason   any
	}{
		{firstID, "sent", 1, nil},
		{firstID + 1, "failed", 3, nil},
		{firstID + 2, "skipped", 0, "email_off"},
	}
	for _, d := range deliveries {
		if _, err := pre.db.ExecContext(ctx,
			`INSERT INTO notification_emails (notification_id, status, attempts, next_attempt_at, skip_reason, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			d.id, d.status, d.attempts, formatTime(testClock), d.reason, formatTime(testClock), formatTime(testClock)); err != nil {
			t.Fatalf("seed delivery %d: %v", d.id, err)
		}
	}
	// The highest id is deleted before the upgrade, so the COPY must carry the
	// counter: a fresh table would restart below it and reuse the id.
	const deletedID = 5000
	if _, err := pre.db.ExecContext(ctx,
		`INSERT INTO notifications (id, user_id, ticket_id, kind, created_at) VALUES (?, ?, ?, 'assigned', ?)`,
		int64(deletedID), recipient, tk.ID, formatTime(testClock)); err != nil {
		t.Fatalf("seed the deleted row: %v", err)
	}
	if _, err := pre.db.ExecContext(ctx, `DELETE FROM notifications WHERE id = ?`, int64(deletedID)); err != nil {
		t.Fatalf("delete the highest row: %v", err)
	}

	before := dumpNotifications(t, pre)

	if err := migrate(ctx, pre.db, migrationsFS); err != nil {
		t.Fatalf("upgrade pre-0034 db: %v", err)
	}

	after := dumpNotifications(t, pre)
	if len(after) != len(before) {
		t.Fatalf("notifications rows = %d after the upgrade, want the %d already there", len(after), len(before))
	}
	for i := range before {
		if after[i] != before[i] {
			t.Errorf("row %d changed by the rebuild:\n before %s\n after  %s", i, before[i], after[i])
			break
		}
	}
	var oldWithAccount int
	if err := pre.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM notifications WHERE pending_user_id IS NOT NULL`).Scan(&oldWithAccount); err != nil {
		t.Fatal(err)
	}
	if oldWithAccount != 0 {
		t.Errorf("pre-existing rows with an account subject = %d, want 0", oldWithAccount)
	}

	var deliveryCount int
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_emails`).Scan(&deliveryCount); err != nil {
		t.Fatal(err)
	}
	if deliveryCount != len(deliveries) {
		t.Errorf("notification_emails rows = %d after the upgrade, want %d", deliveryCount, len(deliveries))
	}
	for _, d := range deliveries {
		var status string
		var attempts int
		if err := pre.db.QueryRowContext(ctx,
			`SELECT status, attempts FROM notification_emails WHERE notification_id = ?`, d.id).Scan(&status, &attempts); err != nil {
			t.Fatalf("delivery %d after the upgrade: %v", d.id, err)
		}
		if status != d.status || attempts != d.attempts {
			t.Errorf("delivery %d = (%s,%d), want (%s,%d)", d.id, status, attempts, d.status, d.attempts)
		}
	}

	// The new kind is storable after the upgrade, with its account subject and
	// no ticket, and the store reads it back as the bell will.
	waiting := seedPendingUser(t, pre, "Wanda", "wanda@example.com")
	res, err = pre.db.ExecContext(ctx,
		`INSERT INTO notifications (user_id, ticket_id, pending_user_id, kind, created_at)
		 VALUES (?, NULL, ?, ?, ?)`, recipient, waiting, string(domain.NotificationCompanyAccountPending), formatTime(testClock))
	if err != nil {
		t.Fatalf("insert the account announcement after the upgrade: %v", err)
	}
	accountID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if accountID <= deletedID {
		t.Errorf("id of the new account announcement = %d, want > %d (a deleted id is never reused)", accountID, deletedID)
	}
	rows, err := pre.NotificationStore().ListForUser(ctx, recipient, 100)
	if err != nil {
		t.Fatalf("ListForUser after the upgrade: %v", err)
	}
	var found bool
	for _, n := range rows {
		if n.Kind == domain.NotificationCompanyAccountPending {
			found = true
			if n.ID != accountID || n.TicketID != 0 || n.PendingUserID == nil || *n.PendingUserID != waiting {
				t.Errorf("account announcement read back as %+v, want id %d, no ticket, subject %d", n, accountID, waiting)
			}
		}
		if n.Kind != domain.NotificationCompanyAccountPending && n.PendingUserID != nil {
			t.Errorf("ticket notification %d carries an account subject", n.ID)
		}
	}
	if !found {
		t.Error("the account announcement is missing from the recipient's list")
	}
	var leftovers int
	if err := pre.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE name = 'notifications_new'`).Scan(&leftovers); err != nil {
		t.Fatal(err)
	}
	if leftovers != 0 {
		t.Error("the rebuild left notifications_new behind")
	}
}
