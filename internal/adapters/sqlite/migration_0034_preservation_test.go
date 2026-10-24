package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// TestMigration0034PreservesEveryExistingObjectAndRow proves 0034 rebuilds
// nothing: every schema object keeps its definition and root page (only the
// users DDL changes, by the appended column), every existing row reads back
// unchanged, and objects later migrations add to users — a column, a partial
// index and an enforcing trigger stand in for them here — survive and keep
// working. Existing accounts read pending = 0, version 34 is recorded once,
// and a rerun changes nothing.
func TestMigration0034PreservesEveryExistingObjectAndRow(t *testing.T) {
	s, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	s.db.SetMaxOpenConns(1)
	ctx := context.Background()
	if err := migrate(ctx, s.db, pre0034Migrations(t)); err != nil {
		t.Fatal(err)
	}
	exec := func(q string) {
		t.Helper()
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO users(id,name,email,password_hash,role,active,created_at) VALUES
 (1,'Root','root@example.com','hash','root',1,'2026-08-06T10:00:00Z'),
 (2,'Imported','import@example.com','hash','agent',1,'2026-08-06T10:00:00Z'),
 (3,'Inactive','inactive@example.com','hash','admin',0,'2026-08-06T10:00:00Z')`)
	exec(`ALTER TABLE users ADD COLUMN extension_fact TEXT NOT NULL DEFAULT 'kept'`)
	exec(`CREATE INDEX extension_users_index ON users(extension_fact) WHERE active=1`)
	exec(`CREATE TRIGGER extension_users_guard BEFORE UPDATE OF extension_fact ON users WHEN NEW.extension_fact='' BEGIN SELECT RAISE(ABORT,'extension fact required'); END`)
	exec(`INSERT INTO sessions(id,user_id,created_at,expires_at) VALUES('session',2,'2026-08-06T10:00:00Z','2027-08-06T10:00:00Z')`)
	cat := seedCategory(t, s, "Preservation")
	exec(fmt.Sprintf(`INSERT INTO tickets(number,title,requester_name,requester_email,requester_user_id,category_id,priority,state,created_at,updated_at) VALUES(912,'Keep','Imported','import@example.com',2,%d,'medium','new','2026-08-06T10:00:00Z','2026-08-06T10:00:00Z')`, cat))
	exec(`INSERT INTO comments(ticket_id,author,body,created_at) SELECT id,'Imported','Keep comment','2026-08-06T10:00:00Z' FROM tickets WHERE number=912`)
	exec(`INSERT INTO audit_events(ticket_id,actor,action,created_at) SELECT id,'Imported','created','2026-08-06T10:00:00Z' FROM tickets WHERE number=912`)
	var ticketID, commentID int64
	if err := s.db.QueryRow(`SELECT id FROM tickets WHERE number=912`).Scan(&ticketID); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT id FROM comments WHERE ticket_id=?`, ticketID).Scan(&commentID); err != nil {
		t.Fatal(err)
	}
	// One notification of every kind 0028 left, under HIGH explicit ids, so
	// the rebuild has to copy them rather than renumber them, and the email
	// delivery rows that point at them have to survive the table's rebuild.
	for i, kind := range priorNotificationKinds {
		var comment, from, to, step any
		if kind.IsComment() {
			comment = commentID
		}
		switch kind {
		case domain.NotificationStateChanged:
			from, to = "new", "in_progress"
		case domain.NotificationPriorityChanged:
			from, to = "medium", "high"
		case domain.NotificationFormWaiting:
			step = 1
		}
		mustExec(`INSERT INTO notifications(id,user_id,ticket_id,kind,actor_user_id,from_value,to_value,comment_id,step_index,created_at,read_at)
			VALUES(?,2,?,?,2,?,?,?,?,'2026-08-06T10:00:00Z','2026-08-06T11:00:00Z')`,
			int64(5001+i), ticketID, string(kind), from, to, comment, step)
	}
	mustExec(`INSERT INTO notification_emails(notification_id,status,attempts,next_attempt_at,created_at,updated_at)
		VALUES(5001,'sent',1,'2026-08-06T10:00:00Z','2026-08-06T10:00:00Z','2026-08-06T10:00:00Z')`)
	mustExec(`INSERT INTO notification_emails(notification_id,status,attempts,next_attempt_at,last_error,created_at,updated_at)
		VALUES(5002,'failed',3,'2026-08-06T10:00:00Z','smtp refused','2026-08-06T10:00:00Z','2026-08-06T10:00:00Z')`)
	exec(`INSERT INTO password_reset_tokens(user_id,token_hash,created_at,expires_at) VALUES(2,'token','2026-08-06T10:00:00Z','2027-08-06T10:00:00Z')`)

	type object struct {
		kind, table, definition string
		root                    int
	}
	master := func() map[string]object {
		t.Helper()
		rows, err := s.db.Query(`SELECT name,type,tbl_name,coalesce(sql,''),rootpage FROM sqlite_master ORDER BY name`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]object{}
		for rows.Next() {
			var name string
			var o object
			if err := rows.Scan(&name, &o.kind, &o.table, &o.definition, &o.root); err != nil {
				t.Fatal(err)
			}
			out[name] = o
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}

	before := master()
	columns := map[string][]string{}
	for name, o := range before {
		if o.kind != "table" || name == "schema_migrations" {
			continue
		}
		rows, err := s.db.Query(`SELECT name FROM pragma_table_info(?) ORDER BY cid`, name)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var c string
			if err := rows.Scan(&c); err != nil {
				t.Fatal(err)
			}
			columns[name] = append(columns[name], `"`+strings.ReplaceAll(c, `"`, `""`)+`"`)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}

	snapshot := func() map[string][]string {
		t.Helper()
		out := map[string][]string{}
		for table, cols := range columns {
			rows, err := s.db.Query(`SELECT ` + strings.Join(cols, ",") + ` FROM "` + strings.ReplaceAll(table, `"`, `""`) + `"`)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				vals := make([]any, len(cols))
				ptrs := make([]any, len(cols))
				for i := range vals {
					ptrs[i] = &vals[i]
				}
				if err := rows.Scan(ptrs...); err != nil {
					t.Fatal(err)
				}
				out[table] = append(out[table], fmt.Sprintf("%#v", vals))
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			sort.Strings(out[table])
		}
		return out
	}

	data := snapshot()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	// Two objects are allowed to change, and only in the documented way: users
	// gains the appended pending column, and notifications is rebuilt so the
	// bell can carry a ticketless account announcement. The rebuild recreates
	// its index and may move root pages, including the sequence row's; the
	// definitions of those two must not drift. Every other object keeps its
	// definition AND its root page, so no unrelated table was rebuilt.
	after := master()
	for name, want := range before {
		got, ok := after[name]
		if !ok {
			t.Errorf("lost schema object %s", name)
			continue
		}
		switch name {
		case "users":
			if got.definition == want.definition {
				t.Error("users DDL did not change")
			}
			got.definition = want.definition
			if got != want {
				t.Errorf("users object changed beyond the appended column: %#v -> %#v", want, got)
			}
		case "idx_notifications_recipient", "sqlite_sequence":
			got.root = want.root
			if got != want {
				t.Errorf("schema object %s changed: %#v -> %#v", name, want, got)
			}
		case "notifications":
			if got.kind != want.kind || got.table != want.table {
				t.Errorf("notifications object type changed: %#v -> %#v", want, got)
			}
			if got.definition == want.definition {
				t.Error("notifications DDL did not change, want the documented rebuild for the account kind")
			}
		default:
			if got != want {
				t.Errorf("schema object %s changed: %#v -> %#v", name, want, got)
			}
		}
	}
	for name := range after {
		if _, ok := before[name]; ok {
			continue
		}
		switch name {
		case "user_identities", "oidc_sign_in_attempts", "idx_oidc_sign_in_attempts_expires",
			"sqlite_autoindex_user_identities_1", "sqlite_autoindex_user_identities_2",
			"sqlite_autoindex_oidc_sign_in_attempts_1":
		default:
			t.Errorf("unexpected new schema object %s", name)
		}
	}
	if got := snapshot(); !reflect.DeepEqual(got, data) {
		t.Fatalf("pre-existing rows changed:\nbefore=%#v\nafter=%#v", data, got)
	}
	// The rebuild adds only the account subject: every copied row has none.
	var oldWithAccount int
	if err := s.db.QueryRow(`SELECT count(*) FROM notifications WHERE pending_user_id IS NOT NULL`).Scan(&oldWithAccount); err != nil {
		t.Fatal(err)
	}
	if oldWithAccount != 0 {
		t.Errorf("pre-existing notifications with an account subject = %d, want 0", oldWithAccount)
	}
	var deliveryRows int
	if err := s.db.QueryRow(`SELECT count(*) FROM notification_emails`).Scan(&deliveryRows); err != nil {
		t.Fatal(err)
	}
	if deliveryRows != 2 {
		t.Errorf("notification_emails rows after the rebuild = %d, want 2 (no delivery row is cascaded away)", deliveryRows)
	}
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM users WHERE pending<>0`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("legacy pending count=%d err=%v", n, err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE version=34`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("version34 count=%d err=%v", n, err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if got := snapshot(); !reflect.DeepEqual(got, data) {
		t.Fatal("rerun changed data")
	}
	if _, err := s.db.Exec(`UPDATE users SET extension_fact='' WHERE id=2`); err == nil || !strings.Contains(err.Error(), "extension fact required") {
		t.Fatalf("extension trigger no longer enforces: %v", err)
	}
}

// TestMigration0034RefusesInvalidWritesWithoutTouchingTheFile covers rejected
// UPDATEs as well as INSERTs on the three new shapes, and proves each refusal
// leaves the database and WAL bytes unchanged. Approval and rejection are the
// two accepted transitions of a pending account, and imported-style inserts
// that omit pending keep their role and read pending = 0.
func TestMigration0034RefusesInvalidWritesWithoutTouchingTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "probe.db")
	s, err := OpenForTests(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	s.db.SetMaxOpenConns(1)
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	exec := func(q string) {
		t.Helper()
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO users(id,name,email,password_hash,active,pending,created_at) VALUES(1,'Waiting','waiting@example.com','',0,1,'2026-08-06T10:00:00Z'),(2,'Other','other@example.com','',0,0,'2026-08-06T10:00:00Z')`)

	bytes := func() [32]byte {
		t.Helper()
		var all []byte
		for _, suffix := range []string{"", "-wal"} {
			b, err := os.ReadFile(path + suffix)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			all = append(all, b...)
		}
		return sha256.Sum256(all)
	}
	reject := func(q string) {
		t.Helper()
		before := bytes()
		_, err := s.db.Exec(q)
		if err == nil || !isConstraint(err) {
			t.Fatalf("expected constraint rejection: %s: %v", q, err)
		}
		if after := bytes(); after != before {
			t.Fatalf("rejected statement changed DB/WAL bytes: %s", q)
		}
	}

	for _, v := range []string{"-1", "2", "0.5", "'invalid'", "NULL"} {
		reject(`INSERT INTO users(name,email,password_hash,active,pending,created_at) VALUES('Bad','bad@example.com','',0,` + v + `,'2026-08-06T10:00:00Z')`)
		reject(`UPDATE users SET pending=` + v + ` WHERE id=2`)
	}
	reject(`UPDATE users SET active=1 WHERE id=1`)
	reject(`UPDATE users SET active=1,pending=1 WHERE id=2`)
	exec(`UPDATE users SET active=1,pending=0 WHERE id=1`)
	exec(`UPDATE users SET active=0,pending=1 WHERE id=1`)
	exec(`UPDATE users SET pending=0 WHERE id=1`)

	for i, role := range []string{"user", "agent", "admin", "root"} {
		exec(fmt.Sprintf(`INSERT INTO users(name,email,password_hash,role,active,created_at) VALUES('Imported','csv%d@example.com','hash','%s',1,'2026-08-06T10:00:00Z')`, i, role))
	}
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM users WHERE email LIKE 'csv%' AND pending=0`).Scan(&n); err != nil || n != 4 {
		t.Fatalf("import defaults n=%d err=%v", n, err)
	}

	identity := `INSERT INTO user_identities(user_id,provider,issuer,subject,created_at,last_sign_in_at) VALUES(1,'google','issuer','subject','2026-08-06T10:00:00Z','2026-08-06T10:00:00Z')`
	exec(identity)
	reject(strings.Replace(identity, "VALUES(1,", "VALUES(2,", 1))
	reject(strings.Replace(identity, "'subject'", "'different'", 1))
	reject(strings.Replace(identity, "VALUES(1,", "VALUES(999,", 1))
	exec(strings.Replace(strings.Replace(identity, "VALUES(1,", "VALUES(2,", 1), "'subject'", "'other'", 1))
	reject(`UPDATE user_identities SET subject='subject' WHERE user_id=2`)
	reject(`UPDATE user_identities SET user_id=1 WHERE user_id=2`)
	for _, q := range []string{
		`UPDATE user_identities SET provider='ldap' WHERE user_id=2`,
		`UPDATE user_identities SET issuer=' ' WHERE user_id=2`,
		`UPDATE user_identities SET subject='' WHERE user_id=2`,
	} {
		reject(q)
	}

	attempt := `INSERT INTO oidc_sign_in_attempts(state_hash,provider,nonce,code_verifier,created_at,expires_at) VALUES('hash','google','nonce','verifier','2026-08-06T10:00:00Z','2026-08-06T10:10:00Z')`
	exec(attempt)
	reject(attempt)
	for _, column := range []string{"state_hash", "nonce", "code_verifier"} {
		reject(`UPDATE oidc_sign_in_attempts SET ` + column + `=''`)
		reject(`UPDATE oidc_sign_in_attempts SET ` + column + `=NULL`)
	}
	reject(`UPDATE oidc_sign_in_attempts SET provider='ldap'`)
	exec(`DELETE FROM users WHERE id=1`)
	if err := s.db.QueryRow(`SELECT count(*) FROM user_identities WHERE user_id=1`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("cascade n=%d err=%v", n, err)
	}
	if err := s.db.QueryRow(`PRAGMA foreign_key_check`).Scan(new(any), new(any), new(any), new(any)); err != sql.ErrNoRows {
		t.Fatalf("foreign_key_check: %v", err)
	}
}
