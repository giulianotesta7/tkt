package application

import "context"

type apiKeyIDContextKey struct{}
type ticketScopeAllContextKey struct{}

// WithTicketScopeAll carries the authenticated key's tickets:all scope. Policy
// still requires an active agent service user; this flag grants no capability.
func WithTicketScopeAll(ctx context.Context) context.Context {
	return context.WithValue(ctx, ticketScopeAllContextKey{}, true)
}

func ticketScopeAll(ctx context.Context) bool {
	all, _ := ctx.Value(ticketScopeAllContextKey{}).(bool)
	return all
}

// WithAPIKeyID attributes audit events in this request to its authenticated key.
func WithAPIKeyID(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, apiKeyIDContextKey{}, id)
}

// APIKeyIDFrom returns the authenticated API key, if this is an API request.
func APIKeyIDFrom(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(apiKeyIDContextKey{}).(int64)
	return id, ok
}
