package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// TicketVisitService (issue #415): the three rules this layer
// owns — what counts as somebody else's change, which visibility a reader is
// allowed to compare against, and which instant a visit is stamped with. The
// history derivation itself is the store's, and is covered in the sqlite
// adapter tests; the fake models only the shape the rules need (see its
// documented simplifications in fakes_test.go).

func newVisitHarness() (*fakeTicketVisitStore, *application.TicketVisitService, *fakeClock) {
	visits := newFakeTicketVisitStore()
	clock := fixedClock()
	return visits, application.NewTicketVisitService(visits, clock), clock
}

func visitActor(id int64, role domain.Role) domain.User {
	return domain.User{ID: id, Name: "actor", Role: role}
}

// A change the reader made is never news
// to the reader, while the same history written by somebody else is.
func TestTicketVisitChangedIgnoresTheReadersOwnChange(t *testing.T) {
	visits, svc, _ := newVisitHarness()
	const readerID, ticketID int64 = 7, 20
	visits.addAudit(ticketID, readerID)
	visits.addComment(ticketID, readerID, false)

	changed, err := svc.Changed(context.Background(), visitActor(readerID, domain.RoleAdmin), []int64{ticketID})
	if err != nil {
		t.Fatalf("Changed: %v", err)
	}
	if changed[ticketID] {
		t.Fatalf("the reader's own audit event and comment marked the ticket changed: %v", changed)
	}

	visits.addComment(ticketID, 8, false)
	changed, err = svc.Changed(context.Background(), visitActor(readerID, domain.RoleAdmin), []int64{ticketID})
	if err != nil {
		t.Fatalf("Changed after another author: %v", err)
	}
	if !changed[ticketID] {
		t.Fatalf("somebody else's comment did not mark the ticket changed: %v", changed)
	}
}

// The
// service derives internal-comment visibility from CapCommentInternal, so an
// agent and an admin compare against internal comments and a requester does
// not.
func TestTicketVisitChangedFiltersInternalCommentsByRole(t *testing.T) {
	visits, svc, _ := newVisitHarness()
	const ticketID int64 = 21
	// No audit event and no public comment: the internal comment is the ONLY
	// history, so each role's answer below is about visibility alone.
	visits.addComment(ticketID, 2, true)

	for _, role := range []domain.Role{domain.RoleAgent, domain.RoleAdmin, domain.RoleRoot} {
		changed, err := svc.Changed(context.Background(), visitActor(3, role), []int64{ticketID})
		if err != nil {
			t.Fatalf("Changed as %s: %v", role, err)
		}
		if !changed[ticketID] {
			t.Errorf("an internal comment did not mark the ticket changed for a %s", role)
		}
	}

	changed, err := svc.Changed(context.Background(), visitActor(3, domain.RoleUser), []int64{ticketID})
	if err != nil {
		t.Fatalf("Changed as user: %v", err)
	}
	if changed[ticketID] {
		t.Fatalf("an internal comment marked the ticket changed for a requester: %v", changed)
	}

	// A public comment is visible to everyone, so it marks for the requester too.
	visits.addComment(ticketID, 2, false)
	changed, err = svc.Changed(context.Background(), visitActor(3, domain.RoleUser), []int64{ticketID})
	if err != nil {
		t.Fatalf("Changed as user after a public comment: %v", err)
	}
	if !changed[ticketID] {
		t.Fatalf("a public comment did not mark the ticket changed for a requester: %v", changed)
	}
}

// A ticket the reader never opened is judged from its creation, and a
// ticket they did open is judged from the mark the visit stored.
func TestTicketVisitChangedRespectsTheStoredMark(t *testing.T) {
	visits, svc, clock := newVisitHarness()
	const readerID, ticketID int64 = 9, 22
	visits.addAudit(ticketID, 1)
	read := visitActor(readerID, domain.RoleAdmin)

	changed, err := svc.Changed(context.Background(), read, []int64{ticketID})
	if err != nil {
		t.Fatalf("Changed before any visit: %v", err)
	}
	if !changed[ticketID] {
		t.Fatal("a never-opened ticket with history from somebody else must be marked")
	}

	mark, err := svc.Mark(context.Background(), ticketID)
	if err != nil {
		t.Fatalf("Mark: %v", err)
	}
	if err := svc.RecordVisit(context.Background(), read, ticketID, mark); err != nil {
		t.Fatalf("RecordVisit: %v", err)
	}

	changed, err = svc.Changed(context.Background(), read, []int64{ticketID})
	if err != nil {
		t.Fatalf("Changed after the visit: %v", err)
	}
	if changed[ticketID] {
		t.Fatalf("the visited ticket still reads as changed: %v", changed)
	}

	visits.addAudit(ticketID, 1)
	changed, err = svc.Changed(context.Background(), read, []int64{ticketID})
	if err != nil {
		t.Fatalf("Changed after a newer event: %v", err)
	}
	if !changed[ticketID] {
		t.Fatal("an event past the stored mark must mark the ticket changed again")
	}
	if got := clock.now; !got.Equal(fixedClock().now) {
		t.Fatalf("clock moved to %v", got)
	}
}

// A visit is stamped with ONE clock instant, and it is the injected one —
// never the wall clock — and it reads a ticket's mark without a user.
func TestTicketVisitRecordVisitStampsTheInjectedClock(t *testing.T) {
	visits, svc, clock := newVisitHarness()
	const readerID, ticketID int64 = 4, 23
	clock.Advance(90 * timeMinute)
	mark := application.VisitMark{AuditID: 3, CommentID: 2}

	if err := svc.RecordVisit(context.Background(), visitActor(readerID, domain.RoleAgent), ticketID, mark); err != nil {
		t.Fatalf("RecordVisit: %v", err)
	}
	if len(visits.recorded) != 1 {
		t.Fatalf("recorded %d visits, want 1", len(visits.recorded))
	}
	got := visits.recorded[0]
	if got.userID != readerID || got.ticketID != ticketID {
		t.Fatalf("recorded visit = %+v, want user %d ticket %d", got, readerID, ticketID)
	}
	if got.mark != mark {
		t.Fatalf("recorded mark = %+v, want %+v", got.mark, mark)
	}
	if !got.at.Equal(clock.now) {
		t.Fatalf("visit instant = %v, want the injected clock's %v", got.at, clock.now)
	}
}

// Opening the ticket reads that ticket's unread bell
// rows and leaves another ticket's alone.
func TestTicketVisitRecordVisitReadsOnlyThatTicketsNotifications(t *testing.T) {
	visits, svc, clock := newVisitHarness()
	const readerID, openedID, otherID int64 = 5, 24, 25
	visits.addNotification(readerID, openedID)
	visits.addNotification(readerID, otherID)
	visits.addNotification(6, openedID)

	if err := svc.RecordVisit(context.Background(), visitActor(readerID, domain.RoleAgent), openedID, application.VisitMark{}); err != nil {
		t.Fatalf("RecordVisit: %v", err)
	}
	for i, n := range visits.notifications {
		read := n.ReadAt != nil
		wantRead := i == 0
		if read != wantRead {
			t.Fatalf("notification %d (user %d ticket %d) read = %v, want %v", i, n.UserID, n.TicketID, read, wantRead)
		}
		if read && !n.ReadAt.Equal(clock.now) {
			t.Fatalf("notification read at %v, want the clock's %v", *n.ReadAt, clock.now)
		}
	}
}

// No store error is ever swallowed: each call answers it unchanged, so a
// handler can map it exactly like the reads beside it.
func TestTicketVisitServicePropagatesStoreErrors(t *testing.T) {
	wantErr := errors.New("store down")
	visits, svc, _ := newVisitHarness()
	visits.markErr, visits.recordErr, visits.changedErr = wantErr, wantErr, wantErr
	ctx, actor := context.Background(), visitActor(1, domain.RoleAdmin)

	if _, err := svc.Mark(ctx, 1); !errors.Is(err, wantErr) {
		t.Errorf("Mark error = %v, want %v", err, wantErr)
	}
	if err := svc.RecordVisit(ctx, actor, 1, application.VisitMark{}); !errors.Is(err, wantErr) {
		t.Errorf("RecordVisit error = %v, want %v", err, wantErr)
	}
	if _, err := svc.Changed(ctx, actor, []int64{1}); !errors.Is(err, wantErr) {
		t.Errorf("Changed error = %v, want %v", err, wantErr)
	}
}
