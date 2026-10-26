package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Mentions inside a comment (issue #418). Three rules meet here:
//
//   - only a staff author mentions anybody; a requester writing "@Name" notifies
//     nobody, because the mention list is a staff list;
//   - a name is mentioned only when it belongs to somebody who can already open
//     the ticket, so a mention never grants access to the ticket or to the
//     comment;
//   - a mention subscribes the mentioned person to the ticket in the SAME
//     transaction as the comment, so a comment that is not stored subscribes
//     nobody, and a mention in an internal note reaches the mentioned staff
//     alone — never the requester.

func TestMentionNotifiesOnceAndSubscribes(t *testing.T) {
	h := newFanOutHarness(t)
	ctx := context.Background()

	c, err := h.svc.Add(ctx, h.admin, h.ticket.ID, "@Ana, could you check the logs?", "public")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	got := h.commentTx.notified(h.assignee.ID)
	if len(got) != 1 {
		t.Fatalf("mentioned rows = %+v, want exactly one", got)
	}
	n := got[0]
	if n.Kind != domain.NotificationMentioned {
		t.Errorf("kind = %q, want %q", n.Kind, domain.NotificationMentioned)
	}
	if n.CommentID == nil || *n.CommentID != c.ID {
		t.Errorf("comment id = %v, want the stored comment %d", n.CommentID, c.ID)
	}
	if n.ActorUserID == nil || *n.ActorUserID != h.admin.ID {
		t.Errorf("actor = %v, want the commenting admin %d", n.ActorUserID, h.admin.ID)
	}
	following, err := h.followSvc.IsFollowing(ctx, h.assignee, h.ticket.ID)
	if err != nil {
		t.Fatalf("IsFollowing: %v", err)
	}
	if !following {
		t.Fatal("a mentioned person did not become a follower")
	}
}

func TestMentionSubscribesInTheCommentTransaction(t *testing.T) {
	h := newFanOutHarness(t)
	h.commentTx.failNotifications = true

	_, err := h.svc.Add(context.Background(), h.admin, h.ticket.ID, "@Ana please confirm", "public")
	if err == nil {
		t.Fatal("Add succeeded although the notification insert was refused")
	}
	if !errors.Is(err, errNotificationAppendFailed) {
		t.Fatalf("Add error = %v, want the refused notification insert", err)
	}
	comments, err := h.comments.ListByTicket(context.Background(), h.ticket.ID, true)
	if err != nil {
		t.Fatalf("ListByTicket: %v", err)
	}
	if len(comments) != 0 {
		t.Fatalf("comments = %+v, want none (rolled back with the refused notification)", comments)
	}
	if following, _ := h.followers.IsFollowing(context.Background(), h.ticket.ID, h.assignee.ID); following {
		t.Fatal("the mention subscription survived a rolled-back comment")
	}
}

func TestMentionInAnInternalNoteReachesTheStaffAlone(t *testing.T) {
	h := newFanOutHarness(t)

	if _, err := h.svc.Add(context.Background(), h.admin, h.ticket.ID, "@Ana, the refund is approved", "internal"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	got := h.commentTx.notified(h.assignee.ID)
	if len(got) != 1 || got[0].Kind != domain.NotificationMentioned {
		t.Fatalf("mentioned staff rows = %+v, want one mentioned", got)
	}
	if rows := h.commentTx.notified(h.requester.ID); len(rows) != 0 {
		t.Fatalf("the requester was told about an internal note: %+v", rows)
	}
	if following, _ := h.followers.IsFollowing(context.Background(), h.ticket.ID, h.requester.ID); following {
		t.Fatal("the requester was subscribed to the ticket by an internal note")
	}
}

func TestRequesterCommentMentionsNobody(t *testing.T) {
	h := newFanOutHarness(t)
	requester := domain.User{ID: h.requester.ID, Name: h.requester.Name, Role: h.requester.Role}

	if _, err := h.svc.Add(context.Background(), requester, h.ticket.ID, "@Ada could you look at this?", "public"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	for _, n := range h.commentTx.notifications {
		if n.Kind == domain.NotificationMentioned {
			t.Fatalf("a requester's comment produced a mention: %+v", n)
		}
	}
	if following, _ := h.followers.IsFollowing(context.Background(), h.ticket.ID, h.admin.ID); following {
		t.Fatal("a requester's comment subscribed somebody to the ticket")
	}
}

func TestMentionOfSomebodyWhoCannotOpenTheTicketDoesNothing(t *testing.T) {
	h := newFanOutHarness(t)
	bea := h.users.seedRole("Bea", "bea@example.com", domain.RoleAgent, true)

	if _, err := h.svc.Add(context.Background(), h.admin, h.ticket.ID, "@Bea please take a look", "public"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	for _, n := range h.commentTx.notifications {
		if n.UserID == bea.ID || n.Kind == domain.NotificationMentioned {
			t.Fatalf("an out-of-scope name was treated as a mention: %+v", n)
		}
	}
	if following, _ := h.followers.IsFollowing(context.Background(), h.ticket.ID, bea.ID); following {
		t.Fatal("an out-of-scope name was subscribed to the ticket")
	}
}

func TestMentionNeedsTheWholeDisplayName(t *testing.T) {
	h := newFanOutHarness(t)
	bob := h.users.seedRole("Bob Ruiz", "bob@example.com", domain.RoleAdmin, true)

	if _, err := h.svc.Add(context.Background(), h.admin, h.ticket.ID, "@Bob Ruizz hi", "public"); err != nil {
		t.Fatalf("Add (longer word): %v", err)
	}
	if rows := h.commentTx.notified(bob.ID); len(rows) != 0 {
		t.Fatalf("a longer word mentioned Bob Ruiz: %+v", rows)
	}

	if _, err := h.svc.Add(context.Background(), h.admin, h.ticket.ID, "@Bob Ruiz, hi", "public"); err != nil {
		t.Fatalf("Add (exact name): %v", err)
	}
	rows := h.commentTx.notified(bob.ID)
	if len(rows) != 1 || rows[0].Kind != domain.NotificationMentioned {
		t.Fatalf("rows = %+v, want one mentioned", rows)
	}
}

// A managed user's display name may itself contain an "@". Mentioning the longer
// name must not stop the notification scan from also naming the person whose
// name starts inside it (issue #438 correction): the baseline notifies and
// subscribes BOTH, and the presentation splitter's non-overlapping spans never
// take that away.
func TestNestedDisplayNamesNotifyAndSubscribeEverybodyNamed(t *testing.T) {
	h := newFanOutHarness(t)
	ctx := context.Background()
	operations := h.users.seedRole("Operations @Ana", "operations@example.com", domain.RoleAdmin, true)

	if _, err := h.svc.Add(ctx, h.admin, h.ticket.ID, "@Operations @Ana please look", "public"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	for _, who := range []domain.User{operations, h.assignee} {
		rows := h.commentTx.notified(who.ID)
		if len(rows) != 1 || rows[0].Kind != domain.NotificationMentioned {
			t.Errorf("%q notifications = %+v, want exactly one mentioned", who.Name, rows)
		}
		following, err := h.followSvc.IsFollowing(ctx, who, h.ticket.ID)
		if err != nil {
			t.Fatalf("IsFollowing(%q): %v", who.Name, err)
		}
		if !following {
			t.Errorf("%q was named but not subscribed", who.Name)
		}
	}
}
