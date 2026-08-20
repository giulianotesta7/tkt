package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

type workflowStore struct{ db *sql.DB }

var _ application.WorkflowStore = (*workflowStore)(nil)

func newWorkflowStore(db *sql.DB) *workflowStore { return &workflowStore{db: db} }
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
func (w *workflowStore) Publish(ctx context.Context, categoryID int64, draft []byte, by *int64) (int64, []domain.WorkflowValidationIssue, error) {
	var iss []domain.WorkflowValidationIssue
	if len(draft) == 0 {
		iss = append(iss, domain.WorkflowValidationIssue{Step: 1, Field: "steps", Message: "workflow must have at least one step"})
		return 0, iss, nil
	}
	def, err := domain.ParseWorkflowDefinition(draft)
	if err != nil {
		return 0, []domain.WorkflowValidationIssue{{Step: 1, Field: "steps", Message: err.Error()}}, nil
	}
	iss = def.Validate()
	if len(iss) > 0 {
		return 0, iss, nil
	}
	for i, st := range def {
		if st.Type == domain.StepAssignToDesk && st.AssignToDesk != nil {
			var one int
			if err := w.db.QueryRowContext(ctx, `SELECT 1 FROM desks WHERE id=?`, st.AssignToDesk.DeskID).Scan(&one); err != nil {
				if err == sql.ErrNoRows {
					iss = append(iss, domain.WorkflowValidationIssue{Step: i + 1, Field: "desk_id", Message: fmt.Sprintf("Step %d: choose a desk", i+1)})
				} else {
					return 0, nil, fmt.Errorf("sqlite: check desk: %w", err)
				}
			}
		}
	}
	if len(iss) > 0 {
		return 0, iss, nil
	}
	tx, err := beginImmediate(ctx, w.db, "publish")
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO category_workflows(category_id, draft_json) VALUES (?, '[]') ON CONFLICT(category_id) DO NOTHING`, categoryID); err != nil {
		return 0, nil, fmt.Errorf("sqlite: publish ensure: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE category_workflows SET draft_json=? WHERE category_id=?`, string(draft), categoryID); err != nil {
		return 0, nil, fmt.Errorf("sqlite: publish draft: %w", err)
	}
	var next int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version_no),0)+1 FROM workflow_versions WHERE category_id=?`, categoryID).Scan(&next); err != nil {
		return 0, nil, fmt.Errorf("sqlite: next version: %w", err)
	}
	now := formatTime(time.Now().UTC())
	res, err := tx.ExecContext(ctx, `INSERT INTO workflow_versions(category_id, version_no, steps_json, published_by_user_id, published_at) VALUES (?,?,?,?,?)`, categoryID, next, string(draft), nullableInt64(by), now)
	if err != nil {
		return 0, nil, fmt.Errorf("sqlite: insert version: %w", err)
	}
	vid, err := res.LastInsertId()
	if err != nil {
		return 0, nil, fmt.Errorf("sqlite: version id: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE category_workflows SET current_version_id=? WHERE category_id=?`, vid, categoryID); err != nil {
		return 0, nil, fmt.Errorf("sqlite: switch current: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, nil, fmt.Errorf("sqlite: publish commit: %w", err)
	}
	return vid, nil, nil
}
func (w *workflowStore) ListSummaries(ctx context.Context) ([]application.WorkflowSummary, error) {
	rows, err := w.db.QueryContext(ctx, `SELECT c.id, c.name, cw.draft_json, cw.current_version_id, wv.version_no, wv.steps_json FROM categories c LEFT JOIN category_workflows cw ON cw.category_id=c.id LEFT JOIN workflow_versions wv ON wv.id=cw.current_version_id ORDER BY c.id ASC`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list summaries: %w", err)
	}
	defer rows.Close()
	var out []application.WorkflowSummary
	for rows.Next() {
		var cid int64
		var cname string
		var draft sql.NullString
		var cur sql.NullInt64
		var vno sql.NullInt64
		var steps sql.NullString
		if err := rows.Scan(&cid, &cname, &draft, &cur, &vno, &steps); err != nil {
			return nil, fmt.Errorf("sqlite: scan summary: %w", err)
		}
		badge := "none"
		if draft.Valid {
			if !cur.Valid {
				badge = "Draft"
			} else if vno.Valid && steps.Valid && draft.String == steps.String {
				badge = fmt.Sprintf("Published v%d", vno.Int64)
			} else {
				badge = "Draft"
			}
		}
		out = append(out, application.WorkflowSummary{CategoryID: cid, CategoryName: cname, Badge: badge})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: summaries rows: %w", err)
	}
	return out, nil
}
func (w *workflowStore) ListAvailableCategories(ctx context.Context) ([]domain.Category, error) {
	rows, err := w.db.QueryContext(ctx, `SELECT c.id, c.name, c.created_at FROM categories c JOIN category_workflows cw ON cw.category_id=c.id WHERE cw.current_version_id IS NOT NULL ORDER BY c.id ASC`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: available: %w", err)
	}
	defer rows.Close()
	var out []domain.Category
	for rows.Next() {
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
