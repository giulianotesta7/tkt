package domain

import "time"

// APIKeyScope is one of the closed API permission scopes.
type APIKeyScope string

const (
	ScopeCatalogRead      APIKeyScope = "catalog:read"
	ScopeTicketsRead      APIKeyScope = "tickets:read"
	ScopeTicketsWrite     APIKeyScope = "tickets:write"
	ScopeCommentsWrite    APIKeyScope = "comments:write"
	ScopeCommentsInternal APIKeyScope = "comments:internal"
	ScopeTicketsAll       APIKeyScope = "tickets:all"
)

// ParseAPIKeyScopes validates and returns a deduplicated, canonically ordered set.
func ParseAPIKeyScopes(raw []string) ([]APIKeyScope, error) {
	order := []APIKeyScope{ScopeCatalogRead, ScopeTicketsRead, ScopeTicketsWrite, ScopeCommentsWrite, ScopeCommentsInternal, ScopeTicketsAll}
	seen := make(map[APIKeyScope]bool, len(raw))
	for _, s := range raw {
		scope := APIKeyScope(s)
		switch scope {
		case ScopeCatalogRead, ScopeTicketsRead, ScopeTicketsWrite, ScopeCommentsWrite, ScopeCommentsInternal, ScopeTicketsAll:
			seen[scope] = true
		default:
			return nil, &ValidationError{Field: "scopes", Message: "unknown scope", Key: "error.api.scope_invalid"}
		}
	}
	if len(seen) == 0 {
		return nil, &ValidationError{Field: "scopes", Message: "choose at least one scope", Key: "error.api.scope_required"}
	}
	result := make([]APIKeyScope, 0, len(seen))
	for _, scope := range order {
		if seen[scope] {
			result = append(result, scope)
		}
	}
	return result, nil
}

// APIKey is the persisted key metadata. SecretHash is a SHA-256 digest, not the token.
type APIKey struct {
	ID              int64
	PublicID        string
	UserID          int64
	Name            string
	SecretHash      string
	Scopes          []APIKeyScope
	CreatedByUserID int64
	CreatedAt       time.Time
	ExpiresAt       *time.Time
	LastUsedAt      *time.Time
	RevokedAt       *time.Time
	RevokedByUserID *int64
}
