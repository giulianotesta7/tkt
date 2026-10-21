// Package msgtext resolves application message references through an i18n
// catalogue. The HTTP renderer and the email renderer share it, so a
// notification reads the same in the panel and in the inbox (issue #362).
package msgtext

import (
	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/i18n"
)

// Resolve renders one application message reference through the catalogue:
// its key names the sentence and each argument is either a resolved value
// (never translated) or a nested catalogue key (a translated placeholder), so
// a composed sentence carries no English of its own.
func Resolve(tr i18n.Translator, m application.Msg) string {
	if m.Key == "" {
		// Data, not copy: an admin-authored value carried as one field of a
		// composed message renders verbatim.
		return m.Raw
	}
	args := make([]any, len(m.Args))
	for i, arg := range m.Args {
		switch {
		case arg.Msg != nil:
			args[i] = Resolve(tr, *arg.Msg)
		case arg.Key != "":
			args[i] = tr.T(arg.Key)
		default:
			args[i] = arg.Value
		}
	}
	return tr.Tf(m.Key, args...)
}

// Renderer implements application.MessageRenderer for text rendered outside
// a request, where the language is explicit.
type Renderer struct{}

var _ application.MessageRenderer = Renderer{}

// Render resolves m in language.
func (Renderer) Render(language string, m application.Msg) string {
	return Resolve(i18n.New(language), m)
}
