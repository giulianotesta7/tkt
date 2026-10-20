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
	snap := &application.WorkflowExecutionSnapshot{Ticket: t, Run: run, Workflow: def}
	// Notification policy facts for the two candidate recipients of a
	// workflow-driven change (S10): the application decides who is notified, and
	// it can only prove "can open the ticket" with the role and active state.
	// A user row that no longer exists stays nil — never notified.
	if snap.Requester, err = notificationRecipientByID(s.db, ctx, t.RequesterUserID); err != nil {
		return nil, err
	}
	if snap.Assignee, err = notificationRecipientByID(s.db, ctx, t.UserID); err != nil {
		return nil, err
	}
	return snap, nil
}

// notificationRecipientByID reads one user's role and active state as the
// notification policy input. A nil id or a missing row answers nil (no
// candidate); any other failure propagates so the caller never decides on
// unproven facts.
func notificationRecipientByID(db *sql.DB, ctx context.Context, userID *int64) (*application.NotificationRecipient, error) {
	if userID == nil {
		return nil, nil
	}
	var (
		r      application.NotificationRecipient
		role   string
		active int
	)
	err := db.QueryRowContext(ctx, `SELECT id, role, active FROM users WHERE id = ?`, *userID).Scan(&r.UserID, &role, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: read notification recipient %d: %w", *userID, err)
	}
	r.Role = domain.Role(role)
	r.Active = active == 1
	return &r, nil
}
