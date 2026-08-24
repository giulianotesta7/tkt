package sqlite

import (
	"context"
	"testing"
)

func TestMigration0006(t *testing.T) {
	s := newTestDB(t)
	for _, tbl := range []string{"workflow_versions", "category_workflows", "ticket_workflow_runs", "ticket_form_answers"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&n); err != nil {
			t.Fatalf("table %s: %v", tbl, err)
		}
		if n != 1 {
			t.Fatalf("table %s missing", tbl)
		}
	}
	rows, err := s.db.Query(`PRAGMA table_info(tickets)`)
	if err != nil {
		t.Fatalf("pragma: %v", err)
	}
	defer rows.Close()
	found := false
	var cid, cname, ctype string
	var nn, pk int
	var dflt *string
	for rows.Next() {
		if err := rows.Scan(&cid, &cname, &ctype, &nn, &dflt, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if cname == "workflow_version_id" {
			found = true
			if nn != 0 {
				t.Fatal("workflow_version_id must be nullable")
			}
		}
	}
	if !found {
		t.Fatal("tickets.workflow_version_id missing")
	}
	if _, err := s.db.ExecContext(context.Background(), `INSERT INTO categories (name, created_at) VALUES (?,?)`, "cat-mig-1", "2026-08-06T10:00:00Z"); err != nil {
		t.Fatalf("seed cat: %v", err)
	}
	var catID int64
	if err := s.db.QueryRow(`SELECT id FROM categories WHERE name='cat-mig-1'`).Scan(&catID); err != nil {
		t.Fatalf("cat id: %v", err)
	}
	if _, err := s.db.ExecContext(context.Background(), `INSERT INTO workflow_versions (category_id, version_no, steps_json, published_at) VALUES (?,1,'[{"type":"manual_task","manual_task":{"instructions":"do"}}]','2026-08-06T10:00:00Z')`, catID); err != nil {
		t.Fatalf("insert version: %v", err)
	}
	if _, err := s.db.ExecContext(context.Background(), `UPDATE workflow_versions SET steps_json='[{"type":"manual_task","manual_task":{"instructions":"x"}}]' WHERE category_id=?`, catID); err == nil {
		t.Fatal("trigger must abort UPDATE")
	}
	var cnt int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM category_workflows`).Scan(&cnt); err != nil {
		t.Fatalf("count cw: %v", err)
	}
	if cnt != 0 {
		t.Fatalf("no backfill expected, got %d", cnt)
	}
	if _, err := s.db.ExecContext(context.Background(), `INSERT INTO tickets (number,title,description,requester_name,requester_email,category_id,priority,state,created_at,updated_at,workflow_version_id) VALUES (999,'legacy','','r','e',?,'medium','new','2026-08-06T10:00:00Z','2026-08-06T10:00:00Z',NULL)`, catID); err != nil {
		t.Fatalf("legacy ticket: %v", err)
	}
	var wid *int64
	if err := s.db.QueryRow(`SELECT workflow_version_id FROM tickets WHERE number=999`).Scan(&wid); err != nil {
		t.Fatalf("read legacy: %v", err)
	}
	if wid != nil {
		t.Fatal("legacy pin must stay NULL")
	}
	var fts int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='tickets_fts'`).Scan(&fts); err != nil {
		t.Fatalf("fts: %v", err)
	}
	if fts != 1 {
		t.Fatal("tickets_fts missing")
	}
	var applied int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=6`).Scan(&applied); err != nil {
		t.Fatalf("migration: %v", err)
	}
	if applied != 1 {
		t.Fatal("migration 6 not recorded")
	}
	if _, err := s.db.ExecContext(context.Background(), `INSERT INTO workflow_versions (category_id,version_no,steps_json,published_at) VALUES (?,2,'not-json','2026-08-06T10:00:00Z')`, catID); err == nil {
		t.Fatal("invalid json must be rejected")
	}
}
