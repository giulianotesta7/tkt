package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Follower and mention access (issue #418), proven end to end over the real
// migrations and the real services: following or being mentioned never widens
// what a person can open, a follower who loses the ticket stops hearing about
// it, assignment changes reach followers once, and a mention in an internal note
// never reaches the requester.

var followerAccessNow = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

type followerAccessClock struct{ now time.Time }

func (c followerAccessClock) Now() time.Time { return c.now }

// notificationRowsOf lists one user's stored rows as "kind|from|to".
func notificationRowsOf(t *testing.T, s *Store, userID int64) []string {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT kind, COALESCE(from_value,'<nil>'), COALESCE(to_value,'<nil>') FROM notifications WHERE user_id = ? ORDER BY kind, id`, userID)
	if err != nil {
		t.Fatalf("read notification rows: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var kind, from, to string
		if err := rows.Scan(&kind, &from, &to); err != nil {
			t.Fatalf("scan row: %v", err)
		}
		out = append(out, kind+"|"+from+"|"+to)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

func followServiceOver(s *Store, clock domain.Clock) *application.FollowService {
	return application.NewFollowService(s.TicketStore(), s.UserStore(), s.TicketFollowerStore(), clock).WithDesks(s.DeskStore())
}

func ticketServiceWithFollowers(s *Store, clock domain.Clock) *application.TicketService {
	vb := application.NewViewBuilder(s.TicketStore(), s.UserStore(), s.CategoryStore(), s.CommentStore(), s.AuditStore(), s.DeskStore())
	return application.NewTicketServiceWithWorkflowCreate(s.TicketStore(), s.UserStore(), s.CategoryStore(), s.TicketUnitOfWork(), vb, clock,
		s.WorkflowVersionStore(), application.NewWorkflowRunner(clock).WithFollowers(followServiceOver(s, clock)), s.WorkflowUnitOfWork(), nil).WithFollowers(followServiceOver(s, clock))
}

func commentServiceWithFollowers(s *Store, clock domain.Clock) *application.CommentService {
	return application.NewCommentService(s.TicketStore(), s.CommentStore(), s.UserStore(), s.CommentUnitOfWork(), clock).WithFollowers(followServiceOver(s, clock))
}

// addedRows returns the rows of after that before did not already hold.
func addedRows(before, after []string) []string {
	count := map[string]int{}
	for _, r := range before {
		count[r]++
	}
	var out []string
	for _, r := range after {
		if count[r] > 0 {
			count[r]--
			continue
		}
		out = append(out, r)
	}
	return out
}

func followerRowCount(t *testing.T, s *Store, ticketID, userID int64) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM ticket_followers WHERE ticket_id = ? AND user_id = ?`, ticketID, userID).Scan(&n); err != nil {
		t.Fatalf("count follower: %v", err)
	}
	return n
}

func claimPendingTicketFor(t *testing.T, s *Store, number int, requesterID, deskID int64) domain.Ticket {
	t.Helper()
	cat := seedCategory(t, s, "Followers")
	vid := seedPublished(t, s, cat, claimDef(deskID))
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: number, Title: "Claim pending", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateNew, RequesterUserID: &requesterID,
		CreatedAt: followerAccessNow, UpdatedAt: followerAccessNow, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", followerAccessNow)
	return tk
}

// TestFollowerScopeIsTheTicketDetailReadScope: an agent on the claim desk may
// follow a claim-pending ticket and be mentioned; an agent off that desk and
// the requester may do neither; once the claim moves the ticket elsewhere, the
// former desk member keeps the subscription row but hears nothing.
func TestFollowerScopeIsTheTicketDetailReadScope(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	clock := followerAccessClock{now: followerAccessNow}
	requester := seedUserRole(t, s, "Rita R", "rita@example.com", true, domain.RoleUser)
	member := seedUserRole(t, s, "Manu M", "manu@example.com", true, domain.RoleAgent)
	offDesk := seedUserRole(t, s, "Olga O", "olga@example.com", true, domain.RoleAgent)
	admin := seedUserRole(t, s, "Adela A", "adela@example.com", true, domain.RoleAdmin)
	tk := claimPendingTicketFor(t, s, 900, requester, seedDeskWithMember(t, s, member))
	svc := followServiceOver(s, clock)

	if err := svc.Follow(ctx, domain.User{ID: member, Role: domain.RoleAgent, Active: true}, tk.ID); err != nil {
		t.Fatalf("claim-desk member Follow = %v, want nil", err)
	}
	if err := svc.Follow(ctx, domain.User{ID: offDesk, Role: domain.RoleAgent, Active: true}, tk.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("off-desk Follow = %v, want ErrNotFound", err)
	}
	if _, err := svc.IsFollowing(ctx, domain.User{ID: offDesk, Role: domain.RoleAgent, Active: true}, tk.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("off-desk IsFollowing = %v, want ErrNotFound", err)
	}
	if err := svc.Follow(ctx, domain.User{ID: requester, Role: domain.RoleUser, Active: true}, tk.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("requester Follow = %v, want ErrNotFound", err)
	}
	candidates, err := svc.MentionCandidates(ctx, domain.User{ID: admin, Role: domain.RoleAdmin, Active: true}, tk.ID)
	if err != nil {
		t.Fatalf("MentionCandidates: %v", err)
	}
	seen := map[int64]bool{}
	for _, c := range candidates {
		seen[c.ID] = true
	}
	if !seen[member] || seen[offDesk] || seen[requester] {
		t.Fatalf("candidates = %v, want only the claim-desk member", candidates)
	}

	if _, err := s.db.ExecContext(ctx, `UPDATE tickets SET user_id = ? WHERE id = ?`, admin, tk.ID); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE ticket_workflow_runs SET status = 'completed', completed_at = ? WHERE ticket_id = ?`, formatTime(followerAccessNow), tk.ID); err != nil {
		t.Fatalf("complete run: %v", err)
	}
	if _, err := ticketServiceWithFollowers(s, clock).Transition(ctx, domain.User{ID: admin, Role: domain.RoleAdmin, Active: true}, tk.ID, domain.StateInProgress, ""); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	if rows := notificationRowsOf(t, s, member); len(rows) != 0 {
		t.Fatalf("lost-access follower rows = %v, want none", rows)
	}
	if got := followerRowCount(t, s, tk.ID, member); got != 1 {
		t.Fatalf("follower rows = %d, want the subscription kept", got)
	}
}

// TestFollowerAssignmentRows: the new holder is told "assigned" even when they
// follow, followers are told the new holder's name once, the actor and the
// outgoing holder hear nothing, re-assigning the same person writes nothing,
// and an unassignment reaches followers with no name.
func TestFollowerAssignmentRows(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	clock := followerAccessClock{now: followerAccessNow}
	requester := seedUserRole(t, s, "Rita R", "rita@example.com", true, domain.RoleUser)
	ana := seedUserRole(t, s, "Ana A", "ana@example.com", true, domain.RoleAgent)
	dora := seedUserRole(t, s, "Dora T", "dora@example.com", true, domain.RoleAdmin)
	actor := seedUserRole(t, s, "Adela A", "adela@example.com", true, domain.RoleAdmin)
	bruno := seedUserRole(t, s, "Bruno B", "bruno@example.com", true, domain.RoleAdmin)
	cat := seedCategory(t, s, "Follower assignment")
	tk := seedTicket(t, s, domain.Ticket{Number: 902, Title: "Assign", CategoryID: cat, Priority: domain.PriorityMedium,
		State: domain.StateInProgress, RequesterUserID: &requester, UserID: &ana, CreatedAt: followerAccessNow, UpdatedAt: followerAccessNow})
	svc, follow := ticketServiceWithFollowers(s, clock), followServiceOver(s, clock)
	who := domain.User{ID: actor, Name: "Adela A", Role: domain.RoleAdmin, Active: true}
	for _, id := range []int64{actor, bruno, dora} {
		if err := follow.Follow(ctx, domain.User{ID: id, Role: domain.RoleAdmin, Active: true}, tk.ID); err != nil {
			t.Fatalf("arrange Follow(%d): %v", id, err)
		}
	}
	if _, err := svc.Assign(ctx, who, tk.ID, &dora, "hand over"); err != nil {
		t.Fatalf("Assign: %v", err)
	}
	if rows := notificationRowsOf(t, s, dora); len(rows) != 1 || rows[0] != "assigned|<nil>|<nil>" {
		t.Fatalf("new holder rows = %v, want one assigned", rows)
	}
	if rows := notificationRowsOf(t, s, bruno); len(rows) != 1 || rows[0] != "assignee_changed|<nil>|Dora T" {
		t.Fatalf("follower rows = %v, want one assignee_changed naming Dora T", rows)
	}
	if rows := notificationRowsOf(t, s, actor); len(rows) != 0 {
		t.Fatalf("acting follower rows = %v, want none", rows)
	}
	if rows := notificationRowsOf(t, s, ana); len(rows) != 0 {
		t.Fatalf("outgoing holder rows = %v, want none", rows)
	}
	if _, err := svc.Assign(ctx, who, tk.ID, &dora, "again"); err != nil {
		t.Fatalf("re-Assign: %v", err)
	}
	if rows := notificationRowsOf(t, s, bruno); len(rows) != 1 {
		t.Fatalf("re-selection rows = %v, want the first one alone", rows)
	}
	if _, err := svc.Assign(ctx, who, tk.ID, nil, ""); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if rows := notificationRowsOf(t, s, bruno); len(rows) != 2 || rows[1] != "assignee_changed|<nil>|<nil>" {
		t.Fatalf("unassign follower rows = %v, want a second row with a NULL name", rows)
	}
}

// TestMentionRowsAndSubscription: a mention in an internal note tells the
// mentioned staff member once and subscribes them, never the requester or a
// follower who can no longer read internal notes; an assignee who also follows
// and is mentioned gets one row; a requester's "@Name" mentions nobody.
func TestMentionRowsAndSubscription(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	clock := followerAccessClock{now: followerAccessNow}
	requester := seedUserRole(t, s, "Rita R", "rita@example.com", true, domain.RoleUser)
	ana := seedUserRole(t, s, "Ana A", "ana@example.com", true, domain.RoleAgent)
	dora := seedUserRole(t, s, "Dora D", "dora@example.com", true, domain.RoleAdmin)
	mora := seedUserRole(t, s, "Mora M", "mora@example.com", true, domain.RoleAdmin)
	downgraded := seedUserRole(t, s, "Dani D", "dani@example.com", true, domain.RoleUser)
	cat := seedCategory(t, s, "Follower mentions")
	tk := seedTicket(t, s, domain.Ticket{Number: 903, Title: "Mentions", CategoryID: cat, Priority: domain.PriorityMedium,
		State: domain.StateInProgress, RequesterUserID: &requester, UserID: &ana, CreatedAt: followerAccessNow, UpdatedAt: followerAccessNow})
	if err := s.TicketFollowerStore().Follow(ctx, tk.ID, downgraded, followerAccessNow); err != nil {
		t.Fatalf("arrange downgraded follower: %v", err)
	}
	svc := commentServiceWithFollowers(s, clock)
	doraUser := domain.User{ID: dora, Name: "Dora D", Role: domain.RoleAdmin, Active: true}

	if _, err := svc.Add(ctx, doraUser, tk.ID, "@Mora M refund approved", "internal"); err != nil {
		t.Fatalf("internal Add: %v", err)
	}
	if rows := notificationRowsOf(t, s, mora); len(rows) != 1 || rows[0] != "mentioned|<nil>|<nil>" {
		t.Fatalf("mentioned rows = %v, want one mentioned", rows)
	}
	if got := followerRowCount(t, s, tk.ID, mora); got != 1 {
		t.Fatalf("mention did not subscribe (rows=%d)", got)
	}
	if rows := notificationRowsOf(t, s, requester); len(rows) != 0 {
		t.Fatalf("requester told about an internal note: %v", rows)
	}
	if rows := notificationRowsOf(t, s, downgraded); len(rows) != 0 {
		t.Fatalf("downgraded follower rows = %v, want none", rows)
	}
	if rows := notificationRowsOf(t, s, ana); len(rows) != 1 || rows[0] != "comment_internal|<nil>|<nil>" {
		t.Fatalf("assignee rows = %v, want one comment_internal", rows)
	}

	if err := followServiceOver(s, clock).Follow(ctx, domain.User{ID: ana, Role: domain.RoleAgent, Active: true}, tk.ID); err != nil {
		t.Fatalf("Follow(assignee): %v", err)
	}
	if _, err := svc.Add(ctx, domain.User{ID: mora, Name: "Mora M", Role: domain.RoleAdmin, Active: true}, tk.ID, "@Ana A @Dora D thanks", "public"); err != nil {
		t.Fatalf("public Add: %v", err)
	}
	if rows := notificationRowsOf(t, s, ana); len(rows) != 2 || rows[1] != "mentioned|<nil>|<nil>" {
		t.Fatalf("assignee+follower+mentioned rows = %v, want one mention after the internal row", rows)
	}
	before := notificationRowsOf(t, s, ana)
	if _, err := svc.Add(ctx, domain.User{ID: requester, Name: "Rita R", Role: domain.RoleUser, Active: true}, tk.ID, "@Ana A any news?", "public"); err != nil {
		t.Fatalf("requester Add: %v", err)
	}
	if added := addedRows(before, notificationRowsOf(t, s, ana)); strings.Join(added, ",") != "comment_public|<nil>|<nil>" {
		t.Fatalf("rows added by a mention in a requester's comment = %v, want one plain comment_public", added)
	}
}
