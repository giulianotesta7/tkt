package sqlite

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Migration 0042 (issue #493): OAuth clients, grants, codes and tokens for MCP
// clients acting as a person, the person-only and immutability guards, the
// deactivation trigger, audit_events.oauth_grant_id, and the api_admin_events
// rebuild that admits the MCP switch actions.

func insertOAuthClientRaw(t *testing.T, s *Store, clientID string) int64 {
	t.Helper()
	res, err := s.db.Exec(`INSERT INTO oauth_clients (client_id, name, redirect_uris, created_at)
		VALUES (?, 'Claude', 'https://claude.ai/api/mcp/auth_callback', '2026-10-29T10:00:00Z')`, clientID)
	if err != nil {
		t.Fatalf("seed oauth client %s: %v", clientID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

const insertOAuthGrantSQL = `INSERT INTO oauth_grants (user_id, client_id, scopes, resource, created_at)
	VALUES (?, ?, 'tickets:read', 'https://tkt.example.com/mcp', '2026-10-29T10:00:00Z')`

func insertOAuthGrantRaw(t *testing.T, s *Store, userID, clientID int64) int64 {
	t.Helper()
	res, err := s.db.Exec(insertOAuthGrantSQL, userID, clientID)
	if err != nil {
		t.Fatalf("seed oauth grant: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

const insertOAuthCodeSQL = `INSERT INTO oauth_codes (code_hash, client_id, user_id, redirect_uri, code_challenge, scopes, resource, created_at, expires_at)
	VALUES (?, ?, ?, 'https://claude.ai/api/mcp/auth_callback', ?, 'tickets:read', 'https://tkt.example.com/mcp', '2026-10-29T10:00:00Z', '2026-10-29T10:01:00Z')`

// tableShape lists "name type notnull pk" for every column, in order.
func tableShape(t *testing.T, s *Store, table string) []string {
	t.Helper()
	rows, err := s.db.Query(`SELECT name, type, "notnull", pk FROM pragma_table_info(?) ORDER BY cid`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name, typ string
		var notNull, pk int
		if err := rows.Scan(&name, &typ, &notNull, &pk); err != nil {
			t.Fatal(err)
		}
		col := name + " " + typ
		if notNull == 1 {
			col += " notnull"
		}
		if pk == 1 {
			col += " pk"
		}
		out = append(out, col)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// foreignKeys lists "from->table.to on_delete" for every foreign key, ordered by column.
func foreignKeys(t *testing.T, s *Store, table string) []string {
	t.Helper()
	rows, err := s.db.Query(`SELECT "from", "table", "to", on_delete FROM pragma_foreign_key_list(?) ORDER BY "from"`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var from, ref, to, onDelete string
		if err := rows.Scan(&from, &ref, &to, &onDelete); err != nil {
			t.Fatal(err)
		}
		out = append(out, from+"->"+ref+"."+to+" "+onDelete)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMigration0042Shape(t *testing.T) {
	s := newTestDB(t)
	for _, tc := range []struct {
		table string
		cols  []string
		fks   []string
	}{
		{"oauth_clients",
			[]string{"id INTEGER pk", "client_id TEXT notnull", "name TEXT notnull", "redirect_uris TEXT notnull", "created_at TEXT notnull"},
			nil},
		{"oauth_grants",
			[]string{"id INTEGER pk", "user_id INTEGER notnull", "client_id INTEGER notnull", "scopes TEXT notnull", "resource TEXT notnull",
				"created_at TEXT notnull", "last_used_at TEXT", "revoked_at TEXT", "revoked_by_user_id INTEGER"},
			[]string{"client_id->oauth_clients.id NO ACTION", "revoked_by_user_id->users.id NO ACTION", "user_id->users.id NO ACTION"}},
		{"oauth_codes",
			[]string{"id INTEGER pk", "code_hash TEXT notnull", "client_id INTEGER notnull", "user_id INTEGER notnull", "redirect_uri TEXT notnull",
				"code_challenge TEXT notnull", "scopes TEXT notnull", "resource TEXT notnull", "created_at TEXT notnull", "expires_at TEXT notnull",
				"used_at TEXT", "grant_id INTEGER"},
			[]string{"client_id->oauth_clients.id NO ACTION", "grant_id->oauth_grants.id NO ACTION", "user_id->users.id NO ACTION"}},
		{"oauth_tokens",
			[]string{"id INTEGER pk", "grant_id INTEGER notnull", "kind TEXT notnull", "token_hash TEXT notnull", "created_at TEXT notnull",
				"expires_at TEXT notnull", "used_at TEXT"},
			[]string{"grant_id->oauth_grants.id NO ACTION"}},
		{"api_admin_events",
			[]string{"id INTEGER pk", "action TEXT notnull", "actor_user_id INTEGER notnull", "service_user_id INTEGER", "api_key_id INTEGER", "created_at TEXT notnull"},
			[]string{"actor_user_id->users.id NO ACTION", "api_key_id->api_keys.id NO ACTION", "service_user_id->users.id NO ACTION"}},
	} {
		if got := tableShape(t, s, tc.table); !reflect.DeepEqual(got, tc.cols) {
			t.Errorf("%s columns = %q, want %q", tc.table, got, tc.cols)
		}
		if got := foreignKeys(t, s, tc.table); !reflect.DeepEqual(got, tc.fks) {
			t.Errorf("%s foreign keys = %q, want %q", tc.table, got, tc.fks)
		}
	}
	var typ string
	var notNull int
	var def sql.NullString
	if err := s.db.QueryRow(`SELECT type, "notnull", dflt_value FROM pragma_table_info('audit_events') WHERE name = 'oauth_grant_id'`).Scan(&typ, &notNull, &def); err != nil {
		t.Fatal(err)
	}
	if typ != "INTEGER" || notNull != 0 || def.Valid {
		t.Fatalf("audit_events.oauth_grant_id = %s notnull=%d default=%v", typ, notNull, def)
	}
	var table, to, onDelete string
	if err := s.db.QueryRow(`SELECT "table", "to", on_delete FROM pragma_foreign_key_list('audit_events') WHERE "from" = 'oauth_grant_id'`).Scan(&table, &to, &onDelete); err != nil {
		t.Fatal(err)
	}
	if table != "oauth_grants" || to != "id" || onDelete != "NO ACTION" {
		t.Fatalf("audit_events.oauth_grant_id foreign key = %s.%s %s", table, to, onDelete)
	}
}

func TestMigration0042PersonOnlyGuards(t *testing.T) {
	s := newTestDB(t)
	person := seedUserRaw(t, s, "Ana", "ana@example.com", "agent")
	svc := seedServiceUserRaw(t, s, "Bot", "bot@service.invalid", "agent")
	client := insertOAuthClientRaw(t, s, "client-1")

	assertRefused(t, s, "grant for a service user", "an OAuth grant belongs to a person", insertOAuthGrantSQL, svc, client)
	assertRefused(t, s, "grant for an unknown user", "an OAuth grant belongs to a person", insertOAuthGrantSQL, 99999, client)
	assertRefused(t, s, "code for a service user", "an OAuth grant belongs to a person", insertOAuthCodeSQL,
		strings.Repeat("a", 64), client, svc, strings.Repeat("c", 43))
	var n int
	if err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM oauth_grants) + (SELECT COUNT(*) FROM oauth_codes)`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("refused rows stored: %d (err %v)", n, err)
	}

	grant := insertOAuthGrantRaw(t, s, person, client)
	if _, err := s.db.Exec(insertOAuthCodeSQL, strings.Repeat("b", 64), client, person, strings.Repeat("c", 43)); err != nil {
		t.Fatalf("code for a person: %v", err)
	}
	other := seedUserRaw(t, s, "Bruno", "bruno@example.com", "agent")
	otherClient := insertOAuthClientRaw(t, s, "client-2")
	for _, tc := range []struct{ name, sql string }{
		{"user", `UPDATE oauth_grants SET user_id = ? WHERE id = ?`},
		{"client", `UPDATE oauth_grants SET client_id = ? WHERE id = ?`},
	} {
		arg := other
		if tc.name == "client" {
			arg = otherClient
		}
		assertRefused(t, s, "change grant "+tc.name, "an OAuth grant identity is immutable", tc.sql, arg, grant)
	}
	assertRefused(t, s, "change grant scopes", "an OAuth grant identity is immutable", `UPDATE oauth_grants SET scopes = 'tickets:all' WHERE id = ?`, grant)
	assertRefused(t, s, "change grant resource", "an OAuth grant identity is immutable", `UPDATE oauth_grants SET resource = 'https://evil.example/mcp' WHERE id = ?`, grant)
	if _, err := s.db.Exec(`UPDATE oauth_grants SET last_used_at = '2026-10-29T11:00:00Z', revoked_at = '2026-10-29T12:00:00Z', revoked_by_user_id = ? WHERE id = ?`, person, grant); err != nil {
		t.Fatalf("usage and revocation must stay writable: %v", err)
	}
	assertRefused(t, s, "unknown token kind", "CHECK constraint failed", `INSERT INTO oauth_tokens (grant_id, kind, token_hash, created_at, expires_at)
		VALUES (?, 'id', ?, '2026-10-29T10:00:00Z', '2026-10-29T11:00:00Z')`, grant, strings.Repeat("d", 64))
	assertRefused(t, s, "unknown audit grant", "FOREIGN KEY constraint failed", `INSERT INTO audit_events (ticket_id, actor, action, created_at, oauth_grant_id)
		VALUES (?, 'Ana', 'created', '2026-10-29T10:00:00Z', 99999)`, seedLoadTicket(t, s, 901, person, seedCategory(t, s, "Support"), domain.StateNew))
}

func TestMigration0042DeactivationRevokesGrants(t *testing.T) {
	s := newTestDB(t)
	ana := seedUserRaw(t, s, "Ana", "ana@example.com", "agent")
	bruno := seedUserRaw(t, s, "Bruno", "bruno@example.com", "agent")
	client := insertOAuthClientRaw(t, s, "client-1")
	live := insertOAuthGrantRaw(t, s, ana, client)
	revoked := insertOAuthGrantRaw(t, s, ana, client)
	if _, err := s.db.Exec(`UPDATE oauth_grants SET revoked_at = '2026-10-29T09:00:00Z', revoked_by_user_id = ? WHERE id = ?`, ana, revoked); err != nil {
		t.Fatal(err)
	}
	brunos := insertOAuthGrantRaw(t, s, bruno, client)

	before := time.Now().UTC().Truncate(time.Second)
	if _, err := s.db.Exec(`UPDATE users SET active = 0 WHERE id = ?`, ana); err != nil {
		t.Fatal(err)
	}
	after := time.Now().UTC()
	grantRevocation := func(id int64) (sql.NullString, sql.NullInt64) {
		t.Helper()
		var at sql.NullString
		var by sql.NullInt64
		if err := s.db.QueryRow(`SELECT revoked_at, revoked_by_user_id FROM oauth_grants WHERE id = ?`, id).Scan(&at, &by); err != nil {
			t.Fatal(err)
		}
		return at, by
	}
	at, by := grantRevocation(live)
	if !at.Valid || by.Valid {
		t.Fatalf("live grant after deactivation: revoked_at=%v by=%v", at, by)
	}
	stamp, err := time.Parse(timeLayout, at.String)
	if err != nil || stamp.Before(before) || stamp.After(after) {
		t.Fatalf("revoked_at = %q (err %v), want an RFC 3339 instant within the update", at.String, err)
	}
	if at, by := grantRevocation(revoked); at.String != "2026-10-29T09:00:00Z" || by.Int64 != ana {
		t.Fatalf("already revoked grant changed: %v %v", at, by)
	}
	if at, _ := grantRevocation(brunos); at.Valid {
		t.Fatalf("another person's grant was revoked: %v", at)
	}

	if _, err := s.db.Exec(`UPDATE users SET active = 1 WHERE id = ?`, ana); err != nil {
		t.Fatal(err)
	}
	if again, _ := grantRevocation(live); again != at {
		t.Fatalf("reactivation changed the revoked grant: %v", again)
	}
	if _, err := s.db.Exec(`UPDATE users SET name = 'Bruno B' WHERE id = ?`, bruno); err != nil {
		t.Fatal(err)
	}
	if at, _ := grantRevocation(brunos); at.Valid {
		t.Fatalf("an unrelated user update revoked a grant: %v", at)
	}
}

func TestMigration0042Upgrade(t *testing.T) {
	ctx := context.Background()
	s, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	s.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = s.Close() })
	if err := migrate(ctx, s.db, migrationsBefore(t, "0042_oauth_mcp.sql", false)); err != nil {
		t.Fatal(err)
	}
	root := seedUserRaw(t, s, "Root", "root@example.com", "root")
	actor := seedUserRaw(t, s, "Agent", "agent@example.com", "agent")
	svc := seedServiceUserRaw(t, s, "Bot", "bot@service.invalid", "user")
	if err := insertAPIKeyRaw(s, "audit-key", svc, root); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"service_user_created", "api_key_created", "api_enabled", "api_disabled"} {
		if _, err := s.db.Exec(`INSERT INTO api_admin_events (action, actor_user_id, service_user_id, api_key_id, created_at)
			VALUES (?, ?, ?, (SELECT id FROM api_keys WHERE public_id = 'audit-key'), '2026-10-29T10:00:00Z')`, action, root, svc); err != nil {
			t.Fatal(err)
		}
	}
	// The newest row is gone, so the AUTOINCREMENT counter (4) is ahead of the
	// highest surviving id (3); the rebuild must keep the counter, not MAX(id).
	if _, err := s.db.Exec(`DELETE FROM api_admin_events WHERE id = 4`); err != nil {
		t.Fatal(err)
	}
	cat := seedCategory(t, s, "Support")
	ticket := seedLoadTicket(t, s, 901, actor, cat, domain.StateNew)
	if _, err := s.db.Exec(`INSERT INTO audit_events (ticket_id, actor, action, created_at, api_key_id)
		VALUES (?, 'Bot', 'update', '2026-10-29T11:00:00Z', (SELECT id FROM api_keys WHERE public_id = 'audit-key'))`, ticket); err != nil {
		t.Fatal(err)
	}
	adminRows := func() []string {
		t.Helper()
		rows, err := s.db.Query(`SELECT id || ':' || action || ':' || actor_user_id || ':' || COALESCE(service_user_id, '') || ':' || COALESCE(api_key_id, '') || ':' || created_at
			FROM api_admin_events ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var r string
			if err := rows.Scan(&r); err != nil {
				t.Fatal(err)
			}
			out = append(out, r)
		}
		return out
	}
	beforeRows := adminRows()
	if len(beforeRows) != 3 {
		t.Fatalf("seeded admin rows = %q", beforeRows)
	}

	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = 42`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("version 42 recorded %d times", count)
	}
	if got := adminRows(); !reflect.DeepEqual(got, beforeRows) {
		t.Fatalf("admin rows after upgrade = %q, want %q", got, beforeRows)
	}
	var seq int64
	if err := s.db.QueryRow(`SELECT seq FROM sqlite_sequence WHERE name = 'api_admin_events'`).Scan(&seq); err != nil || seq != 4 {
		t.Fatalf("api_admin_events counter = %d (err %v), want 4", seq, err)
	}
	var leftover int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_sequence WHERE name = 'api_admin_events_new'`).Scan(&leftover); err != nil || leftover != 0 {
		t.Fatalf("rebuild left counter rows: %d (err %v)", leftover, err)
	}
	for _, action := range []string{"mcp_enabled", "mcp_disabled"} {
		if _, err := s.db.Exec(`INSERT INTO api_admin_events (action, actor_user_id, created_at) VALUES (?, ?, '2026-10-30T10:00:00Z')`, action, root); err != nil {
			t.Fatalf("insert %s: %v", action, err)
		}
	}
	var newest int64
	if err := s.db.QueryRow(`SELECT MIN(id) FROM api_admin_events WHERE action = 'mcp_enabled'`).Scan(&newest); err != nil || newest != 5 {
		t.Fatalf("first id after upgrade = %d (err %v), want 5", newest, err)
	}
	assertRefused(t, s, "unknown admin action", "CHECK constraint failed", `INSERT INTO api_admin_events (action, actor_user_id, created_at) VALUES ('mcp_toggled', ?, '2026-10-30T10:00:00Z')`, root)
	var idx int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_api_admin_events_created' AND tbl_name = 'api_admin_events'`).Scan(&idx); err != nil || idx != 1 {
		t.Fatalf("created_at index = %d (err %v)", idx, err)
	}

	var key, grant sql.NullInt64
	var gotActor, action, created string
	if err := s.db.QueryRow(`SELECT api_key_id, oauth_grant_id, actor, action, created_at FROM audit_events WHERE ticket_id = ?`, ticket).Scan(&key, &grant, &gotActor, &action, &created); err != nil {
		t.Fatal(err)
	}
	if !key.Valid || grant.Valid || gotActor != "Bot" || action != "update" || created != "2026-10-29T11:00:00Z" {
		t.Fatalf("legacy audit event changed: key=%v grant=%v %s %s %s", key, grant, gotActor, action, created)
	}
	var fkViolations int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&fkViolations); err != nil || fkViolations != 0 {
		t.Fatalf("foreign key violations after upgrade = %d (err %v)", fkViolations, err)
	}
}
