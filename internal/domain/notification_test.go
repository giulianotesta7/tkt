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
	for _, k := range []NotificationKind{NotificationAssigned, NotificationStateChanged, NotificationFormWaiting} {
		if k.IsComment() {
			t.Errorf("%q.IsComment() = true, want false", k)
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
