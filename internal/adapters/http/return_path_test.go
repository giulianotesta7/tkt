package httpadapter

import "testing"

// TestSafeReturnPath pins the return URL rule login honors (issue #493, S5):
// only a local absolute path, query string allowed, is ever a destination.
// Anything a browser could resolve to another origin, or a scheme it would run,
// is refused, and the caller falls back to /tickets.
func TestSafeReturnPath(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		ok   bool
	}{
		{"authorize request with query", "/oauth/authorize?client_id=x&state=y", true},
		{"local page", "/tickets/5", true},
		{"absolute https URL", "https://evil.example/", false},
		{"scheme-relative URL", "//evil.example", false},
		{"slash then escaped backslash", "/\\\\evil.example", false},
		{"slash then backslash", "/\\evil", false},
		{"javascript scheme", "javascript:alert(1)", false},
		{"empty", "", false},
		{"control character", "/tickets\x01", false},
		{"newline", "/tickets\n/evil", false},
		{"delete character", "/tickets\x7f", false},
		{"scheme with one slash", "http:/evil", false},
		{"relative path", "tickets/5", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := safeReturnPath(tc.raw)
			if ok != tc.ok {
				t.Fatalf("safeReturnPath(%q) ok = %v, want %v", tc.raw, ok, tc.ok)
			}
			if ok && got != tc.raw {
				t.Errorf("safeReturnPath(%q) = %q, want the path unchanged", tc.raw, got)
			}
			if !ok && got != "" {
				t.Errorf("safeReturnPath(%q) = %q, want empty when refused", tc.raw, got)
			}
		})
	}
}
