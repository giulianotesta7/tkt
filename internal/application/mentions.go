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
// The boundary is Unicode-aware on purpose: a name ending in an accented letter
// is matched the same way, and a letter or digit in any script continues the
// word. The match is case-sensitive — the name must be written as it is stored,
// the same way the ticket names people everywhere else.
//
// The grammar only says whether a name is WRITTEN in a body. Whether that person
// can be mentioned at all is the candidate list's decision (FollowService), and
// nobody outside it is ever mentioned.

// mentionsName reports whether body mentions name.
func mentionsName(body, name string) bool {
	if name == "" || body == "" {
		return false
	}
	needle := "@" + name
	for from := 0; from < len(body); {
		i := strings.Index(body[from:], needle)
		if i < 0 {
			return false
		}
		end := from + i + len(needle)
		if mentionBoundaryEnd(body, end) {
			return true
		}
		// The name is written, but as the beginning of a longer word: keep
		// looking for another occurrence.
		from += i + 1
	}
	return false
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
