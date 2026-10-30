package sqlite

import (
	"context"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Migration 0037 tests (service users and API keys — issue #488): a service
// user is a users row of kind 'service' that holds the user or agent role only,
// keeps its kind for life, and is refused by every people path the database
// owns (sessions, company identities, recovery links, desk membership). API keys
// belong to service users only, keep their identity, and the administrative
// history accepts only its closed action vocabulary.

// seedServiceUserRaw inserts one service user with the given role.
func seedServiceUserRaw(t *testing.T, s *Store, name, email, role string) int64 {
	t.Helper()
	res, err := s.db.ExecContext(context.Background(),
		`INSERT INTO users (name, email, password_hash, active, role, kind, created_at)
		 VALUES (?, ?, '', 1, ?, 'service', '2026-10-29T10:00:00Z')`, name, email, role)
	if err != nil {
		t.Fatalf("seed service user %s: %v", email, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed service user %s: last insert id: %v", email, err)
	}
	return id
}

// assertRefused runs one statement that a 0037 guard must abort with want.
func assertRefused(t *testing.T, s *Store, name, want, sql string, args ...any) {
	t.Helper()
	_, err := s.db.Exec(sql, args...)
	if err == nil {
		t.Fatalf("%s: succeeded, want it refused", name)
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("%s: error = %v, want it to mention %q", name, err, want)
	}
}

func insertAPIKeyRaw(s *Store, publicID string, userID, creatorID int64) error {
	_, err := s.db.Exec(`INSERT INTO api_keys (public_id, user_id, name, secret_hash, scopes, created_by_user_id, created_at)
		VALUES (?, ?, 'Monitoring', ?, 'tickets:read', ?, '2026-10-29T10:00:00Z')`,
		publicID, userID, strings.Repeat("a", 64), creatorID)
	return err
}

func TestMigration0037ServiceUserKind(t *testing.T) {
	s := newTestDB(t)
	personID := seedUserRaw(t, s, "Ana", "ana@example.com", "agent")

	var kind string
	if err := s.db.QueryRow(`SELECT kind FROM users WHERE id = ?`, personID).Scan(&kind); err != nil {
		t.Fatal(err)
	}
	if kind != "person" {
		t.Errorf("default kind = %q, want person", kind)
	}

	userSvc := seedServiceUserRaw(t, s, "Monitoring", "monitoring@service.invalid", "user")
	agentSvc := seedServiceUserRaw(t, s, "Triage bot", "triage@service.invalid", "agent")

	for _, role := range []string{"admin", "root"} {
		assertRefused(t, s, "service user with role "+role, "CHECK constraint failed",
			`INSERT INTO users (name, email, password_hash, active, role, kind, created_at)
			 VALUES ('Svc', ?, '', 1, ?, 'service', '2026-10-29T10:00:00Z')`, role+"@service.invalid", role)
	}
	assertRefused(t, s, "promote a service user to admin", "CHECK constraint failed",
		`UPDATE users SET role = 'admin' WHERE id = ?`, agentSvc)
	assertRefused(t, s, "unknown kind", "CHECK constraint failed",
		`INSERT INTO users (name, email, password_hash, active, role, kind, created_at)
		 VALUES ('Svc', 'x@service.invalid', '', 1, 'user', 'robot', '2026-10-29T10:00:00Z')`)
	assertRefused(t, s, "person becomes service", "user kind is immutable",
		`UPDATE users SET kind = 'service' WHERE id = ?`, personID)
	assertRefused(t, s, "service becomes person", "user kind is immutable",
		`UPDATE users SET kind = 'person' WHERE id = ?`, userSvc)

	// A service user still moves between user and agent and can be deactivated.
	if _, err := s.db.Exec(`UPDATE users SET role = 'agent', active = 0 WHERE id = ?`, userSvc); err != nil {
		t.Fatalf("service user role/active update = %v, want success", err)
	}

	// The root update guard lists the new column (0036 contract).
	var guard string
	if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'trg_users_root_immutable_update'`).Scan(&guard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(guard, "NEW.kind IS NOT OLD.kind") {
		t.Errorf("root update guard does not protect kind:\n%s", guard)
	}
}

func TestMigration0037ServiceUserPeoplePathsRefused(t *testing.T) {
	s := newTestDB(t)
	personID := seedUserRole(t, s, "Ana", "ana@example.com", true, domain.RoleAgent)
	svcID := seedServiceUserRaw(t, s, "Triage bot", "triage@service.invalid", "agent")
	deskID := seedDesk(t, s, "Support")

	assertRefused(t, s, "service session", "a service user cannot sign in",
		`INSERT INTO sessions (id, user_id, created_at, expires_at) VALUES ('tok', ?, '2026-10-29T10:00:00Z', '2026-10-30T10:00:00Z')`, svcID)
	assertRefused(t, s, "service company identity", "a service user cannot sign in",
		`INSERT INTO user_identities (user_id, provider, issuer, subject, created_at, last_sign_in_at)
		 VALUES (?, 'google', 'https://accounts.google.com', 'sub-1', '2026-10-29T10:00:00Z', '2026-10-29T10:00:00Z')`, svcID)
	assertRefused(t, s, "service recovery link", "a service user has no password",
		`INSERT INTO password_reset_tokens (user_id, token_hash, created_at, expires_at)
		 VALUES (?, 'h', '2026-10-29T10:00:00Z', '2026-10-29T11:00:00Z')`, svcID)
	assertRefused(t, s, "service desk member", "a service user cannot be a desk member",
		`INSERT INTO desk_members (desk_id, user_id, created_at) VALUES (?, ?, '2026-10-29T10:00:00Z')`, deskID, svcID)

	if _, err := s.db.Exec(`INSERT INTO desk_members (desk_id, user_id, created_at) VALUES (?, ?, '2026-10-29T10:00:00Z')`, deskID, personID); err != nil {
		t.Fatalf("person desk member = %v, want success", err)
	}
	assertRefused(t, s, "move a membership onto a service user", "a service user cannot be a desk member",
		`UPDATE desk_members SET user_id = ? WHERE user_id = ?`, svcID, personID)

	// The UPDATE that would move an existing row onto a service user is refused too.
	if _, err := s.db.Exec(`INSERT INTO user_identities (user_id, provider, issuer, subject, created_at, last_sign_in_at)
		VALUES (?, 'google', 'https://accounts.google.com', 'sub-ana', '2026-10-29T10:00:00Z', '2026-10-29T10:00:00Z')`, personID); err != nil {
		t.Fatalf("person company identity = %v, want success", err)
	}
	if _, err := s.db.Exec(`INSERT INTO sessions (id, user_id, created_at, expires_at) VALUES ('ana-tok', ?, '2026-10-29T10:00:00Z', '2026-10-30T10:00:00Z')`, personID); err != nil {
		t.Fatalf("person session = %v, want success", err)
	}
	if _, err := s.db.Exec(`INSERT INTO password_reset_tokens (user_id, token_hash, created_at, expires_at)
		VALUES (?, 'ana-h', '2026-10-29T10:00:00Z', '2026-10-29T11:00:00Z')`, personID); err != nil {
		t.Fatalf("person recovery link = %v, want success", err)
	}
	assertRefused(t, s, "move a session onto a service user", "a service user cannot sign in",
		`UPDATE sessions SET user_id = ? WHERE id = 'ana-tok'`, svcID)
	assertRefused(t, s, "move an identity onto a service user", "a service user cannot sign in",
		`UPDATE user_identities SET user_id = ? WHERE subject = 'sub-ana'`, svcID)
	assertRefused(t, s, "move a recovery link onto a service user", "a service user has no password",
		`UPDATE password_reset_tokens SET user_id = ? WHERE token_hash = 'ana-h'`, svcID)

	// A service user is never a ticket's assignee, on insert or update.
	catID := seedCategory(t, s, "Access")
	assertRefused(t, s, "create a ticket assigned to a service user", "a service user cannot be assigned tickets",
		`INSERT INTO tickets (number, title, category_id, user_id, priority, state, created_at, updated_at)
		 VALUES (900, 'T', ?, ?, 'low', 'new', '2026-10-29T10:00:00Z', '2026-10-29T10:00:00Z')`, catID, svcID)
	ticketID := seedLoadTicket(t, s, 901, personID, catID, domain.StateNew)
	assertRefused(t, s, "assign a ticket to a service user", "a service user cannot be assigned tickets",
		`UPDATE tickets SET user_id = ? WHERE id = ?`, svcID, ticketID)
	if _, err := s.db.Exec(`UPDATE tickets SET user_id = NULL WHERE id = ?`, ticketID); err != nil {
		t.Fatalf("unassign = %v, want success", err)
	}

	// People keep every path.
	if _, err := s.db.Exec(`INSERT INTO sessions (id, user_id, created_at, expires_at) VALUES ('tok', ?, '2026-10-29T10:00:00Z', '2026-10-30T10:00:00Z')`, personID); err != nil {
		t.Fatalf("person session = %v, want success", err)
	}
	if _, err := s.db.Exec(`INSERT INTO password_reset_tokens (user_id, token_hash, created_at, expires_at)
		VALUES (?, 'h', '2026-10-29T10:00:00Z', '2026-10-29T11:00:00Z')`, personID); err != nil {
		t.Fatalf("person recovery link = %v, want success", err)
	}

	for _, table := range []string{"sessions", "user_identities", "password_reset_tokens"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE user_id = ?`, svcID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s holds %d rows for the service user, want 0", table, n)
		}
	}
}

func TestMigration0037APIKeys(t *testing.T) {
	s := newTestDB(t)
	adminID := seedUserRaw(t, s, "Admin", "admin@example.com", "admin")
	svcID := seedServiceUserRaw(t, s, "Monitoring", "monitoring@service.invalid", "user")

	if err := insertAPIKeyRaw(s, "pub1", adminID, adminID); err == nil || !strings.Contains(err.Error(), "an API key belongs to a service user") {
		t.Errorf("key for a person = %v, want the service-user-only refusal", err)
	}
	if err := insertAPIKeyRaw(s, "pub1", 9999, adminID); err == nil {
		t.Error("key for a missing user succeeded, want a refusal")
	}
	if err := insertAPIKeyRaw(s, "pub1", svcID, adminID); err != nil {
		t.Fatalf("key for a service user = %v, want success", err)
	}
	if err := insertAPIKeyRaw(s, "pub1", svcID, adminID); err == nil || !strings.Contains(err.Error(), "UNIQUE") {
		t.Errorf("duplicate public id = %v, want a UNIQUE refusal", err)
	}
	assertRefused(t, s, "short secret hash", "CHECK constraint failed",
		`INSERT INTO api_keys (public_id, user_id, name, secret_hash, scopes, created_by_user_id, created_at)
		 VALUES ('pub2', ?, 'K', 'short', 'tickets:read', ?, '2026-10-29T10:00:00Z')`, svcID, adminID)
	assertRefused(t, s, "empty scopes", "CHECK constraint failed",
		`INSERT INTO api_keys (public_id, user_id, name, secret_hash, scopes, created_by_user_id, created_at)
		 VALUES ('pub2', ?, 'K', ?, ' ', ?, '2026-10-29T10:00:00Z')`, svcID, strings.Repeat("b", 64), adminID)

	otherSvc := seedServiceUserRaw(t, s, "Other", "other@service.invalid", "user")
	assertRefused(t, s, "move a key to another owner", "an API key identity is immutable",
		`UPDATE api_keys SET user_id = ? WHERE public_id = 'pub1'`, otherSvc)
	assertRefused(t, s, "rewrite the secret", "an API key identity is immutable",
		`UPDATE api_keys SET secret_hash = ? WHERE public_id = 'pub1'`, strings.Repeat("c", 64))

	// Revocation and last use are ordinary writes.
	if _, err := s.db.Exec(`UPDATE api_keys SET revoked_at = '2026-10-29T12:00:00Z', revoked_by_user_id = ?, last_used_at = '2026-10-29T11:00:00Z' WHERE public_id = 'pub1'`, adminID); err != nil {
		t.Fatalf("revoke = %v, want success", err)
	}

	// The administrative history accepts its closed vocabulary only.
	if _, err := s.db.Exec(`INSERT INTO api_admin_events (action, actor_user_id, service_user_id, created_at) VALUES ('service_user_created', ?, ?, '2026-10-29T10:00:00Z')`, adminID, svcID); err != nil {
		t.Fatalf("admin event = %v, want success", err)
	}
	assertRefused(t, s, "unknown admin action", "CHECK constraint failed",
		`INSERT INTO api_admin_events (action, actor_user_id, created_at) VALUES ('key_leaked', ?, '2026-10-29T10:00:00Z')`, adminID)
}

// TestMigration0037UpgradesAnExistingDatabase applies 0037 over rows written by
// the 0036 schema: every existing account becomes a person, nothing else about
// it moves, and the version is recorded once.
func TestMigration0037UpgradesAnExistingDatabase(t *testing.T) {
	ctx := context.Background()
	s, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open pre-0037 db: %v", err)
	}
	s.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = s.db.Close() })
	if err := migrate(ctx, s.db, migrationsBefore(t, "0037_service_users_api_keys.sql", false)); err != nil {
		t.Fatalf("migrate pre-0037 db: %v", err)
	}
	rootID := seedUserRaw(t, s, "Root", "root@example.com", "root")
	agentID := seedUserRole(t, s, "Agent", "agent@example.com", true, domain.RoleAgent)
	rootBefore := readStoredUser(t, s, rootID)
	agentBefore := readStoredUser(t, s, agentID)

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("upgrade to the full set: %v", err)
	}

	var recorded int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = 37`).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded != 1 {
		t.Errorf("schema_migrations holds %d rows for version 37, want 1", recorded)
	}
	var people int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE kind = 'person'`).Scan(&people); err != nil {
		t.Fatal(err)
	}
	if people != 2 {
		t.Errorf("people after the upgrade = %d, want 2", people)
	}
	if after := readStoredUser(t, s, rootID); after != rootBefore {
		t.Errorf("the upgrade changed the root row:\nbefore=%+v\nafter=%+v", rootBefore, after)
	}
	if after := readStoredUser(t, s, agentID); after != agentBefore {
		t.Errorf("the upgrade changed the agent row:\nbefore=%+v\nafter=%+v", agentBefore, after)
	}
	assertRootImmutabilityRefused(t, s, "rename after the upgrade", `UPDATE users SET name = 'Hacker' WHERE id = ?`, rootID)
	if _, err := s.db.Exec(`UPDATE users SET password_hash = 'rotated' WHERE id = ?`, rootID); err != nil {
		t.Fatalf("root password rotation after the upgrade = %v, want success", err)
	}
	for _, object := range []string{
		"api_keys", "api_admin_events", "trg_users_kind_immutable", "trg_sessions_no_service_user",
		"trg_user_identities_no_service_user", "trg_password_reset_tokens_no_service_user",
		"trg_desk_members_no_service_user", "trg_desk_members_no_service_user_update",
		"trg_api_keys_service_user_only", "trg_api_keys_owner_immutable",
	} {
		assertSchemaObjectPresent(t, s, object)
	}
}
