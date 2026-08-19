package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// auditStore implements application.AuditStore (task 4.3): the append-only
// audit trail (audit-log spec). Append is atomic — the whole batch lands in
// one immediate transaction or none of it does.
type auditStore struct {
	db *sql.DB
}

var _ application.AuditStore = (*auditStore)(nil)

func newAuditStore(db *sql.DB) *auditStore { return &auditStore{db: db} }

// Append stores all events in occurrence order (one mutation batch) in a
// single immediate transaction.
func (as *auditStore) Append(ctx context.Context, events ...domain.AuditEvent) error {
	if len(events) == 0 {
		return nil
	}
	tx, err := as.db.BeginTx(ctx, nil) // _txlock=immediate
	if err != nil {
		return fmt.Errorf("sqlite: begin append: %w", err)
	}
	if err := appendAuditEventsTx(ctx, tx, events...); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit append: %w", err)
	}
	return nil
}

// ListByTicket returns the ticket's audit events in occurrence order (ASC);
// the id tiebreak preserves the order within a same-timestamp batch. The
// domain AuditEvent carries no id (port contract); the row id orders only.
func (as *auditStore) ListByTicket(ctx context.Context, ticketID int64) ([]domain.AuditEvent, error) {
	rows, err := as.db.QueryContext(ctx,
		`SELECT ticket_id, actor, action, field, from_value, to_value, note, actor_user_id, reason, created_at
		 FROM audit_events WHERE ticket_id = ? ORDER BY created_at ASC, id ASC`, ticketID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list audit events: %w", err)
	}
	defer rows.Close()

	var out []domain.AuditEvent
	for rows.Next() {
		var e domain.AuditEvent
		var field, fromValue, toValue, note, reason sql.NullString
		var actorUserID sql.NullInt64
		var createdAt string
		if err := rows.Scan(&e.TicketID, &e.Actor, &e.Action, &field, &fromValue, &toValue, &note, &actorUserID, &reason, &createdAt); err != nil {
			return nil, fmt.Errorf("sqlite: scan audit event: %w", err)
		}
		e.Field = nullableStringPtr(field)
		e.FromValue = nullableStringPtr(fromValue)
		e.ToValue = nullableStringPtr(toValue)
		e.Note = nullableStringPtr(note)
		e.Reason = nullableStringPtr(reason)
		if actorUserID.Valid {
			v := actorUserID.Int64
			e.ActorUserID = &v
		}
		if e.CreatedAt, err = time.Parse(timeLayout, createdAt); err != nil {
			return nil, fmt.Errorf("sqlite: parse audit created_at %q: %w", createdAt, err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list audit events: %w", err)
	}
	return out, nil
}

// nullableStringPtr converts a scanned NULLable column to a *string.
func nullableStringPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	v := ns.String
	return &v
}
