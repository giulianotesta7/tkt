package domain

import (
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Canned reply bounds (issue #418). They mirror the canned_replies CHECK of
// migration 0033 exactly, so the application and the database cannot disagree
// about what a savable reply is. The bounds count CHARACTERS, exactly like
// SQLite's length(trim(...)), which is why a 120-rune title of multi-byte
// letters is accepted rather than measured in bytes.
const (
	MaxCannedReplyTitleRunes = 120
	MaxCannedReplyBodyRunes  = 10000
)

// English user-facing message constants for the saved-reply bounds: the English
// wording lives in the domain and the presentation layer maps the
// ValidationError's Key to the catalogue sentence.
const (
	ErrMsgCannedReplyTitleLength = "the title must be from 1 to 120 characters"
	ErrMsgCannedReplyBodyLength  = "the reply text must be from 1 to 10,000 characters"
)

// ErrCannedReplyOwnerRequired names the ownership invariant of a saved reply:
// it belongs to exactly one of a person or a desk — never both and never
// neither (the canned_replies CHECK). It is a plain sentinel and
// deliberately NOT a ValidationError: no user input can produce this state, so
// it guards a programming error rather than a form field, and it is therefore
// outside the catalogue-key contract every user-visible refusal follows.
var ErrCannedReplyOwnerRequired = errors.New("a saved reply belongs to exactly one person or one desk")

// CannedReply is a reusable reply an agent inserts into the comment box
// (issue #418). Exactly one of OwnerUserID (a personal reply, private to
// its owner) and DeskID (a shared reply every member of that desk may use) is
// set; Validate enforces it.
//
// Title is the short label the picker and the manager list show; Body is the
// reply text itself, which may carry the {requester} and {ticket} placeholders
// FillPlaceholders substitutes when the reply is inserted.
type CannedReply struct {
	ID    int64
	Title string
	Body  string
	// OwnerUserID is the user a personal reply belongs to; nil for a shared
	// reply.
	OwnerUserID *int64
	// DeskID is the desk a shared reply belongs to; nil for a personal reply.
	DeskID    *int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Validate normalizes the two text fields in place — it trims them, and the
// caller persists exactly what was validated — and refuses a reply the
// canned_replies CHECK would refuse:
//
//   - the title, trimmed, must be 1..120 characters;
//   - the body, trimmed, must be 1..10000 characters;
//   - exactly one of OwnerUserID and DeskID must be set.
//
// The first two are user-visible refusals: they carry the field, the English
// message and the catalogue Key the form renders beside that field. The
// ownership invariant returns the ErrCannedReplyOwnerRequired sentinel, because
// it is a programming guard (see its documentation).
func (r *CannedReply) Validate() error {
	if (r.OwnerUserID == nil) == (r.DeskID == nil) {
		return ErrCannedReplyOwnerRequired
	}
	r.Title = strings.TrimSpace(r.Title)
	r.Body = strings.TrimSpace(r.Body)
	switch {
	case r.Title == "" || utf8.RuneCountInString(r.Title) > MaxCannedReplyTitleRunes:
		return &ValidationError{Field: "title", Message: ErrMsgCannedReplyTitleLength, Key: "error.canned_reply_title_length"}
	case r.Body == "" || utf8.RuneCountInString(r.Body) > MaxCannedReplyBodyRunes:
		return &ValidationError{Field: "body", Message: ErrMsgCannedReplyBodyLength, Key: "error.canned_reply_body_length"}
	}
	return nil
}

// FillPlaceholders substitutes the two documented placeholders of a saved reply
// (issue #418) on the server, when the reply is inserted into the comment
// box:
//
//	{requester} -> the ticket's requester display name
//	{ticket}    -> the readable ticket number, "TKT-<n>", the same format the
//	               ticket views render
//
// Every other {placeholder} stays exactly as the author typed it, which is the
// documented behavior. The substitution is deliberately ONE pass, so text
// substituted in is never scanned again: a requester whose display name spells
// a placeholder can never be expanded a second time.
func FillPlaceholders(body, requesterName string, ticketNumber int) string {
	return strings.NewReplacer(
		"{requester}", requesterName,
		"{ticket}", "TKT-"+strconv.Itoa(ticketNumber),
	).Replace(body)
}
