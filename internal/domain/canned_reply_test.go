package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Canned replies (issue #418): Validate mirrors the
// canned_replies CHECK of migration 0033 exactly — trimmed title 1..120
// characters, trimmed body 1..10000 characters, exactly one owner — and
// FillPlaceholders substitutes the two documented placeholders in ONE pass and
// leaves anything else as typed.

func ptr[T any](v T) *T { return &v }

func validPersonalReply() *domain.CannedReply {
	return &domain.CannedReply{
		OwnerUserID: ptr(int64(7)),
		Title:       "Greeting",
		Body:        "Hi {requester}, about {ticket}",
		CreatedAt:   time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC),
	}
}

func TestCannedReplyValidateAcceptsBothOwnersAndTrims(t *testing.T) {
	personal := validPersonalReply()
	personal.Title = "  Greeting  "
	personal.Body = "\n Hi {requester} \n"
	if err := personal.Validate(); err != nil {
		t.Fatalf("personal reply Validate: %v", err)
	}
	if personal.Title != "Greeting" || personal.Body != "Hi {requester}" {
		t.Fatalf("Validate kept %q / %q, want the trimmed text the store persists", personal.Title, personal.Body)
	}

	shared := &domain.CannedReply{DeskID: ptr(int64(3)), Title: "Escalation", Body: "Escalating {ticket}"}
	if err := shared.Validate(); err != nil {
		t.Fatalf("desk reply Validate: %v", err)
	}

	// The bounds are CHARACTER counts, like the CHECK's length(trim(...)): a
	// 120-rune title of multi-byte letters is 240 bytes and still valid, so a
	// byte count here would refuse what the database accepts.
	multibyte := &domain.CannedReply{
		OwnerUserID: ptr(int64(7)),
		Title:       strings.Repeat("á", domain.MaxCannedReplyTitleRunes),
		Body:        strings.Repeat("é", domain.MaxCannedReplyBodyRunes),
	}
	if err := multibyte.Validate(); err != nil {
		t.Fatalf("reply at both limits Validate: %v", err)
	}
}

func TestCannedReplyValidateRequiresExactlyOneOwner(t *testing.T) {
	ownerless := &domain.CannedReply{Title: "Greeting", Body: "Hi"}
	if err := ownerless.Validate(); !errors.Is(err, domain.ErrCannedReplyOwnerRequired) {
		t.Fatalf("ownerless reply Validate = %v, want ErrCannedReplyOwnerRequired", err)
	}

	ambiguous := &domain.CannedReply{OwnerUserID: ptr(int64(7)), DeskID: ptr(int64(3)), Title: "Greeting", Body: "Hi"}
	if err := ambiguous.Validate(); !errors.Is(err, domain.ErrCannedReplyOwnerRequired) {
		t.Fatalf("reply with two owners Validate = %v, want ErrCannedReplyOwnerRequired", err)
	}
}

func TestCannedReplyValidateBounds(t *testing.T) {
	cases := []struct {
		name  string
		build func() *domain.CannedReply
		field string
		key   string
		msg   string
	}{
		{
			name: "whitespace-only title",
			// The SQL CHECK is length(trim(title)), and SQLite's one-argument trim
			// strips SPACES only: a title of tabs and newlines passes the database.
			// The domain is therefore the first line of defence and trims all Unicode
			// whitespace (strings.TrimSpace), so this refusal never reaches the store.
			build: func() *domain.CannedReply { r := validPersonalReply(); r.Title = "\n\t "; return r },
			field: "title", key: "error.canned_reply_title_length", msg: domain.ErrMsgCannedReplyTitleLength,
		},
		{
			name: "title one character over the limit",
			build: func() *domain.CannedReply {
				r := validPersonalReply()
				r.Title = strings.Repeat("a", domain.MaxCannedReplyTitleRunes+1)
				return r
			},
			field: "title", key: "error.canned_reply_title_length", msg: domain.ErrMsgCannedReplyTitleLength,
		},
		{
			name: "whitespace-only body",
			// Same asymmetry as the title: SQLite's trim() would accept "\n\t ".
			build: func() *domain.CannedReply { r := validPersonalReply(); r.Body = "\n\t "; return r },
			field: "body", key: "error.canned_reply_body_length", msg: domain.ErrMsgCannedReplyBodyLength,
		},
		{
			name: "body one character over the limit",
			build: func() *domain.CannedReply {
				r := validPersonalReply()
				r.Body = strings.Repeat("b", domain.MaxCannedReplyBodyRunes+1)
				return r
			},
			field: "body", key: "error.canned_reply_body_length", msg: domain.ErrMsgCannedReplyBodyLength,
		},
	}
	for _, tc := range cases {
		err := tc.build().Validate()
		var validation *domain.ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("%s Validate = %v, want a ValidationError", tc.name, err)
		}
		if validation.Field != tc.field || validation.Key != tc.key || validation.Error() != tc.msg {
			t.Fatalf("%s Validate = %+v, want field %q key %q message %q", tc.name, validation, tc.field, tc.key, tc.msg)
		}
	}
}

func TestFillPlaceholdersReplacesBothPlaceholders(t *testing.T) {
	body := "Hi {requester}, this is about {ticket}. Again: {requester} / {ticket}."
	want := "Hi Ada Lovelace, this is about TKT-42. Again: Ada Lovelace / TKT-42."
	if got := domain.FillPlaceholders(body, "Ada Lovelace", 42); got != want {
		t.Fatalf("FillPlaceholders = %q, want %q", got, want)
	}
	if got := domain.FillPlaceholders("No placeholders here.", "Ada", 42); got != "No placeholders here." {
		t.Fatalf("FillPlaceholders without placeholders = %q, want the body unchanged", got)
	}
	if got := domain.FillPlaceholders("", "Ada", 42); got != "" {
		t.Fatalf("FillPlaceholders of an empty body = %q, want empty", got)
	}
}

// TestFillPlaceholdersLeavesUnknownPlaceholdersAsTyped is the documented rule: a
// {placeholder} nobody documented stays exactly as the author typed it, so a
// reply may still talk about braces and a future placeholder is never eaten.
// The match is literal and case-sensitive — {Ticket} and {ticket_id} are not
// {ticket} — and the substitution has no escape syntax, which is why neither
// test asserts one.
func TestFillPlaceholdersLeavesUnknownPlaceholdersAsTyped(t *testing.T) {
	body := "{greeting} {requester} writes {Ticket} {ticket_id} {requester_name}"
	want := "{greeting} Ada writes {Ticket} {ticket_id} {requester_name}"
	if got := domain.FillPlaceholders(body, "Ada", 7); got != want {
		t.Fatalf("FillPlaceholders = %q, want %q", got, want)
	}
}

// TestFillPlaceholdersIsOnePass proves the substitution cannot be re-expanded:
// a requester whose display name spells a placeholder is inserted as typed and
// never expanded again.
func TestFillPlaceholdersIsOnePass(t *testing.T) {
	if got := domain.FillPlaceholders("Hi {requester}!", "{ticket}", 9); got != "Hi {ticket}!" {
		t.Fatalf("FillPlaceholders = %q, want the typed name inserted unchanged", got)
	}
}
