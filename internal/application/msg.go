package application

import "fmt"

// Msg is one catalogue-backed user-facing message: a stable key plus the values
// its sentence interpolates. It carries NO locale and no English copy — the
// application layer decides WHICH sentence a fact produces, and the
// presentation layer resolves it through the active instance catalogue
// (issue #335, S8). The zero value renders nothing.
//
// A message reference is built with MsgRef(key, args...), arg values with Text
// (admin-authored or resolved data, never translated) and arg placeholders with
// Copy (a catalogue key the presentation layer resolves, such as the
// Unassigned / Unknown user degrade labels).
type Msg struct {
	Key  string
	Args []Arg
	// Raw is data rendered verbatim, never through a catalogue lookup: an
	// admin-authored desk name or step instruction that a composed message
	// carries as one field. A non-empty Key wins over Raw. The zero value
	// renders nothing.
	Raw string
}

// Arg is one interpolation value of a Msg. A non-empty Key wins: it names a
// catalogue entry the presentation layer resolves; otherwise Value is used
// verbatim (a user name, a category name, a state code).
type Arg struct {
	Key   string
	Value string
	// Msg is a NESTED message: a sentence used as one value of another
	// sentence, e.g. a workflow badge inside an attributed status line
	// ("Published v2 · by Ana"). The presentation layer resolves it first.
	Msg *Msg
}

// Msg builds a message reference. The key must exist in every shipped
// catalogue: i18n's reference-coverage test scans the Go sources for Msg keys
// and fails when one is missing.
func MsgRef(key string, args ...Arg) Msg { return Msg{Key: key, Args: args} }

// MsgValues builds a message reference from raw data values: each value is
// interpolated verbatim, never resolved through a catalogue. It is the form a
// store uses when a domain finding carries a key plus plain strings (a desk
// name).
func MsgValues(key string, values ...any) Msg {
	args := make([]Arg, len(values))
	for i, value := range values {
		args[i] = Text(fmt.Sprint(value))
	}
	return Msg{Key: key, Args: args}
}

// Data is a message that is not catalogue copy: it renders its value verbatim,
// with no lookup and no fallback. A view field that is sometimes copy and
// sometimes admin-authored content (a step summary, a desk name) uses it for
// the content half, so the template has ONE rendering path.
func Data(value string) Msg { return Msg{Raw: value} }

// Empty reports whether the message renders nothing.
func (m Msg) Empty() bool { return m.Key == "" && m.Raw == "" }

// Text wraps a resolved value (a user or category name, a state code) that is
// never translated.
func Text(value string) Arg { return Arg{Value: value} }

// Copy wraps a catalogue key the presentation layer resolves, so a message can
// interpolate a translated placeholder.
func Copy(key string) Arg { return Arg{Key: key} }

// Nested wraps a message as one value of another message, so a composed
// sentence can embed a translated sentence without either one owning copy.
func Nested(message Msg) Arg { return Arg{Msg: &message} }
