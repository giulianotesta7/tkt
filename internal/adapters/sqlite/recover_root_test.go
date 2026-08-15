package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Task 2.3: RED tests for -recover-root (design "Persistence and Recovery",
// role-authorization "Operator-Selected Root Recovery"). RecoverRoot is the
// one-shot operator recovery: atomically verify NO root exists and the
// selected user exists, activate/promote it, record the recovery in
// role_changes, and hand control back to the command layer to exit. Written
// before RecoverRoot exists: compile-error RED.

// seedUsersWithoutRoot builds the ambiguous legacy shape the backfill fails
// closed on: users exist, no root, and the reliable id=1 setup user is
// absent. The migration backfill promoted nothing (empty table at migrate
// time), so the test seeds explicit agent users and deletes id=1.
func seedUsersWithoutRoot(t *testing.T, s *Store) []int64 {
	t.Helper()
	ctx := context.Background()
	var ids []int64
	for _, name := range []string{"Ana", "Beto", "Caro"} {
		u := &domain.User{Name: name, Email: name + "@example.com", PasswordHash: "hash",
			Role: domain.RoleAgent, Active: true, CreatedAt: testClock}
		if err := s.UserStore().Create(ctx, u); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		ids = append(ids, u.ID)
	}
	// Remove id=1: the reliable legacy setup user is gone, so a root cannot
	// be proven automatically and recovery is the only path.
	if err := s.UserStore().Delete(ctx, ids[0]); err != nil {
		t.Fatalf("delete id=1: %v", err)
	}
	return ids[1:]
}

// TestRecoverRootPromotesAndAudits proves the happy path: an operator
// selects an existing user, and RecoverRoot activates and promotes it to
// root while recording the recovery in role_changes (actor NULL — an
// operator action, no session actor — with the operator-selected reason).
func TestRecoverRootPromotesAndAudits(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	remaining := seedUsersWithoutRoot(t, s)

	u, err := s.UserStore().RecoverRoot(ctx, remaining[0])
	if err != nil {
		t.Fatalf("RecoverRoot: %v", err)
	}
	if u.ID != remaining[0] {
		t.Errorf("returned user id = %d, want %d", u.ID, remaining[0])
	}
	if u.Role != domain.RoleRoot {
		t.Errorf("recovered role = %q, want %q", u.Role, domain.RoleRoot)
	}
	if !u.Active {
		t.Error("recovered user must be active")
	}

	got, err := s.UserStore().GetByID(ctx, remaining[0])
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != domain.RoleRoot || !got.Active {
		t.Errorf("persisted recovery = role %q active %v, want root active", got.Role, got.Active)
	}

	// The recovery is audited with the operator as the actor (NULL actor).
	var fromRole, toRole, reason string
	var actor sql.NullString
	if err := s.db.QueryRow(
		`SELECT from_role, to_role, actor_user_id, reason FROM role_changes WHERE user_id = ? ORDER BY id DESC LIMIT 1`,
		remaining[0]).Scan(&fromRole, &toRole, &actor, &reason); err != nil {
		t.Fatalf("read role_changes: %v", err)
	}
	if fromRole != string(domain.RoleAgent) || toRole != string(domain.RoleRoot) {
		t.Errorf("audit roles = %q -> %q, want agent -> root", fromRole, toRole)
	}
	if actor.Valid {
		t.Errorf("actor_user_id = %v, want NULL (operator action)", actor.String)
	}
	if reason != "operator-selected root recovery" {
		t.Errorf("audit reason = %q, want %q", reason, "operator-selected root recovery")
	}

	// A single root exists afterwards.
	var rootCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE role = 'root'`).Scan(&rootCount); err != nil {
		t.Fatal(err)
	}
	if rootCount != 1 {
		t.Errorf("root count = %d, want 1", rootCount)
	}
}

// TestRecoverRootActivatesInactiveUser proves recovery re-activates a
// deactivated user (the operator's selected identity must be usable — design:
// "activates/promotes it").
func TestRecoverRootActivatesInactiveUser(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	remaining := seedUsersWithoutRoot(t, s)

	u, err := s.UserStore().GetByID(ctx, remaining[0])
	if err != nil {
		t.Fatal(err)
	}
	u.Active = false
	if err := s.UserStore().Update(ctx, u); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	recovered, err := s.UserStore().RecoverRoot(ctx, remaining[0])
	if err != nil {
		t.Fatalf("RecoverRoot: %v", err)
	}
	if !recovered.Active {
		t.Error("recovered user must be active")
	}
}

// TestRecoverRootFailsWhenRootExists proves fail-closed: once a root exists
// (fresh bootstrap or an earlier recovery), recovery refuses to run — there
// is nothing to recover and a second root must never appear.
func TestRecoverRootFailsWhenRootExists(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	root := &domain.User{Name: "Root", Email: "root@example.com", PasswordHash: "hash",
		Role: domain.RoleRoot, Active: true, CreatedAt: testClock}
	if err := s.UserStore().Create(ctx, root); err != nil {
		t.Fatalf("seed root: %v", err)
	}

	_, err := s.UserStore().RecoverRoot(ctx, root.ID)
	if err == nil {
		t.Fatal("RecoverRoot with an existing root succeeded, want failure")
	}
	var rootCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE role = 'root'`).Scan(&rootCount); err != nil {
		t.Fatal(err)
	}
	if rootCount != 1 {
		t.Errorf("root count = %d, want 1 (no second root)", rootCount)
	}
}

// TestRecoverRootFailsForUnknownUser proves fail-closed: an operator cannot
// point recovery at a user that does not exist.
func TestRecoverRootFailsForUnknownUser(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	_, err := s.UserStore().RecoverRoot(ctx, 4242)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("RecoverRoot missing user = %v, want ErrNotFound", err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE role = 'root'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("root count = %d, want 0", n)
	}
}
