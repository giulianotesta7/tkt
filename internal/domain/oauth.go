package domain

import "time"

// OAuthClient is an MCP client registered through Dynamic Client Registration
// (issue #493). Clients are public: ClientID identifies them and nothing else
// authenticates them.
type OAuthClient struct {
	ID           int64
	ClientID     string
	Name         string
	RedirectURIs []string
	CreatedAt    time.Time
}

// OAuthGrant is one person's approval of one client. It is revoked rather than
// deleted; a nil RevokedByUserID on a revoked grant means tkt revoked it.
type OAuthGrant struct {
	ID              int64
	UserID          int64
	ClientID        int64
	ClientName      string
	Scopes          []APIKeyScope
	Resource        string
	CreatedAt       time.Time
	LastUsedAt      *time.Time
	RevokedAt       *time.Time
	RevokedByUserID *int64
}

// OAuthCode is a single-use authorization code. CodeHash is the SHA-256 hex
// digest of the code; GrantID names the grant it was exchanged for.
type OAuthCode struct {
	ID            int64
	CodeHash      string
	ClientID      int64
	UserID        int64
	RedirectURI   string
	CodeChallenge string
	Scopes        []APIKeyScope
	Resource      string
	CreatedAt     time.Time
	ExpiresAt     time.Time
	UsedAt        *time.Time
	GrantID       *int64
}

// OAuthTokenKind distinguishes access tokens from refresh tokens.
type OAuthTokenKind string

const (
	OAuthTokenAccess  OAuthTokenKind = "access"
	OAuthTokenRefresh OAuthTokenKind = "refresh"
)

// OAuthToken is an access or refresh token of a grant. TokenHash is the
// SHA-256 hex digest of the token; UsedAt marks a rotated refresh token.
type OAuthToken struct {
	ID        int64
	GrantID   int64
	Kind      OAuthTokenKind
	TokenHash string
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    *time.Time
}
