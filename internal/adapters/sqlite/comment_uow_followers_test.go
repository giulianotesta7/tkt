package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// A mention subscribes the mentioned person IN the comment's transaction (issue
// #418): the comment row, the follower rows it adds, and the notifications
// commit or roll back together, so a subscription can never describe a comment
// that was never stored.

// TestCommentUnitOfWorkSubscribesMentionedPeople proves the follower rows land
// with the comment, stamped with the comment's own instant, and that mentioning
// an existing follower changes nothing.
func TestCommentUnitOfWorkSubscribesMentionedPeople(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	_, agent, tk := mutationFixture(t, s)
	mentioned := seedUserRole(t, s, "Nora", "nora@x", true, domain.RoleAgent)
	at := testClock.Add(time.Minute)

	// The agent already follows the ticket before the comment mentions them.
	if err := s.TicketFollowerStore().Follow(ctx, tk.ID, agent, testClock); err != nil {
		t.Fatalf("Follow: %v", err)
	}

	c := commentMutation(tk.ID, agent, domain.CommentPublic, at)
	if err := s.CommentUnitOfWork().Add(ctx, application.CommentMutation{
		Comment:       c,
		Followers:     []int64{mentioned, agent},
		Notifications: []domain.Notification{{UserID: mentioned, TicketID: tk.ID, Kind: domain.NotificationMentioned, ActorUserID: &agent, CreatedAt: at}},
	}); err != nil {
		t.Fatalf("comment unit: %v", err)
	}

	followers, err := s.TicketFollowerStore().ListFollowers(ctx, tk.ID)
	if err != nil {
		t.Fatalf("ListFollowers: %v", err)
	}
	if len(followers) != 2 {
		t.Fatalf("followers = %+v, want the mentioned person and the existing follower", followers)
	}
	instants := map[int64]string{}
	rows, err := s.db.QueryContext(ctx, `SELECT user_id, created_at FROM ticket_followers WHERE ticket_id = ?`, tk.ID)
	if err != nil {
		t.Fatalf("read follower rows: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var createdAt string
		if err := rows.Scan(&id, &createdAt); err != nil {
			t.Fatalf("scan follower row: %v", err)
		}
		instants[id] = createdAt
	}
	if got := instants[mentioned]; got != formatTime(at) {
		t.Errorf("mentioned follower created_at = %q, want the comment instant %q", got, formatTime(at))
	}
	if got := instants[agent]; got != formatTime(testClock) {
		t.Errorf("existing follower created_at = %q, want the original follow %q", got, formatTime(testClock))
	}

	// The mention row points at the stored comment.
	stored, err := s.NotificationStore().ListForUser(ctx, mentioned, 10)
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if len(stored) != 1 || stored[0].Kind != domain.NotificationMentioned {
		t.Fatalf("notifications = %+v, want one mentioned", stored)
	}
	if stored[0].CommentID == nil || *stored[0].CommentID != c.ID {
		t.Errorf("comment id = %v, want %d", stored[0].CommentID, c.ID)
	}
}

// TestCommentUnitOfWorkRollsBackTheCommentAndItsFollowers is the failure half:
// the refused notification is LAST, so the comment and the follower rows it
// added have already been written when the write fails. Only a shared
// transaction leaves none of them behind.
func TestCommentUnitOfWorkRollsBackTheCommentAndItsFollowers(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	_, agent, tk := mutationFixture(t, s)
	mentioned := seedUserRole(t, s, "Nora", "nora@x", true, domain.RoleAgent)
	at := testClock.Add(time.Minute)

	c := commentMutation(tk.ID, agent, domain.CommentPublic, at)
	err := s.CommentUnitOfWork().Add(ctx, application.CommentMutation{
		Comment:   c,
		Followers: []int64{mentioned},
		Notifications: []domain.Notification{
			{UserID: mentioned, TicketID: tk.ID, Kind: domain.NotificationMentioned, ActorUserID: &agent, CreatedAt: at},
			// A policy bug that must never be stored: the author is told about his
			// own comment. It is refused AFTER the comment and the follower rows.
			{UserID: agent, TicketID: tk.ID, Kind: domain.NotificationMentioned, ActorUserID: &agent, CreatedAt: at},
		},
	})
	if err == nil {
		t.Fatal("comment unit accepted a notification addressed to its own actor")
	}

	comments, err := s.CommentStore().ListByTicket(ctx, tk.ID, true)
	if err != nil {
		t.Fatalf("list comments: %v", err)
	}
	if len(comments) != 0 {
		t.Fatalf("comments = %+v, want none (rolled back with the refused notification)", comments)
	}
	followers, err := s.TicketFollowerStore().ListFollowers(ctx, tk.ID)
	if err != nil {
		t.Fatalf("ListFollowers: %v", err)
	}
	if len(followers) != 0 {
		t.Fatalf("followers = %+v, want none (rolled back with the comment)", followers)
	}
	if stored, err := s.NotificationStore().ListForUser(ctx, mentioned, 10); err != nil {
		t.Fatalf("list notifications: %v", err)
	} else if len(stored) != 0 {
		t.Fatalf("notifications = %+v, want none", stored)
	}
}
