package application_test

import (
	"context"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// The requester's reply ends the hold (issue #352, S1): a public comment by the
// requester on a ticket on hold resumes it in the SAME write as the comment,
// shifting the resolution SLA by the working time spent on hold. Staff
// comments never resume, and a hold that changed meanwhile never costs the
// requester their reply.

type commentHoldHarness struct {
	*holdHarness
	commentTx *fakeCommentUnitOfWork
	comments  *application.CommentService
	requester domain.User
	agent     domain.User
	ticket    domain.Ticket
	frozenSLA domain.TicketSLA
	since     time.Time
}

func newCommentHoldHarness(t *testing.T) *commentHoldHarness {
	t.Helper()
	h := newHoldHarness()
	ticket, requester, agent := seedRequesterTicket(t, h, true)
	frozen := holdFrozenSLA(h.clock.now)
	h.seedFrozen(ticket.ID, frozen)
	since := h.clock.now.Add(time.Hour)
	ticket.OnHoldSince = &since
	ticket.OnHoldReason = "send the serial number"
	h.storeTicket(t, ticket)
	h.clock.Advance(5 * time.Hour)
	commentTx := newFakeCommentUnitOfWork(h.comments)
	commentTx.holds = h.tx
	svc := application.NewCommentService(h.tickets, h.comments, h.users, commentTx, h.clock).WithSLA(h.sla)
	return &commentHoldHarness{holdHarness: h, commentTx: commentTx, comments: svc,
		requester: requester, agent: agent, ticket: ticket, frozenSLA: frozen, since: since}
}

func TestRequesterCommentResumesTheHold(t *testing.T) {
	h := newCommentHoldHarness(t)
	c, err := h.comments.Add(context.Background(), h.requester, h.ticket.ID, "here is the serial", "public")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if c.ID == 0 {
		t.Error("comment not stored")
	}
	stored := h.stored(t, h.ticket.ID)
	if stored.OnHoldSince != nil || stored.OnHoldReason != "" {
		t.Errorf("hold not ended: %v %q", stored.OnHoldSince, stored.OnHoldReason)
	}
	if !stored.UpdatedAt.Equal(h.clock.now) {
		t.Errorf("updated_at = %v, want %v", stored.UpdatedAt, h.clock.now)
	}
	events := h.auditEvents(t, h.ticket.ID)
	if len(events) != 1 {
		t.Fatalf("audit events = %+v, want one resume", events)
	}
	assertHoldAudit(t, events[0], domain.ActionResume, nil, h.requester, h.clock.now)
	want := h.frozenSLA.ShiftResolveForHold(h.since, h.clock.now, holdCalendar())
	assertSLAEquals(t, "resumed by reply", h.frozen0(t), want)
	// The comment's own news is unchanged: the assignee hears about the reply.
	if got := h.commentTx.notified(h.agent.ID); len(got) != 1 || got[0].Kind != domain.NotificationCommentPublic {
		t.Errorf("assignee notifications = %+v, want one comment_public", got)
	}
	if len(h.commentTx.notifications) != 1 {
		t.Errorf("notifications = %+v, want only the comment's", h.commentTx.notifications)
	}
}

func (h *commentHoldHarness) frozen0(t *testing.T) *domain.TicketSLA {
	t.Helper()
	return h.holdHarness.frozen(t, h.ticket.ID)
}

func TestStaffCommentKeepsTheHold(t *testing.T) {
	for _, vis := range []string{"public", "internal"} {
		t.Run(vis, func(t *testing.T) {
			h := newCommentHoldHarness(t)
			actor := domain.User{ID: h.agent.ID, Name: h.agent.Name, Role: domain.RoleAgent}
			if _, err := h.comments.Add(context.Background(), actor, h.ticket.ID, "any news?", vis); err != nil {
				t.Fatalf("Add: %v", err)
			}
			stored := h.stored(t, h.ticket.ID)
			if stored.OnHoldSince == nil || !stored.OnHoldSince.Equal(h.since) {
				t.Errorf("staff comment ended the hold: %v", stored.OnHoldSince)
			}
			if events := h.auditEvents(t, h.ticket.ID); len(events) != 0 {
				t.Errorf("audit events = %+v, want none", events)
			}
			assertSLAEquals(t, "staff comment", h.frozen0(t), h.frozenSLA)
		})
	}
}

// TestRequesterCommentWithoutAHoldIsUnchanged pins the previous behaviour of
// CommentService.Add for a ticket that is not on hold.
func TestRequesterCommentWithoutAHoldIsUnchanged(t *testing.T) {
	h := newCommentHoldHarness(t)
	ticket := h.ticket
	ticket.OnHoldSince = nil
	ticket.OnHoldReason = ""
	h.storeTicket(t, ticket)
	before := h.stored(t, ticket.ID)
	if _, err := h.comments.Add(context.Background(), h.requester, ticket.ID, "hello", "public"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if after := h.stored(t, ticket.ID); after.UpdatedAt != before.UpdatedAt || after.OnHoldSince != nil {
		t.Errorf("ticket changed by a plain comment: %+v", after)
	}
	if events := h.auditEvents(t, ticket.ID); len(events) != 0 {
		t.Errorf("audit events = %+v, want none", events)
	}
	if h.tx.updateHoldCalls != 0 {
		t.Errorf("UpdateHold calls = %d, want 0", h.tx.updateHoldCalls)
	}
}

// TestRequesterCommentSurvivesALostHoldRace: the hold changed after the read
// (an agent resumed it first), so the reply is stored but resumes nothing.
func TestRequesterCommentSurvivesALostHoldRace(t *testing.T) {
	h := newCommentHoldHarness(t)
	// The service reads the held ticket; the store already lost the hold.
	stale := h.ticket
	cleared := h.ticket
	cleared.OnHoldSince = nil
	cleared.OnHoldReason = ""
	h.storeTicket(t, cleared)
	svc := application.NewCommentService(staleHoldStore{fakeTicketStore: h.tickets, snapshot: stale}, h.holdHarness.comments, h.users, h.commentTx, h.clock).WithSLA(h.sla)
	c, err := svc.Add(context.Background(), h.requester, h.ticket.ID, "here is the serial", "public")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if c.ID == 0 {
		t.Error("the reply was lost to the race")
	}
	if events := h.auditEvents(t, h.ticket.ID); len(events) != 0 {
		t.Errorf("audit events = %+v, want no resume", events)
	}
	assertSLAEquals(t, "lost race", h.frozen0(t), h.frozenSLA)
}
