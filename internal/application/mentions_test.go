package application

import "testing"

// The mention grammar (issue #418): the @ sign, the person's display name
// exactly as stored, then either the end of the text or a character that is
// neither a letter nor a digit. These cases pin the boundary from both sides —
// the punctuation that ends a name, and the letter or digit that makes the text
// a LONGER word, which must not mention the shorter name inside it.

func TestMentionsName(t *testing.T) {
	cases := map[string]struct {
		body string
		name string
		want bool
	}{
		"name at the end of the text":            {"Thanks @Bob Ruiz", "Bob Ruiz", true},
		"name followed by a comma":               {"@Bob Ruiz, please look", "Bob Ruiz", true},
		"name followed by a full stop":           {"Fixed by @Bob Ruiz.", "Bob Ruiz", true},
		"name followed by a newline":             {"@Bob Ruiz\nsecond line", "Bob Ruiz", true},
		"name alone in the body":                 {"@Bob Ruiz", "Bob Ruiz", true},
		"a longer word is not the name":          {"@Bob Ruizz", "Bob Ruiz", false},
		"a longer word later is not the name":    {"@Bob Ruizz and @Bob Ruiz, thanks", "Bob Ruiz", true},
		"a letter right after the name":          {"@Anaë", "Ana", false},
		"a digit right after the name":           {"@Ticket7", "Ticket", false},
		"the name is written differently":        {"@bob ruiz", "Bob Ruiz", false},
		"the name is only a prefix":              {"@Bob Ru", "Bob Ruiz", false},
		"no at sign":                             {"Bob Ruiz said so", "Bob Ruiz", false},
		"the label without the at sign":          {"mail bobruiz@example", "Bob Ruiz", false},
		"an accented name":                       {"Lo revisó @José Núñez!", "José Núñez", true},
		"an accented name in a longer word":      {"@José Núñez2", "José Núñez", false},
		"an empty name is never mentioned":       {"@ and more", "", false},
		"a name with a comma inside it is exact": {"@Ruiz, Bob", "Ruiz, Bob", true},
	}
	for name, tc := range cases {
		if got := mentionsName(tc.body, tc.name); got != tc.want {
			t.Errorf("%s: mentionsName(%q, %q) = %v, want %v", name, tc.body, tc.name, got, tc.want)
		}
	}
}
