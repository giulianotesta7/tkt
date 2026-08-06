// Package application implements the use cases of the ticketing system
// behind store ports (hexagonal-lite, D13). It imports the domain only;
// persistence and presentation live in adapters that implement these ports.
package application

import (
	"context"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// TicketStore persists tickets and answers list/count queries (D2, D13).
//
// Numbering is a store concern (D8): Create assigns the ticket's unique
// readable Number atomically (MAX+1 inside the store transaction) and its ID.
// The service never computes numbers.
//
// Atomicity contract: a mutation on a ticket (Create/Update) followed by an
// AuditStore.Append of the corresponding events MUST be applied atomically by
// implementations — the application layer issues them in sequence and relies
// on the store to keep ticket state and its audit trail consistent. The
// in-memory fakes satisfy this by construction.
type TicketStore interface {
	// Create persists t, assigning t.ID and t.Number (MAX+1, atomic).
	Create(ctx context.Context, t *domain.Ticket) error
	// Update persists the ticket's fields, state, and timestamps.
	Update(ctx context.Context, t *domain.Ticket) error
	// GetByID returns the ticket or ErrNotFound.
	GetByID(ctx context.Context, id int64) (*domain.Ticket, error)
	// List returns tickets matching q, ordered created_at DESC, id DESC (D2),
	// limited by p.
	List(ctx context.Context, q TicketQuery, p Page) ([]domain.Ticket, error)
	// Count returns the number of tickets matching q (no pagination).
	Count(ctx context.Context, q TicketQuery) (int, error)
	// CountsByState returns chips counts per state for tickets matching q
	// (no pagination), reflecting the filtered result set.
	CountsByState(ctx context.Context, q TicketQuery) (map[domain.State]int, error)
	// CountsByPriority returns chips counts per priority for tickets matching
	// q (no pagination), reflecting the filtered result set.
	CountsByPriority(ctx context.Context, q TicketQuery) (map[domain.Priority]int, error)
}

// SearchStore provides FTS5 full-text search over title, description, and
// comment bodies (ticket-search spec). TicketQuery.Text carries the
// D4-tokenized expression: each token double-quoted with embedded quotes
// escaped, joined with AND; empty means no text filter.
type SearchStore interface {
	// Search returns tickets matching q (text AND filters), ordered
	// created_at DESC, id DESC, limited by p.
	Search(ctx context.Context, q TicketQuery, p Page) ([]domain.Ticket, error)
	// SearchCount returns the number of matches (no pagination).
	SearchCount(ctx context.Context, q TicketQuery) (int, error)
}

// CommentStore persists the append-only comment timeline
// (comment-timeline spec).
type CommentStore interface {
	// Add stores c, assigning c.ID.
	Add(ctx context.Context, c *domain.Comment) error
	// ListByTicket returns the ticket's comments in creation order (ASC).
	ListByTicket(ctx context.Context, ticketID int64) ([]domain.Comment, error)
}

// AuditStore persists the append-only audit trail (audit-log spec).
type AuditStore interface {
	// Append stores all events in occurrence order (one mutation batch).
	Append(ctx context.Context, events ...domain.AuditEvent) error
	// ListByTicket returns the ticket's audit events in occurrence order (ASC).
	ListByTicket(ctx context.Context, ticketID int64) ([]domain.AuditEvent, error)
}

// UserStore persists managed users (user-management spec).
type UserStore interface {
	// Create stores u, assigning u.ID; ErrDuplicate when the email exists.
	Create(ctx context.Context, u *domain.User) error
	// Update persists the user's fields, including deactivation (Active).
	Update(ctx context.Context, u *domain.User) error
	// Delete removes an unreferenced user; ErrReferenced when the user is
	// assigned to tickets (deactivation is the removal path then).
	Delete(ctx context.Context, id int64) error
	// GetByID returns the user, including inactive ones (historical display);
	// ErrNotFound when absent.
	GetByID(ctx context.Context, id int64) (*domain.User, error)
	// GetByEmail returns the user by email; ErrNotFound when absent.
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
	// Count returns the number of users (first-user bootstrap check, D16).
	Count(ctx context.Context) (int, error)
	// List returns all users.
	List(ctx context.Context) ([]domain.User, error)
	// ListActive returns only active users.
	ListActive(ctx context.Context) ([]domain.User, error)
}

// SessionStore persists server-side login sessions (D14).
type SessionStore interface {
	// Create stores s.
	Create(ctx context.Context, s *domain.Session) error
	// GetByID returns the session or ErrNotFound when missing or expired.
	GetByID(ctx context.Context, id string) (*domain.Session, error)
	// Delete removes the session (logout).
	Delete(ctx context.Context, id string) error
}

// CategoryStore persists managed categories (category-management spec).
type CategoryStore interface {
	// Create stores c, assigning c.ID; ErrDuplicate when the name exists.
	Create(ctx context.Context, c *domain.Category) error
	// Update persists the category (rename); ErrDuplicate when the new name
	// is taken by another category.
	Update(ctx context.Context, c *domain.Category) error
	// Delete removes an unreferenced category; ErrReferenced when tickets
	// use it.
	Delete(ctx context.Context, id int64) error
	// GetByID returns the category; ErrNotFound when absent.
	GetByID(ctx context.Context, id int64) (*domain.Category, error)
	// List returns all categories.
	List(ctx context.Context) ([]domain.Category, error)
}

// TicketQuery is the filter set shared by list, count, and chips queries
// (ticket-search spec). All active filters compose with AND semantics; an
// empty filter set returns all tickets.
type TicketQuery struct {
	State      *domain.State
	Priority   *domain.Priority
	CategoryID *int64
	UserID     *int64
	// Text is the D4-tokenized FTS expression ("" = no text filter).
	Text string
}

// Page is the pagination window (D2). Limit is FIXED at 10 — the service
// always sets it; there is no configuration knob.
type Page struct {
	Offset int
	Limit  int
}
