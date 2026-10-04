package sqlite

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
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
	// A desk with an eligible member: an assignment step routing to an empty desk
	// is refused at publish, because a ticket routed there can never move.
	desk := seedDeskWithMemberNamed(t, s, seedUserRaw(t, s, "Pub Agent", "pub@tkt.test", "agent"), "DeskPub")
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
	desk := seedDeskWithMemberNamed(t, s, seedUserRaw(t, s, "Sum Agent", "sum@tkt.test", "agent"), "DeskS")
	ws := s.WorkflowStore()
	ws.UpsertDraft(context.Background(), cDraft, mustCanon(t, domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "a"}}}))
	ws.Publish(context.Background(), cPub, mustCanon(t, domain.WorkflowDefinition{{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: desk, Strategy: domain.StrategyClaim}}}), nil)
	sums, _ := ws.ListSummaries(context.Background())
	m := map[int64]application.WorkflowSummary{}
	for _, v := range sums {
		m[v.CategoryID] = v
	}
	// Facts, not three words. "Never published" and "published with edits not yet
	// live" used to be the SAME value here, which is exactly what the index
	// rendered as one word.
	if got := m[cNone]; got.Version != 0 || got.HasDraft || got.PendingSteps != 0 {
		t.Fatalf("not configured: %+v", got)
	}
	if got := m[cDraft]; got.Version != 0 || !got.HasDraft {
		t.Fatalf("draft never published: %+v", got)
	}
	if got := m[cPub]; got.Version != 1 || !got.HasDraft || got.PendingSteps != 0 {
		t.Fatalf("a published category whose draft matches its live version has no pending work: %+v", got)
	}
	divergent := mustCanon(t, domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "diff"}}})
	ws.UpsertDraft(context.Background(), cPub, divergent)
	sums, _ = ws.ListSummaries(context.Background())
	for _, v := range sums {
		if v.CategoryID == cPub && v.PendingSteps != 1 {
			t.Fatalf("one differing step must report exactly 1 pending change: %+v", v)
		}
	}
	// Draft edits never touch immutable versions: rows and numbers stay fixed,
	// and reconverging the draft restores a state with no pending work.
	good := mustCanon(t, domain.WorkflowDefinition{{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: desk, Strategy: domain.StrategyClaim}}})
	var vid int64
	var vno int
	if err := s.db.QueryRow(`SELECT id, version_no FROM workflow_versions WHERE category_id=? ORDER BY version_no DESC LIMIT 1`, cPub).Scan(&vid, &vno); err != nil || vno != 1 {
		t.Fatalf("published version before reconverge: (%v, %d)", err, vno)
	}
	ws.UpsertDraft(context.Background(), cPub, good)
	sums, _ = ws.ListSummaries(context.Background())
	for _, v := range sums {
		if v.CategoryID == cPub && v.PendingSteps != 0 {
			t.Fatalf("reconverged draft must report no pending work: %+v", v)
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
	// The state the publish gate exists to prevent, and the one the index could
	// not show. Publish can no longer create it — an assignment step on an empty
	// desk is refused — but operation still can: the desk loses its last member
	// after a healthy publish. A published category that cannot take tickets MUST
	// NOT read as a healthy one, or the admin has no way to discover it.
	if _, err := s.db.Exec(`DELETE FROM desk_members WHERE desk_id=?`, desk); err != nil {
		t.Fatalf("empty the desk: %v", err)
	}
	sums, _ = ws.ListSummaries(context.Background())
	for _, v := range sums {
		if v.CategoryID == cPub && v.CannotRun == "" {
			t.Fatalf("a published category whose desk has no member must say why it cannot run: %+v", v)
		}
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

// TestWorkflowStore_SummariesCountsOpenTickets pins what "Open" means: not closed
// and not cancelled. Resolved COUNTS, because an unconfirmed resolution is still
// work somebody owes. Nothing else in the app reported this number, so the row
// could say a category was broken without saying whether anything was waiting.
func TestWorkflowStore_SummariesCountsOpenTickets(t *testing.T) {
	s := newTestDB(t)
	c := seedCategory(t, s, "opens")
	other := seedCategory(t, s, "quiet")
	ws := s.WorkflowStore()
	insert := `INSERT INTO tickets (number,title,description,requester_name,requester_email,category_id,priority,state,created_at,updated_at,workflow_version_id)
	                     VALUES (?, 't', '', 'r', 'e', ?, 'medium', ?, '2026-08-06T10:00:00Z', '2026-08-06T10:00:00Z', NULL)`
	for i, st := range []string{"new", "in_progress", "resolved", "closed", "cancelled"} {
		if _, err := s.db.Exec(insert, i+1, c, st); err != nil {
			t.Fatalf("insert %s: %v", st, err)
		}
	}
	sums, err := ws.ListSummaries(context.Background())
	if err != nil {
		t.Fatalf("summaries: %v", err)
	}
	seen := map[int64]int{}
	for _, v := range sums {
		seen[v.CategoryID] = v.OpenTickets
	}
	if seen[c] != 3 {
		t.Fatalf("open tickets = %d, want 3 (new, in_progress, resolved)", seen[c])
	}
	if _, ok := seen[other]; !ok {
		t.Fatal("a category with no tickets must still appear, reporting 0")
	}
	if seen[other] != 0 {
		t.Fatalf("a category with no tickets must report 0, got %d", seen[other])
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

// TestWorkflowStore_SaveDraftIfRevisionRefusesStaleWriter is the issue #254
// falsification test at the store boundary: two writers carry the SAME
// expected revision (the stale tab and the tab that saved first). The first
// write must win, the second must be refused with ErrDraftRevisionConflict,
// and the winner's bytes must be the stored draft. Sequential on purpose: no
// sleeps, no polling, no t.Parallel.
func TestWorkflowStore_SaveDraftIfRevisionRefusesStaleWriter(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "cat-rev")
	ws := newWorkflowStore(s.db)
	ctx := context.Background()

	winner := mustCanon(t, domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "winner"}}})
	stale := mustCanon(t, domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "stale tab"}}})

	next, err := ws.SaveDraftIfRevision(ctx, cat, 0, winner)
	if err != nil {
		t.Fatalf("first writer: %v", err)
	}
	if next != 1 {
		t.Fatalf("first guarded write revision = %d, want 1", next)
	}

	// The stale tab carries the SAME expected revision 0.
	if _, err := ws.SaveDraftIfRevision(ctx, cat, 0, stale); !errors.Is(err, application.ErrDraftRevisionConflict) {
		t.Fatalf("stale write err = %v, want ErrDraftRevisionConflict", err)
	}

	got, revision, err := ws.GetDraftWithRevision(ctx, cat)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(winner) {
		t.Fatalf("stored draft = %s, want the winner's %s", got, winner)
	}
	if revision != 1 {
		t.Fatalf("stored revision = %d, want 1 (a refused write must not advance it)", revision)
	}
}

// The positive path: the correct revision succeeds and advances the revision,
// and the second write is accepted where the stale one was not.
func TestWorkflowStore_SaveDraftIfRevisionAdvancesOnCorrectRevision(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "cat-rev-advance")
	ws := newWorkflowStore(s.db)
	ctx := context.Background()

	first := mustCanon(t, domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "one"}}})
	second := mustCanon(t, domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "two"}}})

	if next, err := ws.SaveDraftIfRevision(ctx, cat, 0, first); err != nil || next != 1 {
		t.Fatalf("first write next=%d err=%v, want 1 and nil", next, err)
	}
	if next, err := ws.SaveDraftIfRevision(ctx, cat, 1, second); err != nil || next != 2 {
		t.Fatalf("second write next=%d err=%v, want 2 and nil", next, err)
	}
	got, revision, err := ws.GetDraftWithRevision(ctx, cat)
	if err != nil {
		t.Fatal(err)
	}
	if revision != 2 || string(got) != string(second) {
		t.Fatalf("after two guarded writes: revision=%d draft=%s, want 2 and %s", revision, got, second)
	}
}

// A category that was never edited has no row and no revision: both reads
// answer (nil, 0, nil) without creating a row.
func TestWorkflowStore_GetDraftWithRevisionAbsentIsZero(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "cat-rev-absent")
	ws := newWorkflowStore(s.db)

	got, revision, err := ws.GetDraftWithRevision(context.Background(), cat)
	if err != nil || got != nil || revision != 0 {
		t.Fatalf("absent draft = (%q, %d, %v), want (nil, 0, nil)", got, revision, err)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM category_workflows WHERE category_id=?`, cat).Scan(&n)
	if n != 0 {
		t.Fatalf("reading an absent revision created a row (%d)", n)
	}
}

// TestWorkflowStore_PublishAtRevisionRefusesStaleWriter is the publish-side
// falsification test at the store boundary: publishing superseded bytes with an
// out-of-date revision must write NOTHING — no draft overwrite and no version —
// and report ErrDraftRevisionConflict.
func TestWorkflowStore_PublishAtRevisionRefusesStaleWriter(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "cat-pub-rev")
	ws := newWorkflowStore(s.db)
	ctx := context.Background()

	newer := mustCanon(t, domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "newer"}}})
	stale := mustCanon(t, domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "stale"}}})

	// A newer draft is already at revision 1.
	if rev, err := ws.SaveDraftIfRevision(ctx, cat, 0, newer); err != nil || rev != 1 {
		t.Fatalf("advance revision: rev=%d err=%v, want 1 and nil", rev, err)
	}

	// The stale tab still publishes at revision 0.
	if _, _, _, err := ws.PublishAtRevision(ctx, cat, stale, 0, nil); !errors.Is(err, application.ErrDraftRevisionConflict) {
		t.Fatalf("stale publish err = %v, want ErrDraftRevisionConflict", err)
	}
	got, revision, err := ws.GetDraftWithRevision(ctx, cat)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(newer) || revision != 1 {
		t.Fatalf("after refused publish: revision=%d draft=%s, want 1 and the newer bytes", revision, got)
	}
	var versions int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM workflow_versions WHERE category_id=?`, cat).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 0 {
		t.Fatalf("a refused publish created %d versions, want 0", versions)
	}
}

// TestWorkflowStore_CloneConcurrentWritesHaveOneWinner drives two consumers of
// the clone use case at the same empty target. Whatever the interleaving, the
// target draft is written ONCE: either the two guarded writes race and the
// compare-and-swap refuses the loser, or the second clone sees the first's
// bytes at its emptiness check and is refused there. The winner's bytes are the
// source's, and the target's revision advances exactly once. No sleeps, no
// polling, no t.Parallel.
func TestWorkflowStore_CloneConcurrentWritesHaveOneWinner(t *testing.T) {
	s := newTestDB(t)
	source := seedCategory(t, s, "clone-source")
	target := seedCategory(t, s, "clone-target")
	ws := s.WorkflowStore()
	ctx := context.Background()
	def := mustCanon(t, domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "concurrent clone"}}})
	if _, iss, err := ws.Publish(ctx, source, def, nil); err != nil || len(iss) != 0 {
		t.Fatalf("publish source: iss=%v err=%v", iss, err)
	}
	svc := application.NewWorkflowService(ws)
	admin := domain.User{ID: 1, Role: domain.RoleAdmin, Active: true}

	const clones = 2
	errs := make([]error, clones)
	var wg sync.WaitGroup
	wg.Add(clones)
	for i := 0; i < clones; i++ {
		go func(i int) { defer wg.Done(); errs[i] = svc.Clone(ctx, admin, source, target) }(i)
	}
	wg.Wait()

	succeeded, refused := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case strings.Contains(err.Error(), "already has a draft"):
			refused++
		default:
			t.Fatalf("unexpected clone error: %v", err)
		}
	}
	if succeeded != 1 || refused != 1 {
		t.Fatalf("concurrent clones: %d succeeded, %d refused; want exactly one of each (%v)", succeeded, refused, errs)
	}
	got, revision, err := ws.(application.WorkflowDraftRevisionStore).GetDraftWithRevision(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(def) {
		t.Fatalf("stored draft = %s, want the winner's bytes %s", got, def)
	}
	if revision != 1 {
		t.Fatalf("target revision = %d, want exactly 1 accepted write", revision)
	}
	var versions int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workflow_versions WHERE category_id=?`, target).Scan(&versions); err != nil || versions != 0 {
		t.Fatalf("clone must not publish; target versions = %d (%v)", versions, err)
	}
}

// The positive path: a publish at the correct revision creates the version,
// advances the revision, and leaves a later save at the pre-publish revision
// refused.
func TestWorkflowStore_PublishAtRevisionAdvancesAndBlocksOldRevision(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "cat-pub-advance")
	ws := newWorkflowStore(s.db)
	ctx := context.Background()
	draft := mustCanon(t, domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "one"}}})

	vid, revision, iss, err := ws.PublishAtRevision(ctx, cat, draft, 0, nil)
	if err != nil || len(iss) != 0 || vid == 0 || revision != 1 {
		t.Fatalf("publish: vid=%d revision=%d iss=%v err=%v, want a version, 1, none, nil", vid, revision, iss, err)
	}
	if got, stored, err := ws.GetDraftWithRevision(ctx, cat); err != nil || string(got) != string(draft) || stored != 1 {
		t.Fatalf("after publish: revision=%d draft=%s err=%v, want 1 and the published bytes", stored, got, err)
	}
	if _, err := ws.SaveDraftIfRevision(ctx, cat, 0, draft); !errors.Is(err, application.ErrDraftRevisionConflict) {
		t.Fatalf("save at the pre-publish revision err = %v, want ErrDraftRevisionConflict", err)
	}
	if next, err := ws.SaveDraftIfRevision(ctx, cat, revision, draft); err != nil || next != 2 {
		t.Fatalf("save at the published revision: next=%d err=%v, want 2 and nil", next, err)
	}
}
