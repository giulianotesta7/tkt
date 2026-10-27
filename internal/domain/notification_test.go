package domain

import (
	"testing"
	"time"
)

// Issue #344 slice B1: the notification kind vocabulary and its value rules
// (spec S10 rows, S15 "kind + structured facts, never rendered text").
// Written against the domain API that does not exist yet (strict TDD RED).

func notifStrPtr(s string) *string { return &s }
func ptrInt64(v int64) *int64      { return &v }
func ptrInt(v int) *int            { return &v }

func TestNotificationKindValid(t *testing.T) {
	// Every row of the S10 table has exactly one kind; the vocabulary is
	// closed, so anything else fails closed.
	for _, k := range []NotificationKind{
		NotificationAssigned,
		NotificationStateChanged,
		NotificationResolvedAwaitingConfirmation,
		NotificationResolutionConfirmed,
		NotificationResolutionRejected,
		NotificationPriorityChanged,
		NotificationCommentPublic,
		NotificationCommentInternal,
		NotificationFormWaiting,
		NotificationOnHold,
		// The automatic hold expiry of issue #353 adds the reminder and the
		// close notice to the same closed vocabulary.
		NotificationHoldReminder,
		NotificationHoldClosed,
		// The ticketless account announcement of issue #419 is a kind of its
		// own, so it cannot be confused with a ticket event.
		NotificationCompanyAccountPending,
		// Issue #418 adds the mention and the follower's assignee change.
		NotificationMentioned,
		NotificationAssigneeChanged,
	} {
		if !k.Valid() {
			t.Errorf("%q.Valid() = false, want true", k)
		}
	}
	for _, k := range []NotificationKind{"", "mention", "assignee", "ASSIGNED", "assigned "} {
		if k.Valid() {
			t.Errorf("%q.Valid() = true, want false", k)
		}
	}
}

func TestNotificationKindIsComment(t *testing.T) {
	// A mention is written from a comment and addressed to staff only, so it
	// carries a comment id like the two comment kinds do (issue #418).
	for _, k := range []NotificationKind{NotificationCommentPublic, NotificationCommentInternal, NotificationMentioned} {
		if !k.IsComment() {
			t.Errorf("%q.IsComment() = false, want true", k)
		}
	}
	for _, k := range []NotificationKind{NotificationAssigned, NotificationStateChanged, NotificationFormWaiting,
		NotificationCompanyAccountPending} {
		if k.IsComment() {
			t.Errorf("%q.IsComment() = true, want false", k)
		}
	}
}

// validAccountNotification is the ticketless account kind of issue #419 (S3):
// an account waits for approval and an administrator learns about it in the
// bell. Its subject is the pending account, so it names no ticket.
func validAccountNotification() Notification {
	pending := int64(23)
	return Notification{
		UserID:        7,
		Kind:          NotificationCompanyAccountPending,
		PendingUserID: &pending,
		CreatedAt:     time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC),
	}
}

func TestNotificationCompanyAccountPendingValidate(t *testing.T) {
	if err := validAccountNotification().Validate(); err != nil {
		t.Fatalf("account notification rejected: %v", err)
	}

	// The recipient is whoever has to act and the subject is the account being
	// announced; they are independent facts, so an announcement caused by one
	// administrator may still be addressed to another.
	withActor := validAccountNotification()
	actor := int64(8)
	withActor.ActorUserID = &actor
	if err := withActor.Validate(); err != nil {
		t.Fatalf("account notification with another actor rejected: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Notification)
		field  string
	}{
		// The account kind is about the account, never about a ticket: a
		// ticket id here would make the panel open a ticket that has nothing
		// to do with the announcement.
		{"account kind naming a ticket", func(n *Notification) { n.TicketID = 11 }, "ticket_id"},
		// A positive subject user is what the notification announces; without
		// it the row would be about nothing.
		{"account kind without a subject", func(n *Notification) { n.PendingUserID = nil }, "pending_user_id"},
		{"account kind with a zero subject", func(n *Notification) {
			zero := int64(0)
			n.PendingUserID = &zero
		}, "pending_user_id"},
		{"account kind with a negative subject", func(n *Notification) {
			negative := int64(-3)
			n.PendingUserID = &negative
		}, "pending_user_id"},
		// The account kind is a system announcement: it carries no change
		// facts, no workflow step and no comment, so the renderer has exactly
		// one sentence to render.
		{"account kind with a comment", func(n *Notification) { n.CommentID = ptrInt64(9) }, "comment_id"},
		{"account kind with a from value", func(n *Notification) { n.FromValue = notifStrPtr("new") }, "from_value"},
		{"account kind with a to value", func(n *Notification) { n.ToValue = notifStrPtr("in_progress") }, "to_value"},
		{"account kind with a step index", func(n *Notification) { n.StepIndex = ptrInt(2) }, "step_index"},
		// The rules every kind keeps: a recipient, an instant, and never the
		// actor as the recipient.
		{"account kind without a recipient", func(n *Notification) { n.UserID = 0 }, "user_id"},
		{"account kind without created_at", func(n *Notification) { n.CreatedAt = time.Time{} }, "created_at"},
		{"account kind addressed to its actor", func(n *Notification) {
			actor := n.UserID
			n.ActorUserID = &actor
		}, "actor_user_id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := validAccountNotification()
			tc.mutate(&n)
			err := n.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want a %s validation error", tc.field)
			}
			ie, ok := err.(*NotificationInvariantError)
			if !ok {
				t.Fatalf("Validate() error type = %T, want *NotificationInvariantError", err)
			}
			if ie.Field != tc.field {
				t.Errorf("NotificationInvariantError.Field = %q, want %q", ie.Field, tc.field)
			}
			if ie.Message == "" {
				t.Error("NotificationInvariantError.Message is empty, want the domain wording")
			}
		})
	}
}

// TestNotificationTicketKindsRefuseAnAccountSubject is the other half of the
// closed shape: only the account kind names a pending account, so a ticket
// notification can never be read as an approval request.
func TestNotificationTicketKindsRefuseAnAccountSubject(t *testing.T) {
	for _, kind := range []NotificationKind{
		NotificationAssigned, NotificationStateChanged, NotificationCommentPublic,
		NotificationHoldReminder, NotificationNoMovement,
	} {
		n := validNotification()
		n.Kind = kind
		n.CommentID = nil
		pending := int64(23)
		n.PendingUserID = &pending
		err := n.Validate()
		ie, ok := err.(*NotificationInvariantError)
		if !ok || ie.Field != "pending_user_id" {
			t.Errorf("%q with an account subject = %v, want a *NotificationInvariantError on pending_user_id", kind, err)
		}
	}
}

func validNotification() Notification {
	return Notification{
		UserID:    7,
		TicketID:  11,
		Kind:      NotificationStateChanged,
		FromValue: notifStrPtr("resolved"),
		ToValue:   notifStrPtr("in_progress"),
		CreatedAt: time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC),
	}
}

func TestNotificationValidate(t *testing.T) {
	if err := validNotification().Validate(); err != nil {
		t.Fatalf("valid notification rejected: %v", err)
	}

	comment := validNotification()
	comment.Kind = NotificationCommentInternal
	comment.CommentID = ptrInt64(42)
	if err := comment.Validate(); err != nil {
		t.Fatalf("comment notification with its comment id rejected: %v", err)
	}

	// An automatic workflow event has no user actor: nil is legal, it means
	// "the system did it" (S10 automatic rows, actor_user_id NULL).
	automatic := validNotification()
	automatic.Kind = NotificationAssigned
	automatic.ActorUserID = nil
	if err := automatic.Validate(); err != nil {
		t.Fatalf("automatic notification rejected: %v", err)
	}

	// The two hold-expiry kinds of issue #353 carry no from/to/comment/step
	// facts at all, and they are system rows: no actor is required.
	for _, kind := range []NotificationKind{NotificationHoldReminder, NotificationHoldClosed} {
		hold := Notification{UserID: 7, TicketID: 11, Kind: kind,
			CreatedAt: time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)}
		if err := hold.Validate(); err != nil {
			t.Errorf("%q notification without change facts rejected: %v", kind, err)
		}
	}

	// Issue #418: a mention carries its comment id and an assignee change
	// carries the new assignee's name, or nothing when the ticket is now
	// unassigned.
	mentioned := validNotification()
	mentioned.Kind = NotificationMentioned
	mentioned.CommentID = ptrInt64(43)
	if err := mentioned.Validate(); err != nil {
		t.Fatalf("mention with its comment id rejected: %v", err)
	}
	assigneeChanged := validNotification()
	assigneeChanged.Kind = NotificationAssigneeChanged
	assigneeChanged.FromValue = nil
	assigneeChanged.ToValue = notifStrPtr("Bob Ray")
	if err := assigneeChanged.Validate(); err != nil {
		t.Fatalf("assignee change naming the new assignee rejected: %v", err)
	}
	unassigned := validNotification()
	unassigned.Kind = NotificationAssigneeChanged
	unassigned.FromValue = nil
	unassigned.ToValue = nil
	if err := unassigned.Validate(); err != nil {
		t.Fatalf("assignee change with a NULL new assignee rejected: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Notification)
		field  string
	}{
		{"unknown kind", func(n *Notification) { n.Kind = "mention" }, "kind"},
		{"missing recipient", func(n *Notification) { n.UserID = 0 }, "user_id"},
		{"missing ticket", func(n *Notification) { n.TicketID = 0 }, "ticket_id"},
		{"missing created_at", func(n *Notification) { n.CreatedAt = time.Time{} }, "created_at"},
		// S10 "never the person who performed the action": the domain is the
		// last place that can refuse a self-notification.
		{"recipient is the actor", func(n *Notification) {
			actor := n.UserID
			n.ActorUserID = &actor
		}, "actor_user_id"},
		// A comment notification without its comment is unreferenceable: the
		// panel could never open it, so it is refused instead of stored.
		{"comment kind without comment id", func(n *Notification) {
			n.Kind = NotificationCommentPublic
		}, "comment_id"},
		{"comment id on a non-comment kind", func(n *Notification) {
			n.CommentID = ptrInt64(9)
		}, "comment_id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := validNotification()
			tc.mutate(&n)
			err := n.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want a %s validation error", tc.field)
			}
			ie, ok := err.(*NotificationInvariantError)
			if !ok {
				t.Fatalf("Validate() error type = %T, want *NotificationInvariantError", err)
			}
			if ie.Field != tc.field {
				t.Errorf("NotificationInvariantError.Field = %q, want %q", ie.Field, tc.field)
			}
			if ie.Message == "" {
				t.Error("NotificationInvariantError.Message is empty, want the domain wording")
			}
		})
	}
}
