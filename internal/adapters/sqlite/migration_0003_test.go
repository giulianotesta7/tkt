package sqlite

import (
	"context"
	"testing"
)

// Task 1.4: RED tests for migration 0003 (roles and views) — schema
// constraints, the single-root partial unique index, and the root/group
// triggers. Written against the schema that does not exist yet: every
// assertion fails until 0003 lands (strict TDD RED).

// seedUserRaw inserts a user with an explicit role (raw SQL; seedUser
// relies on the DB default). Returns the new user id.
func seedUserRaw(t *testing.T, s *Store, name, email, role string) int64 {
	t.Helper()
	res, err := s.db.ExecContext(context.Background(),
		`INSERT INTO users (name, email, password_hash, active, role, created_at)
		 VALUES (?, ?, 'bcrypt-hash', 1, ?, '2026-08-06T10:00:00Z')`,
		name, email, role)
	if err != nil {
		t.Fatalf("seed user %s: %v", email, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed user %s: last insert id: %v", email, err)
	}
	return id
}

func columnExists(t *testing.T, s *Store, table, column string) bool {
	t.Helper()
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column,
	).Scan(&n); err != nil {
		t.Fatalf("pragma_table_info(%s): %v", table, err)
	}
	return n == 1
}

func TestMigration0003RoleColumnAndCheck(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	if !columnExists(t, s, "users", "role") {
		t.Fatal("users.role column missing after 0003")
	}

	// Invalid role values are rejected by the CHECK constraint.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO users (name, email, password_hash, active, role, created_at)
		 VALUES ('Nope', 'nope@example.com', 'h', 1, 'superuser', '2026-08-06T10:00:00Z')`); err == nil {
		t.Fatal("insert with invalid role succeeded, want CHECK failure")
	}

	// An insert that omits role falls back to the legacy default 'agent'
	// (design: remaining users backfill to agent).
	uid := seedUser(t, s, "Legacy", "legacy@example.com", true)
	var role string
	if err := s.db.QueryRow(`SELECT role FROM users WHERE id = ?`, uid).Scan(&role); err != nil {
		t.Fatalf("read role: %v", err)
	}
	if role != "agent" {
		t.Errorf("default role = %q, want %q", role, "agent")
	}
}

func TestMigration0003UniqueRoot(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	rootID := seedUserRaw(t, s, "Root", "root@example.com", "root")
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_users_one_root'`).Scan(&n); err != nil {
		t.Fatalf("check root index: %v", err)
	}
	if n != 1 {
		t.Fatal("partial unique index idx_users_one_root missing")
	}

	// A second root row — via INSERT or via promotion — is rejected.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO users (name, email, password_hash, active, role, created_at)
		 VALUES ('Other', 'other@example.com', 'h', 1, 'root', '2026-08-06T10:00:00Z')`); err == nil {
		t.Fatal("second root insert succeeded, want UNIQUE failure")
	}
	agentID := seedUser(t, s, "Agent", "agent@example.com", true)
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET role = 'root' WHERE id = ?`, agentID); err == nil {
		t.Fatal("promoting a second user to root succeeded, want UNIQUE failure")
	}

	// The original root is untouched.
	var got string
	if err := s.db.QueryRow(`SELECT role FROM users WHERE id = ?`, rootID).Scan(&got); err != nil {
		t.Fatalf("read root role: %v", err)
	}
	if got != "root" {
		t.Errorf("root role = %q, want %q", got, "root")
	}
}

func TestMigration0003RootImmutable(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	rootID := seedUserRaw(t, s, "Root", "root@example.com", "root")

	// No actor — including root itself — may update the root account.
	updates := []struct {
		name string
		sql  string
	}{
		{"rename", `UPDATE users SET name = 'Hacker' WHERE id = ?`},
		{"change email", `UPDATE users SET email = 'hack@example.com' WHERE id = ?`},
		{"deactivate", `UPDATE users SET active = 0 WHERE id = ?`},
		{"demote", `UPDATE users SET role = 'user' WHERE id = ?`},
	}
	for _, u := range updates {
		t.Run(u.name, func(t *testing.T) {
			if _, err := s.db.ExecContext(ctx, u.sql, rootID); err == nil {
				t.Fatal("root update succeeded, want trigger abort")
			}
		})
	}

	// Root cannot be deleted either.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, rootID); err == nil {
		t.Fatal("root delete succeeded, want trigger abort")
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE id = ? AND role = 'root' AND active = 1`, rootID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("root row count = %d, want 1 (root preserved)", n)
	}
}

func TestMigration0003RequesterColumn(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	if !columnExists(t, s, "tickets", "requester_user_id") {
		t.Fatal("tickets.requester_user_id column missing after 0003")
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_tickets_requester'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("idx_tickets_requester index missing")
	}

	catID := seedCategory(t, s, "Bugs")
	uid := seedUser(t, s, "Ana", "ana@example.com", true)

	// A valid requester reference is stored and read back.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO tickets (number, title, description, requester_name, requester_email, requester_user_id, category_id, priority, state, created_at, updated_at)
		 VALUES (1, 'mine', '', 'Ana', 'ana@example.com', ?, ?, 'medium', 'new', '2026-08-06T10:00:00Z', '2026-08-06T10:00:00Z')`,
		uid, catID); err != nil {
		t.Fatalf("insert with requester_user_id: %v", err)
	}

	// A dangling requester reference is rejected by the FK.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO tickets (number, title, description, requester_name, requester_email, requester_user_id, category_id, priority, state, created_at, updated_at)
		 VALUES (2, 'bad', '', 'Ghost', 'ghost@example.com', 999, ?, 'medium', 'new', '2026-08-06T10:00:00Z', '2026-08-06T10:00:00Z')`,
		catID); err == nil {
		t.Fatal("insert with dangling requester_user_id succeeded, want FK failure")
	}
}

func TestMigration0003CommentVisibility(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	if !columnExists(t, s, "comments", "visibility") {
		t.Fatal("comments.visibility column missing after 0003")
	}

	catID := seedCategory(t, s, "Bugs")
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO tickets (number, title, description, requester_name, requester_email, category_id, priority, state, created_at, updated_at)
		 VALUES (1, 't', '', 'r', 'e', ?, 'medium', 'new', '2026-08-06T10:00:00Z', '2026-08-06T10:00:00Z')`,
		catID)
	if err != nil {
		t.Fatalf("seed ticket: %v", err)
	}
	ticketID, _ := res.LastInsertId()

	// Legacy comments (no visibility) backfill to 'public' via the default.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO comments (ticket_id, author, body, created_at) VALUES (?, 'Ana', 'legacy note', '2026-08-06T10:00:00Z')`,
		ticketID); err != nil {
		t.Fatalf("insert legacy comment: %v", err)
	}
	var vis string
	if err := s.db.QueryRow(`SELECT visibility FROM comments WHERE ticket_id = ?`, ticketID).Scan(&vis); err != nil {
		t.Fatal(err)
	}
	if vis != "public" {
		t.Errorf("legacy comment visibility = %q, want %q (public backfill)", vis, "public")
	}

	// 'internal' is legal; anything else is rejected by the CHECK.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO comments (ticket_id, author, body, visibility, created_at) VALUES (?, 'Ana', 'staff note', 'internal', '2026-08-06T10:00:00Z')`,
		ticketID); err != nil {
		t.Fatalf("insert internal comment: %v", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO comments (ticket_id, author, body, visibility, created_at) VALUES (?, 'Ana', 'secret', 'secret', '2026-08-06T10:00:00Z')`,
		ticketID); err == nil {
		t.Fatal("insert with invalid visibility succeeded, want CHECK failure")
	}
}

func TestMigration0003GroupsAndMembers(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	for _, table := range []string{"groups", "group_members"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("table %s missing after 0003", table)
		}
	}

	// Unique, non-empty group names.
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO groups (name, created_at) VALUES ('Support', '2026-08-06T10:00:00Z')`)
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	groupID, _ := res.LastInsertId()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO groups (name, created_at) VALUES ('Support', '2026-08-06T10:00:00Z')`); err == nil {
		t.Fatal("duplicate group name succeeded, want UNIQUE failure")
	}

	// N:N membership: an agent can belong to two groups, a group holds two
	// agents.
	agentA := seedUser(t, s, "A", "a@example.com", true)
	agentB := seedUser(t, s, "B", "b@example.com", true)
	res, err = s.db.ExecContext(ctx,
		`INSERT INTO groups (name, created_at) VALUES ('Tier2', '2026-08-06T10:00:00Z')`)
	if err != nil {
		t.Fatalf("create second group: %v", err)
	}
	group2ID, _ := res.LastInsertId()

	memberInsert := `INSERT INTO group_members (group_id, user_id, created_at) VALUES (?, ?, '2026-08-06T10:00:00Z')`
	for _, m := range []struct {
		groupID int64
		userID  int64
	}{
		{groupID, agentA},
		{group2ID, agentA}, // agent A in two groups
		{groupID, agentB},  // two agents in one group
	} {
		if _, err := s.db.ExecContext(ctx, memberInsert, m.groupID, m.userID); err != nil {
			t.Fatalf("N:N member insert: %v", err)
		}
	}

	// A user-role account can never become a member (trigger).
	userID := seedUserRaw(t, s, "U", "u@example.com", "user")
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO group_members (group_id, user_id, created_at) VALUES (?, ?, '2026-08-06T10:00:00Z')`,
		groupID, userID); err == nil {
		t.Fatal("user-role member insert succeeded, want trigger abort")
	}

	// A member cannot be downgraded to role 'user' (trigger).
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET role = 'user' WHERE id = ?`, agentA); err == nil {
		t.Fatal("downgrading a group member to user succeeded, want trigger abort")
	}
	var role string
	if err := s.db.QueryRow(`SELECT role FROM users WHERE id = ?`, agentA).Scan(&role); err != nil {
		t.Fatal(err)
	}
	if role != "agent" {
		t.Errorf("member role after rejected downgrade = %q, want %q", role, "agent")
	}
}

func TestMigration0003AuditExtensions(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	for _, col := range []string{"actor_user_id", "reason"} {
		if !columnExists(t, s, "audit_events", col) {
			t.Errorf("audit_events.%s column missing after 0003", col)
		}
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='role_changes'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("role_changes table missing after 0003")
	}

	catID := seedCategory(t, s, "Bugs")
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO tickets (number, title, description, requester_name, requester_email, category_id, priority, state, created_at, updated_at)
		 VALUES (1, 't', '', 'r', 'e', ?, 'medium', 'new', '2026-08-06T10:00:00Z', '2026-08-06T10:00:00Z')`,
		catID)
	if err != nil {
		t.Fatalf("seed ticket: %v", err)
	}
	ticketID, _ := res.LastInsertId()
	uid := seedUser(t, s, "Ana", "ana@example.com", true)

	// A valid actor_user_id is accepted and read back.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_events (ticket_id, actor, action, actor_user_id, reason, created_at)
		 VALUES (?, 'Ana', 'transition', ?, 'reopened with evidence', '2026-08-06T10:00:00Z')`,
		ticketID, uid); err != nil {
		t.Fatalf("insert audit event with actor_user_id: %v", err)
	}

	// A dangling actor_user_id is rejected (FK).
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_events (ticket_id, actor, action, actor_user_id, created_at)
		 VALUES (?, 'Ghost', 'transition', 999, '2026-08-06T10:00:00Z')`,
		ticketID); err == nil {
		t.Fatal("audit event with dangling actor_user_id succeeded, want FK failure")
	}

	// role_changes enforces its role CHECK and user FK.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO role_changes (user_id, from_role, to_role, actor_user_id, reason, created_at)
		 VALUES (?, 'agent', 'superuser', NULL, 'bad', '2026-08-06T10:00:00Z')`,
		uid); err == nil {
		t.Fatal("role_changes with invalid to_role succeeded, want CHECK failure")
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO role_changes (user_id, from_role, to_role, actor_user_id, reason, created_at)
		 VALUES (999, 'agent', 'admin', NULL, 'ghost', '2026-08-06T10:00:00Z')`); err == nil {
		t.Fatal("role_changes with dangling user_id succeeded, want FK failure")
	}
}

// The existing schema-inventory tests (TestMigrateCreatesSchema,
// TestMigrateRerunIsNoOp) are updated to include 0003 in the same commit
// as the migration itself — see sqlite_test.go.
