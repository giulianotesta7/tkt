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
		if got := mentionedNames(tc.body, []string{tc.name})[tc.name]; got != tc.want {
			t.Errorf("%s: mentionedNames(%q, [%q]) = %v, want %v", name, tc.body, tc.name, got, tc.want)
		}
	}
}

// A mention starts at the beginning of the text or after a character that is
// neither a letter nor a digit, so an address-like word never mentions anyone.
// When several people's names are written from the same @ sign, the longest
// one is the person meant: "@Ana Assignee" names Ana Assignee, not a colleague
// called Ana.
func TestMentionedNamesPicksOnePersonPerAtSign(t *testing.T) {
	names := []string{"Ana", "Ana Assignee", "Bob Ruiz"}
	cases := map[string]struct {
		body string
		want []string
	}{
		"the longer name wins at one @":         {"@Ana Assignee please check", []string{"Ana Assignee"}},
		"the shorter name alone":                {"@Ana please check", []string{"Ana"}},
		"both people written separately":        {"@Ana and @Ana Assignee", []string{"Ana", "Ana Assignee"}},
		"a letter before the @ is a word":       {"mail foo@Ana Assignee", nil},
		"a digit before the @ is a word":        {"x1@Bob Ruiz", nil},
		"punctuation before the @ is fine":      {"(@Bob Ruiz)", []string{"Bob Ruiz"}},
		"a newline before the @ is fine":        {"hi\n@Bob Ruiz", []string{"Bob Ruiz"}},
		"a longer word leaves the shorter name": {"@Ana Assigneex", []string{"Ana"}},
	}
	for label, tc := range cases {
		got := mentionedNames(tc.body, names)
		if len(got) != len(tc.want) {
			t.Errorf("%s: mentionedNames(%q) = %v, want %v", label, tc.body, got, tc.want)
			continue
		}
		for _, n := range tc.want {
			if !got[n] {
				t.Errorf("%s: mentionedNames(%q) = %v, want %v", label, tc.body, got, tc.want)
			}
		}
	}
}
