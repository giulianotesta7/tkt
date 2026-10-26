package application

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Mention grammar (issue #418). A mention is the @ sign, the person's display
// name exactly as it is stored, and then the end of the text or any character
// that is neither a letter nor a digit. That last part is what keeps one name
// from swallowing another: "@Bob Ruiz," names Bob Ruiz, while "@Bob Ruizz" names
// nobody, because the "z" after the name makes it a different word.
//
// The @ sign itself must start a word too: it is the first character of the
// text or follows a character that is neither a letter nor a digit, so an
// address-like "foo@Bob Ruiz" mentions nobody. And one @ sign names one person:
// when several names are written from it, the longest wins, so "@Ana Assignee"
// names Ana Assignee and not a colleague called Ana.
//
// The boundaries are Unicode-aware on purpose: a name ending in an accented
// letter is matched the same way, and a letter or digit in any script continues
// the word. The match is case-sensitive — the name must be written as it is
// stored, the same way the ticket names people everywhere else.
//
// The grammar only says whether a name is WRITTEN in a body. Whether that person
// can be mentioned at all is the candidate list's decision (FollowService), and
// nobody outside it is ever mentioned.

// mentionedNames returns the names, among the given ones, that body mentions.
//
// This is the NOTIFICATION half of the contract (issue #418) and it must stay
// byte-compatible with the behavior it shipped with: it looks at EVERY @ sign
// that starts a word, and one @ sign names the longest candidate written from
// it. A managed user's display name may itself contain an "@", so
// "@Operations @Ana" names BOTH "Operations @Ana" and "Ana" — the scan never
// skips ahead over a name it already matched.
func mentionedNames(body string, names []string) map[string]bool {
	found := map[string]bool{}
	for i := 0; i < len(body); i++ {
		if body[i] != '@' || !mentionBoundaryStart(body, i) {
			continue
		}
		if name := mentionAt(body, i, names); name != "" {
			found[name] = true
		}
	}
	return found
}

// SplitMentions is the PRESENTATION half of the mention grammar (issue #438,
// S4): it turns a comment body into text spans and mention spans so a timeline
// can chip a name without parsing markup itself. A span cannot overlap the next
// one, so this scan DOES skip ahead over a name it just matched: for
// "@Operations @Ana" it renders one chip for "Operations @Ana" and leaves "Ana"
// inside it, while mentionedNames reports both. The two halves therefore agree
// on every non-nested body and are allowed to differ exactly there; no caller
// may read a chip as the notification set.
func SplitMentions(body string, names []string) []MentionSpan {
	var spans []MentionSpan
	textStart := 0
	for i := 0; i < len(body); i++ {
		if body[i] != '@' || !mentionBoundaryStart(body, i) {
			continue
		}
		best := mentionAt(body, i, names)
		if best == "" {
			continue
		}
		if textStart < i {
			spans = append(spans, MentionSpan{Text: body[textStart:i]})
		}
		spans = append(spans, MentionSpan{Text: body[i : i+1+len(best)], Mention: true, Name: best})
		i += len(best)
		textStart = i + 1
	}
	if textStart < len(body) {
		spans = append(spans, MentionSpan{Text: body[textStart:]})
	}
	return spans
}

// mentionAt returns the longest candidate name written at the @ sign at i, or
// "" when the text there names nobody. It is the one matching primitive both
// halves share: the @ sign must start a word, the name must be written exactly
// as stored, and the character after it must close the word.
func mentionAt(body string, i int, names []string) string {
	best := ""
	rest := body[i+1:]
	for _, name := range names {
		if name == "" || len(name) <= len(best) || !strings.HasPrefix(rest, name) {
			continue
		}
		if mentionBoundaryEnd(body, i+1+len(name)) {
			best = name
		}
	}
	return best
}

// mentionBoundaryStart reports whether the @ sign at i starts a word: it is the
// first character of the text, or the character before it is neither a letter
// nor a digit.
func mentionBoundaryStart(body string, i int) bool {
	if i == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(body[:i])
	return !unicode.IsLetter(r) && !unicode.IsDigit(r)
}

// mentionBoundaryEnd reports whether the rune starting at i closes a mention:
// the end of the text, or a character that is neither a letter nor a digit.
func mentionBoundaryEnd(body string, i int) bool {
	if i >= len(body) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(body[i:])
	return !unicode.IsLetter(r) && !unicode.IsDigit(r)
}

// MentionSpan is one run of a comment body under the mention grammar: either
// plain text, or an @mention of one of the names the caller offered. A span
// never carries markup — a caller that escapes every span renders both kinds
// safely — and Name is the stored display name the grammar matched, which is
// what lets a reader tell a mention of themselves from a mention of somebody
// else.
type MentionSpan struct {
	Text    string
	Mention bool
	Name    string
}
