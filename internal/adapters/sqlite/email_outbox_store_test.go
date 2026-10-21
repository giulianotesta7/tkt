package sqlite

import (
	"context"
	"database/sql"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// The notification email outbox (issue #362, specs S6/S12/S13/S14): the
// delivery row written with each notification, the atomic claim that makes
// "never send twice" a storage fact, and the settlement transitions a sender
// drives. Every arrange path goes through the REAL append helper
// (insertNotifications → appendNotificationsTx), so these tests exercise the
// same write the units of work perform.

// emailInterruptedError is the last_error a restart leaves on a row that was
// mid-send (email_outbox_store.go); the sender's contract is that such a row
// is failed, never re-sent, and the test asserts the exact stored text.
type outboxFixture struct {
	s        *Store
	ctx      context.Context
	userID   int64
	actorID  int64
	ticketID int64
}

func newOutboxFixture(t *testing.T) *outboxFixture {
	t.Helper()
	s := newTestDB(t)
	userID, ticketID := notificationFixture(t, s)
	// A second user, so a notification can name an actor who is not its
	// recipient (the domain refuses a self-addressed row).
	actorID := seedUser(t, s, "Bea", "bea@example.com", true)
	return &outboxFixture{s: s, ctx: context.Background(), userID: userID, actorID: actorID, ticketID: ticketID}
}

// enqueue writes one notification (and therefore its delivery row) at created
// and returns the notification id.
func (f *outboxFixture) enqueue(t *testing.T, created time.Time) int64 {
	t.Helper()
	insertNotifications(t, f.s, domain.Notification{
		UserID: f.userID, TicketID: f.ticketID, Kind: domain.NotificationAssigned, CreatedAt: created,
	})
	var id int64
	if err := f.s.db.QueryRowContext(f.ctx, `SELECT MAX(id) FROM notifications`).Scan(&id); err != nil {
		t.Fatalf("read queued notification id: %v", err)
	}
	return id
}

// outbox returns the port under test.
func (f *outboxFixture) outbox() application.EmailOutbox { return f.s.EmailOutbox() }

// emailDeliveryRow is the stored delivery state, read back verbatim so a test
// can assert a column the port does not expose.
type emailDeliveryRow struct {
	status     string
	attempts   int
	nextAt     string
	claimedAt  sql.NullString
	sentAt     sql.NullString
	skipReason sql.NullString
	lastErr    sql.NullString
	createdAt  string
	updatedAt  string
}

func readEmailRow(t *testing.T, s *Store, notificationID int64) emailDeliveryRow {
	t.Helper()
	var r emailDeliveryRow
	err := s.db.QueryRowContext(context.Background(), `
		SELECT status, attempts, next_attempt_at, claimed_at, sent_at, skip_reason, last_error, created_at, updated_at
		FROM notification_emails WHERE notification_id = ?`, notificationID).
		Scan(&r.status, &r.attempts, &r.nextAt, &r.claimedAt, &r.sentAt, &r.skipReason, &r.lastErr, &r.createdAt, &r.updatedAt)
	if err != nil {
		t.Fatalf("read delivery row %d: %v", notificationID, err)
	}
	return r
}

func emailRowExists(t *testing.T, s *Store, notificationID int64) bool {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM notification_emails WHERE notification_id = ?`, notificationID).Scan(&n); err != nil {
		t.Fatalf("count delivery rows: %v", err)
	}
	return n == 1
}

func claimedIDs(pending []application.PendingEmail) []int64 {
	out := make([]int64, 0, len(pending))
	for _, p := range pending {
		out = append(out, p.Notification.ID)
	}
	return out
}

func wantIDs(t *testing.T, got []application.PendingEmail, want ...int64) {
	t.Helper()
	ids := claimedIDs(got)
	if len(ids) != len(want) {
		t.Fatalf("claimed %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("claimed %v, want %v (order matters: oldest first)", ids, want)
		}
	}
}

// TestEmailOutboxClaimDueReturnsTheNotificationFacts proves one claim hands
// the sender everything it needs to compose the email — the whole notification
// plus the attempts already made — and moves the row to 'sending' with the
// claim instant (S6, S13).
func TestEmailOutboxClaimDueReturnsTheNotificationFacts(t *testing.T) {
	f := newOutboxFixture(t)
	actor := f.actorID
	from, to := "new", "in_progress"
	insertNotifications(t, f.s, domain.Notification{
		UserID: f.userID, TicketID: f.ticketID, Kind: domain.NotificationStateChanged,
		ActorUserID: &actor, FromValue: &from, ToValue: &to, CreatedAt: testClock,
	})
	var id int64
	if err := f.s.db.QueryRowContext(f.ctx, `SELECT MAX(id) FROM notifications`).Scan(&id); err != nil {
		t.Fatalf("read queued notification id: %v", err)
	}

	claimed, err := f.outbox().ClaimDue(f.ctx, testClock, 10)
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d rows, want 1", len(claimed))
	}
	got := claimed[0]
	if got.Attempts != 0 {
		t.Errorf("Attempts = %d, want 0", got.Attempts)
	}
	n := got.Notification
	if n.ID != id || n.UserID != f.userID || n.TicketID != f.ticketID ||
		n.Kind != domain.NotificationStateChanged {
		t.Errorf("notification = %+v, want the claimed row's identity", n)
	}
	if n.ActorUserID == nil || *n.ActorUserID != actor {
		t.Errorf("actor = %v, want %d", n.ActorUserID, actor)
	}
	if n.FromValue == nil || *n.FromValue != from || n.ToValue == nil || *n.ToValue != to {
		t.Errorf("change = (%v, %v), want (%q, %q)", n.FromValue, n.ToValue, from, to)
	}
	if n.CommentID != nil || n.StepIndex != nil || n.ReadAt != nil {
		t.Errorf("absent facts = (%v, %v, %v), want nil", n.CommentID, n.StepIndex, n.ReadAt)
	}
	if !n.CreatedAt.Equal(testClock) {
		t.Errorf("created_at = %s, want %s", n.CreatedAt, testClock)
	}

	row := readEmailRow(t, f.s, id)
	if row.status != string(application.EmailSending) {
		t.Errorf("status = %q, want sending", row.status)
	}
	if !row.claimedAt.Valid || row.claimedAt.String != formatTime(testClock) {
		t.Errorf("claimed_at = %v, want %s", row.claimedAt, formatTime(testClock))
	}
	if row.attempts != 0 {
		t.Errorf("stored attempts = %d, want 0", row.attempts)
	}
	// The claim is a delivery fact only: the notification itself is untouched.
	rows, err := f.s.NotificationStore().ListForUser(f.ctx, f.userID, 10)
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	if len(rows) != 1 || rows[0].ReadAt != nil {
		t.Errorf("notifications = %+v, want the single unread row unchanged by the claim", rows)
	}
}

// TestEmailOutboxClaimDueLeavesRowsThatAreNotDue proves the backoff is
// honoured: a row whose next_attempt_at is in the future is not claimed, and
// it is claimed once it becomes due (S14).
func TestEmailOutboxClaimDueLeavesRowsThatAreNotDue(t *testing.T) {
	f := newOutboxFixture(t)
	due := f.enqueue(t, testClock)
	later := f.enqueue(t, testClock.Add(time.Minute))

	claimed, err := f.outbox().ClaimDue(f.ctx, testClock, 10)
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	wantIDs(t, claimed, due)
	if row := readEmailRow(t, f.s, later); row.status != string(application.EmailPending) || row.claimedAt.Valid {
		t.Fatalf("not-yet-due row = %+v, want still pending and unclaimed", row)
	}

	claimed, err = f.outbox().ClaimDue(f.ctx, testClock.Add(time.Minute), 10)
	if err != nil {
		t.Fatalf("ClaimDue when due: %v", err)
	}
	wantIDs(t, claimed, later)
}

// TestEmailOutboxClaimDueIsOldestFirstAndHonoursTheLimit proves the claim
// order (next_attempt_at, then notification id) and the batch bound, so a
// backlog drains oldest first instead of in insertion order.
func TestEmailOutboxClaimDueIsOldestFirstAndHonoursTheLimit(t *testing.T) {
	f := newOutboxFixture(t)
	oldest := f.enqueue(t, testClock)
	newest := f.enqueue(t, testClock.Add(2*time.Minute))
	middle := f.enqueue(t, testClock.Add(time.Minute))

	// A non-positive limit claims nothing at all.
	if claimed, err := f.outbox().ClaimDue(f.ctx, testClock.Add(time.Hour), 0); err != nil {
		t.Fatalf("ClaimDue(0): %v", err)
	} else if len(claimed) != 0 {
		t.Fatalf("ClaimDue(0) returned %v, want nothing", claimedIDs(claimed))
	}

	claimed, err := f.outbox().ClaimDue(f.ctx, testClock.Add(3*time.Minute), 2)
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	wantIDs(t, claimed, oldest, middle)
	if row := readEmailRow(t, f.s, newest); row.status != string(application.EmailPending) {
		t.Fatalf("newest row status = %q, want still pending behind the limit", row.status)
	}

	claimed, err = f.outbox().ClaimDue(f.ctx, testClock.Add(3*time.Minute), 2)
	if err != nil {
		t.Fatalf("second ClaimDue: %v", err)
	}
	wantIDs(t, claimed, newest)
}

// TestEmailOutboxClaimDueNeverReturnsARowTwice is the at-most-once proof for
// one process: a row moved to 'sending' is not pending any more, so the next
// claim at the very same instant returns nothing (S6).
func TestEmailOutboxClaimDueNeverReturnsARowTwice(t *testing.T) {
	f := newOutboxFixture(t)
	id := f.enqueue(t, testClock)

	first, err := f.outbox().ClaimDue(f.ctx, testClock, 10)
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	wantIDs(t, first, id)

	second, err := f.outbox().ClaimDue(f.ctx, testClock, 10)
	if err != nil {
		t.Fatalf("second ClaimDue: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("second ClaimDue returned %v, want nothing (already sending)", claimedIDs(second))
	}
}

// TestEmailOutboxConcurrentClaimsNeverShareARow is the same proof under
// concurrency: two senders claiming at the same instant partition the due
// rows, so no notification is ever handed to two of them. Synchronized with a
// start channel and a WaitGroup — no sleeps (go-tests rule 9).
func TestEmailOutboxConcurrentClaimsNeverShareARow(t *testing.T) {
	f := newOutboxFixture(t)
	const total = 10
	for i := 0; i < total; i++ {
		f.enqueue(t, testClock)
	}
	outbox := f.outbox()

	start := make(chan struct{})
	results := make([][]application.PendingEmail, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = outbox.ClaimDue(f.ctx, testClock, total)
		}(i)
	}
	close(start)
	wg.Wait()

	seen := map[int64]int{}
	claimedTotal := 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("ClaimDue[%d]: %v", i, err)
		}
		for _, p := range results[i] {
			seen[p.Notification.ID]++
			claimedTotal++
		}
	}
	if claimedTotal != total {
		t.Fatalf("two claims returned %d rows, want %d (every due row claimed exactly once)", claimedTotal, total)
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("notification %d was claimed %d times, want exactly 1", id, n)
		}
	}

	var sending int
	if err := f.s.db.QueryRowContext(f.ctx,
		`SELECT COUNT(*) FROM notification_emails WHERE status = 'sending'`).Scan(&sending); err != nil {
		t.Fatalf("count sending rows: %v", err)
	}
	if sending != total {
		t.Errorf("%d rows are sending, want %d", sending, total)
	}
}

// TestEmailOutboxMarkSent proves the success settlement: sent_at is the
// acceptance instant, the attempt counter advances, and the row is no longer
// claimable.
func TestEmailOutboxMarkSent(t *testing.T) {
	f := newOutboxFixture(t)
	id := f.enqueue(t, testClock)
	if _, err := f.outbox().ClaimDue(f.ctx, testClock, 10); err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	sentAt := testClock.Add(3 * time.Second)
	if err := f.outbox().MarkSent(f.ctx, id, sentAt); err != nil {
		t.Fatalf("MarkSent: %v", err)
	}
	row := readEmailRow(t, f.s, id)
	if row.status != string(application.EmailSent) {
		t.Errorf("status = %q, want sent", row.status)
	}
	if !row.sentAt.Valid || row.sentAt.String != formatTime(sentAt) {
		t.Errorf("sent_at = %v, want %s", row.sentAt, formatTime(sentAt))
	}
	if row.attempts != 1 {
		t.Errorf("attempts = %d, want 1", row.attempts)
	}
	if row.updatedAt != formatTime(sentAt) {
		t.Errorf("updated_at = %q, want %s", row.updatedAt, formatTime(sentAt))
	}
	if again, err := f.outbox().ClaimDue(f.ctx, testClock.Add(time.Hour), 10); err != nil {
		t.Fatalf("ClaimDue after send: %v", err)
	} else if len(again) != 0 {
		t.Fatalf("a sent row was claimed again: %v", claimedIDs(again))
	}
}

// TestEmailOutboxMarkSkipped proves a deliberate skip is settled with its
// reason, so an operator can tell it apart from a failure (S14).
func TestEmailOutboxMarkSkipped(t *testing.T) {
	f := newOutboxFixture(t)
	id := f.enqueue(t, testClock)
	if _, err := f.outbox().ClaimDue(f.ctx, testClock, 10); err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	at := testClock.Add(time.Second)
	if err := f.outbox().MarkSkipped(f.ctx, id, at, application.EmailSkipOutOfReach); err != nil {
		t.Fatalf("MarkSkipped: %v", err)
	}
	row := readEmailRow(t, f.s, id)
	if row.status != string(application.EmailSkipped) {
		t.Errorf("status = %q, want skipped", row.status)
	}
	if !row.skipReason.Valid || row.skipReason.String != application.EmailSkipOutOfReach {
		t.Errorf("skip_reason = %v, want %q", row.skipReason, application.EmailSkipOutOfReach)
	}
	if row.sentAt.Valid {
		t.Errorf("sent_at = %v, want NULL for a skip", row.sentAt)
	}
	if row.updatedAt != formatTime(at) {
		t.Errorf("updated_at = %q, want %s", row.updatedAt, formatTime(at))
	}
}

// TestEmailOutboxMarkFailed proves the terminal failure settlement: the
// attempt count reached and the last error are stored for the operator, and
// the row is not claimable again (S13, S14).
func TestEmailOutboxMarkFailed(t *testing.T) {
	f := newOutboxFixture(t)
	id := f.enqueue(t, testClock)
	if _, err := f.outbox().ClaimDue(f.ctx, testClock, 10); err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	at := testClock.Add(time.Minute)
	if err := f.outbox().MarkFailed(f.ctx, id, at, 6, "550 mailbox unavailable"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	row := readEmailRow(t, f.s, id)
	if row.status != string(application.EmailFailed) {
		t.Errorf("status = %q, want failed", row.status)
	}
	if row.attempts != 6 {
		t.Errorf("attempts = %d, want 6", row.attempts)
	}
	if !row.lastErr.Valid || row.lastErr.String != "550 mailbox unavailable" {
		t.Errorf("last_error = %v, want the SMTP failure", row.lastErr)
	}
	if row.updatedAt != formatTime(at) {
		t.Errorf("updated_at = %q, want %s", row.updatedAt, formatTime(at))
	}
	if again, err := f.outbox().ClaimDue(f.ctx, testClock.Add(24*time.Hour), 10); err != nil {
		t.Fatalf("ClaimDue after failure: %v", err)
	} else if len(again) != 0 {
		t.Fatalf("a failed row was claimed again: %v", claimedIDs(again))
	}
}

// TestEmailOutboxRescheduleReturnsTheRowToPending proves a transient failure
// re-queues the row for its next attempt: attempts and the backoff instant are
// stored, the error is kept for diagnosis, and the claim is released.
func TestEmailOutboxReschedule(t *testing.T) {
	f := newOutboxFixture(t)
	id := f.enqueue(t, testClock)
	if _, err := f.outbox().ClaimDue(f.ctx, testClock, 10); err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	next := testClock.Add(time.Minute)
	if err := f.outbox().Reschedule(f.ctx, id, 1, next, "421 try again later"); err != nil {
		t.Fatalf("Reschedule: %v", err)
	}
	row := readEmailRow(t, f.s, id)
	if row.status != string(application.EmailPending) {
		t.Errorf("status = %q, want pending", row.status)
	}
	if row.attempts != 1 {
		t.Errorf("attempts = %d, want 1", row.attempts)
	}
	if row.nextAt != formatTime(next) {
		t.Errorf("next_attempt_at = %q, want %s", row.nextAt, formatTime(next))
	}
	if !row.lastErr.Valid || row.lastErr.String != "421 try again later" {
		t.Errorf("last_error = %v, want the transient failure", row.lastErr)
	}
	if row.claimedAt.Valid {
		t.Errorf("claimed_at = %v, want NULL once the claim is released", row.claimedAt)
	}

	// The retry becomes claimable exactly at its backoff instant, not before.
	if early, err := f.outbox().ClaimDue(f.ctx, testClock.Add(time.Minute-time.Second), 10); err != nil {
		t.Fatalf("ClaimDue before backoff: %v", err)
	} else if len(early) != 0 {
		t.Fatalf("rescheduled row claimed before its backoff: %v", claimedIDs(early))
	}
	claimed, err := f.outbox().ClaimDue(f.ctx, next, 10)
	if err != nil {
		t.Fatalf("ClaimDue at backoff: %v", err)
	}
	wantIDs(t, claimed, id)
	if claimed[0].Attempts != 1 {
		t.Errorf("re-claim Attempts = %d, want the stored 1", claimed[0].Attempts)
	}
}

// TestEmailOutboxSettlesOnlyAClaimedRow proves the guard behind at-most-once
// delivery: every settlement refuses a row this sender does not hold (still
// pending, or absent) and changes nothing. A settlement that silently
// succeeded on a pending row would let a stale sender overwrite another's
// claim.
func TestEmailOutboxSettlesOnlyAClaimedRow(t *testing.T) {
	settlements := map[string]func(application.EmailOutbox, int64) error{
		"MarkSent": func(o application.EmailOutbox, id int64) error {
			return o.MarkSent(context.Background(), id, testClock)
		},
		"MarkSkipped": func(o application.EmailOutbox, id int64) error {
			return o.MarkSkipped(context.Background(), id, testClock, application.EmailSkipDisabled)
		},
		"MarkFailed": func(o application.EmailOutbox, id int64) error {
			return o.MarkFailed(context.Background(), id, testClock, 1, "boom")
		},
		"Reschedule": func(o application.EmailOutbox, id int64) error {
			return o.Reschedule(context.Background(), id, 1, testClock, "boom")
		},
	}
	names := make([]string, 0, len(settlements))
	for name := range settlements {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		settle := settlements[name]
		t.Run(name+"/pending row", func(t *testing.T) {
			f := newOutboxFixture(t)
			id := f.enqueue(t, testClock)
			if err := settle(f.outbox(), id); err == nil {
				t.Fatal("settling an unclaimed row succeeded, want an error")
			}
			row := readEmailRow(t, f.s, id)
			if row.status != string(application.EmailPending) || row.attempts != 0 ||
				row.lastErr.Valid || row.sentAt.Valid || row.skipReason.Valid {
				t.Fatalf("row = %+v, want it untouched by a refused settlement", row)
			}
		})
		t.Run(name+"/absent row", func(t *testing.T) {
			f := newOutboxFixture(t)
			if err := settle(f.outbox(), 9999); err == nil {
				t.Fatal("settling a missing row succeeded, want an error")
			}
		})
	}
}

// TestEmailOutboxAbandonInFlightFailsInterruptedRows proves the restart rule
// (S14): a row a dead process left in 'sending' becomes failed — it may or may
// not have been delivered, and at-most-once forbids re-sending it — while every
// other state is left alone.
func TestEmailOutboxAbandonInFlightFailsInterruptedRows(t *testing.T) {
	f := newOutboxFixture(t)
	interrupted := f.enqueue(t, testClock)
	sent := f.enqueue(t, testClock)
	// Not due yet, so the claim below leaves it pending.
	pending := f.enqueue(t, testClock.Add(2*time.Hour))
	if claimed, err := f.outbox().ClaimDue(f.ctx, testClock, 10); err != nil {
		t.Fatalf("ClaimDue: %v", err)
	} else {
		wantIDs(t, claimed, interrupted, sent)
	}
	if err := f.outbox().MarkSent(f.ctx, sent, testClock.Add(time.Second)); err != nil {
		t.Fatalf("MarkSent: %v", err)
	}

	n, err := f.outbox().AbandonInFlight(f.ctx, testClock.Add(time.Hour))
	if err != nil {
		t.Fatalf("AbandonInFlight: %v", err)
	}
	if n != 1 {
		t.Fatalf("AbandonInFlight = %d, want 1 (only the claimed row)", n)
	}
	row := readEmailRow(t, f.s, interrupted)
	if row.status != string(application.EmailFailed) {
		t.Errorf("interrupted row status = %q, want failed", row.status)
	}
	if !row.lastErr.Valid || row.lastErr.String != "interrupted by restart" {
		t.Errorf("interrupted row last_error = %v, want %q", row.lastErr, "interrupted by restart")
	}
	if got := readEmailRow(t, f.s, pending); got.status != string(application.EmailPending) {
		t.Errorf("pending row status = %q, want pending", got.status)
	}
	if got := readEmailRow(t, f.s, sent); got.status != string(application.EmailSent) {
		t.Errorf("sent row status = %q, want sent", got.status)
	}
	if again, err := f.outbox().ClaimDue(f.ctx, testClock.Add(3*time.Hour), 10); err != nil {
		t.Fatalf("ClaimDue after abandon: %v", err)
	} else if len(again) != 1 || again[0].Notification.ID != pending {
		t.Fatalf("ClaimDue after abandon = %v, want only the still-pending row", claimedIDs(again))
	}
}

// TestEmailOutboxSkipPendingSkipsEveryPendingRow proves the "email is off"
// rule (S14): every pending row is skipped with the reason, and rows already
// sent or in flight are not touched.
func TestEmailOutboxSkipPendingSkipsEveryPendingRow(t *testing.T) {
	f := newOutboxFixture(t)
	inFlight := f.enqueue(t, testClock)
	first := f.enqueue(t, testClock.Add(2*time.Hour))
	second := f.enqueue(t, testClock.Add(3*time.Hour))
	if claimed, err := f.outbox().ClaimDue(f.ctx, testClock, 10); err != nil {
		t.Fatalf("ClaimDue: %v", err)
	} else {
		wantIDs(t, claimed, inFlight)
	}

	n, err := f.outbox().SkipPending(f.ctx, testClock.Add(time.Hour), application.EmailSkipDisabled)
	if err != nil {
		t.Fatalf("SkipPending: %v", err)
	}
	if n != 2 {
		t.Fatalf("SkipPending = %d, want 2", n)
	}
	for _, id := range []int64{first, second} {
		row := readEmailRow(t, f.s, id)
		if row.status != string(application.EmailSkipped) ||
			!row.skipReason.Valid || row.skipReason.String != application.EmailSkipDisabled {
			t.Errorf("row %d = %+v, want skipped as %q", id, row, application.EmailSkipDisabled)
		}
	}
	if row := readEmailRow(t, f.s, inFlight); row.status != string(application.EmailSending) {
		t.Errorf("in-flight row status = %q, want sending (a skip must not race a claim)", row.status)
	}
	// Nothing skipped can be claimed later, so enabling email again does not
	// flood the inbox with the rows skipped while it was off.
	if claimed, err := f.outbox().ClaimDue(f.ctx, testClock.Add(24*time.Hour), 10); err != nil {
		t.Fatalf("ClaimDue after skip: %v", err)
	} else if len(claimed) != 0 {
		t.Fatalf("a skipped row was claimed: %v", claimedIDs(claimed))
	}
}

// TestEmailOutboxClaimDueDropsOrphanRows proves the integrity rule that
// replaces the (deliberately absent) foreign key: a delivery row whose
// notification no longer exists — cascaded away with its ticket, user, or
// comment — is never claimed and is removed by the next claim.
func TestEmailOutboxClaimDueDropsOrphanRows(t *testing.T) {
	f := newOutboxFixture(t)
	orphan := f.enqueue(t, testClock)
	live := f.enqueue(t, testClock)
	// Simulate the notification disappearing the way the schema can: the row
	// is deleted (here directly; in production by a ticket/user/comment
	// cascade).
	if _, err := f.s.db.ExecContext(f.ctx, `DELETE FROM notifications WHERE id = ?`, orphan); err != nil {
		t.Fatalf("delete notification: %v", err)
	}
	if !emailRowExists(t, f.s, orphan) {
		t.Fatal("the delivery row vanished with its notification, want it orphaned until the next claim")
	}

	claimed, err := f.outbox().ClaimDue(f.ctx, testClock, 10)
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	wantIDs(t, claimed, live)
	if emailRowExists(t, f.s, orphan) {
		t.Error("the orphan delivery row survived the claim, want it deleted")
	}
}

// TestEmailOutboxClaimDueReportsStoreFailures proves a claim failure is
// reported instead of silently claiming nothing: a closed database must not
// look like "no work to do".
func TestEmailOutboxClaimDueReportsStoreFailures(t *testing.T) {
	f := newOutboxFixture(t)
	f.enqueue(t, testClock)
	if err := f.s.db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	if _, err := f.outbox().ClaimDue(f.ctx, testClock, 10); err == nil {
		t.Fatal("ClaimDue on a closed database returned no error")
	}
}
