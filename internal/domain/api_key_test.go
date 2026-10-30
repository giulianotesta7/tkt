package domain

import (
	"errors"
	"reflect"
	"testing"
)

func TestParseAPIKeyScopes(t *testing.T) {
	got, err := ParseAPIKeyScopes([]string{"tickets:write", "catalog:read", "tickets:write", "comments:internal", "tickets:read", "comments:write"})
	want := []APIKeyScope{ScopeCatalogRead, ScopeTicketsRead, ScopeTicketsWrite, ScopeCommentsWrite, ScopeCommentsInternal}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("scopes = %v, %v; want %v", got, err, want)
	}
	for _, scopes := range [][]string{nil, {}, {"unknown"}, {"catalog:read", "unknown"}, {""}} {
		got, err := ParseAPIKeyScopes(scopes)
		var validation *ValidationError
		if got != nil || !errors.As(err, &validation) || validation.Field != "scopes" {
			t.Errorf("ParseAPIKeyScopes(%v) = %v, %v; want scopes validation error", scopes, got, err)
		}
	}
}
