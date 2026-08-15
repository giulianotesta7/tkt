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

// ticketStore implements application.TicketStore (task 4.2). Reads go
// through the shared filter builder (filters.go); the mutation methods
// (Create/Update) run in immediate transactions.
type ticketStore struct {
	db *sql.DB
}

var _ application.TicketStore = (*ticketStore)(nil)

func newTicketStore(db *sql.DB) *ticketStore { return &ticketStore{db: db} }

// unitOfWork implements application.TicketUnitOfWork (C1): the ticket write
// and its audit events persist in ONE immediate transaction, and a failed
// audit append rolls the ticket write back — a mutation can never be
// committed without its audit trail (no-silent-mutations contract).
type unitOfWork struct {
	db *sql.DB
}

var _ application.TicketUnitOfWork = (*unitOfWork)(nil)

func newUnitOfWork(db *sql.DB) *unitOfWork { return &unitOfWork{db: db} }

// timeLayout is the persisted timestamp format (D7): ISO-8601 UTC TEXT.
const timeLayout = time.RFC3339

// ticketColumns is the SELECT column list shared by every ticket read.
const ticketColumns = `t.id, t.number, t.title, t.description, t.requester_name, t.requester_email, t.requester_user_id, t.category_id, t.priority, t.state, t.user_id, t.created_at, t.updated_at, t.resolved_at, t.closed_at`

// Create persists t inside an immediate transaction, assigning the
// store-owned identity fields: ID (autoincrement) and Number (MAX+1, D8).
// The UNIQUE backstop retries the whole insert up to 3 times if a number
// collision ever slips through (belt-and-suspenders; _txlock=immediate
// serializes writers so it cannot happen in practice).
func (st *ticketStore) Create(ctx context.Context, t *domain.Ticket) error {
	return retryUnique(3, func() error {
		tx, err := st.db.BeginTx(ctx, nil) // _txlock=immediate → BEGIN IMMEDIATE
		if err != nil {
			return fmt.Errorf("sqlite: begin create: %w", err)
		}
		if err := createTicketTx(ctx, tx, t); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("sqlite: commit create: %w", err)
		}
		return nil
	})
}

// createTicketTx inserts t inside the caller's transaction. The caller
// holds an immediate transaction, so the MAX+1 read is race-free (D8).
func createTicketTx(ctx context.Context, tx *sql.Tx, t *domain.Ticket) error {
	var number int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(number), 0) + 1 FROM tickets`).Scan(&number); err != nil {
		return fmt.Errorf("sqlite: next ticket number: %w", err)
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO tickets (number, title, description, requester_name, requester_email, requester_user_id, category_id, priority, state, user_id, created_at, updated_at, resolved_at, closed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		number, t.Title, t.Description, t.RequesterName, t.RequesterEmail,
		nullableInt64(t.RequesterUserID), t.CategoryID,
		string(t.Priority), string(t.State), nullableInt64(t.UserID),
		formatTime(t.CreatedAt), formatTime(t.UpdatedAt),
		formatTimePtr(t.ResolvedAt), formatTimePtr(t.ClosedAt))
	if err != nil {
		return fmt.Errorf("sqlite: insert ticket: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("sqlite: ticket id: %w", err)
	}
	t.ID = id
	t.Number = number
	return nil
}

// Update persists the ticket's fields, state, and timestamps. A missing id
// is a NotFoundError (the caller's transaction rolls back).
func (st *ticketStore) Update(ctx context.Context, t *domain.Ticket) error {
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin update: %w", err)
	}
	if err := updateTicketTx(ctx, tx, t); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit update: %w", err)
	}
	return nil
}

// updateTicketTx writes the ticket row inside the caller's transaction.
func updateTicketTx(ctx context.Context, tx *sql.Tx, t *domain.Ticket) error {
	res, err := tx.ExecContext(ctx, `UPDATE tickets SET title = ?, description = ?, requester_name = ?, requester_email = ?, requester_user_id = ?, category_id = ?, priority = ?, state = ?, user_id = ?, created_at = ?, updated_at = ?, resolved_at = ?, closed_at = ?
		WHERE id = ?`,
		t.Title, t.Description, t.RequesterName, t.RequesterEmail,
		nullableInt64(t.RequesterUserID), t.CategoryID,
		string(t.Priority), string(t.State), nullableInt64(t.UserID),
		formatTime(t.CreatedAt), formatTime(t.UpdatedAt),
		formatTimePtr(t.ResolvedAt), formatTimePtr(t.ClosedAt), t.ID)
	if err != nil {
		return fmt.Errorf("sqlite: update ticket: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: update ticket rows: %w", err)
	}
	if n == 0 {
		return &domain.NotFoundError{Kind: "ticket", ID: t.ID}
	}
	return nil
}

// GetByID returns the ticket or a NotFoundError, restricted to the actor's
// ticket access scope carried in q (ticket-access spec): a ticket outside
// the actor's scope is indistinguishable from a missing one, so direct
// lookups never leak existence. Only the scope restriction of q applies;
// the filter fields are ignored.
func (st *ticketStore) GetByID(ctx context.Context, id int64, q application.TicketQuery) (*domain.Ticket, error) {
	where := "WHERE t.id = ?"
	args := []any{id}
	if scope, scopeArgs := scopeClause(q); scope != "" {
		where += " AND " + scope
		args = append(args, scopeArgs...)
	}
	t, err := scanTicketFrom(st.db.QueryRowContext(ctx, `SELECT `+ticketColumns+` FROM tickets t `+where, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &domain.NotFoundError{Kind: "ticket", ID: id}
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

// List returns tickets matching q, newest first with a stable id tiebreak
// (D2), limited by p.
func (st *ticketStore) List(ctx context.Context, q application.TicketQuery, p application.Page) ([]domain.Ticket, error) {
	where, args := buildTicketWhere(q)
	args = append(args, p.Limit, p.Offset)
	rows, err := st.db.QueryContext(ctx, `SELECT `+ticketColumns+` FROM tickets t `+where+` `+orderBy(q)+` LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list tickets: %w", err)
	}
	defer rows.Close()
	var out []domain.Ticket
	for rows.Next() {
		t, err := scanTicketFrom(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan ticket: %w", err)
		}
		out = append(out, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list tickets: %w", err)
	}
	return out, nil
}

// Count returns the number of tickets matching q (no pagination).
func (st *ticketStore) Count(ctx context.Context, q application.TicketQuery) (int, error) {
	where, args := buildTicketWhere(q)
	var n int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tickets t `+where, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("sqlite: count tickets: %w", err)
	}
	return n, nil
}

// CountsByState returns chips counts per state for the filtered result set
// (no pagination): the chips reflect the filters, not the whole table.
func (st *ticketStore) CountsByState(ctx context.Context, q application.TicketQuery) (map[domain.State]int, error) {
	where, args := buildTicketWhere(q)
	rows, err := st.db.QueryContext(ctx, `SELECT t.state, COUNT(*) FROM tickets t `+where+` GROUP BY t.state`, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: state counts: %w", err)
	}
	defer rows.Close()
	m := map[domain.State]int{}
	for rows.Next() {
		var state string
		var n int
		if err := rows.Scan(&state, &n); err != nil {
			return nil, fmt.Errorf("sqlite: scan state count: %w", err)
		}
		m[domain.State(state)] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: state counts: %w", err)
	}
	return m, nil
}

// CountsByPriority returns chips counts per priority for the filtered
// result set (no pagination).
func (st *ticketStore) CountsByPriority(ctx context.Context, q application.TicketQuery) (map[domain.Priority]int, error) {
	where, args := buildTicketWhere(q)
	rows, err := st.db.QueryContext(ctx, `SELECT t.priority, COUNT(*) FROM tickets t `+where+` GROUP BY t.priority`, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: priority counts: %w", err)
	}
	defer rows.Close()
	m := map[domain.Priority]int{}
	for rows.Next() {
		var priority string
		var n int
		if err := rows.Scan(&priority, &n); err != nil {
			return nil, fmt.Errorf("sqlite: scan priority count: %w", err)
		}
		m[domain.Priority(priority)] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: priority counts: %w", err)
	}
	return m, nil
}

// --- TicketUnitOfWork (atomic ticket + audit writes, C1) ---

// Create persists t and its created audit event as ONE atomic unit: both in
// the same immediate transaction, event.TicketID stamped from the
// store-assigned ID (port contract). A failed audit append rolls the ticket
// back; the UNIQUE number backstop retries the whole unit.
func (u *unitOfWork) Create(ctx context.Context, t *domain.Ticket, event domain.AuditEvent) error {
	return retryUnique(3, func() error {
		tx, err := u.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("sqlite: begin create unit: %w", err)
		}
		if err := createTicketTx(ctx, tx, t); err != nil {
			tx.Rollback()
			return err
		}
		event.TicketID = t.ID
		if err := appendAuditEventsTx(ctx, tx, event); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("sqlite: commit create unit: %w", err)
		}
		return nil
	})
}

// Update persists t and its event batch (a transition or field-edit batch)
// as ONE atomic unit; a failed append restores the pre-mutation ticket.
// events may be empty: a plain ticket write is still atomic by construction.
func (u *unitOfWork) Update(ctx context.Context, t *domain.Ticket, events ...domain.AuditEvent) error {
	tx, err := u.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin update unit: %w", err)
	}
	if err := updateTicketTx(ctx, tx, t); err != nil {
		tx.Rollback()
		return err
	}
	if err := appendAuditEventsTx(ctx, tx, events...); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit update unit: %w", err)
	}
	return nil
}

// appendAuditEventsTx inserts the events in occurrence order inside the
// caller's transaction. Defined here (the unit-of-work's atomicity half)
// and reused by the audit store (4.3).
func appendAuditEventsTx(ctx context.Context, tx *sql.Tx, events ...domain.AuditEvent) error {
	for _, e := range events {
		if _, err := tx.ExecContext(ctx, `INSERT INTO audit_events (ticket_id, actor, action, field, from_value, to_value, note, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			e.TicketID, e.Actor, e.Action, nullableString(e.Field), nullableString(e.FromValue),
			nullableString(e.ToValue), nullableString(e.Note), formatTime(e.CreatedAt)); err != nil {
			return fmt.Errorf("sqlite: append audit event: %w", err)
		}
	}
	return nil
}

// --- scan helpers ---

// rowScanner is satisfied by *sql.Row and *sql.Rows (single-row projection
// shared by GetByID and List).
type rowScanner interface {
	Scan(dest ...any) error
}

// scanTicketFrom projects one row into a domain.Ticket. Timestamps are
// stored as ISO-8601 UTC TEXT (D7) and parsed back; NULL user_id and
// lifecycle timestamps map to nil pointers.
func scanTicketFrom(scan rowScanner) (*domain.Ticket, error) {
	var (
		t                    domain.Ticket
		userID               sql.NullInt64
		requesterUserID      sql.NullInt64
		priority, state      string
		createdAt, updatedAt string
		resolvedAt, closedAt sql.NullString
	)
	if err := scan.Scan(&t.ID, &t.Number, &t.Title, &t.Description, &t.RequesterName, &t.RequesterEmail,
		&requesterUserID, &t.CategoryID, &priority, &state, &userID, &createdAt, &updatedAt, &resolvedAt, &closedAt); err != nil {
		return nil, err
	}
	t.Priority = domain.Priority(priority)
	t.State = domain.State(state)

	var err error
	if t.CreatedAt, err = time.Parse(timeLayout, createdAt); err != nil {
		return nil, fmt.Errorf("sqlite: parse created_at %q: %w", createdAt, err)
	}
	if t.UpdatedAt, err = time.Parse(timeLayout, updatedAt); err != nil {
		return nil, fmt.Errorf("sqlite: parse updated_at %q: %w", updatedAt, err)
	}
	if resolvedAt.Valid {
		v, err := time.Parse(timeLayout, resolvedAt.String)
		if err != nil {
			return nil, fmt.Errorf("sqlite: parse resolved_at %q: %w", resolvedAt.String, err)
		}
		t.ResolvedAt = &v
	}
	if closedAt.Valid {
		v, err := time.Parse(timeLayout, closedAt.String)
		if err != nil {
			return nil, fmt.Errorf("sqlite: parse closed_at %q: %w", closedAt.String, err)
		}
		t.ClosedAt = &v
	}
	if userID.Valid {
		v := userID.Int64
		t.UserID = &v
	}
	if requesterUserID.Valid {
		v := requesterUserID.Int64
		t.RequesterUserID = &v
	}
	return &t, nil
}
