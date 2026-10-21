package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Issue #344 slice B3, storage half (specs S10 comment rows, S12, S16; S11 is
// the contract they instantiate): the comment insert and the notifications the
// APPLICATION computed for it are ONE transaction. Before this slice the
// comment insert ran on its own connection (comment_store.go), so a comment and
// the news about it could not be committed — or refused — together.

// commentMutation is the fixture's comment from the given author.
func commentMutation(ticketID, authorID int64, visibility domain.CommentVisibility, at time.Time) *domain.Comment {
	return &domain.Comment{
		TicketID:     ticketID,
		Author:       "Ada",
		AuthorUserID: &authorID,
		AuthorRole:   domain.RoleAgent,
		Body:         "Deployed the fix to staging",
		Visibility:   visibility,
		CreatedAt:    at,
	}
}

// TestCommentUnitOfWorkCommitsTheCommentWithItsNotifications is the success
// half of S12: the comment, its notifications, and the full-text reindex the
// migration 0002 trigger performs all commit together, and every notification
// points at the comment row it describes and carries that comment's author as
// its actor (the users-deletion advisory: the comment already references that
// person, so the actor adds no reference a user deletion did not already have).
func TestCommentUnitOfWorkCommitsTheCommentWithItsNotifications(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	requester, agent, tk := mutationFixture(t, s)
	at := testClock.Add(time.Minute)

	c := commentMutation(tk.ID, agent, domain.CommentPublic, at)
	if err := s.CommentUnitOfWork().Add(ctx, application.CommentMutation{
		Comment: c,
		Notifications: []domain.Notification{
			{UserID: requester, TicketID: tk.ID, Kind: domain.NotificationCommentPublic,
				ActorUserID: &agent, CreatedAt: at},
		},
	}); err != nil {
		t.Fatalf("comment unit: %v", err)
	}
	if c.ID == 0 {
		t.Fatal("comment unit did not assign the comment id")
	}

	// The comment persisted with its authorship.
	comments, err := s.CommentStore().ListByTicket(ctx, tk.ID, true)
	if err != nil {
		t.Fatalf("list comments: %v", err)
	}
	if len(comments) != 1 || comments[0].ID != c.ID {
		t.Fatalf("comments = %+v, want the single inserted comment %d", comments, c.ID)
	}
	if comments[0].AuthorUserID == nil || *comments[0].AuthorUserID != agent {
		t.Errorf("stored author = %v, want the agent %d", comments[0].AuthorUserID, agent)
	}

	// The notification persisted, pointing at the comment.
	rows, err := s.NotificationStore().ListForUser(ctx, requester, 10)
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if len(rows) != 1 || rows[0].Kind != domain.NotificationCommentPublic {
		t.Fatalf("notifications = %+v, want one comment_public", rows)
	}
	if rows[0].CommentID == nil || *rows[0].CommentID != c.ID {
		t.Errorf("comment id = %v, want %d", rows[0].CommentID, c.ID)
	}
	if rows[0].ActorUserID == nil || *rows[0].ActorUserID != agent {
		t.Errorf("actor = %v, want the commenting author %d", rows[0].ActorUserID, agent)
	}
	// The comment body lives in comments, never in the notification row (S15,
	// S16): the row carries a kind and the comment's id.
	assertNotificationRowHoldsNoText(t, s.db, requester)

	// The comment reached the full-text index: the 0002 trigger fires inside the
	// unit of work's transaction exactly as it did on the plain insert.
	if got := ftsMatches(t, s.db, `"staging"`); got != 1 {
		t.Errorf("tickets_fts matches = %d, want the comment indexed once", got)
	}
}

// TestCommentUnitOfWorkRollsBackTheCommentWhenANotificationIsRefused is the
// failure half of S12/S11: the refused row is LAST, so the comment insert has
// already happened when validation fails. Only a shared transaction can leave
// no comment, no notification, and no full-text entry behind.
func TestCommentUnitOfWorkRollsBackTheCommentWhenANotificationIsRefused(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	requester, agent, tk := mutationFixture(t, s)
	at := testClock.Add(time.Minute)

	c := commentMutation(tk.ID, agent, domain.CommentInternal, at)
	err := s.CommentUnitOfWork().Add(ctx, application.CommentMutation{
		Comment: c,
		Notifications: []domain.Notification{
			{UserID: requester, TicketID: tk.ID, Kind: domain.NotificationCommentInternal,
				ActorUserID: &agent, CreatedAt: at},
			// A policy bug that must never be stored: the author is told about
			// his own comment. It is refused AFTER the comment insert, which is
			// what makes the shared transaction observable.
			{UserID: agent, TicketID: tk.ID, Kind: domain.NotificationCommentInternal,
				ActorUserID: &agent, CreatedAt: at},
		},
	})
	if err == nil {
		t.Fatal("comment unit accepted a notification addressed to its own actor")
	}
	var invariant *domain.NotificationInvariantError
	if !errors.As(err, &invariant) {
		t.Fatalf("error = %v, want a *domain.NotificationInvariantError", err)
	}

	comments, err := s.CommentStore().ListByTicket(ctx, tk.ID, true)
	if err != nil {
		t.Fatalf("list comments: %v", err)
	}
	if len(comments) != 0 {
		t.Fatalf("comments = %+v, want none (rolled back with the refused notification)", comments)
	}
	rows, err := s.NotificationStore().ListForUser(ctx, requester, 10)
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("notifications = %+v, want none", rows)
	}
	if got := ftsMatches(t, s.db, `"staging"`); got != 0 {
		t.Errorf("tickets_fts matches = %d, want the rolled-back comment absent from the index", got)
	}
}

// TestCommentUnitOfWorkRefusesANotificationFromAnotherActor pins the second
// invariant the comment unit adds to the generic append: a comment
// notification's actor is the comment's author. Anything else would name an
// actor the comment row does not reference, reviving the users-deletion
// advisory the B1 verifier raised.
func TestCommentUnitOfWorkRefusesANotificationFromAnotherActor(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	requester, agent, tk := mutationFixture(t, s)
	other := seedUserRole(t, s, "Otto", "otto@x", true, domain.RoleAgent)
	at := testClock.Add(time.Minute)

	c := commentMutation(tk.ID, agent, domain.CommentPublic, at)
	err := s.CommentUnitOfWork().Add(ctx, application.CommentMutation{
		Comment: c,
		Notifications: []domain.Notification{
			{UserID: requester, TicketID: tk.ID, Kind: domain.NotificationCommentPublic,
				ActorUserID: &other, CreatedAt: at},
		},
	})
	if err == nil {
		t.Fatal("comment unit accepted a notification whose actor is not the comment author")
	}
	if !strings.Contains(err.Error(), "actor") {
		t.Errorf("error = %v, want it to name the actor mismatch", err)
	}

	if comments, err := s.CommentStore().ListByTicket(ctx, tk.ID, true); err != nil {
		t.Fatalf("list comments: %v", err)
	} else if len(comments) != 0 {
		t.Fatalf("comments = %+v, want none (refused before commit)", comments)
	}
	if rows, err := s.NotificationStore().ListForUser(ctx, requester, 10); err != nil {
		t.Fatalf("list notifications: %v", err)
	} else if len(rows) != 0 {
		t.Fatalf("notifications = %+v, want none", rows)
	}
}

// ftsMatches counts the tickets_fts documents matching the expression. The
// index is contentless, so a MATCH count is the only readable answer — and the
// one that proves the trigger wrote (or rolled back) with the comment.
func ftsMatches(t *testing.T, db *sql.DB, expr string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tickets_fts WHERE tickets_fts MATCH ?`, expr).Scan(&n); err != nil {
		t.Fatalf("query fts %q: %v", expr, err)
	}
	return n
}

// assertNotificationRowHoldsNoText proves a comment notification stores a KIND
// and the comment's id, never the body or any rendered sentence.
func assertNotificationRowHoldsNoText(t *testing.T, db *sql.DB, userID int64) {
	t.Helper()
	var from, to, kind sql.NullString
	var commentID sql.NullInt64
	if err := db.QueryRow(`SELECT from_value, to_value, kind, comment_id FROM notifications WHERE user_id = ?`, userID).
		Scan(&from, &to, &kind, &commentID); err != nil {
		t.Fatalf("read notification row: %v", err)
	}
	if from.Valid || to.Valid {
		t.Errorf("notification carries change facts from=%v to=%v, want none for a comment", from, to)
	}
	if strings.Contains(kind.String, "Deployed") {
		t.Errorf("notification kind %q carries the comment text", kind.String)
	}
	if !commentID.Valid {
		t.Error("comment notification has no comment id")
	}
}
