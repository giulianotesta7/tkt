package domain

import (
	"strings"
	"testing"
)

// TestFolderNameKey pins that two names compare equal exactly when Go's
// case-insensitive comparison says so, beyond ASCII.
func TestFolderNameKey(t *testing.T) {
	for _, pair := range [][2]string{
		{"IT services", "it SERVICES"},
		{"ÁREA", "área"},
		{"ΟΣ", "ος"},
		{"Kelvin K", "kelvin \u212a"},
		{"Straße", "STRASSE"},
		{"IT", "IT "},
	} {
		sameKey := FolderNameKey(pair[0]) == FolderNameKey(pair[1])
		if want := strings.EqualFold(pair[0], pair[1]); sameKey != want {
			t.Errorf("FolderNameKey(%q) == FolderNameKey(%q) is %v, want %v (strings.EqualFold)", pair[0], pair[1], sameKey, want)
		}
	}
}
