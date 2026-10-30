package application

import (
	"context"
	"testing"
)

func TestAPIKeyContext(t *testing.T) {
	base := context.Background()
	if id, ok := APIKeyIDFrom(base); ok || id != 0 {
		t.Fatalf("empty context: %d %v", id, ok)
	}
	ctx := WithAPIKeyID(base, 42)
	if id, ok := APIKeyIDFrom(ctx); !ok || id != 42 {
		t.Fatalf("key context: %d %v", id, ok)
	}
	if _, ok := APIKeyIDFrom(base); ok {
		t.Fatal("parent context mutated")
	}
	if id, ok := APIKeyIDFrom(WithAPIKeyID(ctx, 7)); !ok || id != 7 {
		t.Fatal("nested key not replaced")
	}
}
