package sqlite

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

type passClock struct{ now time.Time }

func (c passClock) Now() time.Time { return c.now }

// Issue #353: the whole pass against the REAL SQLite store (no fakes): the deadline
// read, the CAS close, the audit rows, the notifications with their pending
// email rows, and the active workflow run closure.
func TestHoldExpiryPassClosesOverTheRealStore(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	cat := seedCategory(t, s, "Bugs")
	requester := seedUserRaw(t, s, "Ana", "ana@example.com", "user")
	assignee := seedUserRaw(t, s, "Bea", "bea@example.com", "agent")
	outsider := seedUserRaw(t, s, "Cid", "cid@example.com", "agent")
	since := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC) // Thursday
	tk := seedTicket(t, s, domain.Ticket{Number: 1, Title: "held", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateInProgress,
		RequesterUserID: &requester, UserID: &assignee, CreatedAt: since, UpdatedAt: since})
	holdAt(t, s, tk.ID, since)
	seedRun(t, s, tk.ID, 0, "active", since)
	if err := s.SettingsStore().SetHoldAutoCloseDays(ctx, 2); err != nil {
		t.Fatal(err)
	}
	clock := passClock{now: time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)} // Monday: deadline
	svc := application.NewHoldExpiryService(s.HoldExpiryStore(), s.TicketUnitOfWork(), s.UserStore(),
		application.NewSLAService(s.SLAStore(), s.SettingsStore(), clock), s.SettingsStore(), clock)
	if err := svc.Pass(ctx); err != nil {
		t.Fatalf("Pass: %v", err)
	}

	var state string
	var holdSince any
	if err := s.db.QueryRowContext(ctx, `SELECT state, on_hold_since FROM tickets WHERE id = ?`, tk.ID).Scan(&state, &holdSince); err != nil {
		t.Fatal(err)
	}
	if state != "closed" || holdSince != nil {
		t.Errorf("ticket = %q on hold %v, want closed and off hold", state, holdSince)
	}
	// Audit: resume, resolve, closed with closure_via hold_expired, actor system.
	events, err := s.AuditStore().ListByTicket(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	var closedVia any
	for _, e := range events {
		t.Logf("audit %s %v->%v actor=%q actorID=%v via=%v", e.Action, derefStr(e.FromValue), derefStr(e.ToValue), e.Actor, e.ActorUserID, derefPtr(e.ClosureVia))
		if e.Action == domain.ActionTransition && e.ToValue != nil && *e.ToValue == "closed" {
			closedVia = derefPtr(e.ClosureVia)
			if e.Actor != domain.AutomaticActorSystem || e.ActorUserID != nil {
				t.Errorf("close actor = %q/%v, want system/nil", e.Actor, e.ActorUserID)
			}
		}
	}
	if closedVia != domain.ClosureViaHoldExpired {
		t.Errorf("closure_via = %v, want %q", closedVia, domain.ClosureViaHoldExpired)
	}
	// Notifications: the requester and the assignee, never the outsider.
	rows, err := s.db.QueryContext(ctx, `SELECT user_id, kind FROM notifications WHERE ticket_id = ?`, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var users []int64
	kinds := map[string]int{}
	for rows.Next() {
		var u int64
		var k string
		if err := rows.Scan(&u, &k); err != nil {
			t.Fatal(err)
		}
		users = append(users, u)
		kinds[k]++
	}
	sort.Slice(users, func(i, j int) bool { return users[i] < users[j] })
	want := []int64{requester, assignee}
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	if len(users) != 2 || users[0] != want[0] || users[1] != want[1] {
		t.Errorf("recipients = %v, want %v (outsider %d excluded)", users, want, outsider)
	}
	if kinds[string(domain.NotificationHoldClosed)] != 2 || len(kinds) != 1 {
		t.Errorf("kinds = %v, want 2 hold_closed and nothing else", kinds)
	}
	var emails int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_emails`).Scan(&emails); err != nil {
		t.Fatal(err)
	}
	if emails != 2 {
		t.Errorf("notification_emails = %d, want 2 pending deliveries", emails)
	}
	// The active workflow run is closed with the ticket.
	var status string
	if err := s.db.QueryRowContext(ctx, `SELECT status FROM ticket_workflow_runs WHERE ticket_id = ?`, tk.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status == "active" {
		t.Errorf("workflow run is still active on a closed ticket")
	}
}

// HoldExpiry: a second pass at the same instant writes nothing more (the close is
// idempotent, the reminder is once per hold).
func TestHoldExpiryPassSecondPassOverTheRealStoreIsSilent(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	cat := seedCategory(t, s, "Bugs")
	requester := seedUserRaw(t, s, "Ana", "ana@example.com", "user")
	since := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	tk := seedTicket(t, s, domain.Ticket{Number: 1, Title: "held", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateInProgress,
		RequesterUserID: &requester, CreatedAt: since, UpdatedAt: since})
	holdAt(t, s, tk.ID, since)
	if err := s.SettingsStore().SetHoldAutoCloseDays(ctx, 3); err != nil {
		t.Fatal(err)
	}
	clock := passClock{now: time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)} // reminder instant
	days, _ := s.SettingsStore().GetHoldAutoCloseDays(ctx)
	held, herr := s.HoldExpiryStore().HeldTickets(ctx, 0, 100)
	t.Logf("DEBUG days=%d held=%d herr=%v ticketID=%d", days, len(held), herr, tk.ID)
	svc := application.NewHoldExpiryService(s.HoldExpiryStore(), s.TicketUnitOfWork(), s.UserStore(),
		application.NewSLAService(s.SLAStore(), s.SettingsStore(), clock), s.SettingsStore(), clock)
	if err := svc.Pass(ctx); err != nil {
		t.Fatalf("Pass: %v", err)
	}
	if err := svc.Pass(ctx); err != nil {
		t.Fatalf("second Pass: %v", err)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications WHERE ticket_id = ?`, tk.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("notifications after two passes = %d, want the single reminder", n)
	}
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefPtr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
