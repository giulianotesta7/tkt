package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

type workflowStore struct{ db *sql.DB }

var _ application.WorkflowStore = (*workflowStore)(nil)
var _ application.WorkflowVersionStore = (*workflowStore)(nil)
var _ application.WorkflowDraftRevisionStore = (*workflowStore)(nil)

func newWorkflowStore(db *sql.DB) *workflowStore { return &workflowStore{db: db} }

// GetCurrentVersion resolves a category's current published workflow for ticket
// creation (WorkflowVersionStore, design S5). Availability is a published
// version: the query reads category_workflows.current_version_id joined to the
// IMMUTABLE workflow_versions.steps_json and NEVER touches draft_json. A
// category with no row or a NULL current pointer returns (nil, nil) — the
// application answers the exact 422 category-unavailable message and writes
// nothing. The returned PublishedWorkflow.Workflow is parsed fresh from the
// stored canonical JSON, so it is a deep INDEPENDENT snapshot owned by the
// caller (no aliasing of store memory).
func (w *workflowStore) GetCurrentVersion(ctx context.Context, categoryID int64) (*application.PublishedWorkflow, error) {
	var (
		ver   int64
		steps string
	)
	err := w.db.QueryRowContext(ctx, `SELECT wv.id, wv.steps_json FROM category_workflows cw
		JOIN workflow_versions wv ON wv.id = cw.current_version_id AND wv.category_id = cw.category_id
		WHERE cw.category_id = ?`, categoryID).Scan(&ver, &steps)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil // draft-only or no workflow row: unavailable
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get current version: %w", err)
	}
	wf, err := domain.ParseWorkflowDefinition([]byte(steps))
	if err != nil {
		return nil, fmt.Errorf("sqlite: parse current workflow: %w", err)
	}
	// The immutable steps_json must be a VALID closed definition: domain
	// validation runs after JSON decode so a corrupt/unknown persisted snapshot
	// returns an error and never escapes as a usable current workflow.
	if iss := wf.Validate(); len(iss) > 0 {
		return nil, fmt.Errorf("sqlite: invalid current workflow: %v", iss)
	}
	return &application.PublishedWorkflow{CategoryID: categoryID, VersionID: ver, Workflow: wf}, nil
}
func (w *workflowStore) GetDraft(ctx context.Context, categoryID int64) ([]byte, error) {
	var d sql.NullString
	err := w.db.QueryRowContext(ctx, `SELECT draft_json FROM category_workflows WHERE category_id=?`, categoryID).Scan(&d)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get draft: %w", err)
	}
	if !d.Valid {
		return nil, nil
	}
	return []byte(d.String), nil
}
func (w *workflowStore) UpsertDraft(ctx context.Context, categoryID int64, draft []byte) error {
	tx, err := beginImmediate(ctx, w.db, "upsert draft")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO category_workflows(category_id, draft_json) VALUES (?, '[]') ON CONFLICT(category_id) DO NOTHING`, categoryID); err != nil {
		return fmt.Errorf("sqlite: upsert insert: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE category_workflows SET draft_json=? WHERE category_id=?`, string(draft), categoryID); err != nil {
		return fmt.Errorf("sqlite: upsert update: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: upsert commit: %w", err)
	}
	return nil
}

// GetDraftWithRevision reads the draft bytes and their revision in ONE
// statement (issue #254). A missing row is (nil, 0, nil): revision 0 is the
// state a never-edited category presents to the builder, not a failure. The
// pair must come from one read, so a builder can never render revision N+1
// next to revision N's bytes.
func (w *workflowStore) GetDraftWithRevision(ctx context.Context, categoryID int64) ([]byte, int64, error) {
	var (
		d   sql.NullString
		rev int64
	)
	err := w.db.QueryRowContext(ctx, `SELECT draft_json, draft_revision FROM category_workflows WHERE category_id=?`, categoryID).Scan(&d, &rev)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("sqlite: get draft revision: %w", err)
	}
	if !d.Valid {
		return nil, rev, nil
	}
	return []byte(d.String), rev, nil
}

// SaveDraftIfRevision is the guarded draft write (issue #254). It is a
// compare-and-swap: the UPDATE carries `draft_revision = expected` in its
// WHERE clause, so a writer whose expected revision is stale updates zero
// rows and is refused with ErrDraftRevisionConflict, leaving the newer
// writer's bytes exactly as they were. The row is created first (revision 0)
// so the first guarded write of a fresh category expects 0 and lands as
// revision 1. Everything runs in the store's immediate transaction, so the
// check and the write cannot interleave with another writer.
func (w *workflowStore) SaveDraftIfRevision(ctx context.Context, categoryID int64, expectedRevision int64, draft []byte) (int64, error) {
	tx, err := beginImmediate(ctx, w.db, "save draft at revision")
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO category_workflows(category_id, draft_json) VALUES (?, '[]') ON CONFLICT(category_id) DO NOTHING`, categoryID); err != nil {
		return 0, fmt.Errorf("sqlite: save draft ensure: %w", err)
	}
	// The compare-and-swap: the UPDATE only matches while the stored revision is
	// still the expected one, so a stale writer updates nothing at all.
	res, err := tx.ExecContext(ctx, `UPDATE category_workflows SET draft_json=?, draft_revision=draft_revision+1 WHERE category_id=? AND draft_revision=?`, string(draft), categoryID, expectedRevision)
	if err != nil {
		return 0, fmt.Errorf("sqlite: save draft update: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlite: save draft rows: %w", err)
	}
	if affected == 0 {
		// Either the row's revision moved past the writer's, or the writer
		// expected a revision on a category that has none yet. Both mean the
		// writer's copy is not the current one, and nothing was written.
		return 0, application.ErrDraftRevisionConflict
	}
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT draft_revision FROM category_workflows WHERE category_id=?`, categoryID).Scan(&revision); err != nil {
		return 0, fmt.Errorf("sqlite: save draft revision: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("sqlite: save draft commit: %w", err)
	}
	return revision, nil
}

// Publish keeps its historical signature for the callers that arrange a
// published version directly (the HTTP harness and adapter fixtures). It
// mirrors PublishAtRevision against the CURRENT revision: the draft write still
// advances draft_revision in the same transaction, so a later save at the
// previous revision cannot overwrite the published draft. Production authoring
// goes through PublishAtRevision, which refuses a stale expectation.
func (w *workflowStore) Publish(ctx context.Context, categoryID int64, draft []byte, by *int64) (int64, []domain.WorkflowValidationIssue, error) {
	vid, _, iss, err := w.publish(ctx, categoryID, draft, by, nil)
	return vid, iss, err
}

// PublishAtRevision is the guarded publish (issue #254): the same validation,
// version allocation and current-pointer switch as Publish, plus a
// compare-and-swap on draft_revision inside the SAME immediate transaction. A
// revision another writer already advanced writes NOTHING and is refused with
// ErrDraftRevisionConflict, leaving that writer's draft bytes and current
// version untouched. It reports the version id and the advanced draft revision.
func (w *workflowStore) PublishAtRevision(ctx context.Context, categoryID int64, draft []byte, expectedRevision int64, by *int64) (int64, int64, []domain.WorkflowValidationIssue, error) {
	return w.publish(ctx, categoryID, draft, by, &expectedRevision)
}

// publish is the single publish transaction. A nil expectedRevision adopts the
// revision the row currently has (the fixture path) instead of refusing a
// mismatch; either way the revision advances by one, so the published draft can
// never be overwritten by a later save carrying the previous revision.
func (w *workflowStore) publish(ctx context.Context, categoryID int64, draft []byte, by *int64, expectedRevision *int64) (int64, int64, []domain.WorkflowValidationIssue, error) {
	var iss []domain.WorkflowValidationIssue
	if len(draft) == 0 {
		iss = append(iss, domain.WorkflowValidationIssue{Step: 1, Field: "steps", Message: "workflow must have at least one step"})
		return 0, 0, iss, nil
	}
	def, err := domain.ParseWorkflowDefinition(draft)
	if err != nil {
		return 0, 0, []domain.WorkflowValidationIssue{{Step: 1, Field: "steps", Message: err.Error()}}, nil
	}
	iss = def.Validate()
	if len(iss) > 0 {
		return 0, 0, iss, nil
	}
	// The membership rules need desk facts, and this lookup is their only reader:
	// domain.AssessRunnable owns both rules, so the gate has no second copy.
	facts, err := w.deskFacts(ctx, def)
	if err != nil {
		return 0, 0, nil, err
	}
	// Only BLOCKERS refuse the publish. A warning describes a workflow that cannot
	// route on its own, and that is a legitimate manual process in this app:
	// creation is unassigned-only by requirement, so a human step with no
	// assignment step before it is how the executor's own fixtures are built.
	// Refusing those would forbid a working operating model.
	if iss = def.AssessRunnable(func(deskID int64) (string, int, bool) {
		f, ok := facts[deskID]
		return f.name, f.members, ok
	}).Blockers; len(iss) > 0 {
		return 0, 0, iss, nil
	}
	tx, err := beginImmediate(ctx, w.db, "publish")
	if err != nil {
		return 0, 0, nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO category_workflows(category_id, draft_json) VALUES (?, '[]') ON CONFLICT(category_id) DO NOTHING`, categoryID); err != nil {
		return 0, 0, nil, fmt.Errorf("sqlite: publish ensure: %w", err)
	}
	expected := expectedRevision
	if expected == nil {
		var current int64
		if err := tx.QueryRowContext(ctx, `SELECT draft_revision FROM category_workflows WHERE category_id=?`, categoryID).Scan(&current); err != nil {
			return 0, 0, nil, fmt.Errorf("sqlite: publish current revision: %w", err)
		}
		expected = &current
	}
	// The compare-and-swap: the write lands only while the stored revision is
	// still the expected one, and it advances the revision as it writes.
	res, err := tx.ExecContext(ctx, `UPDATE category_workflows SET draft_json=?, draft_revision=draft_revision+1 WHERE category_id=? AND draft_revision=?`, string(draft), categoryID, *expected)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("sqlite: publish draft: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, 0, nil, fmt.Errorf("sqlite: publish draft rows: %w", err)
	}
	if affected == 0 {
		return 0, 0, nil, application.ErrDraftRevisionConflict
	}
	revision := *expected + 1
	var next int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version_no),0)+1 FROM workflow_versions WHERE category_id=?`, categoryID).Scan(&next); err != nil {
		return 0, 0, nil, fmt.Errorf("sqlite: next version: %w", err)
	}
	now := formatTime(time.Now().UTC())
	res, err = tx.ExecContext(ctx, `INSERT INTO workflow_versions(category_id, version_no, steps_json, published_by_user_id, published_at) VALUES (?,?,?,?,?)`, categoryID, next, string(draft), nullableInt64(by), now)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("sqlite: insert version: %w", err)
	}
	vid, err := res.LastInsertId()
	if err != nil {
		return 0, 0, nil, fmt.Errorf("sqlite: version id: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE category_workflows SET current_version_id=? WHERE category_id=?`, vid, categoryID); err != nil {
		return 0, 0, nil, fmt.Errorf("sqlite: switch current: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, nil, fmt.Errorf("sqlite: publish commit: %w", err)
	}
	return vid, revision, nil, nil
}

// deskFact is one desk's name and the number of members who could actually act on
// a ticket: active users holding agent, admin or root.
type deskFact struct {
	name    string
	members int
}

// deskFacts resolves the desks a definition assigns to, once per desk.
//
// It delegates to allDeskFacts because the eligibility predicate has to be
// written once: it must match the executor's least_loaded selection
// (leastLoadedAssigneeTx: active, agent or above), or the publish gate and the
// assignment could disagree about who is available.
func (w *workflowStore) deskFacts(ctx context.Context, def domain.WorkflowDefinition) (map[int64]deskFact, error) {
	all, err := w.allDeskFacts(ctx)
	if err != nil {
		return nil, err
	}
	facts := map[int64]deskFact{}
	for _, st := range def {
		if st.Type != domain.StepAssignToDesk || st.AssignToDesk == nil {
			continue
		}
		if f, ok := all[st.AssignToDesk.DeskID]; ok {
			facts[st.AssignToDesk.DeskID] = f
		}
	}
	return facts, nil
}

// allDeskFacts resolves every desk's name and the number of members who could
// actually act on a ticket. An absent desk is simply not in the map, which is
// what makes the rule report "choose a desk".
func (w *workflowStore) allDeskFacts(ctx context.Context) (map[int64]deskFact, error) {
	rows, err := w.db.QueryContext(ctx, `
		SELECT d.id, d.name,
		       (SELECT COUNT(*)
		          FROM desk_members dm
		          JOIN users u ON u.id = dm.user_id
		         WHERE dm.desk_id = d.id
		           AND u.active = 1
		           AND u.role IN ('agent', 'admin', 'root'))
		  FROM desks d`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: read desk facts: %w", err)
	}
	defer rows.Close()
	facts := map[int64]deskFact{}
	for rows.Next() {
		var id int64
		var f deskFact
		if err := rows.Scan(&id, &f.name, &f.members); err != nil {
			return nil, fmt.Errorf("sqlite: scan desk facts: %w", err)
		}
		facts[id] = f
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: desk facts rows: %w", err)
	}
	return facts, nil
}

func (w *workflowStore) ListSummaries(ctx context.Context) ([]application.WorkflowSummary, error) {
	// Two passes on purpose. This store's pool can hold a single connection (the
	// test DSN does), so issuing a second query while these rows are still open
	// would block on itself. Collect the raw columns, close the rows, then resolve
	// desk facts once for the whole list.
	type rawSummary struct {
		id      int64
		name    string
		draft   sql.NullString
		current sql.NullInt64
		version sql.NullInt64
		steps   sql.NullString
	}
	rows, err := w.db.QueryContext(ctx, `SELECT c.id, c.name, cw.draft_json, cw.current_version_id, wv.version_no, wv.steps_json FROM categories c LEFT JOIN category_workflows cw ON cw.category_id=c.id LEFT JOIN workflow_versions wv ON wv.id=cw.current_version_id ORDER BY c.id ASC`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list summaries: %w", err)
	}
	var raw []rawSummary
	for rows.Next() {
		var r rawSummary
		if err := rows.Scan(&r.id, &r.name, &r.draft, &r.current, &r.version, &r.steps); err != nil {
			rows.Close()
			return nil, fmt.Errorf("sqlite: scan summary: %w", err)
		}
		raw = append(raw, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("sqlite: summaries rows: %w", err)
	}
	rows.Close()

	// A second query, deliberately after the cursor above is closed: this store's
	// pool can hold a single connection and a nested query would block on itself.
	// "Open" means not closed and not cancelled — resolved counts, because an
	// unconfirmed resolution is still work somebody owes.
	openByCategory := map[int64]int{}
	countRows, err := w.db.QueryContext(ctx, `
		SELECT category_id, COUNT(*) FROM tickets
		 WHERE state IN ('new', 'in_progress', 'resolved')
		 GROUP BY category_id`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: count open tickets: %w", err)
	}
	for countRows.Next() {
		var cid int64
		var n int
		if err := countRows.Scan(&cid, &n); err != nil {
			countRows.Close()
			return nil, fmt.Errorf("sqlite: scan open tickets: %w", err)
		}
		openByCategory[cid] = n
	}
	if err := countRows.Err(); err != nil {
		countRows.Close()
		return nil, fmt.Errorf("sqlite: open ticket rows: %w", err)
	}
	countRows.Close()

	facts, err := w.allDeskFacts(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]application.WorkflowSummary, 0, len(raw))
	for _, r := range raw {
		s := application.WorkflowSummary{CategoryID: r.id, CategoryName: r.name, HasDraft: r.draft.Valid, OpenTickets: openByCategory[r.id]}
		if r.current.Valid && r.version.Valid && r.steps.Valid {
			s.Version = int(r.version.Int64)
			def, perr := domain.ParseWorkflowDefinition([]byte(r.steps.String))
			switch {
			case perr != nil:
				// A published definition that cannot be read is a broken category,
				// not a silent one: say so rather than reporting nothing.
				s.CannotRun = "its published definition cannot be read"
			default:
				if a := def.AssessRunnable(func(deskID int64) (string, int, bool) {
					f, ok := facts[deskID]
					return f.name, f.members, ok
				}); len(a.Blockers) > 0 {
					s.CannotRun = a.Blockers[0].Reason
				}
			}
			// The draft is compared against the LIVE version, so a draft that
			// canonicalizes to the same bytes reports no pending work at all.
			if r.draft.Valid && r.draft.String != r.steps.String {
				s.PendingSteps = 1
				if dd, derr := domain.ParseWorkflowDefinition([]byte(r.draft.String)); derr == nil && perr == nil {
					s.PendingSteps = domain.CountChanges(dd, def)
				}
			}
		}
		out = append(out, s)
	}
	return out, nil
}
func (w *workflowStore) ListAvailableCategories(ctx context.Context) ([]domain.Category, error) {
	rows, err := w.db.QueryContext(ctx, `SELECT c.id, c.name, c.description, c.desk_id, c.created_at FROM categories c JOIN category_workflows cw ON cw.category_id=c.id WHERE cw.current_version_id IS NOT NULL ORDER BY c.id ASC`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: available: %w", err)
	}
	defer rows.Close()
	var out []domain.Category
	for rows.Next() {
		// scanCategoryFrom consumes all five columns selected above.
		c, err := scanCategoryFrom(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan available: %w", err)
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: available rows: %w", err)
	}
	return out, nil
}
