package application

import "context"

type apiKeyIDContextKey struct{}

// WithAPIKeyID attributes audit events in this request to its authenticated key.
func WithAPIKeyID(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, apiKeyIDContextKey{}, id)
}

// APIKeyIDFrom returns the authenticated API key, if this is an API request.
func APIKeyIDFrom(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(apiKeyIDContextKey{}).(int64)
	return id, ok
}
