package sqlite

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

func mustCanon(t *testing.T, d domain.WorkflowDefinition) []byte {
	t.Helper()
	b, err := d.MarshalCanonical()
	if err != nil {
		t.Fatalf("canon: %v", err)
	}
	return b
}
func seedDesk(t *testing.T, s *Store, n string) int64 {
	t.Helper()
	d := &domain.Desk{Name: n, CreatedAt: testClock}
	if err := newDeskStore(s.db).Create(context.Background(), d); err != nil {
		t.Fatalf("seed desk: %v", err)
	}
	return d.ID
}
func TestWorkflowStore_DraftLifecycle(t *testing.T) {
	s := newTestDB(t)
	c1 := seedCategory(t, s, "cat-a")
	ws := s.WorkflowStore()
	d, err := ws.GetDraft(context.Background(), c1)
	if err != nil || d != nil {
		t.Fatalf("want nil got %q %v", string(d), err)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM category_workflows WHERE category_id=?`, c1).Scan(&n)
	if n != 0 {
		t.Fatalf("GetDraft created row %d", n)
	}
	c2 := seedCategory(t, s, "cat-b")
	a := mustCanon(t, domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "do"}}})
	if err := ws.UpsertDraft(context.Background(), c2, a); err != nil {
		t.Fatal(err)
	}
	got, _ := ws.GetDraft(context.Background(), c2)
	if string(got) != string(a) {
		t.Fatalf("mismatch %q vs %q", string(got), string(a))
	}
	b := mustCanon(t, domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "do2"}}})
	ws.UpsertDraft(context.Background(), c2, b)
	got2, _ := ws.GetDraft(context.Background(), c2)
	if string(got2) != string(b) {
		t.Fatal("update mismatch")
	}
	s.db.QueryRow(`SELECT COUNT(*) FROM category_workflows WHERE category_id=?`, c2).Scan(&n)
	if n != 1 {
		t.Fatalf("want 1 got %d", n)
	}
	c3 := seedCategory(t, s, "cat-conc")
	d3 := mustCanon(t, domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "do"}}})
	var wg sync.WaitGroup
	wg.Add(2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		go func(i int) { defer wg.Done(); errs[i] = ws.UpsertDraft(context.Background(), c3, d3) }(i)
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	s.db.QueryRow(`SELECT COUNT(*) FROM category_workflows WHERE category_id=?`, c3).Scan(&n)
	if n != 1 {
		t.Fatalf("want 1 got %d", n)
	}
}
func TestWorkflowStore_Publish(t *testing.T) {
	s := newTestDB(t)
	c := seedCategory(t, s, "cat-pub")
	desk := seedDesk(t, s, "DeskPub")
	ws := s.WorkflowStore()
	_, iss, _ := ws.Publish(context.Background(), c, []byte(`[]`), nil)
	if len(iss) == 0 {
		t.Fatal("empty want issues")
	}
	var vc int
	s.db.QueryRow(`SELECT COUNT(*) FROM workflow_versions WHERE category_id=?`, c).Scan(&vc)
	if vc != 0 {
		t.Fatal("empty created version")
	}
	_, iss, _ = ws.Publish(context.Background(), c, []byte(`[{"type":"unknown"}]`), nil)
	if len(iss) == 0 {
		t.Fatal("invalid want issues")
	}
	bad := mustCanon(t, domain.WorkflowDefinition{{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: 9999, Strategy: domain.StrategyClaim}}})
	_, iss, _ = ws.Publish(context.Background(), c, bad, nil)
	if len(iss) == 0 {
		t.Fatal("bad desk want issues")
	}
	good := mustCanon(t, domain.WorkflowDefinition{{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: desk, Strategy: domain.StrategyClaim}}})
	vid, iss, err := ws.Publish(context.Background(), c, good, nil)
	if err != nil || len(iss) != 0 || vid == 0 {
		t.Fatalf("publish %v %v %d", err, iss, vid)
	}
	var vno int
	var steps string
	s.db.QueryRow(`SELECT version_no, steps_json FROM workflow_versions WHERE id=?`, vid).Scan(&vno, &steps)
	if vno != 1 || steps != string(good) {
		t.Fatalf("vno %d steps %q", vno, steps)
	}
	var cur *int64
	s.db.QueryRow(`SELECT current_version_id FROM category_workflows WHERE category_id=?`, c).Scan(&cur)
	if cur == nil || *cur != vid {
		t.Fatal("current not switched")
	}
	got, _ := ws.GetDraft(context.Background(), c)
	if string(got) != string(good) {
		t.Fatal("draft not stored")
	}
	vid2, iss, err := ws.Publish(context.Background(), c, good, nil)
	if err != nil || len(iss) != 0 || vid2 == vid {
		t.Fatalf("republish %v %v %d", err, iss, vid2)
	}
	var vno2 int
	s.db.QueryRow(`SELECT version_no FROM workflow_versions WHERE id=?`, vid2).Scan(&vno2)
	if vno2 != 2 {
		t.Fatalf("want 2 got %d", vno2)
	}
	var before int
	s.db.QueryRow(`SELECT COUNT(*) FROM workflow_versions WHERE category_id=?`, c).Scan(&before)
	_, _, _ = ws.Publish(context.Background(), c, bad, nil)
	var after int
	s.db.QueryRow(`SELECT COUNT(*) FROM workflow_versions WHERE category_id=?`, c).Scan(&after)
	if after != before {
		t.Fatal("leaked")
	}
	var curAfter int64
	s.db.QueryRow(`SELECT current_version_id FROM category_workflows WHERE category_id=?`, c).Scan(&curAfter)
	if curAfter != vid2 {
		t.Fatal("pointer changed")
	}
	if _, err := s.db.ExecContext(context.Background(), `UPDATE workflow_versions SET steps_json='[]' WHERE id=?`, vid); err == nil {
		t.Fatal("trigger must block")
	}
}
func TestWorkflowStore_Summaries(t *testing.T) {
	s := newTestDB(t)
	cNone := seedCategory(t, s, "none")
	cDraft := seedCategory(t, s, "draft")
	cPub := seedCategory(t, s, "pub")
	desk := seedDesk(t, s, "DeskS")
	ws := s.WorkflowStore()
	ws.UpsertDraft(context.Background(), cDraft, mustCanon(t, domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "a"}}}))
	ws.Publish(context.Background(), cPub, mustCanon(t, domain.WorkflowDefinition{{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: desk, Strategy: domain.StrategyClaim}}}), nil)
	sums, _ := ws.ListSummaries(context.Background())
	m := map[int64]string{}
	for _, v := range sums {
		m[v.CategoryID] = v.Badge
	}
	if m[cNone] != "none" || m[cDraft] != "Draft" || m[cPub] != "Published" {
		t.Fatalf("badges %v", m)
	}
	if strings.Contains(m[cPub], "v") {
		t.Fatalf("equal published draft must show exactly Published without vN, got %q", m[cPub])
	}
	divergent := mustCanon(t, domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "diff"}}})
	ws.UpsertDraft(context.Background(), cPub, divergent)
	sums, _ = ws.ListSummaries(context.Background())
	for _, v := range sums {
		if v.CategoryID == cPub && v.Badge != "Draft" {
			t.Fatalf("diff %q", v.Badge)
		}
	}
	// Draft edits never touch immutable versions: rows and numbers stay fixed,
	// and reconverging the draft restores exactly "Published".
	good := mustCanon(t, domain.WorkflowDefinition{{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: desk, Strategy: domain.StrategyClaim}}})
	var vid int64
	var vno int
	if err := s.db.QueryRow(`SELECT id, version_no FROM workflow_versions WHERE category_id=? ORDER BY version_no DESC LIMIT 1`, cPub).Scan(&vid, &vno); err != nil || vno != 1 {
		t.Fatalf("published version before reconverge: (%v, %d)", err, vno)
	}
	ws.UpsertDraft(context.Background(), cPub, good)
	sums, _ = ws.ListSummaries(context.Background())
	for _, v := range sums {
		if v.CategoryID == cPub && v.Badge != "Published" {
			t.Fatalf("reconverged draft badge = %q, want exactly Published", v.Badge)
		}
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM workflow_versions WHERE category_id=?`, cPub).Scan(&n); err != nil || n != 1 {
		t.Fatalf("draft upserts must not create versions, rows = %d (%v)", n, err)
	}
	var curID int64
	var curNo int
	if err := s.db.QueryRow(`SELECT wv.id, wv.version_no FROM workflow_versions wv JOIN category_workflows cw ON cw.current_version_id=wv.id WHERE cw.category_id=?`, cPub).Scan(&curID, &curNo); err != nil || curID != vid || curNo != 1 {
		t.Fatalf("current version pointer must stay immutable, got (%v, %d, %d)", curID, curNo, err)
	}
	avail, _ := ws.ListAvailableCategories(context.Background())
	ids := map[int64]bool{}
	for _, c := range avail {
		ids[c.ID] = true
	}
	if ids[cNone] || ids[cDraft] || !ids[cPub] {
		t.Fatalf("available %v", ids)
	}
}
func TestWorkflowStore_CascadeNullPin(t *testing.T) {
	s := newTestDB(t)
	c := seedCategory(t, s, "cascade")
	desk := seedDesk(t, s, "DeskC")
	ws := s.WorkflowStore()
	def := mustCanon(t, domain.WorkflowDefinition{{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: desk, Strategy: domain.StrategyClaim}}})
	ws.Publish(context.Background(), c, def, nil)
	var vid int64
	s.db.QueryRow(`SELECT current_version_id FROM category_workflows WHERE category_id=?`, c).Scan(&vid)
	s.db.ExecContext(context.Background(), `INSERT INTO tickets (number,title,description,requester_name,requester_email,category_id,priority,state,created_at,updated_at,workflow_version_id) VALUES (1,'t','', 'r','e',?,'medium','new','2026-08-06T10:00:00Z','2026-08-06T10:00:00Z',NULL)`, c)
	s.db.ExecContext(context.Background(), `INSERT INTO tickets (number,title,description,requester_name,requester_email,category_id,priority,state,created_at,updated_at,workflow_version_id) VALUES (2,'t2','', 'r','e',?,'medium','new','2026-08-06T10:00:00Z','2026-08-06T10:00:00Z',?)`, c, vid)
	if _, err := s.db.ExecContext(context.Background(), `INSERT INTO tickets (number,title,description,requester_name,requester_email,category_id,priority,state,created_at,updated_at,workflow_version_id) VALUES (3,'t3','', 'r','e',?,'medium','new','2026-08-06T10:00:00Z','2026-08-06T10:00:00Z',99999)`, c); err == nil {
		t.Fatal("bad pin must fail")
	}
	if _, err := s.db.ExecContext(context.Background(), `DELETE FROM categories WHERE id=?`, c); err == nil {
		t.Fatal("delete with tickets must fail")
	}
	s.db.ExecContext(context.Background(), `DELETE FROM tickets WHERE category_id=?`, c)
	if _, err := s.db.ExecContext(context.Background(), `DELETE FROM categories WHERE id=?`, c); err != nil {
		t.Fatal(err)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM category_workflows WHERE category_id=?`, c).Scan(&n)
	if n != 0 {
		t.Fatalf("cw %d", n)
	}
	s.db.QueryRow(`SELECT COUNT(*) FROM workflow_versions WHERE category_id=?`, c).Scan(&n)
	if n != 0 {
		t.Fatalf("wv %d", n)
	}
	c2 := seedCategory(t, s, "cascade2")
	ws.Publish(context.Background(), c2, def, nil)
	s.db.ExecContext(context.Background(), `DELETE FROM categories WHERE id=?`, c2)
	s.db.QueryRow(`SELECT COUNT(*) FROM category_workflows WHERE category_id=?`, c2).Scan(&n)
	if n != 0 {
		t.Fatalf("cw2 %d", n)
	}
}
