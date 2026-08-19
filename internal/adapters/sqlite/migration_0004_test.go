package sqlite

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestMigration0004RenamesGroupsToDesksInPlace(t *testing.T) {
	ctx := context.Background()
	s := newMigration0003DB(t)

	agentID := seedUser(t, s, "Agent", "agent@example.com", true)
	memberID := seedUser(t, s, "Member", "member@example.com", true)
	userID := seedUserRaw(t, s, "User", "user@example.com", "user")
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO groups (name, created_at) VALUES ('Support', '2026-08-16T10:00:00Z')`)
	if err != nil {
		t.Fatalf("seed group: %v", err)
	}
	groupID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("group id: %v", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO group_members (group_id, user_id, created_at) VALUES (?, ?, '2026-08-16T10:00:00Z')`, groupID, agentID); err != nil {
		t.Fatalf("seed group member: %v", err)
	}
	categoryID := seedCategory(t, s, "Support")
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO tickets (number, title, description, requester_name, requester_email, category_id, user_id, priority, state, created_at, updated_at)
		 VALUES (1, 'Assigned', '', 'Requester', 'requester@example.com', ?, ?, 'medium', 'new', '2026-08-16T10:00:00Z', '2026-08-16T10:00:00Z')`, categoryID, agentID); err != nil {
		t.Fatalf("seed assigned ticket: %v", err)
	}

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("apply 0004: %v", err)
	}

	for _, oldName := range []string{"groups", "group_members", "trg_group_members_no_user", "trg_group_members_no_user_update", "trg_users_no_group_member_downgrade"} {
		assertSchemaObjectAbsent(t, s, oldName)
	}
	for _, newName := range []string{"desks", "desk_members", "trg_desk_members_no_user", "trg_desk_members_no_user_update", "trg_users_no_desk_member_downgrade"} {
		assertSchemaObjectPresent(t, s, newName)
	}

	var name, createdAt string
	if err := s.db.QueryRowContext(ctx, `SELECT name, created_at FROM desks WHERE id = ?`, groupID).Scan(&name, &createdAt); err != nil {
		t.Fatalf("read migrated desk: %v", err)
	}
	if name != "Support" || createdAt != "2026-08-16T10:00:00Z" {
		t.Fatalf("migrated desk = (%q, %q), want (Support, timestamp)", name, createdAt)
	}
	var memberCreatedAt string
	if err := s.db.QueryRowContext(ctx, `SELECT created_at FROM desk_members WHERE desk_id = ? AND user_id = ?`, groupID, agentID).Scan(&memberCreatedAt); err != nil {
		t.Fatalf("read migrated member: %v", err)
	}
	if memberCreatedAt != "2026-08-16T10:00:00Z" {
		t.Errorf("member timestamp = %q, want preserved value", memberCreatedAt)
	}

	assertDeskSchema(t, s)
	assertDeskConstraints(t, s, groupID, agentID, memberID, userID)
	var assigneeID int64
	if err := s.db.QueryRowContext(ctx, `SELECT user_id FROM tickets WHERE number = 1`).Scan(&assigneeID); err != nil {
		t.Fatalf("read preserved ticket assignment: %v", err)
	}
	if assigneeID != agentID {
		t.Errorf("ticket assignee = %d, want preserved user %d", assigneeID, agentID)
	}
	if columnExists(t, s, "tickets", "desk_id") || columnExists(t, s, "tickets", "group_id") {
		t.Error("ticket schema gained a desk/group assignment column")
	}

	result, err = s.db.ExecContext(ctx, `INSERT INTO desks (name, created_at) VALUES ('Escalations', '2026-08-16T11:00:00Z')`)
	if err != nil {
		t.Fatalf("insert next desk: %v", err)
	}
	nextID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("next desk id: %v", err)
	}
	if nextID <= groupID {
		t.Errorf("next desk id = %d, want > preserved id %d", nextID, groupID)
	}

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("rerun migration: %v", err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM desks`).Scan(&count); err != nil {
		t.Fatalf("count desks after rerun: %v", err)
	}
	if count != 2 {
		t.Errorf("desk count after rerun = %d, want 2", count)
	}
}

func newMigration0003DB(t *testing.T) *Store {
	t.Helper()
	s, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	s.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = s.db.Close() })
	if err := migrate(context.Background(), s.db, migrationsBefore0004(t)); err != nil {
		t.Fatalf("migrate through 0003: %v", err)
	}
	return s
}

func migrationsBefore0004(t *testing.T) fs.FS {
	t.Helper()
	files := fstest.MapFS{}
	for _, name := range []string{"0001_init.sql", "0002_fts.sql", "0003_roles_and_views.sql"} {
		contents, err := fs.ReadFile(migrationsFS, "migrations/"+name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		files["migrations/"+name] = &fstest.MapFile{Data: contents}
	}
	return files
}

func assertSchemaObjectAbsent(t *testing.T, s *Store, name string) {
	t.Helper()
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = ?`, name).Scan(&count); err != nil {
		t.Fatalf("lookup %s: %v", name, err)
	}
	if count != 0 {
		t.Errorf("old schema object %q still exists", name)
	}
}

func assertSchemaObjectPresent(t *testing.T, s *Store, name string) {
	t.Helper()
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = ?`, name).Scan(&count); err != nil {
		t.Fatalf("lookup %s: %v", name, err)
	}
	if count != 1 {
		t.Errorf("schema object %q count = %d, want 1", name, count)
	}
}

func assertDeskSchema(t *testing.T, s *Store) {
	t.Helper()
	var table, from, to string
	if err := s.db.QueryRow(`SELECT "table", "from", "to" FROM pragma_foreign_key_list('desk_members') WHERE "from" = 'desk_id'`).Scan(&table, &from, &to); err != nil {
		t.Fatalf("desk_members desk FK: %v", err)
	}
	if table != "desks" || from != "desk_id" || to != "id" {
		t.Errorf("desk FK = %s.%s -> %s, want desks.desk_id -> id", table, from, to)
	}

	assertIndexColumns(t, s, "desks", "sqlite_autoindex_desks_1", []string{"name"})
	assertIndexColumns(t, s, "desk_members", "sqlite_autoindex_desk_members_1", []string{"desk_id", "user_id"})

	rows, err := s.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatalf("foreign key check: %v", err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign key check returned a violation")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("foreign key check iterate: %v", err)
	}
}

func assertIndexColumns(t *testing.T, s *Store, table, index string, want []string) {
	t.Helper()
	var exists int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_index_list(?) WHERE name = ?`, table, index).Scan(&exists); err != nil {
		t.Fatalf("index list %s: %v", table, err)
	}
	if exists != 1 {
		t.Fatalf("index %s missing from %s", index, table)
	}
	rows, err := s.db.Query(`SELECT name FROM pragma_index_info(?) ORDER BY seqno`, index)
	if err != nil {
		t.Fatalf("index info %s: %v", index, err)
	}
	defer rows.Close()
	for i, column := range want {
		if !rows.Next() {
			t.Fatalf("index %s has fewer columns than %v", index, want)
		}
		var got string
		if err := rows.Scan(&got); err != nil {
			t.Fatalf("scan index column: %v", err)
		}
		if got != column {
			t.Errorf("index %s column %d = %q, want %q", index, i, got, column)
		}
	}
}

func assertDeskConstraints(t *testing.T, s *Store, deskID, agentID, memberID, userID int64) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO desk_members (desk_id, user_id, created_at) VALUES (?, ?, '2026-08-16T11:00:00Z')`, deskID, userID); err == nil {
		t.Error("user-role desk member insert succeeded, want trigger abort")
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE desk_members SET user_id = ? WHERE desk_id = ? AND user_id = ?`, userID, deskID, agentID); err == nil {
		t.Error("user-role desk member update succeeded, want trigger abort")
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE desk_members SET user_id = ? WHERE desk_id = ? AND user_id = ?`, memberID, deskID, agentID); err != nil {
		t.Errorf("agent-plus desk member update failed: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET role = 'user' WHERE id = ?`, memberID); err == nil {
		t.Error("desk member downgrade succeeded, want trigger abort")
	}
	var role string
	if err := s.db.QueryRowContext(ctx, `SELECT role FROM users WHERE id = ?`, memberID).Scan(&role); err != nil {
		t.Fatalf("read member role: %v", err)
	}
	if role != "agent" {
		t.Errorf("member role after failed downgrade = %q, want agent", role)
	}

}
