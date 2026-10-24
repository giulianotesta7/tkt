package sqlite

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// TestMigration0021KindVocabularyMatchesDomainConstants derives the kind list
// from the domain source, so a new domain.NotificationKind cannot drift past
// the current CHECK without a red test (issue #344, S10/S18). Contributed by
// the independent verifier. The CHECK it probes is the CURRENT one: 0023
// rebuilt the table to add on_hold (issue #352), 0027 for hold_reminder and
// hold_closed (issue #353), 0028 for the SLA alert kinds, and 0034 for the
// ticketless account kind (issue #419), so a new kind needs the same rebuild.
func TestMigration0021KindVocabularyMatchesDomainConstants(t *testing.T) {
	s := newTestDB(t)
	f, err := parser.ParseFile(token.NewFileSet(), "../../domain/notification.go", nil, 0)
	if err != nil {
		t.Fatalf("parse domain source: %v", err)
	}
	var kinds []string
	ast.Inspect(f, func(n ast.Node) bool {
		gd, ok := n.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			return true
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if id, ok := vs.Type.(*ast.Ident); !ok || id.Name != "NotificationKind" {
				continue
			}
			for _, v := range vs.Values {
				if bl, ok := v.(*ast.BasicLit); ok && bl.Kind == token.STRING {
					k, err := strconv.Unquote(bl.Value)
					if err != nil {
						t.Fatalf("unquote %s: %v", bl.Value, err)
					}
					kinds = append(kinds, k)
				}
			}
		}
		return true
	})
	// Eighteen after issue #419 added the ticketless account kind to the
	// seventeen that issue #408 left; the count is asserted so a kind added to
	// the domain without the rebuild fails here.
	if len(kinds) != 18 {
		t.Fatalf("domain kinds = %v, want 18", kinds)
	}
	userID, ticketID := notificationFixture(t, s)
	pendingID := seedPendingUser(t, s, "Wanda", "wanda@example.com")
	res, err := s.db.Exec(`INSERT INTO comments (ticket_id, author, body, created_at) VALUES (?, 'Ana', 'b', ?)`,
		ticketID, formatTime(testClock))
	if err != nil {
		t.Fatal(err)
	}
	commentID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range kinds {
		// The account kind names the pending account and no ticket; every other
		// kind keeps the ticket shape it had before the rebuild.
		var ticket, pending, c any
		kind := domain.NotificationKind(k)
		if kind == domain.NotificationCompanyAccountPending {
			pending = pendingID
		} else {
			ticket = ticketID
			if kind.IsComment() {
				c = commentID
			}
		}
		if _, err := s.db.Exec(`INSERT INTO notifications (user_id, ticket_id, pending_user_id, kind, comment_id, created_at)
			VALUES (?,?,?,?,?,?)`, userID, ticket, pending, k, c, formatTime(testClock)); err != nil {
			t.Errorf("domain kind %q rejected by the notifications CHECK: %v", k, err)
		}
	}
	for _, bad := range []string{"", "mentioned", "ASSIGNED", "assigned "} {
		if _, err := s.db.Exec(`INSERT INTO notifications (user_id, ticket_id, kind, created_at)
			VALUES (?,?,?,?)`, userID, ticketID, bad, formatTime(testClock)); err == nil {
			t.Errorf("kind %q accepted, want CHECK rejection", bad)
		}
	}
}

// TestNotificationListForUserBoundsNonPositiveLimit pins that a non-positive
// limit is bounded, never an unbounded scan.
func TestNotificationListForUserBoundsNonPositiveLimit(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ana, ticketID := notificationFixture(t, s)
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 60; i++ {
		insertNotifications(t, s, domain.Notification{UserID: ana, TicketID: ticketID,
			Kind: domain.NotificationAssigned, CreatedAt: base.Add(time.Duration(i) * time.Second)})
	}
	for _, limit := range []int{0, -5, -1000} {
		got, err := s.NotificationStore().ListForUser(ctx, ana, limit)
		if err != nil {
			t.Fatalf("ListForUser(%d): %v", limit, err)
		}
		if len(got) != defaultNotificationListLimit {
			t.Errorf("ListForUser(limit=%d) = %d rows, want %d", limit, len(got), defaultNotificationListLimit)
		}
	}
}

// TestNotificationsCascadeOnCommentAndUserDelete pins the comment and
// recipient cascades, and that the actor edge never dangles.
func TestNotificationsCascadeOnCommentAndUserDelete(t *testing.T) {
	s := newTestDB(t)
	ana, ticketID := notificationFixture(t, s)
	bob := seedUser(t, s, "Bob", "bob@example.com", true)
	res, err := s.db.Exec(`INSERT INTO comments (ticket_id, author, body, created_at) VALUES (?, 'Ana', 'b', ?)`,
		ticketID, formatTime(testClock))
	if err != nil {
		t.Fatal(err)
	}
	commentID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	actor := ana
	insertNotifications(t, s,
		domain.Notification{UserID: bob, TicketID: ticketID, Kind: domain.NotificationCommentPublic,
			CommentID: &commentID, ActorUserID: &actor, CreatedAt: testClock},
		domain.Notification{UserID: ana, TicketID: ticketID, Kind: domain.NotificationAssigned, CreatedAt: testClock},
	)
	if _, err := s.db.Exec(`DELETE FROM comments WHERE id = ?`, commentID); err != nil {
		t.Fatalf("delete comment: %v", err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE comment_id IS NOT NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d comment notifications survived the comment delete, want 0 (cascade)", n)
	}
	if _, err := s.db.Exec(`DELETE FROM users WHERE id = ?`, ana); err != nil {
		t.Fatalf("delete recipient: %v", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE user_id = ?`, ana).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d notifications survived the recipient delete, want 0 (cascade)", n)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE actor_user_id IS NOT NULL
		AND actor_user_id NOT IN (SELECT id FROM users)`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d notifications dangle on a deleted actor", n)
	}
}
