package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// workflowRunStore resolves a ticket's workflow execution snapshot for the
// completion route and the Pending Actions card (design S9). It reuses the same
// row projections as the unit of work (scanTicketFrom / scanRunRow) so the
// driver-side snapshot matches the persisted facts the adapter rechecks.
type workflowRunStore struct{ db *sql.DB }

func newWorkflowRunStore(db *sql.DB) *workflowRunStore { return &workflowRunStore{db: db} }

var _ application.WorkflowRunStore = (*workflowRunStore)(nil)

// GetWorkflowExecution loads the ticket, its run, and the pinned immutable
// definition in a consistent read. It returns (nil, nil) when the ticket is a
// legacy unpinned ticket or has no run row — there is nothing to complete and no
// Pending Actions card to render. A missing ticket returns ErrNotFound; a
// pinned ticket with an unparsable definition is an infrastructure error.
func (s *workflowRunStore) GetWorkflowExecution(ctx context.Context, ticketID int64) (*application.WorkflowExecutionSnapshot, error) {
	t, err := scanTicketFrom(s.db.QueryRowContext(ctx, `SELECT `+ticketColumns+` FROM tickets t WHERE t.id=?`, ticketID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &domain.NotFoundError{Kind: "ticket", ID: ticketID}
		}
		return nil, err
	}
	if t.WorkflowVersionID == nil {
		return nil, nil // legacy unpinned ticket: no workflow execution
	}
	run, err := scanRunRow(ctx, s.db, ticketID)
	if err != nil {
		var nf *domain.NotFoundError
		if errors.As(err, &nf) {
			return nil, nil // pinned but no run row: nothing to complete
		}
		return nil, err
	}
	var steps string
	if err := s.db.QueryRowContext(ctx, `SELECT steps_json FROM workflow_versions WHERE id=?`, *t.WorkflowVersionID).Scan(&steps); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("sqlite: pinned workflow version %d not found", *t.WorkflowVersionID)
		}
		return nil, fmt.Errorf("sqlite: read pinned workflow: %w", err)
	}
	def, err := domain.ParseWorkflowDefinition([]byte(steps))
	if err != nil {
		return nil, fmt.Errorf("sqlite: parse pinned workflow: %w", err)
	}
	return &application.WorkflowExecutionSnapshot{Ticket: t, Run: run, Workflow: def}, nil
}
