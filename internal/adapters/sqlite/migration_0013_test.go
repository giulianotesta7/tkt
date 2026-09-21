package sqlite

import (
	"context"
	"database/sql"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

// RED tests for migration 0013 (comment authorship — issue #211,
// per-category SLA prerequisite): comments.author_user_id and
// comments.author_role, the role CHECK, the user FK, NULL authorship for
// legacy rows, and the version bookkeeping. Written against the schema
// that does not exist yet: every assertion fails until 0013 lands (strict
// TDD RED).

// pre0013Migrations returns the embedded migrations strictly before 0013,
// so the upgrade-path test can seed a pre-0013 database and then apply the
// real full set (pattern of pre0011Migrations).
func pre0013Migrations(t *testing.T) fstest.MapFS {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	out := fstest.MapFS{}
	for _, entry := range entries {
		if strings.Compare(entry.Name(), "0013_comment_authorship.sql") >= 0 {
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

func TestMigration0013AuthorshipColumns(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	if !columnExists(t, s, "comments", "author_user_id") {
		t.Fatal("comments.author_user_id column missing after 0013")
	}
	if !columnExists(t, s, "comments", "author_role") {
		t.Fatal("comments.author_role column missing after 0013")
	}

	ticketID := seedTicketForTimeline(t, s, 1)

	// An out-of-set author_role is rejected by the CHECK constraint.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO comments (ticket_id, author, body, visibility, author_role, created_at)
		 VALUES (?, 'Ada', 'forged role', 'public', 'superuser', '2026-08-06T10:00:00Z')`,
		ticketID); err == nil {
		t.Fatal("insert with invalid author_role succeeded, want CHECK failure")
	}

	// Each of the four roles of the closed hierarchy is accepted.
	for _, role := range []string{"user", "agent", "admin", "root"} {
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO comments (ticket_id, author, body, visibility, author_role, created_at)
			 VALUES (?, 'Ada', ?, 'public', ?, '2026-08-06T10:00:00Z')`,
			ticketID, "role "+role, role); err != nil {
			t.Fatalf("insert with author_role %s: %v", role, err)
		}
	}

	// A row written with only author_user_id keeps author_role NULL — the
	// columns are independent, both nullable, with no default.
	uid := seedUser(t, s, "Solo", "solo-0013@example.com", true)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO comments (ticket_id, author, body, visibility, author_user_id, created_at)
		 VALUES (?, 'Solo', 'id only', 'public', ?, '2026-08-06T10:00:00Z')`,
		ticketID, uid); err != nil {
		t.Fatalf("insert with author_user_id only: %v", err)
	}
	var role sql.NullString
	if err := s.db.QueryRow(`SELECT author_role FROM comments WHERE body = 'id only'`).Scan(&role); err != nil {
		t.Fatalf("read author_role: %v", err)
	}
	if role.Valid {
		t.Errorf("author_role with omitted column = %q, want NULL", role.String)
	}
}

func TestMigration0013AuthorUserIDForeignKey(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ticketID := seedTicketForTimeline(t, s, 1)

	// A dangling author_user_id is rejected by the FK to users(id).
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO comments (ticket_id, author, body, visibility, author_user_id, created_at)
		 VALUES (?, 'Ghost', 'no such user', 'public', 999, '2026-08-06T10:00:00Z')`,
		ticketID); err == nil {
		t.Fatal("insert with dangling author_user_id succeeded, want FK failure")
	}

	// A real user reference is accepted and read back.
	uid := seedUser(t, s, "Ada", "ada-0013@example.com", true)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO comments (ticket_id, author, body, visibility, author_user_id, created_at)
		 VALUES (?, 'Ada', 'known author', 'public', ?, '2026-08-06T10:00:00Z')`,
		ticketID, uid); err != nil {
		t.Fatalf("insert with known author_user_id: %v", err)
	}
	var got sql.NullInt64
	if err := s.db.QueryRow(`SELECT author_user_id FROM comments WHERE body = 'known author'`).Scan(&got); err != nil {
		t.Fatalf("read author_user_id: %v", err)
	}
	if !got.Valid || got.Int64 != uid {
		t.Errorf("author_user_id = %v, want %d", got, uid)
	}
}

// TestMigration0013LegacyRowsKeepNullAuthorship proves the pre-migration
// upgrade path: a comment row created before 0013 (inserted without the
// authorship columns) reads back NULL in both columns after the upgrade —
// the author of a historical row is never guessed.
func TestMigration0013LegacyRowsKeepNullAuthorship(t *testing.T) {
	ctx := context.Background()
	pre, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open pre-0013 db: %v", err)
	}
	pre.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pre.db.Close() })
	if err := migrate(ctx, pre.db, pre0013Migrations(t)); err != nil {
		t.Fatalf("migrate pre-0013 db: %v", err)
	}

	ticketID := seedTicketForTimeline(t, pre, 1)
	// Pre-0013 comment shape: the authorship columns do not exist yet, so
	// the insert omits them entirely.
	if _, err := pre.db.ExecContext(ctx,
		`INSERT INTO comments (ticket_id, author, body, visibility, created_at)
		 VALUES (?, 'Ada', 'written before 0013', 'public', '2026-08-06T10:00:00Z')`,
		ticketID); err != nil {
		t.Fatalf("seed legacy comment: %v", err)
	}

	if err := migrate(ctx, pre.db, migrationsFS); err != nil {
		t.Fatalf("upgrade pre-0013 db: %v", err)
	}

	var uid sql.NullInt64
	var role sql.NullString
	if err := pre.db.QueryRow(`SELECT author_user_id, author_role FROM comments WHERE body = 'written before 0013'`).Scan(&uid, &role); err != nil {
		t.Fatalf("read legacy comment authorship: %v", err)
	}
	if uid.Valid {
		t.Errorf("legacy author_user_id = %d, want NULL", uid.Int64)
	}
	if role.Valid {
		t.Errorf("legacy author_role = %q, want NULL", role.String)
	}
}

func TestMigration0013VersionBookkeeping(t *testing.T) {
	s := newTestDB(t)
	var applied int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=12`).Scan(&applied); err != nil {
		t.Fatalf("schema_migrations read: %v", err)
	}
	if applied != 1 {
		t.Fatalf("schema_migrations version=12 rows = %d, want 1", applied)
	}
}
