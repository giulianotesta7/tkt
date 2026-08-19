package sqlite

import (
	"context"
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
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			u := &domain.User{Name: fmt.Sprintf("R%d", i), Email: fmt.Sprintf("r%d@example.com", i),
				PasswordHash: "hash", Active: true, CreatedAt: testClock}
			<-start
			errs[i] = s.UserStore().BootstrapRoot(ctx, u)
		}(i)
	}
	close(start)
	wg.Wait()

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
