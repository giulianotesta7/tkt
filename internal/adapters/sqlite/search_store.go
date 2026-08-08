package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// searchStore implements application.SearchStore (task 4.5) over the
// contentless FTS5 index (0002_fts.sql, D3). TicketQuery.Text is the
// D4-tokenized expression built by application.BuildTextQuery: each token
// double-quoted with embedded quotes escaped, joined with AND. It binds
// as-is — quoted tokens never carry FTS5 syntax, so special characters in
// user input degrade to no text filter instead of a 500.
type searchStore struct {
	db *sql.DB
}

var _ application.SearchStore = (*searchStore)(nil)

func newSearchStore(db *sql.DB) *searchStore { return &searchStore{db: db} }

// Search returns tickets matching q — text AND filters (the shared builder
// composes the text clause with the filter clauses) — ordered created_at
// DESC, id DESC (D2), limited by p.
func (ss *searchStore) Search(ctx context.Context, q application.TicketQuery, p application.Page) ([]domain.Ticket, error) {
	where, args := buildTicketWhere(q)
	args = append(args, p.Limit, p.Offset)
	rows, err := ss.db.QueryContext(ctx, `SELECT `+ticketColumns+` FROM tickets t `+where+` `+orderByCreatedDesc+` LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: search tickets: %w", err)
	}
	defer rows.Close()
	var out []domain.Ticket
	for rows.Next() {
		t, err := scanTicketFrom(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan search result: %w", err)
		}
		out = append(out, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: search tickets: %w", err)
	}
	return out, nil
}

// SearchCount returns the number of matches (no pagination).
func (ss *searchStore) SearchCount(ctx context.Context, q application.TicketQuery) (int, error) {
	where, args := buildTicketWhere(q)
	var n int
	if err := ss.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tickets t `+where, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("sqlite: search count: %w", err)
	}
	return n, nil
}
