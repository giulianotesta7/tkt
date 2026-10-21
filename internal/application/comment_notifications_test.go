package application_test

import (
	"context"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Issue #344 slice B3: the two S10 COMMENT rows, plus S16 (a requester never
// learns that an internal comment exists) and S12 (the comment and its
// notifications are one write). The recipients are computed by the
// APPLICATION layer from policy and roles and stamped into the comment
// mutation, so these tests read what the comment unit of work recorded.

// commentNotifyHarness is the B2 notification fixture plus the comment service
// under test: same seeded users, same ticket, same clock, and a comment unit of
// work that records the rows the service stamped.
type commentNotifyHarness struct {
	*notifyHarness
	commentTx *fakeCommentUnitOfWork
	svc       *application.CommentService
}

func newCommentNotifyHarness(t *testing.T) *commentNotifyHarness {
	t.Helper()
	h := &commentNotifyHarness{notifyHarness: newNotifyHarness(t)}
	h.commentTx = newFakeCommentUnitOfWork(h.comments)
	h.svc = application.NewCommentService(h.tickets, h.comments, h.users, h.commentTx, h.clock)
	return h
}

// notified returns the recorded comment notifications for one recipient.
func (h *commentNotifyHarness) notified(userID int64) []domain.Notification {
	return h.commentTx.notified(userID)
}

// requireNone fails when ANY comment notification was recorded, for any
// recipient: the negative half of the S10 comment rows and of S16.
func (h *commentNotifyHarness) requireNone(t *testing.T) {
	t.Helper()
	if len(h.commentTx.notifications) != 0 {
		t.Fatalf("recorded %d notifications, want none: %+v", len(h.commentTx.notifications), h.commentTx.notifications)
	}
}

// TestNotificationOnPublicComment covers the S10 "public comment" row: the
// requester and the assignee each learn about it, once, pointing at the stored
// comment and carrying its author as the actor.
func TestNotificationOnPublicComment(t *testing.T) {
	h := newCommentNotifyHarness(t)
	h.clock.Advance(time.Minute)

	c, err := h.svc.Add(context.Background(), h.admin, h.ticket.ID, "It is fixed on staging", "public")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if c.ID == 0 {
		t.Fatal("Add: the comment unit of work must assign the comment id")
	}

	for _, who := range []struct {
		name string
		user domain.User
	}{{"requester", h.requester}, {"assignee", h.assignee}} {
		got := h.notified(who.user.ID)
		if len(got) != 1 {
			t.Fatalf("%s has %d notifications, want 1: %+v", who.name, len(got), got)
		}
		n := got[0]
		if n.Kind != domain.NotificationCommentPublic {
			t.Errorf("%s kind = %q, want %q", who.name, n.Kind, domain.NotificationCommentPublic)
		}
		if n.TicketID != h.ticket.ID {
			t.Errorf("%s ticket = %d, want %d", who.name, n.TicketID, h.ticket.ID)
		}
		if n.CommentID == nil || *n.CommentID != c.ID {
			t.Errorf("%s comment id = %v, want the stored comment %d", who.name, n.CommentID, c.ID)
		}
		if n.ActorUserID == nil || *n.ActorUserID != h.admin.ID {
			t.Errorf("%s actor = %v, want the commenting admin %d", who.name, n.ActorUserID, h.admin.ID)
		}
		// S15/S16: a comment notification stores the KIND and the comment's id,
		// never the comment text and never a rendered sentence.
		assertFact(t, who.name+" from", n.FromValue, "")
		assertFact(t, who.name+" to", n.ToValue, "")
	}

	// The author is the actor: nobody is notified about their own comment.
	if got := h.notified(h.admin.ID); len(got) != 0 {
		t.Fatalf("the commenting admin has %d notifications, want none: %+v", len(got), got)
	}
	if len(h.commentTx.notifications) != 2 {
		t.Fatalf("%d notifications, want exactly the requester's and the assignee's: %+v",
			len(h.commentTx.notifications), h.commentTx.notifications)
	}
	// Every comment notification's actor is the comment's author (the
	// users-deletion advisory: the comment row already references that person).
	if c.AuthorUserID == nil || *c.AuthorUserID != h.admin.ID {
		t.Fatalf("comment author = %v, want the admin %d", c.AuthorUserID, h.admin.ID)
	}
	for _, n := range h.commentTx.notifications {
		if n.ActorUserID == nil || *n.ActorUserID != *c.AuthorUserID {
			t.Fatalf("notification actor %v is not the comment author %v", n.ActorUserID, c.AuthorUserID)
		}
	}
}

// TestNotificationOnInternalCommentNeverReachesRequester is S10 "internal
// comment → assignee, requester NEVER" and the S16 acceptance criterion: a
// requester gets ZERO rows about an internal comment, in every combination.
func TestNotificationOnInternalCommentNeverReachesRequester(t *testing.T) {
	t.Run("admin's internal comment notifies only the assignee", func(t *testing.T) {
		h := newCommentNotifyHarness(t)
		c, err := h.svc.Add(context.Background(), h.admin, h.ticket.ID, "Staff only: customer is upset", "internal")
		if err != nil {
			t.Fatalf("Add: %v", err)
		}
		got := h.notified(h.assignee.ID)
		if len(got) != 1 || got[0].Kind != domain.NotificationCommentInternal {
			t.Fatalf("assignee notifications = %+v, want one %q", got, domain.NotificationCommentInternal)
		}
		if got[0].CommentID == nil || *got[0].CommentID != c.ID {
			t.Errorf("assignee comment id = %v, want %d", got[0].CommentID, c.ID)
		}
		// THE S16 GUARANTEE: the requester has no row at all, so no panel,
		// count, or title can invent an internal comment's existence.
		if got := h.notified(h.requester.ID); len(got) != 0 {
			t.Fatalf("requester has %d notifications about an internal comment, want 0: %+v", len(got), got)
		}
		if len(h.commentTx.notifications) != 1 {
			t.Fatalf("%d notifications, want only the assignee's: %+v", len(h.commentTx.notifications), h.commentTx.notifications)
		}
	})

	t.Run("assignee's own internal comment notifies nobody", func(t *testing.T) {
		h := newCommentNotifyHarness(t)
		if _, err := h.svc.Add(context.Background(), h.assignee, h.ticket.ID, "Mine, for the record", "internal"); err != nil {
			t.Fatalf("Add: %v", err)
		}
		h.requireNone(t)
	})

	t.Run("a recipient without CapCommentInternal is never told", func(t *testing.T) {
		h := newCommentNotifyHarness(t)
		// A legacy ticket can name a user-role assignee: assignment rules stop
		// that today, but the notification policy must not depend on it. A user
		// cannot read internal comments, so learning one exists would leak it.
		userAssignee := h.users.seedRole("Uma", "uma@example.com", domain.RoleUser, true)
		legacy := h.tickets.seed(domain.Ticket{
			Title: "Legacy assignee", CategoryID: h.ticket.CategoryID,
			RequesterUserID: ptr(h.requester.ID), UserID: ptr(userAssignee.ID),
			Priority: domain.PriorityMedium, State: domain.StateNew,
			CreatedAt: h.clock.now, UpdatedAt: h.clock.now,
		})
		if _, err := h.svc.Add(context.Background(), h.admin, legacy.ID, "Staff only", "internal"); err != nil {
			t.Fatalf("Add: %v", err)
		}
		h.requireNone(t)
	})
}

// TestNotificationOnPublicCommentByRequester covers the same public row from
// the requester's side: the assignee learns about it, and the requester — who
// is the actor — does not.
func TestNotificationOnPublicCommentByRequester(t *testing.T) {
	h := newCommentNotifyHarness(t)
	c, err := h.svc.Add(context.Background(), h.requester, h.ticket.ID, "Any news?", "public")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	got := h.notified(h.assignee.ID)
	if len(got) != 1 || got[0].Kind != domain.NotificationCommentPublic {
		t.Fatalf("assignee notifications = %+v, want one %q", got, domain.NotificationCommentPublic)
	}
	if got[0].ActorUserID == nil || *got[0].ActorUserID != h.requester.ID {
		t.Errorf("actor = %v, want the requesting user %d", got[0].ActorUserID, h.requester.ID)
	}
	if got[0].CommentID == nil || *got[0].CommentID != c.ID {
		t.Errorf("comment id = %v, want %d", got[0].CommentID, c.ID)
	}
	if got := h.notified(h.requester.ID); len(got) != 0 {
		t.Fatalf("the commenting requester has %d notifications, want none: %+v", len(got), got)
	}
}

// TestNotificationOnCommentDropsUnreachableRecipients proves the second S10
// rule for comments: someone who cannot open the ticket is never told about a
// comment on it — a deactivated account cannot open anything.
func TestNotificationOnCommentDropsUnreachableRecipients(t *testing.T) {
	h := newCommentNotifyHarness(t)
	for _, u := range []domain.User{h.requester, h.assignee} {
		u.Active = false
		if err := h.users.Update(context.Background(), &u); err != nil {
			t.Fatalf("deactivate %q: %v", u.Email, err)
		}
	}

	if _, err := h.svc.Add(context.Background(), h.admin, h.ticket.ID, "Anyone there?", "public"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	h.requireNone(t)
}
