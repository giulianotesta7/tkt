package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// resultingTicketAccess answers the one question the plan path asks about every
// notification row it is about to store: may this recipient still open the
// ticket the transaction has just written?
//
// A plan decides its recipients from the snapshot it was built on, and the same
// transaction then moves the ticket — a claim hands it to the acting agent, a
// least_loaded step hands it to the member it selects — so a recipient who read
// the ticket at plan time can be out of scope by the instant the row would be
// stored. The rows therefore pass this guard AFTER the resulting ticket and run
// are persisted, inside that same transaction, so the answer comes from the
// persisted result and never from the pre-plan row or from a follower list.
//
// The staff arms are the adapter's canonical read scope (scopeClause): an agent
// reads the ticket as its assignee or as a member of the desk the resulting run
// still waits on to claim, an administrator or root reads the whole queue. The
// requester arm is the access the ticket already granted that person. Nobody
// else is admitted, so following never widens access and a membership lost to
// the change never keeps a row.
type resultingTicketAccess struct {
	ctx       context.Context
	tx        *sql.Tx
	ticketID  int64
	requester *int64
	cache     map[int64]bool
}

// readResultingTicketAccess loads the persisted requester of the ticket this
// transaction just wrote, which is the one access fact the guard reads once.
func readResultingTicketAccess(ctx context.Context, tx *sql.Tx, ticketID int64) (*resultingTicketAccess, error) {
	var requester sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT requester_user_id FROM tickets WHERE id = ?`, ticketID).Scan(&requester); err != nil {
		return nil, fmt.Errorf("sqlite: read the resulting ticket requester: %w", err)
	}
	a := &resultingTicketAccess{ctx: ctx, tx: tx, ticketID: ticketID, cache: map[int64]bool{}}
	if requester.Valid {
		id := requester.Int64
		a.requester = &id
	}
	return a, nil
}

// appendResultingNotificationsTx writes the rows a plan produces, keeping only
// the recipients the resulting ticket still admits. It is the plan path's only
// notification write: the routing rows a least_loaded step builds, the rows the
// application computed, and the released conditional rows all arrive here.
//
// The batch is validated BEFORE any recipient is judged, so a policy bug that
// produces an unrenderable or self-addressed row still aborts the change whatever
// the guard would have decided about the person it names. Dropping a row is not
// an error: the change it describes is still committed, and a recipient who can
// no longer open the ticket simply is not told about it.
func appendResultingNotificationsTx(ctx context.Context, tx *sql.Tx, ticketID int64, rows []domain.Notification) error {
	if len(rows) == 0 {
		return nil
	}
	for _, n := range rows {
		if err := n.Validate(); err != nil {
			return err
		}
	}
	access, err := readResultingTicketAccess(ctx, tx, ticketID)
	if err != nil {
		return err
	}
	kept := make([]domain.Notification, 0, len(rows))
	for _, n := range rows {
		admitted, err := access.admits(n.UserID)
		if err != nil {
			return err
		}
		if admitted {
			kept = append(kept, n)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return appendNotificationsTx(ctx, tx, kept)
}

// admits reports whether the recipient still reads the resulting ticket, caching
// the answer so one batch costs at most one proof per person.
func (a *resultingTicketAccess) admits(userID int64) (bool, error) {
	if admitted, seen := a.cache[userID]; seen {
		return admitted, nil
	}
	admitted, err := a.prove(userID)
	if err != nil {
		return false, err
	}
	a.cache[userID] = admitted
	return admitted, nil
}

// prove applies the arms documented on resultingTicketAccess. A missing or
// deactivated person, an unknown role and an empty capability set all fail
// closed.
func (a *resultingTicketAccess) prove(userID int64) (bool, error) {
	if a.requester != nil && *a.requester == userID {
		return true, nil
	}
	var role string
	var active int64
	err := a.tx.QueryRowContext(a.ctx, `SELECT role, active FROM users WHERE id = ?`, userID).Scan(&role, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("sqlite: read the notification recipient: %w", err)
	}
	policy := application.NewPolicy()
	if active == 0 || !policy.Capabilities(domain.Role(role)).Require(application.CapCommentInternal) {
		return false, nil
	}
	switch policy.ReadScope(domain.Role(role)) {
	case application.ScopeAll:
		return true, nil
	case application.ScopeAssignedOrClaimable:
		// The canonical agent read scope, evaluated on the row this transaction
		// just persisted: the resulting assignee and the resulting run position
		// decide the claimable arm, so a desk the claim already left proves
		// nothing.
		scope, args := scopeClause(application.TicketQuery{Scope: application.ScopeAssignedOrClaimable, ActorID: userID})
		var one int
		err := a.tx.QueryRowContext(a.ctx, `SELECT 1 FROM tickets t WHERE t.id = ? AND `+scope, append([]any{a.ticketID}, args...)...).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("sqlite: prove the resulting ticket access: %w", err)
		}
		return true, nil
	default:
		return false, nil
	}
}
