package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// ticketFollowerStore persists who follows a ticket over the ticket_followers
// table of migration 0033 (issue #418). The table's primary key is the whole
// relation — one row per (ticket, person) — so following twice, or unfollowing
// what was never followed, needs no read: the write itself is the answer, and
// this adapter says so with INSERT OR IGNORE and a plain DELETE.
//
// The store persists relations only. It never decides who MAY follow, or who
// may be told: both are the application layer's policy (canStaffOpenTicket), and
// a row written here grants nothing on its own.
type ticketFollowerStore struct {
	db *sql.DB
}

var _ application.TicketFollowerStore = (*ticketFollowerStore)(nil)

func newTicketFollowerStore(db *sql.DB) *ticketFollowerStore {
	return &ticketFollowerStore{db: db}
}

// TicketFollowerStore returns the follower port (issue #418).
func (s *Store) TicketFollowerStore() application.TicketFollowerStore {
	return newTicketFollowerStore(s.db)
}

// followStatement is the ONE follower insert of the package: the comment unit of
// work subscribes the people a mention names with the same statement, so the
// table cannot be written two different ways.
const followStatement = `INSERT OR IGNORE INTO ticket_followers (ticket_id, user_id, created_at) VALUES (?, ?, ?)`

// followerInserter is the subset of *sql.DB and *sql.Tx the follower insert
// needs, so the store and the comment transaction share ONE statement.
type followerInserter interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// followTicketTx subscribes userIDs to the ticket on the given executor. It is
// called inside the comment transaction for the people a mention names, which is
// why it takes an executor instead of using the store's own connection.
func followTicketTx(ctx context.Context, ex followerInserter, ticketID int64, userIDs []int64, at time.Time) error {
	for _, userID := range userIDs {
		if _, err := ex.ExecContext(ctx, followStatement, ticketID, userID, formatTime(at)); err != nil {
			return fmt.Errorf("sqlite: follow ticket: %w", err)
		}
	}
	return nil
}

func (s *ticketFollowerStore) Follow(ctx context.Context, ticketID, userID int64, at time.Time) error {
	return followTicketTx(ctx, s.db, ticketID, []int64{userID}, at)
}

func (s *ticketFollowerStore) Unfollow(ctx context.Context, ticketID, userID int64) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM ticket_followers WHERE ticket_id = ? AND user_id = ?`, ticketID, userID); err != nil {
		return fmt.Errorf("sqlite: unfollow ticket: %w", err)
	}
	return nil
}

func (s *ticketFollowerStore) IsFollowing(ctx context.Context, ticketID, userID int64) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM ticket_followers WHERE ticket_id = ? AND user_id = ?`, ticketID, userID).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("sqlite: read ticket follower: %w", err)
	}
	return true, nil
}

// ListFollowers joins the users table for the identity and role facts the
// notification policy reads, and for nothing else: the projection deliberately
// leaves out the email and the password hash, so a follower list cannot become a
// second user directory. The ordering is by display name then id, which is the
// order both the Properties row and the mention list show.
func (s *ticketFollowerStore) ListFollowers(ctx context.Context, ticketID int64) ([]domain.User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT u.id, u.name, u.role, u.active
		 FROM ticket_followers f
		 JOIN users u ON u.id = f.user_id
		 WHERE f.ticket_id = ?
		 ORDER BY u.name ASC, u.id ASC`, ticketID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list ticket followers: %w", err)
	}
	defer rows.Close()
	var out []domain.User
	for rows.Next() {
		var (
			u      domain.User
			role   string
			active int64
		)
		if err := rows.Scan(&u.ID, &u.Name, &role, &active); err != nil {
			return nil, fmt.Errorf("sqlite: scan ticket follower: %w", err)
		}
		u.Role = domain.Role(role)
		u.Active = active == 1
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list ticket followers: %w", err)
	}
	return out, nil
}
