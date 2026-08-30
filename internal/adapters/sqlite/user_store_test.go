package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Task 4.4: user store (UNIQUE email → Duplicate, delete guard →
// Referenced, inactive users readable) and session store (create, expired
// lookup → NotFound + lazy purge, logout delete).

func TestUserCreateAssignsIDAndPersists(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	u := &domain.User{Name: "Ana", Email: "ana@example.com", PasswordHash: "hash",
		Active: true, CreatedAt: testClock}
	if err := s.UserStore().Create(ctx, u); err != nil {
		t.Fatalf("create: %v", err)
	}
	if u.ID == 0 {
		t.Error("create did not assign an id")
	}

	got, err := s.UserStore().GetByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Ana" || got.Email != "ana@example.com" || got.PasswordHash != "hash" {
		t.Errorf("user = %+v", got)
	}
	if !got.Active {
		t.Error("new user must be active by default")
	}
	if !got.CreatedAt.Equal(testClock) {
		t.Errorf("created_at = %v, want %v", got.CreatedAt, testClock)
	}
}

// S2 role round-trip RED: the store must persist and scan User.Role so the
// bootstrap/recovery flows can prove what the database holds. Written before
// the role column is wired into Create/Update/scan — the assertions fail
// until the store projects the role (strict TDD RED).

func TestUserRolePersistsAndScans(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	// An explicit role on create survives the round trip (Create → GetByID).
	root := &domain.User{Name: "Root", Email: "root@example.com", PasswordHash: "hash",
		Role: domain.RoleRoot, Active: true, CreatedAt: testClock}
	if err := s.UserStore().Create(ctx, root); err != nil {
		t.Fatalf("create with role: %v", err)
	}
	got, err := s.UserStore().GetByID(ctx, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != domain.RoleRoot {
		t.Errorf("get by id role = %q, want %q", got.Role, domain.RoleRoot)
	}

	// A role change on update persists (Update → GetByID).
	agent := &domain.User{Name: "Agent", Email: "agent@example.com", PasswordHash: "hash",
		Role: domain.RoleAgent, Active: true, CreatedAt: testClock}
	if err := s.UserStore().Create(ctx, agent); err != nil {
		t.Fatal(err)
	}
	agent.Role = domain.RoleAdmin
	if err := s.UserStore().Update(ctx, agent); err != nil {
		t.Fatalf("update role: %v", err)
	}
	got, err = s.UserStore().GetByID(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != domain.RoleAdmin {
		t.Errorf("updated role = %q, want %q", got.Role, domain.RoleAdmin)
	}

	// GetByEmail and List project the role too.
	byEmail, err := s.UserStore().GetByEmail(ctx, "root@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if byEmail.Role != domain.RoleRoot {
		t.Errorf("get by email role = %q, want %q", byEmail.Role, domain.RoleRoot)
	}
	all, err := s.UserStore().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	roles := map[int64]domain.Role{}
	for _, u := range all {
		roles[u.ID] = u.Role
	}
	if roles[root.ID] != domain.RoleRoot || roles[agent.ID] != domain.RoleAdmin {
		t.Errorf("list roles = %v, want root=%q admin=%q", roles, domain.RoleRoot, domain.RoleAdmin)
	}
}

// TestUserCreateWithoutRoleFallsBackToAgent pins the legacy contract:
// callers that do not set a role (the pre-S2 user-management create flow)
// still get the migration default 'agent' — never a guessed role and never
// root (root is only ever created by BootstrapRoot/recovery).
func TestUserCreateWithoutRoleFallsBackToAgent(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	u := &domain.User{Name: "Legacy", Email: "legacy@example.com", PasswordHash: "hash",
		Active: true, CreatedAt: testClock}
	if err := s.UserStore().Create(ctx, u); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.UserStore().GetByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != domain.RoleAgent {
		t.Errorf("default role = %q, want %q (migration default)", got.Role, domain.RoleAgent)
	}
}

// TestBootstrapRootCreatesRootAtomically proves the atomic bootstrap
// contract (role-authorization "First-User Root Bootstrap"): on an empty
// table the first BootstrapRoot creates an active root; any later call —
// even with a different identity — fails with ErrBootstrapUnavailable and
// creates nothing. Written before BootstrapRoot exists: compile-error RED.
func TestBootstrapRootCreatesRootAtomically(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	root := &domain.User{Name: "Root", Email: "root@example.com", PasswordHash: "hash",
		Active: true, CreatedAt: testClock}
	if err := s.UserStore().BootstrapRoot(ctx, root); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if root.ID == 0 {
		t.Error("bootstrap did not assign an id")
	}

	got, err := s.UserStore().GetByID(ctx, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != domain.RoleRoot {
		t.Errorf("bootstrap role = %q, want %q", got.Role, domain.RoleRoot)
	}
	if !got.Active {
		t.Error("bootstrap user must be active")
	}

	// Bootstrap is unavailable once a user exists — the concurrent loser
	// and any later visitor both fail without creating an account.
	other := &domain.User{Name: "Other", Email: "other@example.com", PasswordHash: "hash",
		Active: true, CreatedAt: testClock}
	err = s.UserStore().BootstrapRoot(ctx, other)
	if !errors.Is(err, domain.ErrBootstrapUnavailable) {
		t.Fatalf("second bootstrap err = %v, want ErrBootstrapUnavailable", err)
	}
	var be *domain.BootstrapUnavailableError
	if !errors.As(err, &be) {
		t.Errorf("err = %v, want *BootstrapUnavailableError", err)
	}
	count, err := s.UserStore().Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("user count = %d, want 1 (loser created nothing)", count)
	}
}

// TestBootstrapRootConcurrentYieldsOneRoot races two bootstrap writers on a
// two-connection pool. Only a BEGIN IMMEDIATE transaction with the count
// check INSIDE it yields exactly one root: a naive check-then-insert lets
// both writers observe an empty table and create two users. The shared-cache
// memory database plus two live connections exercises the real SQLite write
// serialization (the DSN carries _txlock=immediate).
func TestBootstrapRootConcurrentYieldsOneRoot(t *testing.T) {
	s, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.db.Close() })
	s.db.SetMaxOpenConns(2)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			u := &domain.User{Name: fmt.Sprintf("R%d", i), Email: fmt.Sprintf("r%d@example.com", i),
				PasswordHash: "hash", Active: true, CreatedAt: testClock}
			<-start
			results <- s.UserStore().BootstrapRoot(ctx, u)
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	errs := make([]error, 0, 2)
	for err := range results {
		errs = append(errs, err)
	}

	succeeds := 0
	for i, err := range errs {
		switch {
		case err == nil:
			succeeds++
		case errors.Is(err, domain.ErrBootstrapUnavailable):
			// expected loser
		default:
			t.Fatalf("writer %d err = %v", i, err)
		}
	}
	if succeeds != 1 {
		t.Fatalf("successful bootstraps = %d, want exactly 1", succeeds)
	}
	count, err := s.UserStore().Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("user count = %d, want exactly 1", count)
	}
}

func TestUserCreateDuplicateEmail(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	if err := s.UserStore().Create(ctx, &domain.User{Name: "Ana", Email: "ana@example.com",
		PasswordHash: "h", Active: true, CreatedAt: testClock}); err != nil {
		t.Fatal(err)
	}
	err := s.UserStore().Create(ctx, &domain.User{Name: "Other", Email: "ana@example.com",
		PasswordHash: "h", Active: true, CreatedAt: testClock})
	if !errors.Is(err, domain.ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
	var de *domain.DuplicateError
	if !errors.As(err, &de) || de.Kind != "user" {
		t.Errorf("err = %v, want DuplicateError{Kind: user}", err)
	}
}

func TestUserUpdatePersistsFields(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	u := &domain.User{Name: "Ana", Email: "ana@example.com", PasswordHash: "old",
		Active: true, CreatedAt: testClock}
	if err := s.UserStore().Create(ctx, u); err != nil {
		t.Fatal(err)
	}

	u.Name = "Ana Maria"
	u.Email = "anamaria@example.com"
	u.PasswordHash = "new-hash"
	u.Active = false
	if err := s.UserStore().Update(ctx, u); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := s.UserStore().GetByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Ana Maria" || got.Email != "anamaria@example.com" || got.PasswordHash != "new-hash" {
		t.Errorf("user = %+v", got)
	}
	if got.Active {
		t.Error("deactivation (active=false) not persisted")
	}
}

// S3.1 RED: the store commits the guarded combined edit and role audit in a
// single immediate transaction, while password changes update only the hash.
func TestUserStoreUpdateManagedUserAndPasswordHash(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	u := &domain.User{Name: "Ana", Email: "ana@example.com", PasswordHash: "old-hash", Role: domain.RoleUser, Active: true, CreatedAt: testClock}
	if err := s.UserStore().Create(ctx, u); err != nil {
		t.Fatal(err)
	}

	actor := &domain.User{Name: "Actor", Email: "actor@example.com", PasswordHash: "hash", Role: domain.RoleAdmin, Active: true, CreatedAt: testClock}
	if err := s.UserStore().Create(ctx, actor); err != nil {
		t.Fatal(err)
	}
	u.Name = "Ana Torres"
	u.Email = "ana.torres@example.com"
	u.Role = domain.RoleAgent
	u.Active = false
	if err := s.UserStore().UpdateManagedUser(ctx, u, domain.RoleUser, actor.ID, testClock); err != nil {
		t.Fatalf("UpdateManagedUser: %v", err)
	}
	got, err := s.UserStore().GetByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != u.Name || got.Email != u.Email || got.Role != domain.RoleAgent || got.Active {
		t.Fatalf("combined update = %+v", got)
	}
	var audits int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM role_changes WHERE user_id = ? AND actor_user_id = ?`, u.ID, actor.ID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("role audits = %d, want 1", audits)
	}
	if err := s.UserStore().UpdatePasswordHash(ctx, u.ID, "new-hash"); err != nil {
		t.Fatalf("UpdatePasswordHash: %v", err)
	}
	got, err = s.UserStore().GetByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PasswordHash != "new-hash" || got.Name != u.Name || got.Email != u.Email || got.Role != u.Role || got.Active != u.Active {
		t.Fatalf("password-only update = %+v", got)
	}
}

func TestUserUpdateDuplicateEmail(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	ana := &domain.User{Name: "Ana", Email: "ana@example.com", PasswordHash: "h", Active: true, CreatedAt: testClock}
	beto := &domain.User{Name: "Beto", Email: "beto@example.com", PasswordHash: "h", Active: true, CreatedAt: testClock}
	if err := s.UserStore().Create(ctx, ana); err != nil {
		t.Fatal(err)
	}
	if err := s.UserStore().Create(ctx, beto); err != nil {
		t.Fatal(err)
	}

	// Renaming Beto to Ana's email must be rejected (spec: reject update
	// to duplicate email) — and Ana's row must keep its own email.
	beto.Email = "ana@example.com"
	err := s.UserStore().Update(ctx, beto)
	if !errors.Is(err, domain.ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
	got, err := s.UserStore().GetByID(ctx, ana.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Email != "ana@example.com" {
		t.Errorf("ana email = %q, want unchanged", got.Email)
	}
}

func TestUserUpdateNotFound(t *testing.T) {
	s := newTestDB(t)
	err := s.UserStore().Update(context.Background(), &domain.User{ID: 42})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestUserDeleteUnreferenced(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	u := &domain.User{Name: "Ana", Email: "ana@example.com", PasswordHash: "h", Active: true, CreatedAt: testClock}
	if err := s.UserStore().Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := s.UserStore().Delete(ctx, u.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.UserStore().GetByID(ctx, u.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("get after delete = %v, want ErrNotFound", err)
	}
}

func TestUserDeleteReferencedByTicket(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	cat := seedCategory(t, s, "Bugs")
	userID := seedUser(t, s, "Ana", "ana@example.com", true)
	seedTicket(t, s, domain.Ticket{Number: 1, Title: "assigned", CategoryID: cat,
		UserID: ptr(userID), Priority: domain.PriorityMedium, State: domain.StateNew,
		CreatedAt: testClock, UpdatedAt: testClock})

	err := s.UserStore().Delete(ctx, userID)
	if !errors.Is(err, domain.ErrReferenced) {
		t.Fatalf("err = %v, want ErrReferenced", err)
	}
	var re *domain.ReferencedError
	if !errors.As(err, &re) || re.Kind != "user" {
		t.Errorf("err = %v, want ReferencedError{Kind: user}", err)
	}
	// The referenced user must survive the blocked delete (deactivation is
	// the only removal path; historical assignment preserved).
	if _, err := s.UserStore().GetByID(ctx, userID); err != nil {
		t.Errorf("user deleted despite reference: %v", err)
	}
}

func TestUserDeleteRemovesSessions(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	u := &domain.User{Name: "Ana", Email: "ana@example.com", PasswordHash: "h", Active: true, CreatedAt: testClock}
	if err := s.UserStore().Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	sess := &domain.Session{ID: "tok", UserID: u.ID, ExpiresAt: time.Now().Add(24 * time.Hour)}
	if err := s.SessionStore().Create(ctx, sess); err != nil {
		t.Fatal(err)
	}

	// A user with sessions but no tickets is deletable; the sessions must
	// go with them (the sessions FK would otherwise block the delete).
	if err := s.UserStore().Delete(ctx, u.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("sessions left behind after user delete: %d", n)
	}
}

func TestUserDeleteNotFound(t *testing.T) {
	s := newTestDB(t)
	err := s.UserStore().Delete(context.Background(), 42)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestUserGetByIDIncludesInactive(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	u := &domain.User{Name: "Ana", Email: "ana@example.com", PasswordHash: "h", Active: false, CreatedAt: testClock}
	if err := s.UserStore().Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	got, err := s.UserStore().GetByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Active {
		t.Error("inactive user must be readable (historical display)")
	}
}

func TestUserGetByEmail(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	u := &domain.User{Name: "Ana", Email: "ana@example.com", PasswordHash: "h", Active: true, CreatedAt: testClock}
	if err := s.UserStore().Create(ctx, u); err != nil {
		t.Fatal(err)
	}

	got, err := s.UserStore().GetByEmail(ctx, "ana@example.com")
	if err != nil {
		t.Fatalf("get by email: %v", err)
	}
	if got.ID != u.ID {
		t.Errorf("id = %d, want %d", got.ID, u.ID)
	}

	_, err = s.UserStore().GetByEmail(ctx, "nobody@example.com")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown email = %v, want ErrNotFound", err)
	}
}

func TestUserCount(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	n, err := s.UserStore().Count(ctx)
	if err != nil || n != 0 {
		t.Fatalf("empty count = %d, err = %v", n, err)
	}
	for _, email := range []string{"a@example.com", "b@example.com"} {
		if err := s.UserStore().Create(ctx, &domain.User{Name: email, Email: email,
			PasswordHash: "h", Active: true, CreatedAt: testClock}); err != nil {
			t.Fatal(err)
		}
	}
	n, err = s.UserStore().Count(ctx)
	if err != nil || n != 2 {
		t.Errorf("count = %d, err = %v; want 2", n, err)
	}
}

func TestUserListAndListActive(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	for _, u := range []domain.User{
		{Name: "Ana", Email: "ana@example.com", PasswordHash: "h", Active: true, CreatedAt: testClock},
		{Name: "Bob", Email: "bob@example.com", PasswordHash: "h", Active: false, CreatedAt: testClock},
		{Name: "Cid", Email: "cid@example.com", PasswordHash: "h", Active: true, CreatedAt: testClock},
	} {
		u := u
		if err := s.UserStore().Create(ctx, &u); err != nil {
			t.Fatal(err)
		}
	}

	all, err := s.UserStore().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Errorf("list len = %d, want 3", len(all))
	}

	active, err := s.UserStore().ListActive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 2 {
		t.Errorf("list active len = %d, want 2", len(active))
	}
	for _, u := range active {
		if !u.Active {
			t.Errorf("inactive user %s in ListActive", u.Email)
		}
	}
}

func TestUserReadsRejectMalformedCreatedAt(t *testing.T) {
	s := newTestDB(t)
	if _, err := s.db.Exec(`INSERT INTO users (name, email, password_hash, active, created_at) VALUES (?, ?, ?, ?, ?)`,
		"Broken", "broken@example.com", "hash", true, "not-a-time"); err != nil {
		t.Fatalf("seed malformed user: %v", err)
	}

	if _, err := s.UserStore().GetByEmail(context.Background(), "broken@example.com"); err == nil || !strings.Contains(err.Error(), `parse user created_at "not-a-time"`) {
		t.Fatalf("GetByEmail malformed timestamp error = %v", err)
	}
	if _, err := s.UserStore().List(context.Background()); err == nil || !strings.Contains(err.Error(), `parse user created_at "not-a-time"`) {
		t.Fatalf("List malformed timestamp error = %v", err)
	}
}

func TestUserStorePropagatesDatabaseErrors(t *testing.T) {
	s := newTestDB(t)
	if err := s.db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	tests := []struct {
		name string
		call func() error
	}{
		{name: "create", call: func() error { return s.UserStore().Create(context.Background(), &domain.User{}) }},
		{name: "update", call: func() error { return s.UserStore().Update(context.Background(), &domain.User{ID: 1}) }},
		{name: "delete", call: func() error { return s.UserStore().Delete(context.Background(), 1) }},
		{name: "get by id", call: func() error { _, err := s.UserStore().GetByID(context.Background(), 1); return err }},
		{name: "get by email", call: func() error { _, err := s.UserStore().GetByEmail(context.Background(), "a@example.com"); return err }},
		{name: "count", call: func() error { _, err := s.UserStore().Count(context.Background()); return err }},
		{name: "list", call: func() error { _, err := s.UserStore().List(context.Background()); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); err == nil {
				t.Fatal("operation on closed database succeeded")
			}
		})
	}
}

// ---- Issue #47: atomic agent-to-user downgrade with ticket handoff ----

// downgradeAuditRow projects one audit_events row for handoff assertions.
type downgradeAuditRow struct {
	actor     string
	actorID   sql.NullInt64
	field     sql.NullString
	fromValue sql.NullString
	toValue   sql.NullString
	reason    sql.NullString
	deskID    sql.NullInt64
	stepIndex sql.NullInt64
}

func downgradeAudits(t *testing.T, s *Store, ticketID int64) []downgradeAuditRow {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(), `SELECT actor, actor_user_id, field, from_value, to_value, reason, desk_id, step_index FROM audit_events WHERE ticket_id = ? ORDER BY id ASC`, ticketID)
	if err != nil {
		t.Fatalf("query audits: %v", err)
	}
	defer rows.Close()
	var out []downgradeAuditRow
	for rows.Next() {
		var r downgradeAuditRow
		if err := rows.Scan(&r.actor, &r.actorID, &r.field, &r.fromValue, &r.toValue, &r.reason, &r.deskID, &r.stepIndex); err != nil {
			t.Fatalf("scan audit: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("audits rows: %v", err)
	}
	return out
}

func downgradeTicketAssignee(t *testing.T, s *Store, ticketID int64) sql.NullInt64 {
	t.Helper()
	var uid sql.NullInt64
	if err := s.db.QueryRowContext(context.Background(), `SELECT user_id FROM tickets WHERE id = ?`, ticketID).Scan(&uid); err != nil {
		t.Fatalf("ticket assignee: %v", err)
	}
	return uid
}

func downgradeRoleChanges(t *testing.T, s *Store, userID int64) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM role_changes WHERE user_id = ?`, userID).Scan(&n); err != nil {
		t.Fatalf("role changes count: %v", err)
	}
	return n
}

func downgradeMembershipCount(t *testing.T, s *Store, userID int64) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM desk_members WHERE user_id = ?`, userID).Scan(&n); err != nil {
		t.Fatalf("membership count: %v", err)
	}
	return n
}

// seedAssignmentAudit seeds a manual-assignment-shaped audit row carrying
// the desk snapshot: the context the handoff desk resolution reads first.
// actorID must be an existing user id (FK).
func seedAssignmentAudit(t *testing.T, s *Store, ticketID, deskID, actorID int64) {
	t.Helper()
	field, from, to := "user", "", fmt.Sprintf("%d", ticketID%1000)
	if _, err := s.db.ExecContext(context.Background(), `INSERT INTO audit_events (ticket_id, actor, action, field, from_value, to_value, actor_user_id, created_at, desk_id)
		VALUES (?, 'Admin', 'update', ?, ?, ?, ?, '2026-08-06T10:00:00Z', ?)`, ticketID, field, from, to, actorID, deskID); err != nil {
		t.Fatalf("seed assignment audit: %v", err)
	}
}

// seedPinnedWorkflowVersion inserts a one-step least_loaded workflow version
// for catID pinned to deskID and returns the version id.
func seedPinnedWorkflowVersion(t *testing.T, s *Store, catID, deskID int64) int64 {
	t.Helper()
	steps := fmt.Sprintf(`[{"type":"assign_to_desk","assign_to_desk":{"desk_id":%d,"strategy":"least_loaded"}}]`, deskID)
	res, err := s.db.ExecContext(context.Background(), `INSERT INTO workflow_versions (category_id, version_no, steps_json, published_at) VALUES (?, 1, ?, '2026-08-06T10:00:00Z')`, catID, steps)
	if err != nil {
		t.Fatalf("seed workflow version: %v", err)
	}
	vid, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("workflow version id: %v", err)
	}
	return vid
}

// TestUserStoreDowngradeToUserAtomicHandoff proves the full atomic lifecycle:
// memberships removed, the open ticket handed to the least-loaded eligible
// member (never the downgraded account itself — proven by a load tie the
// downgraded account would win on the id tie-break), one handoff audit event
// with the initiating actor and NULL step index, and the role_changes row.
func TestUserStoreDowngradeToUserAtomicHandoff(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	cat := seedCategory(t, s, "Support")
	desk := seedDesk(t, s, "D1")
	// Seed the actor first so the downgraded agent gets the LOWER id: with
	// equal open-ticket load the id tie-break would pick the downgraded
	// account as its own replacement if the membership pool were not
	// re-read after deletion.
	actorID := seedUserRole(t, s, "Root", "root@x", true, domain.RoleAdmin)
	agentA := seedUserRole(t, s, "Agent A", "a@x", true, domain.RoleAgent)
	agentB := seedUserRole(t, s, "Agent B", "b@x", true, domain.RoleAgent)
	addMemberRaw(t, s, desk, agentA)
	addMemberRaw(t, s, desk, agentB)
	ticket := seedTicket(t, s, domain.Ticket{Number: 1, Title: "T1", CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateInProgress, UserID: &agentA, CreatedAt: testClock, UpdatedAt: testClock})
	seedAssignmentAudit(t, s, ticket.ID, desk, actorID)
	// Extra open ticket on B makes the load a 1-1 tie.
	seedLoadTicket(t, s, 2, agentB, cat, domain.StateNew)

	downgraded := &domain.User{ID: agentA, Name: "Agent A", Email: "a@x", Role: domain.RoleUser, Active: true}
	got, err := s.UserStore().DowngradeToUser(ctx, downgraded, domain.RoleAgent, actorID, testClock)
	if err != nil {
		t.Fatalf("downgrade: %v", err)
	}
	if got.ID != agentA || got.Role != domain.RoleUser {
		t.Errorf("returned user = %+v, want id %d role user", got, agentA)
	}

	// Role flipped, memberships gone.
	var role string
	if err := s.db.QueryRowContext(context.Background(), `SELECT role FROM users WHERE id = ?`, agentA).Scan(&role); err != nil || role != string(domain.RoleUser) {
		t.Errorf("stored role = %q (err %v), want user", role, err)
	}
	if n := downgradeMembershipCount(t, s, agentA); n != 0 {
		t.Errorf("memberships left = %d, want 0", n)
	}
	if n := downgradeRoleChanges(t, s, agentA); n != 1 {
		t.Errorf("role_changes rows = %d, want 1", n)
	}

	// Ticket handed to B (never to A itself).
	if got := downgradeTicketAssignee(t, s, ticket.ID); !got.Valid || got.Int64 != agentB {
		t.Errorf("ticket assignee = %v, want agent B (%d)", got, agentB)
	}

	// Exactly one handoff audit event with the contract fields.
	audits := downgradeAudits(t, s, ticket.ID)
	if len(audits) != 2 { // seeded assignment + handoff
		t.Fatalf("audit events = %d, want 2", len(audits))
	}
	h := audits[1]
	if h.field.Valid && h.field.String != "user" {
		t.Errorf("event field = %v, want user", h.field)
	}
	if !h.fromValue.Valid || h.fromValue.String != fmt.Sprintf("%d", agentA) {
		t.Errorf("event from = %v, want %d", h.fromValue, agentA)
	}
	if !h.toValue.Valid || h.toValue.String != fmt.Sprintf("%d", agentB) {
		t.Errorf("event to = %v, want %d", h.toValue, agentB)
	}
	if h.actor != "Root" || !h.actorID.Valid || h.actorID.Int64 != actorID {
		t.Errorf("event actor = %q/%v, want Root/%d", h.actor, h.actorID, actorID)
	}
	if !h.reason.Valid || h.reason.String == "" {
		t.Errorf("event reason missing, got %v", h.reason)
	}
	if !h.deskID.Valid || h.deskID.Int64 != desk {
		t.Errorf("event desk_id = %v, want %d", h.deskID, desk)
	}
	if h.stepIndex.Valid {
		t.Errorf("event step_index = %v, want NULL", h.stepIndex)
	}
}

// TestUserStoreDowngradeToUserUnresolvableDeskUnassigns proves the fallback:
// an open ticket with no audit desk context and no pinned workflow becomes
// unassigned with a NULL desk_id on its handoff event.
func TestUserStoreDowngradeToUserUnresolvableDeskUnassigns(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	cat := seedCategory(t, s, "General")
	actorID := seedUserRole(t, s, "Root", "root@x", true, domain.RoleAdmin)
	agentA := seedUserRole(t, s, "Agent A", "a@x", true, domain.RoleAgent)
	ticket := seedTicket(t, s, domain.Ticket{Number: 1, Title: "T1", CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, UserID: &agentA, CreatedAt: testClock, UpdatedAt: testClock})

	if _, err := s.UserStore().DowngradeToUser(ctx, &domain.User{ID: agentA, Name: "Agent A", Email: "a@x", Role: domain.RoleUser, Active: true}, domain.RoleAgent, actorID, testClock); err != nil {
		t.Fatalf("downgrade: %v", err)
	}
	if got := downgradeTicketAssignee(t, s, ticket.ID); got.Valid {
		t.Errorf("ticket assignee = %v, want NULL (unassigned)", got)
	}
	audits := downgradeAudits(t, s, ticket.ID)
	if len(audits) != 1 {
		t.Fatalf("audit events = %d, want 1", len(audits))
	}
	h := audits[0]
	if !h.toValue.Valid || h.toValue.String != "" {
		t.Errorf("event to = %v, want empty", h.toValue)
	}
	if h.deskID.Valid {
		t.Errorf("event desk_id = %v, want NULL", h.deskID)
	}
	if h.stepIndex.Valid {
		t.Errorf("event step_index = %v, want NULL", h.stepIndex)
	}
}

// TestUserStoreDowngradeToUserEmptyPoolUnassigns proves an empty eligible
// pool (the downgraded account was the only member) leaves the ticket
// unassigned while the resolved desk still rides on the event.
func TestUserStoreDowngradeToUserEmptyPoolUnassigns(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	cat := seedCategory(t, s, "Support")
	desk := seedDesk(t, s, "D1")
	actorID := seedUserRole(t, s, "Root", "root@x", true, domain.RoleAdmin)
	agentA := seedUserRole(t, s, "Agent A", "a@x", true, domain.RoleAgent)
	addMemberRaw(t, s, desk, agentA)
	ticket := seedTicket(t, s, domain.Ticket{Number: 1, Title: "T1", CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateInProgress, UserID: &agentA, CreatedAt: testClock, UpdatedAt: testClock})
	seedAssignmentAudit(t, s, ticket.ID, desk, actorID)

	if _, err := s.UserStore().DowngradeToUser(ctx, &domain.User{ID: agentA, Name: "Agent A", Email: "a@x", Role: domain.RoleUser, Active: true}, domain.RoleAgent, actorID, testClock); err != nil {
		t.Fatalf("downgrade: %v", err)
	}
	if got := downgradeTicketAssignee(t, s, ticket.ID); got.Valid {
		t.Errorf("ticket assignee = %v, want NULL", got)
	}
	audits := downgradeAudits(t, s, ticket.ID)
	if len(audits) != 2 {
		t.Fatalf("audit events = %d, want 2", len(audits))
	}
	if !audits[1].deskID.Valid || audits[1].deskID.Int64 != desk {
		t.Errorf("event desk_id = %v, want %d (desk resolved, pool empty)", audits[1].deskID, desk)
	}
}

// TestUserStoreDowngradeToUserClosedTicketsUntouched proves closed-state
// tickets keep their historical assignment and get no handoff event.
func TestUserStoreDowngradeToUserClosedTicketsUntouched(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	cat := seedCategory(t, s, "Support")
	desk := seedDesk(t, s, "D1")
	actorID := seedUserRole(t, s, "Root", "root@x", true, domain.RoleAdmin)
	agentA := seedUserRole(t, s, "Agent A", "a@x", true, domain.RoleAgent)
	agentB := seedUserRole(t, s, "Agent B", "b@x", true, domain.RoleAgent)
	addMemberRaw(t, s, desk, agentA)
	addMemberRaw(t, s, desk, agentB)
	var closedIDs []int64
	for i, st := range []domain.State{domain.StateResolved, domain.StateClosed, domain.StateCancelled} {
		tk := seedTicket(t, s, domain.Ticket{Number: i + 1, Title: "T", CategoryID: cat, Priority: domain.PriorityMedium, State: st, UserID: &agentA, CreatedAt: testClock, UpdatedAt: testClock})
		seedAssignmentAudit(t, s, tk.ID, desk, actorID)
		closedIDs = append(closedIDs, tk.ID)
	}

	if _, err := s.UserStore().DowngradeToUser(ctx, &domain.User{ID: agentA, Name: "Agent A", Email: "a@x", Role: domain.RoleUser, Active: true}, domain.RoleAgent, actorID, testClock); err != nil {
		t.Fatalf("downgrade: %v", err)
	}
	for _, id := range closedIDs {
		if got := downgradeTicketAssignee(t, s, id); !got.Valid || got.Int64 != agentA {
			t.Errorf("closed ticket %d assignee = %v, want agent A (%d) preserved", id, got, agentA)
		}
		if audits := downgradeAudits(t, s, id); len(audits) != 1 {
			t.Errorf("closed ticket %d audits = %d, want 1 (no handoff event)", id, len(audits))
		}
	}
	_ = agentB
}

// TestUserStoreDowngradeToUserTieBreakLowestID proves the deterministic tie:
// two eligible candidates with equal open load resolve to the lowest user id.
func TestUserStoreDowngradeToUserTieBreakLowestID(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	cat := seedCategory(t, s, "Support")
	desk := seedDesk(t, s, "D1")
	actorID := seedUserRole(t, s, "Root", "root@x", true, domain.RoleAdmin)
	agentA := seedUserRole(t, s, "Agent A", "a@x", true, domain.RoleAgent)
	agentB := seedUserRole(t, s, "Agent B", "b@x", true, domain.RoleAgent)
	agentC := seedUserRole(t, s, "Agent C", "c@x", true, domain.RoleAgent)
	addMemberRaw(t, s, desk, agentA)
	addMemberRaw(t, s, desk, agentB)
	addMemberRaw(t, s, desk, agentC)
	ticket := seedTicket(t, s, domain.Ticket{Number: 1, Title: "T1", CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, UserID: &agentA, CreatedAt: testClock, UpdatedAt: testClock})
	seedAssignmentAudit(t, s, ticket.ID, desk, actorID)

	if _, err := s.UserStore().DowngradeToUser(ctx, &domain.User{ID: agentA, Name: "Agent A", Email: "a@x", Role: domain.RoleUser, Active: true}, domain.RoleAgent, actorID, testClock); err != nil {
		t.Fatalf("downgrade: %v", err)
	}
	want := agentB
	if agentC < agentB {
		want = agentC
	}
	if got := downgradeTicketAssignee(t, s, ticket.ID); !got.Valid || got.Int64 != want {
		t.Errorf("ticket assignee = %v, want lowest id among B/C (%d)", got, want)
	}
}

// TestUserStoreDowngradeToUserExpectedRoleMismatchRollsBack proves all-or-
// nothing: a guarded-update miss rolls memberships, tickets, audits, and the
// role_changes row back together.
func TestUserStoreDowngradeToUserExpectedRoleMismatchRollsBack(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	cat := seedCategory(t, s, "Support")
	desk := seedDesk(t, s, "D1")
	actorID := seedUserRole(t, s, "Root", "root@x", true, domain.RoleAdmin)
	agentA := seedUserRole(t, s, "Agent A", "a@x", true, domain.RoleAgent)
	addMemberRaw(t, s, desk, agentA)
	ticket := seedTicket(t, s, domain.Ticket{Number: 1, Title: "T1", CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateInProgress, UserID: &agentA, CreatedAt: testClock, UpdatedAt: testClock})
	seedAssignmentAudit(t, s, ticket.ID, desk, actorID)

	_, err := s.UserStore().DowngradeToUser(ctx, &domain.User{ID: agentA, Name: "Agent A", Email: "a@x", Role: domain.RoleUser, Active: true}, domain.RoleAdmin, actorID, testClock)
	if err == nil {
		t.Fatal("expected role mismatch must fail")
	}
	var nf *domain.NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("error = %v, want NotFoundError", err)
	}
	var role string
	if err := s.db.QueryRowContext(context.Background(), `SELECT role FROM users WHERE id = ?`, agentA).Scan(&role); err != nil || role != string(domain.RoleAgent) {
		t.Errorf("stored role = %q (err %v), want agent (rolled back)", role, err)
	}
	if n := downgradeMembershipCount(t, s, agentA); n != 1 {
		t.Errorf("memberships = %d, want 1 (rolled back)", n)
	}
	if got := downgradeTicketAssignee(t, s, ticket.ID); !got.Valid || got.Int64 != agentA {
		t.Errorf("ticket assignee = %v, want agent A (rolled back)", got)
	}
	if audits := downgradeAudits(t, s, ticket.ID); len(audits) != 1 {
		t.Errorf("audits = %d, want 1 (no handoff event persisted)", len(audits))
	}
	if n := downgradeRoleChanges(t, s, agentA); n != 0 {
		t.Errorf("role_changes rows = %d, want 0", n)
	}
}

// TestUserStoreDowngradeToUserWithoutMembershipsPlain proves an account with
// no desk memberships downgrades as a plain managed update: role flipped,
// role_changes row, no ticket or audit side effects.
func TestUserStoreDowngradeToUserWithoutMembershipsPlain(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	actorID := seedUserRole(t, s, "Root", "root@x", true, domain.RoleAdmin)
	agentA := seedUserRole(t, s, "Agent A", "a@x", true, domain.RoleAgent)

	if _, err := s.UserStore().DowngradeToUser(ctx, &domain.User{ID: agentA, Name: "Agent A", Email: "a@x", Role: domain.RoleUser, Active: true}, domain.RoleAgent, actorID, testClock); err != nil {
		t.Fatalf("downgrade: %v", err)
	}
	var role string
	if err := s.db.QueryRowContext(context.Background(), `SELECT role FROM users WHERE id = ?`, agentA).Scan(&role); err != nil || role != string(domain.RoleUser) {
		t.Errorf("stored role = %q (err %v), want user", role, err)
	}
	if n := downgradeRoleChanges(t, s, agentA); n != 1 {
		t.Errorf("role_changes rows = %d, want 1", n)
	}
	var events int
	if err := s.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM audit_events`).Scan(&events); err != nil || events != 0 {
		t.Errorf("audit events = %d (err %v), want 0", events, err)
	}
}

// TestUserStoreDowngradeToUserPinnedWorkflowDeskFallback proves desk
// resolution priority 2: a pinned ticket with no audit desk context resolves
// its desk from the first assign_to_desk step of its pinned workflow version.
func TestUserStoreDowngradeToUserPinnedWorkflowDeskFallback(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	cat := seedCategory(t, s, "Support")
	desk := seedDesk(t, s, "D1")
	actorID := seedUserRole(t, s, "Root", "root@x", true, domain.RoleAdmin)
	agentA := seedUserRole(t, s, "Agent A", "a@x", true, domain.RoleAgent)
	agentB := seedUserRole(t, s, "Agent B", "b@x", true, domain.RoleAgent)
	addMemberRaw(t, s, desk, agentA)
	addMemberRaw(t, s, desk, agentB)
	vid := seedPinnedWorkflowVersion(t, s, cat, desk)
	ticket := seedPinnedTicket(t, s, domain.Ticket{Number: 1, Title: "T1", CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, UserID: &agentA, CreatedAt: testClock, UpdatedAt: testClock, WorkflowVersionID: &vid})

	if _, err := s.UserStore().DowngradeToUser(ctx, &domain.User{ID: agentA, Name: "Agent A", Email: "a@x", Role: domain.RoleUser, Active: true}, domain.RoleAgent, actorID, testClock); err != nil {
		t.Fatalf("downgrade: %v", err)
	}
	if got := downgradeTicketAssignee(t, s, ticket.ID); !got.Valid || got.Int64 != agentB {
		t.Errorf("ticket assignee = %v, want agent B (%d) via pinned desk", got, agentB)
	}
	audits := downgradeAudits(t, s, ticket.ID)
	if len(audits) != 1 {
		t.Fatalf("audit events = %d, want 1", len(audits))
	}
	if !audits[0].deskID.Valid || audits[0].deskID.Int64 != desk {
		t.Errorf("event desk_id = %v, want %d", audits[0].deskID, desk)
	}
}

// TestUserStoreDowngradeToUserNoOpRoleChangeSkipsRoleChangesRow proves the
// role_changes insert follows the generic managed-update convention: only an
// actual role change appends a row. An edit of an already-user account must
// not record a spurious user-to-user change.
func TestUserStoreDowngradeToUserNoOpRoleChangeSkipsRoleChangesRow(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	actorID := seedUserRole(t, s, "Root", "root@x", true, domain.RoleAdmin)
	target := seedUserRole(t, s, "Terry", "t@x", true, domain.RoleUser)

	if _, err := s.UserStore().DowngradeToUser(ctx, &domain.User{ID: target, Name: "Terry", Email: "t@x", Role: domain.RoleUser, Active: true}, domain.RoleUser, actorID, testClock); err != nil {
		t.Fatalf("no-op downgrade: %v", err)
	}
	if n := downgradeRoleChanges(t, s, target); n != 0 {
		t.Errorf("role_changes rows = %d, want 0 (no actual role change)", n)
	}
}
