package sqlite

import (
	"context"
	"database/sql"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestMigration0038Shape(t *testing.T) {
	s := newTestDB(t)
	var typ string
	var notNull int
	var def sql.NullString
	if err := s.db.QueryRow(`SELECT type, "notnull", dflt_value FROM pragma_table_info('audit_events') WHERE name = 'api_key_id'`).Scan(&typ, &notNull, &def); err != nil {
		t.Fatal(err)
	}
	if typ != "INTEGER" || notNull != 0 || def.Valid {
		t.Fatalf("column = %s notnull=%d default=%v", typ, notNull, def)
	}
	var table, to, onDelete string
	if err := s.db.QueryRow(`SELECT "table", "to", on_delete FROM pragma_foreign_key_list('audit_events') WHERE "from" = 'api_key_id'`).Scan(&table, &to, &onDelete); err != nil {
		t.Fatal(err)
	}
	if table != "api_keys" || to != "id" || onDelete != "NO ACTION" {
		t.Fatalf("foreign key = %s.%s %s", table, to, onDelete)
	}
}

func TestMigration0038Upgrade(t *testing.T) {
	ctx := context.Background()
	s, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	s.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = s.Close() })
	if err := migrate(ctx, s.db, migrationsBefore(t, "0038_audit_api_key.sql", false)); err != nil {
		t.Fatal(err)
	}
	// A real existing instance has its root. Otherwise Migrate deliberately
	// promotes id=1 during legacy root repair, independently of migration 0038.
	seedUserRaw(t, s, "Root", "root@example.com", "root")
	actor := seedUserRaw(t, s, "Agent", "agent@example.com", "agent")
	before := readStoredUser(t, s, actor)
	cat := seedCategory(t, s, "Support")
	ticket := seedLoadTicket(t, s, 901, actor, cat, domain.StateNew)
	if _, err := s.db.Exec(`INSERT INTO audit_events (ticket_id, actor, action, created_at) VALUES (?, 'Agent', 'created', '2026-10-29T10:00:00Z')`, ticket); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=38`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("version recorded %d times", count)
	}
	if after := readStoredUser(t, s, actor); after != before {
		t.Fatalf("user changed during upgrade: before=%+v after=%+v", before, after)
	}
	var key sql.NullInt64
	var gotActor, action, created string
	if err := s.db.QueryRow(`SELECT api_key_id, actor, action, created_at FROM audit_events WHERE ticket_id=?`, ticket).Scan(&key, &gotActor, &action, &created); err != nil {
		t.Fatal(err)
	}
	if key.Valid || gotActor != "Agent" || action != "created" || created != "2026-10-29T10:00:00Z" {
		t.Fatalf("legacy event changed: %v %s %s %s", key, gotActor, action, created)
	}
	assertRefused(t, s, "unknown audit key", "FOREIGN KEY constraint failed", `INSERT INTO audit_events (ticket_id, actor, action, created_at, api_key_id) VALUES (?, 'Agent', 'created', '2026-10-29T10:00:00Z', 99999)`, ticket)
	svc := seedServiceUserRaw(t, s, "Bot", "bot@service.invalid", "user")
	if err := insertAPIKeyRaw(s, "audit-key", svc, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO audit_events (ticket_id, actor, action, created_at, api_key_id) SELECT ?, 'Bot', 'update', '2026-10-29T11:00:00Z', id FROM api_keys WHERE public_id='audit-key'`, ticket); err != nil {
		t.Fatal(err)
	}
	assertRefused(t, s, "delete referenced audit key", "FOREIGN KEY constraint failed", `DELETE FROM api_keys WHERE public_id='audit-key'`)
}
