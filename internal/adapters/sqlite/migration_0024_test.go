package sqlite

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Migration 0024 tests (notification email delivery — issue #362, spec S12):
// the notification_emails table shape, the closed delivery-status vocabulary
// and the non-negative attempt bound, the deliberate ABSENCE of a foreign key
// on notification_id, the due-claim index, the pre-0024 upgrade path leaving
// existing notifications unqueued, and the version bookkeeping. Written
// against the numbered-series conventions of migration_0021_test.go.

// pre0024Migrations returns the embedded migrations strictly before 0024, so
// the upgrade-path test can seed a pre-0024 database and then apply the real
// full set (pattern of pre0021Migrations). The set is everything below 0024,
// including any 0022/0023 that a chained branch contributes later.
func pre0024Migrations(t *testing.T) fstest.MapFS {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	out := fstest.MapFS{}
	for _, entry := range entries {
		if strings.Compare(entry.Name(), "0024_notification_emails.sql") >= 0 {
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

// TestMigration0024NotificationEmailsTableShape proves the delivery columns
// exist with the nullability delivery state relies on: the notification and
// the scheduling facts are required, while every "when/why it ended" fact is
// absent until it happens.
func TestMigration0024NotificationEmailsTableShape(t *testing.T) {
	s := newTestDB(t)

	type columnShape struct {
		column  string
		wantTyp string
		notNull int
		pk      int
	}
	want := []columnShape{
		{"notification_id", "INTEGER", 0, 1},
		{"status", "TEXT", 1, 0},
		{"attempts", "INTEGER", 1, 0},
		{"next_attempt_at", "TEXT", 1, 0},
		{"claimed_at", "TEXT", 0, 0},
		{"sent_at", "TEXT", 0, 0},
		{"skip_reason", "TEXT", 0, 0},
		{"last_error", "TEXT", 0, 0},
		{"created_at", "TEXT", 1, 0},
		{"updated_at", "TEXT", 1, 0},
	}

	rows, err := s.db.Query(`SELECT name, type, "notnull", pk, dflt_value FROM pragma_table_info('notification_emails')`)
	if err != nil {
		t.Fatalf("pragma_table_info('notification_emails'): %v", err)
	}
	defer rows.Close()
	got := map[string]columnShape{}
	defaults := map[string]string{}
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
			defaults[name] = *dflt
		}
		got[name] = columnShape{name, colType, notNull, pk}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Errorf("notification_emails has %d columns, want %d: %v", len(got), len(want), got)
	}
	for _, w := range want {
		g, ok := got[w.column]
		if !ok {
			t.Errorf("column %s missing after 0024", w.column)
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
	// Only the attempt counter carries a default: a fresh row has zero
	// attempts, and every other optional fact stays NULL until it happens.
	for name := range got {
		if name == "attempts" {
			continue
		}
		if d, ok := defaults[name]; ok {
			t.Errorf("%s default = %q, want none", name, d)
		}
	}
	if d, ok := defaults["attempts"]; !ok || d != "0" {
		t.Errorf("attempts default = %q (present=%v), want 0", d, ok)
	}
}

// TestMigration0024StatusVocabularyIsClosed proves the column's CHECK accepts
// exactly the five application.EmailDeliveryStatus values the sender drives
// and rejects anything else, so the SQL and Go halves of the closed set cannot
// drift. It also pins the non-negative attempts bound.
func TestMigration0024StatusVocabularyIsClosed(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	user := seedUser(t, s, "Ana", "ana@example.com", true)
	cat := seedCategory(t, s, "Bugs")
	tk := seedTicket(t, s, domain.Ticket{Number: 1, Title: "email status check", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: testClock, UpdatedAt: testClock})
	notif := seedNotificationRow(t, s, user, tk.ID)

	for i, status := range []string{"pending", "sending", "sent", "skipped", "failed"} {
		id := notif + int64(i) + 1
		seedNotificationRowID(t, s, id, user, tk.ID)
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO notification_emails (notification_id, status, next_attempt_at, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?)`,
			id, status, formatTime(testClock), formatTime(testClock), formatTime(testClock)); err != nil {
			t.Errorf("insert status %q: %v, want it accepted", status, err)
		}
	}

	for _, status := range []string{"", "queued", "PENDING", "pending ", "retry"} {
		id := seedNotificationRow(t, s, user, tk.ID)
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO notification_emails (notification_id, status, next_attempt_at, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?)`,
			id, status, formatTime(testClock), formatTime(testClock), formatTime(testClock)); err == nil {
			t.Errorf("insert status %q succeeded, want CHECK failure", status)
		}
	}

	for _, attempts := range []int{-1, -7} {
		id := seedNotificationRow(t, s, user, tk.ID)
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO notification_emails (notification_id, status, attempts, next_attempt_at, created_at, updated_at)
			 VALUES (?, 'pending', ?, ?, ?, ?)`,
			id, attempts, formatTime(testClock), formatTime(testClock), formatTime(testClock)); err == nil {
			t.Errorf("insert attempts %d succeeded, want CHECK failure", attempts)
		}
	}

	// One delivery row per notification is a storage invariant, not a
	// convention: the PRIMARY KEY refuses the second row.
	id := seedNotificationRow(t, s, user, tk.ID)
	for i := 0; i < 2; i++ {
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO notification_emails (notification_id, status, next_attempt_at, created_at, updated_at)
			 VALUES (?, 'pending', ?, ?, ?)`,
			id, formatTime(testClock), formatTime(testClock), formatTime(testClock))
		if i == 1 && err == nil {
			t.Error("second delivery row for one notification succeeded, want PRIMARY KEY failure")
		}
	}
}

// TestMigration0024DeclaresNoForeignKey pins the DELIBERATE absence of a
// foreign key on notification_id. `notifications` is rebuilt by the documented
// recipe for adding a kind (CREATE/copy/DROP/RENAME — #352's 0023 does it), and
// a cascading edge would delete every delivery row on that DROP while a
// restricting one would fail the rebuild, forcing every future kind to know
// about this table. Integrity comes from the store instead: ClaimDue only
// claims rows that JOIN a live notification and deletes the orphans
// (email_outbox_store_test.go proves it).
func TestMigration0024DeclaresNoForeignKey(t *testing.T) {
	s := newTestDB(t)

	rows, err := s.db.Query(`SELECT "from", "table", "to", on_delete FROM pragma_foreign_key_list('notification_emails')`)
	if err != nil {
		t.Fatalf("pragma_foreign_key_list('notification_emails'): %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var from, table, to, onDelete string
		if err := rows.Scan(&from, &table, &to, &onDelete); err != nil {
			t.Fatal(err)
		}
		t.Errorf("notification_emails declares FK %s -> %s(%s) ON DELETE %s, want none (the notifications rebuild must not touch delivery rows)",
			from, table, to, onDelete)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

// TestMigration0024DueIndex proves the claim index exists over exactly
// (status, next_attempt_at), the filter and the ordering of the one hot read:
// due pending rows, oldest first.
func TestMigration0024DueIndex(t *testing.T) {
	s := newTestDB(t)

	rows, err := s.db.Query(`SELECT name FROM pragma_index_info('idx_notification_emails_due') ORDER BY seqno`)
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
	if got := strings.Join(columns, ","); got != "status,next_attempt_at" {
		t.Errorf("idx_notification_emails_due columns = %q, want %q", got, "status,next_attempt_at")
	}
}

// TestMigration0024UpgradePathLeavesExistingNotificationsUnqueued proves a
// pre-0024 database upgrades cleanly, rewrites nothing, and queues no email
// for the notifications it already holds (S12: no backfill, so nothing old is
// ever mailed retroactively).
func TestMigration0024UpgradePathLeavesExistingNotificationsUnqueued(t *testing.T) {
	ctx := context.Background()
	pre, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open pre-0024 db: %v", err)
	}
	pre.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pre.db.Close() })
	if err := migrate(ctx, pre.db, pre0024Migrations(t)); err != nil {
		t.Fatalf("migrate pre-0024 db: %v", err)
	}

	var n int
	if err := pre.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='notification_emails'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("notification_emails existed before 0024, want it added by the migration")
	}

	user := seedUser(t, pre, "Ana", "ana@example.com", true)
	cat := seedCategory(t, pre, "pre-0024")
	tk := seedTicket(t, pre, domain.Ticket{Number: 77, Title: "written before 0024", CategoryID: cat,
		Priority: domain.PriorityHigh, State: domain.StateInProgress, CreatedAt: testClock, UpdatedAt: testClock})
	notifID := seedNotificationRow(t, pre, user, tk.ID)

	if err := migrate(ctx, pre.db, migrationsFS); err != nil {
		t.Fatalf("upgrade pre-0024 db: %v", err)
	}

	var (
		title string
		kind  string
	)
	if err := pre.db.QueryRowContext(ctx,
		`SELECT t.title, n.kind FROM notifications n JOIN tickets t ON t.id = n.ticket_id WHERE n.id = ?`,
		notifID).Scan(&title, &kind); err != nil {
		t.Fatalf("read upgraded notification: %v", err)
	}
	if title != "written before 0024" || kind != string(domain.NotificationCommentPublic) {
		t.Errorf("upgraded notification = (%q, %q), want it untouched", title, kind)
	}
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_emails`).Scan(&n); err != nil {
		t.Fatalf("read upgraded delivery rows: %v", err)
	}
	if n != 0 {
		t.Errorf("notification_emails rows after upgrade = %d, want 0 — 0024 backfills nothing", n)
	}

	var recorded int
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=24`).Scan(&recorded); err != nil {
		t.Fatalf("schema_migrations read: %v", err)
	}
	if recorded != 1 {
		t.Errorf("schema_migrations version=24 rows = %d, want 1", recorded)
	}
}

// TestMigration0024VersionBookkeeping proves the applied version is 24.
func TestMigration0024VersionBookkeeping(t *testing.T) {
	s := newTestDB(t)
	var applied int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=24`).Scan(&applied); err != nil {
		t.Fatalf("schema_migrations read: %v", err)
	}
	if applied != 1 {
		t.Fatalf("schema_migrations version=24 rows = %d, want 1", applied)
	}
}

// seedNotificationRow inserts one notification directly (the migration tests
// deliberately do not depend on the unit-of-work append path) and returns its
// id.
func seedNotificationRow(t *testing.T, s *Store, userID, ticketID int64) int64 {
	t.Helper()
	res, err := s.db.ExecContext(context.Background(),
		`INSERT INTO notifications (user_id, ticket_id, kind, created_at) VALUES (?, ?, ?, ?)`,
		userID, ticketID, string(domain.NotificationCommentPublic), formatTime(testClock))
	if err != nil {
		t.Fatalf("seed notification: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed notification: last insert id: %v", err)
	}
	return id
}

// seedNotificationRowID inserts one notification WITH a chosen id, so a test
// that needs several independent delivery rows can address them by id.
func seedNotificationRowID(t *testing.T, s *Store, id, userID, ticketID int64) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO notifications (id, user_id, ticket_id, kind, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, userID, ticketID, string(domain.NotificationCommentPublic), formatTime(testClock)); err != nil {
		t.Fatalf("seed notification %d: %v", id, err)
	}
}

// TestMigration0024AppliesAfterALaterMigration covers an instance that ran
// main between #366 and #362: 0025 is applied, 0024 is not. The runner checks
// each version on its own, so the next start applies 0024 and records it.
func TestMigration0024AppliesAfterALaterMigration(t *testing.T) {
	ctx := context.Background()
	db, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.db.Close() })

	without0024 := fstest.MapFS{}
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() == "0024_notification_emails.sql" {
			continue
		}
		blob, err := fs.ReadFile(migrationsFS, "migrations/"+entry.Name())
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		without0024["migrations/"+entry.Name()] = &fstest.MapFile{Data: blob}
	}
	if err := migrate(ctx, db.db, without0024); err != nil {
		t.Fatalf("migrate without 0024: %v", err)
	}
	if err := migrate(ctx, db.db, migrationsFS); err != nil {
		t.Fatalf("apply 0024 after a later migration: %v", err)
	}

	var n int
	if err := db.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='notification_emails'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("notification_emails tables = %d, want 1", n)
	}
	rows, err := db.db.QueryContext(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var versions []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		versions = append(versions, v)
	}
	if fmt.Sprint(versions) != fmt.Sprint(wantMigrationVersions) {
		t.Errorf("versions = %v, want %v", versions, wantMigrationVersions)
	}
}
