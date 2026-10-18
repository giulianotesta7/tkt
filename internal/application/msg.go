package application

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

// Text wraps a resolved value (a user or category name, a state code) that is
// never translated.
func Text(value string) Arg { return Arg{Value: value} }

// Copy wraps a catalogue key the presentation layer resolves, so a message can
// interpolate a translated placeholder.
func Copy(key string) Arg { return Arg{Key: key} }

// Nested wraps a message as one value of another message, so a composed
// sentence can embed a translated sentence without either one owning copy.
func Nested(message Msg) Arg { return Arg{Msg: &message} }
