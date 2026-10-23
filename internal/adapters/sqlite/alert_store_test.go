package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// The operational alert reads and the idempotent alert write (issue #408).
// These cases cover what the application fakes deliberately do not: the real
// candidate query, the activity instant it derives, and the once-per-condition
// guard that runs inside the writing transaction.

func alertTicket(t *testing.T, s *Store, cat int64, number int, state domain.State, assignee *int64, updated time.Time) domain.Ticket {
	t.Helper()
	return seedTicket(t, s, domain.Ticket{
		Number: number, Title: "alert candidate", CategoryID: cat,
		Priority: domain.PriorityMedium, State: state, UserID: assignee,
		CreatedAt: testClock, UpdatedAt: updated,
	})
}

// The candidate read is the first half of "only the assigned agent is told":
// a ticket nobody owns never reaches the rules at all.
func TestAlertCandidatesExcludesUnassignedAndClosedTickets(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	agent := seedUser(t, s, "Bob", "bob@example.com", true)
	cat := seedCategory(t, s, "Bugs")

	wanted := alertTicket(t, s, cat, 1, domain.StateInProgress, &agent, testClock)
	alertTicket(t, s, cat, 2, domain.StateInProgress, nil, testClock)
	for i, state := range domain.ClosedStates() {
		alertTicket(t, s, cat, 10+i, state, &agent, testClock)
	}

	got, err := s.AlertStore().AlertCandidates(ctx, 0, 100)
	if err != nil {
		t.Fatalf("AlertCandidates: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("candidates = %d, want only the open assigned ticket", len(got))
	}
	if got[0].Ticket.ID != wanted.ID {
		t.Errorf("candidate = %d, want %d", got[0].Ticket.ID, wanted.ID)
	}
}

// The activity instant is the latest movement of ANY kind: the ticket's own
// update or its newest comment, whichever happened last.
func TestAlertCandidatesReportsTheLatestMovement(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	agent := seedUser(t, s, "Bob", "bob@example.com", true)
	cat := seedCategory(t, s, "Bugs")
	updated := testClock.Add(2 * time.Hour)
	tk := alertTicket(t, s, cat, 1, domain.StateInProgress, &agent, updated)
	commented := testClock.Add(5 * time.Hour)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO comments (ticket_id, author, body, visibility, created_at) VALUES (?, 'Bob', 'looking into it', 'internal', ?)`,
		tk.ID, formatTime(commented)); err != nil {
		t.Fatalf("seed comment: %v", err)
	}

	got, err := s.AlertStore().AlertCandidates(ctx, 0, 100)
	if err != nil {
		t.Fatalf("AlertCandidates: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1", len(got))
	}
	// An INTERNAL note is movement too: the question is whether anything
	// happened, not whether the requester was answered.
	if !got[0].LastActivityAt.Equal(commented) {
		t.Errorf("LastActivityAt = %s, want the newest comment %s",
			got[0].LastActivityAt.Format(time.RFC3339), commented.Format(time.RFC3339))
	}

	// With no comment at all the ticket's own update is the movement.
	bare := alertTicket(t, s, cat, 2, domain.StateInProgress, &agent, updated)
	got, err = s.AlertStore().AlertCandidates(ctx, bare.ID-1, 100)
	if err != nil {
		t.Fatalf("AlertCandidates: %v", err)
	}
	if len(got) != 1 || !got[0].LastActivityAt.Equal(updated) {
		t.Fatalf("LastActivityAt = %v, want the ticket's own update %s", got, updated.Format(time.RFC3339))
	}
}

// The pass keyset-pages by id, so the read must honour both bounds.
func TestAlertCandidatesPagesByID(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	agent := seedUser(t, s, "Bob", "bob@example.com", true)
	cat := seedCategory(t, s, "Bugs")
	var ids []int64
	for i := 1; i <= 3; i++ {
		ids = append(ids, alertTicket(t, s, cat, i, domain.StateInProgress, &agent, testClock).ID)
	}

	first, err := s.AlertStore().AlertCandidates(ctx, 0, 2)
	if err != nil {
		t.Fatalf("AlertCandidates: %v", err)
	}
	if len(first) != 2 || first[0].Ticket.ID != ids[0] || first[1].Ticket.ID != ids[1] {
		t.Fatalf("first page = %v, want the two lowest ids %v", pageIDs(first), ids[:2])
	}
	second, err := s.AlertStore().AlertCandidates(ctx, first[1].Ticket.ID, 2)
	if err != nil {
		t.Fatalf("AlertCandidates: %v", err)
	}
	if len(second) != 1 || second[0].Ticket.ID != ids[2] {
		t.Fatalf("second page = %v, want %d", pageIDs(second), ids[2])
	}
}

func pageIDs(page []application.AlertCandidate) []int64 {
	var out []int64
	for _, c := range page {
		out = append(out, c.Ticket.ID)
	}
	return out
}

// AlertSent answers "ever" for an SLA threshold and "in this period" for the
// repeatable inactivity condition.
func TestAlertSentScopesTheQuestionToThePeriod(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	agent := seedUser(t, s, "Bob", "bob@example.com", true)
	cat := seedCategory(t, s, "Bugs")
	tk := alertTicket(t, s, cat, 1, domain.StateInProgress, &agent, testClock)
	store := s.AlertStore()

	sent, err := store.AlertSent(ctx, tk.ID, domain.NotificationNoMovement, nil)
	if err != nil {
		t.Fatalf("AlertSent: %v", err)
	}
	if sent {
		t.Fatal("AlertSent = true before anything was written")
	}
	written := testClock.Add(time.Hour)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO notifications (user_id, ticket_id, kind, created_at) VALUES (?, ?, 'no_movement', ?)`,
		agent, tk.ID, formatTime(written)); err != nil {
		t.Fatalf("seed alert: %v", err)
	}

	older := written.Add(-time.Minute)
	newer := written.Add(time.Minute)
	for _, tc := range []struct {
		name  string
		since *time.Time
		want  bool
	}{
		{"ever", nil, true},
		{"this period started before the alert", &older, true},
		{"a later period has no alert yet", &newer, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := store.AlertSent(ctx, tk.ID, domain.NotificationNoMovement, tc.since)
			if err != nil {
				t.Fatalf("AlertSent: %v", err)
			}
			if got != tc.want {
				t.Errorf("AlertSent = %v, want %v", got, tc.want)
			}
		})
	}

	// A different condition on the same ticket is a different question.
	got, err := store.AlertSent(ctx, tk.ID, domain.NotificationSLAResolutionBreached, nil)
	if err != nil {
		t.Fatalf("AlertSent: %v", err)
	}
	if got {
		t.Error("AlertSent reported a condition that was never written")
	}
}

// The guard that actually protects the invariant lives inside the writing
// transaction: appending the same condition twice must leave one row.
func TestAppendAlertWritesOneRowPerCondition(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	agent := seedUser(t, s, "Bob", "bob@example.com", true)
	cat := seedCategory(t, s, "Bugs")
	tk := alertTicket(t, s, cat, 1, domain.StateInProgress, &agent, testClock)
	row := domain.Notification{
		UserID: agent, TicketID: tk.ID,
		Kind: domain.NotificationSLAResolutionAtRisk, CreatedAt: testClock,
	}
	tx := s.AlertUnitOfWork()
	for i := 0; i < 3; i++ {
		if err := tx.AppendAlert(ctx, tk.ID, row.Kind, nil, []domain.Notification{row}); err != nil {
			t.Fatalf("AppendAlert %d: %v", i, err)
		}
	}

	var rows int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications WHERE ticket_id=? AND kind=?`,
		tk.ID, string(row.Kind)).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("stored rows = %d, want 1", rows)
	}
	// The alert owes an email like any other notification (issue #362).
	var queued int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_emails`).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 1 {
		t.Errorf("queued emails = %d, want 1", queued)
	}
}

// A repeatable condition writes again once its period moved on.
func TestAppendAlertWritesAgainInALaterPeriod(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	agent := seedUser(t, s, "Bob", "bob@example.com", true)
	cat := seedCategory(t, s, "Bugs")
	tk := alertTicket(t, s, cat, 1, domain.StateInProgress, &agent, testClock)
	tx := s.AlertUnitOfWork()

	first := domain.Notification{UserID: agent, TicketID: tk.ID,
		Kind: domain.NotificationNoMovement, CreatedAt: testClock}
	firstSince := testClock.Add(-time.Hour)
	if err := tx.AppendAlert(ctx, tk.ID, first.Kind, &firstSince, []domain.Notification{first}); err != nil {
		t.Fatalf("first AppendAlert: %v", err)
	}
	laterSince := testClock.Add(time.Hour)
	second := domain.Notification{UserID: agent, TicketID: tk.ID,
		Kind: domain.NotificationNoMovement, CreatedAt: laterSince.Add(time.Hour)}
	if err := tx.AppendAlert(ctx, tk.ID, second.Kind, &laterSince, []domain.Notification{second}); err != nil {
		t.Fatalf("second AppendAlert: %v", err)
	}

	var rows int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications WHERE ticket_id=? AND kind='no_movement'`, tk.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Errorf("stored rows = %d, want 2 (one per period of silence)", rows)
	}
}

// An alert addressed to the acting user, or carrying a kind the vocabulary
// does not know, is a programming bug: the write refuses it and stores nothing.
func TestAppendAlertRefusesAnInvalidRow(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	agent := seedUser(t, s, "Bob", "bob@example.com", true)
	cat := seedCategory(t, s, "Bugs")
	tk := alertTicket(t, s, cat, 1, domain.StateInProgress, &agent, testClock)
	self := agent
	bad := domain.Notification{UserID: agent, TicketID: tk.ID, ActorUserID: &self,
		Kind: domain.NotificationNoMovement, CreatedAt: testClock}

	if err := s.AlertUnitOfWork().AppendAlert(ctx, tk.ID, bad.Kind, nil, []domain.Notification{bad}); err == nil {
		t.Fatal("AppendAlert accepted a self-addressed alert, want a refusal")
	}
	var rows int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Errorf("stored rows = %d, want 0 after a refused write", rows)
	}
}
