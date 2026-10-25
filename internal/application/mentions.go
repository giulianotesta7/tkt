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
func mentionedNames(body string, names []string) map[string]bool {
	found := map[string]bool{}
	for i := 0; i < len(body); i++ {
		if body[i] != '@' || !mentionBoundaryStart(body, i) {
			continue
		}
		rest := body[i+1:]
		best := ""
		for _, name := range names {
			if name == "" || len(name) <= len(best) || !strings.HasPrefix(rest, name) {
				continue
			}
			if mentionBoundaryEnd(body, i+1+len(name)) {
				best = name
			}
		}
		if best != "" {
			found[best] = true
		}
	}
	return found
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
