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

// SplitMentions is the presentation half of the same grammar (issue #438, S4):
// it turns a comment body into text spans and mention spans, so a timeline can
// chip a name without parsing markup itself. It must agree with mentionedNames
// on every boundary rule — the @ sign starts a word, the name must be written
// as stored, the longest name wins, and a longer word never mentions a shorter
// name inside it — because a body that notifies somebody must be the body that
// shows them as mentioned.
func TestSplitMentionsKeepsTextAndNamesApart(t *testing.T) {
	cases := map[string]struct {
		body  string
		names []string
		want  []MentionSpan
	}{
		"no mention is one text span":   {"Checking now", []string{"Ana"}, []MentionSpan{{Text: "Checking now"}}},
		"a mention splits the text":     {"hi @Ana, please", []string{"Ana"}, []MentionSpan{{Text: "hi "}, {Text: "@Ana", Mention: true, Name: "Ana"}, {Text: ", please"}}},
		"a mention at the start":        {"@Ana hi", []string{"Ana"}, []MentionSpan{{Text: "@Ana", Mention: true, Name: "Ana"}, {Text: " hi"}}},
		"a mention at the end":          {"hi @Ana", []string{"Ana"}, []MentionSpan{{Text: "hi "}, {Text: "@Ana", Mention: true, Name: "Ana"}}},
		"the longest name wins":         {"(@Ana Assignee)", []string{"Ana", "Ana Assignee"}, []MentionSpan{{Text: "("}, {Text: "@Ana Assignee", Mention: true, Name: "Ana Assignee"}, {Text: ")"}}},
		"two mentions":                  {"@Ana and @Bob", []string{"Ana", "Bob"}, []MentionSpan{{Text: "@Ana", Mention: true, Name: "Ana"}, {Text: " and "}, {Text: "@Bob", Mention: true, Name: "Bob"}}},
		"an accented name":              {"hola @José Núñez!", []string{"José Núñez"}, []MentionSpan{{Text: "hola "}, {Text: "@José Núñez", Mention: true, Name: "José Núñez"}, {Text: "!"}}},
		"an address is not a mention":   {"foo@Ana", []string{"Ana"}, []MentionSpan{{Text: "foo@Ana"}}},
		"a longer word is not a name":   {"@Anax", []string{"Ana"}, []MentionSpan{{Text: "@Anax"}}},
		"a bare @ stays text":           {"@ nothing", []string{"Ana"}, []MentionSpan{{Text: "@ nothing"}}},
		"no names leave the body whole": {"@Ana hi", nil, []MentionSpan{{Text: "@Ana hi"}}},
		"an empty body has no spans":    {"", []string{"Ana"}, nil},
	}
	for name, tc := range cases {
		got := SplitMentions(tc.body, tc.names)
		if len(got) != len(tc.want) {
			t.Errorf("%s: SplitMentions(%q) = %+v, want %+v", name, tc.body, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: SplitMentions(%q)[%d] = %+v, want %+v", name, tc.body, i, got[i], tc.want[i])
			}
		}
	}
}

// TestSplitMentionsAgreesWithMentionedNames pins the two halves together: every
// span the splitter marks as a mention is a name mentionedNames reports, and a
// body mentionedNames reports nobody for yields no mention span. A regression
// in either direction would show a chip for a person who is never notified, or
// notify a person the timeline renders as plain text.
func TestSplitMentionsAgreesWithMentionedNames(t *testing.T) {
	names := []string{"Ana", "Ana Assignee", "Bob Ruiz", "José Núñez"}
	bodies := []string{
		"@Ana please", "@Ana Assignee please", "(@Bob Ruiz)", "foo@Ana", "@Anax",
		"@José Núñez2", "@ nothing", "no mention here", "@Ana and @Bob Ruiz",
		"@Ana Assignee and @Ana",
	}
	for _, body := range bodies {
		mentioned := mentionedNames(body, names)
		chipped := map[string]bool{}
		for _, span := range SplitMentions(body, names) {
			if span.Mention {
				chipped[span.Name] = true
			}
		}
		if len(mentioned) != len(chipped) {
			t.Errorf("SplitMentions(%q) chips %v, mentionedNames reports %v", body, chipped, mentioned)
			continue
		}
		for name := range mentioned {
			if !chipped[name] {
				t.Errorf("SplitMentions(%q) must chip %q, which mentionedNames reports", body, name)
			}
		}
	}
}

// TestMentionedNamesScansEveryAtSign pins the NOTIFICATION half of the contract
// against the baseline behavior it must keep (issue #438 correction): a managed
// user's display name may itself contain an @, and writing "@Operations @Ana"
// names BOTH people — the whole name and the person whose name starts at the @
// inside it. The scan therefore looks at every @ that starts a word and never
// skips ahead over a name it already matched.
//
// The presentation splitter is deliberately NOT this scan: SplitMentions returns
// non-overlapping spans, so for nested names it renders one chip for the longer
// name. The two halves agree on every non-overlapping body and are allowed to
// differ here — the notification contract is the one that must not change.
func TestMentionedNamesScansEveryAtSign(t *testing.T) {
	cases := map[string]struct {
		body  string
		names []string
		want  []string
	}{
		"a name that contains an at sign mentions both":     {"@Operations @Ana", []string{"Operations @Ana", "Ana"}, []string{"Operations @Ana", "Ana"}},
		"the same body in the other candidate order":        {"@Operations @Ana", []string{"Ana", "Operations @Ana"}, []string{"Operations @Ana", "Ana"}},
		"an accented name nested in a unicode name":         {"@工程 @José Núñez!", []string{"工程 @José Núñez", "José Núñez"}, []string{"工程 @José Núñez", "José Núñez"}},
		"the longest name wins at one at sign":              {"@Ana Assignee", []string{"Ana", "Ana Assignee"}, []string{"Ana Assignee"}},
		"the longest name wins whatever the order":          {"@Ana Assignee", []string{"Ana Assignee", "Ana"}, []string{"Ana Assignee"}},
		"an at sign inside a word does not mention":         {"@Ops@Ana", []string{"Ops@Ana", "Ana"}, []string{"Ops@Ana"}},
		"a letter before the at sign starts no mention":     {"é@Ana @José Núñez2", []string{"Ana", "José Núñez"}, nil},
		"one person mentioned twice is mentioned once":      {"@Ana and @Ana", []string{"Ana", "Ana"}, []string{"Ana"}},
		"a body with no at sign mentions nobody":            {"no mention here", []string{"Ana"}, nil},
		"punctuation closes one mention and opens the next": {"(@Ana), @José Núñez!", []string{"Ana", "José Núñez"}, []string{"Ana", "José Núñez"}},
	}
	for name, tc := range cases {
		got := mentionedNames(tc.body, tc.names)
		if len(got) != len(tc.want) {
			t.Errorf("%s: mentionedNames(%q, %v) = %v, want %v", name, tc.body, tc.names, got, tc.want)
			continue
		}
		for _, person := range tc.want {
			if !got[person] {
				t.Errorf("%s: mentionedNames(%q, %v) must report %q, got %v", name, tc.body, tc.names, person, got)
			}
		}
	}
}

// TestSplitMentionsSpansAreNeverNested documents where the rendering half stops
// (issue #438 correction): a span cannot overlap the next one, so a body whose
// names nest renders the longer name and leaves the inner one inside that chip
// instead of splitting it. No test claims the two halves coincide here.
func TestSplitMentionsSpansAreNeverNested(t *testing.T) {
	body := "@Operations @Ana"
	names := []string{"Operations @Ana", "Ana"}
	spans := SplitMentions(body, names)
	if len(spans) != 1 || !spans[0].Mention || spans[0].Name != "Operations @Ana" || spans[0].Text != body {
		t.Fatalf("SplitMentions(%q, %v) = %+v, want one mention span naming the longer name", body, names, spans)
	}
	if mentioned := mentionedNames(body, names); len(mentioned) != 2 {
		t.Fatalf("the notification scan must still report both names, got %v", mentioned)
	}
}
