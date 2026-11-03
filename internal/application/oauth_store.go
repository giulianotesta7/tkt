package application

import (
	"context"
	"errors"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

var (
	// ErrOAuthCodeReused answers a second presentation of an authorization
	// code; the store has already revoked the grant the code was exchanged for.
	ErrOAuthCodeReused = errors.New("oauth: authorization code reused")
	// ErrOAuthRefreshReused answers a rotated refresh token presented again; the
	// store has already revoked its grant.
	ErrOAuthRefreshReused = errors.New("oauth: refresh token reused")
)

// OAuthStore persists MCP OAuth clients, codes, grants, tokens and the MCP
// switch (issue #493). Codes and tokens are stored only as SHA-256 hex digests.
type OAuthStore interface {
	// MCPEnabled reports the instance MCP switch; absent means off.
	MCPEnabled(ctx context.Context) (bool, error)
	// SetMCPEnabled flips the switch and writes its api_admin_events row in the
	// same transaction.
	SetMCPEnabled(ctx context.Context, enabled bool, actorID int64, at time.Time) error
	// CreateClient stores c, assigning c.ID. The same transaction deletes the
	// clients created before pruneBefore that no grant references, and their
	// codes.
	CreateClient(ctx context.Context, c *domain.OAuthClient, pruneBefore time.Time) error
	// ClientByClientID answers NotFound when absent.
	ClientByClientID(ctx context.Context, clientID string) (*domain.OAuthClient, error)
	// CreateCode stores c, assigning c.ID. The same transaction deletes the
	// never-used codes that expired before c.CreatedAt.
	CreateCode(ctx context.Context, c *domain.OAuthCode) error
	// ConsumeCode spends a code once and returns it as it was before spending.
	// A spent code revokes the grant it was exchanged for, if any, and answers
	// ErrOAuthCodeReused; an unknown code answers NotFound.
	ConsumeCode(ctx context.Context, codeHash string, at time.Time) (*domain.OAuthCode, error)
	// CreateGrant stores g, assigning g.ID, links code codeID to it, and stores
	// tokens, assigning their ID and GrantID, in one transaction.
	CreateGrant(ctx context.Context, g *domain.OAuthGrant, codeID int64, tokens ...*domain.OAuthToken) error
	// TokenByHash answers NotFound when absent.
	TokenByHash(ctx context.Context, hash string) (*domain.OAuthToken, error)
	// GrantByID answers the grant with its client name; NotFound when absent.
	GrantByID(ctx context.Context, id int64) (*domain.OAuthGrant, error)
	// RotateRefreshToken spends refresh token tokenID and stores tokens in one
	// transaction, deleting the grant's tokens that expired before at. A token
	// already spent revokes its grant, stores nothing, and answers
	// ErrOAuthRefreshReused.
	RotateRefreshToken(ctx context.Context, tokenID int64, at time.Time, tokens ...*domain.OAuthToken) error
	TouchGrant(ctx context.Context, id int64, at time.Time) error
	// ListLiveGrants answers userID's unrevoked grants that still hold an
	// unspent refresh token expiring after now, newest first.
	ListLiveGrants(ctx context.Context, userID int64, now time.Time) ([]domain.OAuthGrant, error)
	// RevokeGrant revokes one live grant owned by userID. NotFound when absent,
	// owned by someone else, or already revoked.
	RevokeGrant(ctx context.Context, id, userID int64, at time.Time) error
}
