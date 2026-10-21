package application_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// The hold use cases (issue #352, specs S2/S3/S5): putting an open ticket on
// hold pauses ONLY the resolution milestone, the pause reason is mandatory and
// audited, and resolving or cancelling a held ticket ends the hold in the same
// write. Every test here asserts the observable triple the UI renders: the
// returned ticket, the persisted row, and the audit timeline — plus the frozen
// SLA, whose resolution instants are the only ones a hold may move.

// holdHarness wires the ticket use cases with the SLA collaborator the hold
// shift needs (issue #352): the frozen commitment lives in the fake SLA store,
// the working calendar comes from the settings fake, and the fake unit of work
// persists a resumed hold's shifted instants into that same store, so a test
// reads the shift back through the SLA store port exactly as the UI would.
// The workflow create path stays unwired (nil version store/runner/workflow
// unit of work): hold tests seed their tickets directly instead of creating
// them.
type holdHarness struct {
	svc        *application.TicketService
	tickets    *fakeTicketStore
	users      *fakeUserStore
	categories *fakeCategoryStore
	comments   *fakeCommentStore
	audits     *fakeAuditStore
	tx         *fakeUnitOfWork
	clock      *fakeClock
	runner     *application.WorkflowRunner
	slaStore   *fakeSLAStore
	settings   *fakeSLASettingsStore
	sla        *application.SLAService
}

func newHoldHarness() *holdHarness { return newHoldHarnessWithSLA(true) }

// newHoldHarnessNoSLA mirrors the legacy NewTicketService wiring: no SLA
// collaborator at all, so a resumed hold reports no shift (documented
// behaviour of the legacy constructor).
func newHoldHarnessNoSLA() *holdHarness { return newHoldHarnessWithSLA(false) }

func newHoldHarnessWithSLA(withSLA bool) *holdHarness {
	clock := fixedClock()
	users := newFakeUserStore()
	categories := newFakeCategoryStore()
	tickets := newFakeTicketStore()
	comments := newFakeCommentStore()
	audits := newFakeAuditStore()
	tx := newFakeUnitOfWork(tickets, audits)
	builder := application.NewViewBuilder(tickets, users, categories, comments, audits, newFakeDeskStore())
	h := &holdHarness{
		tickets: tickets, users: users, categories: categories, comments: comments,
		audits: audits, tx: tx, clock: clock, runner: application.NewWorkflowRunner(clock),
	}
	if !withSLA {
		h.svc = application.NewTicketService(tickets, users, categories, tx, builder, clock)
		return h
	}
	h.slaStore = &fakeSLAStore{frozen: map[int64]*domain.TicketSLA{}}
	h.settings = &fakeSLASettingsStore{calendar: holdCalendar()}
	h.sla = application.NewSLAService(h.slaStore, h.settings, clock)
	// The unit-of-work fake persists a shifted commitment only when an SLA
	// store is attached (documented simplification in fakes_test.go); the hold
	// tests read the shift back through the SLA store port, so attach it here.
	tx.slas = h.slaStore
	h.svc = application.NewTicketServiceWithWorkflowCreate(tickets, users, categories, tx, builder, clock, nil, nil, nil, h.sla)
	return h
}

// withTicketStore returns a TicketService wired exactly like the harness's but
// reading through store, so one test can simulate a read that lost the race at
// the port boundary (issue #352 compare-and-set).
func (h *holdHarness) withTicketStore(store application.TicketStore) *application.TicketService {
	builder := application.NewViewBuilder(h.tickets, h.users, h.categories, h.comments, h.audits, newFakeDeskStore())
	return application.NewTicketServiceWithWorkflowCreate(store, h.users, h.categories, h.tx, builder, h.clock, nil, nil, nil, h.sla)
}

// storeTicket overwrites a seeded ticket's stored copy: the fake has no hold
// setter, and the arrange step must not go through the very use cases under
// test.
func (h *holdHarness) storeTicket(t *testing.T, ticket domain.Ticket) {
	t.Helper()
	if err := h.tickets.Update(context.Background(), &ticket); err != nil {
		t.Fatalf("seed ticket %d: %v", ticket.ID, err)
	}
}

func (h *holdHarness) stored(t *testing.T, id int64) domain.Ticket {
	t.Helper()
	got, err := h.tickets.GetByID(context.Background(), id, application.TicketQuery{Scope: application.ScopeAll})
	if err != nil {
		t.Fatalf("GetByID(%d): unexpected error: %v", id, err)
	}
	return *got
}

func (h *holdHarness) auditEvents(t *testing.T, id int64) []domain.AuditEvent {
	t.Helper()
	events, err := h.audits.ListByTicket(context.Background(), id)
	if err != nil {
		t.Fatalf("ListByTicket(%d): unexpected error: %v", id, err)
	}
	return events
}

func (h *holdHarness) seedFrozen(id int64, sla domain.TicketSLA) {
	h.slaStore.frozen[id] = &sla
}

func (h *holdHarness) frozen(t *testing.T, id int64) *domain.TicketSLA {
	t.Helper()
	frozen, err := h.slaStore.TicketSLA(context.Background(), id)
	if err != nil {
		t.Fatalf("TicketSLA(%d): unexpected error: %v", id, err)
	}
	return frozen
}

// holdCalendar makes working time equal to wall-clock time (every day,
// 00:00-24:00 UTC), so the expected shifted instants are hand-computable
// instead of re-derived from the calendar arithmetic under test.
func holdCalendar() domain.SLACalendar {
	return domain.SLACalendar{
		WorkingDays: []time.Weekday{
			time.Sunday, time.Monday, time.Tuesday, time.Wednesday,
			time.Thursday, time.Friday, time.Saturday,
		},
		StartMinute: 0,
		EndMinute:   24 * 60,
		Location:    time.UTC,
	}
}

// holdFrozenSLA returns the commitment frozen on a ticket ten hours before the
// fixture instant: the first-response instants are already past, the resolution
// warning is two hours ahead and the resolution due point ten hours ahead, so a
// pause shifts exactly those two by the paused duration.
func holdFrozenSLA(now time.Time) domain.TicketSLA {
	return domain.TicketSLA{
		FirstResponseSeconds: 3600,
		ResolveSeconds:       36000,
		WarnFirstResponseAt:  now.Add(-9 * time.Hour),
		DueFirstResponseAt:   now.Add(-8 * time.Hour),
		WarnResolveAt:        now.Add(2 * time.Hour),
		DueResolveAt:         now.Add(10 * time.Hour),
		StartedAt:            now.Add(-10 * time.Hour),
		PolicySnapshotAt:     now.Add(-10 * time.Hour),
	}
}

// seedHeldTicket seeds an open ticket already on hold, the arrange state the
// resume and terminal-transition use cases start from.
func seedHeldTicket(t *testing.T, h *holdHarness, catID int64, since time.Time) domain.Ticket {
	t.Helper()
	ticket := seededTicket(h.tickets, catID, domain.StateInProgress)
	ticket.OnHoldSince = &since
	ticket.OnHoldReason = "waiting for the requester"
	h.storeTicket(t, ticket)
	return ticket
}

func holdActor(id int64, role domain.Role) domain.User {
	return domain.User{ID: id, Name: "Ada", Email: "ada@example.com", Role: role}
}

// requireValidationKey asserts a *domain.ValidationError carrying the exact
// i18n key the UI translates.
func requireValidationKey(t *testing.T, err error, key string) {
	t.Helper()
	var ve *domain.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want *domain.ValidationError with key %q, got %v", key, err)
	}
	if ve.Key != key {
		t.Fatalf("ValidationError key = %q, want %q", ve.Key, key)
	}
}

// requireForbidden asserts a ForbiddenError carrying the exact message.
func requireForbidden(t *testing.T, err error, msg string) {
	t.Helper()
	var ferr *domain.ForbiddenError
	if !errors.As(err, &ferr) {
		t.Fatalf("want a ForbiddenError, got %v", err)
	}
	if ferr.Message != msg {
		t.Fatalf("ForbiddenError message = %q, want %q", ferr.Message, msg)
	}
}

// assertHoldAudit asserts one hold/resume audit event exactly as the timeline
// renders it: the changed field, the pause reason as note (hold only), and the
// session actor stamped with name AND user id (audit-log spec D14).
func assertHoldAudit(t *testing.T, ev domain.AuditEvent, action string, note *string, actor domain.User, at time.Time) {
	t.Helper()
	if ev.Action != action {
		t.Fatalf("audit action = %q, want %q (event %+v)", ev.Action, action, ev)
	}
	if ev.Field == nil || *ev.Field != "on_hold" {
		t.Fatalf("audit field = %v, want %q", ev.Field, "on_hold")
	}
	if ev.FromValue != nil || ev.ToValue != nil {
		t.Fatalf("a hold event never carries from/to values, got %v -> %v", ev.FromValue, ev.ToValue)
	}
	if !reflect.DeepEqual(ev.Note, note) {
		t.Fatalf("audit note = %v, want %v", ev.Note, note)
	}
	if ev.Actor != actor.Name {
		t.Fatalf("audit actor = %q, want the session actor %q", ev.Actor, actor.Name)
	}
	if ev.ActorUserID == nil || *ev.ActorUserID != actor.ID {
		t.Fatalf("audit actor_user_id = %v, want the session id %d", ev.ActorUserID, actor.ID)
	}
	if !ev.CreatedAt.Equal(at) {
		t.Fatalf("audit created_at = %v, want the injected clock instant %v", ev.CreatedAt, at)
	}
}

// assertSLAEquals asserts the commitment field by field. The resolution
// instants are the only ones a hold may move, so each mismatch names its half
// explicitly (issue #352 S2: the first-response clock never pauses).
func assertSLAEquals(t *testing.T, label string, got *domain.TicketSLA, want domain.TicketSLA) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s: no frozen SLA persisted, want %+v", label, want)
	}
	if got.FirstResponseSeconds != want.FirstResponseSeconds || got.ResolveSeconds != want.ResolveSeconds {
		t.Fatalf("%s: targets = %d/%d seconds, want %d/%d",
			label, got.FirstResponseSeconds, got.ResolveSeconds, want.FirstResponseSeconds, want.ResolveSeconds)
	}
	if !got.StartedAt.Equal(want.StartedAt) || !got.PolicySnapshotAt.Equal(want.PolicySnapshotAt) {
		t.Fatalf("%s: start/snapshot moved: got %v/%v, want %v/%v",
			label, got.StartedAt, got.PolicySnapshotAt, want.StartedAt, want.PolicySnapshotAt)
	}
	if !got.WarnFirstResponseAt.Equal(want.WarnFirstResponseAt) || !got.DueFirstResponseAt.Equal(want.DueFirstResponseAt) {
		t.Fatalf("%s: the first-response clock must not pause: got warn %v due %v, want %v/%v",
			label, got.WarnFirstResponseAt, got.DueFirstResponseAt, want.WarnFirstResponseAt, want.DueFirstResponseAt)
	}
	if !got.WarnResolveAt.Equal(want.WarnResolveAt) || !got.DueResolveAt.Equal(want.DueResolveAt) {
		t.Fatalf("%s: resolution instants = warn %v due %v, want %v/%v",
			label, got.WarnResolveAt, got.DueResolveAt, want.WarnResolveAt, want.DueResolveAt)
	}
}

// assertNothingWritten asserts a rejected call left the stored ticket, the
// audit trail, the hold write seam and the frozen SLA exactly as they were:
// every hold rejection writes nothing (issue #352 S3).
func (h *holdHarness) assertNothingWritten(t *testing.T, before domain.Ticket, frozen *domain.TicketSLA) {
	t.Helper()
	if got := h.stored(t, before.ID); !reflect.DeepEqual(got, before) {
		t.Fatalf("rejected call changed the stored ticket:\n got %+v\nwant %+v", got, before)
	}
	if events := h.auditEvents(t, before.ID); len(events) != 0 {
		t.Fatalf("rejected call must not be audited, got %+v", events)
	}
	if h.tx.updateHoldCalls != 0 {
		t.Fatalf("rejected call must not reach UpdateHold, got %d calls", h.tx.updateHoldCalls)
	}
	if h.slaStore == nil {
		return
	}
	if got := h.frozen(t, before.ID); !reflect.DeepEqual(got, frozen) {
		t.Fatalf("rejected call changed the frozen SLA: got %+v, want %+v", got, frozen)
	}
	if h.slaStore.insertTicketSLACalls != 0 {
		t.Fatalf("rejected call must not freeze an SLA, got %d inserts", h.slaStore.insertTicketSLACalls)
	}
}

// --- S3: putting a ticket on hold -----------------------------------------

// TestPutOnHoldPausesTicketAndAuditsReason proves a legal hold records the
// trimmed pause reason and the pause instant from the injected clock, keeps the
// ticket in its lifecycle state, writes ONE compare-and-set hold unit, and
// leaves the frozen SLA untouched (only the resume shifts it).
func TestPutOnHoldPausesTicketAndAuditsReason(t *testing.T) {
	h := newHoldHarness()
	cat := h.categories.seed("Bugs")
	ticket := seededTicket(h.tickets, cat.ID, domain.StateInProgress)
	frozen := holdFrozenSLA(h.clock.now)
	h.seedFrozen(ticket.ID, frozen)
	actor := holdActor(42, domain.RoleAdmin)

	h.clock.Advance(30 * time.Minute)
	held, err := h.svc.PutOnHold(context.Background(), actor, ticket.ID, "  waiting for the requester  ")
	if err != nil {
		t.Fatalf("PutOnHold: unexpected error: %v", err)
	}
	if held.OnHoldSince == nil || !held.OnHoldSince.Equal(h.clock.now) {
		t.Fatalf("PutOnHold: on_hold_since = %v, want the clock instant %v", held.OnHoldSince, h.clock.now)
	}
	if held.OnHoldReason != "waiting for the requester" {
		t.Fatalf("PutOnHold: on_hold_reason = %q, want the trimmed reason", held.OnHoldReason)
	}
	if !held.UpdatedAt.Equal(h.clock.now) {
		t.Fatalf("PutOnHold: updated_at = %v, want %v", held.UpdatedAt, h.clock.now)
	}
	if held.State != domain.StateInProgress {
		t.Fatalf("PutOnHold: a hold is an attribute, not a state; got %q", held.State)
	}

	stored := h.stored(t, ticket.ID)
	if stored.OnHoldSince == nil || !stored.OnHoldSince.Equal(h.clock.now) ||
		stored.OnHoldReason != "waiting for the requester" || !stored.UpdatedAt.Equal(h.clock.now) {
		t.Fatalf("PutOnHold: persisted ticket must carry the hold, got %+v", stored)
	}

	events := h.auditEvents(t, ticket.ID)
	if len(events) != 1 {
		t.Fatalf("PutOnHold: exactly one audit event expected, got %+v", events)
	}
	reason := "waiting for the requester"
	assertHoldAudit(t, events[0], domain.ActionHold, &reason, actor, h.clock.now)

	if h.tx.updateHoldCalls != 1 {
		t.Fatalf("PutOnHold: must issue exactly one hold write, got %d", h.tx.updateHoldCalls)
	}
	assertSLAEquals(t, "PutOnHold", h.frozen(t, ticket.ID), frozen)
	if h.slaStore.insertTicketSLACalls != 0 {
		t.Fatalf("PutOnHold: must never freeze a new commitment, got %d inserts", h.slaStore.insertTicketSLACalls)
	}
}

// TestPutOnHoldRejectionsWriteNothing proves every refusal (blank reason,
// already on hold, closed ticket, role user, out-of-scope agent) leaves the
// stored ticket, the audit trail and the frozen SLA untouched (issue #352 S3),
// and that a denied role never even reads the ticket.
func TestPutOnHoldRejectionsWriteNothing(t *testing.T) {
	otherAgent := int64(777)
	cases := []struct {
		name       string
		state      domain.State
		alreadyOn  bool
		reason     string
		actor      domain.User
		assignee   *int64
		wantNoRead bool
		assertErr  func(*testing.T, error)
	}{
		{
			name: "blank reason", state: domain.StateInProgress, reason: "   ",
			actor:     holdActor(42, domain.RoleAdmin),
			assertErr: func(t *testing.T, err error) { requireValidationKey(t, err, "error.hold_reason_required") },
		},
		{
			name: "already on hold", state: domain.StateInProgress, alreadyOn: true, reason: "again",
			actor:     holdActor(42, domain.RoleAdmin),
			assertErr: func(t *testing.T, err error) { requireValidationKey(t, err, "error.ticket_already_on_hold") },
		},
		{
			name: "closed ticket", state: domain.StateResolved, reason: "waiting",
			actor:     holdActor(42, domain.RoleAdmin),
			assertErr: func(t *testing.T, err error) { requireForbidden(t, err, domain.ErrMsgClosedTicketReadOnly) },
		},
		{
			name: "user role cannot edit", state: domain.StateInProgress, reason: "waiting",
			actor:      holdActor(43, domain.RoleUser),
			wantNoRead: true,
			assertErr:  func(t *testing.T, err error) { requireForbidden(t, err, domain.ErrMsgUserCannotEdit) },
		},
		{
			name: "agent out of scope", state: domain.StateInProgress, reason: "waiting",
			actor:    holdActor(44, domain.RoleAgent),
			assignee: &otherAgent,
			assertErr: func(t *testing.T, err error) {
				if !errors.Is(err, domain.ErrNotFound) {
					t.Fatalf("agent outside the ticket scope must get ErrNotFound, got %v", err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHoldHarness()
			cat := h.categories.seed("Bugs")
			ticket := seededTicket(h.tickets, cat.ID, tc.state)
			if tc.alreadyOn {
				if _, err := ticket.PutOnHold("waiting", h.clock.now); err != nil {
					t.Fatalf("arrange hold: %v", err)
				}
			}
			ticket.UserID = tc.assignee
			h.storeTicket(t, ticket)
			before := h.stored(t, ticket.ID)
			frozen := holdFrozenSLA(h.clock.now)
			h.seedFrozen(ticket.ID, frozen)
			frozenBefore := h.frozen(t, ticket.ID)
			readsBefore := len(h.tickets.getByIDCalls)

			_, err := h.svc.PutOnHold(context.Background(), tc.actor, ticket.ID, tc.reason)
			tc.assertErr(t, err)
			if tc.wantNoRead && len(h.tickets.getByIDCalls) != readsBefore {
				t.Fatalf("a denied role must be refused BEFORE the read, got %d reads", len(h.tickets.getByIDCalls)-readsBefore)
			}
			h.assertNothingWritten(t, before, frozenBefore)
		})
	}
}

// --- S1/S2: resuming a hold ----------------------------------------------

// TestResumeFromHoldShiftsOnlyResolutionInstants proves a manual resume clears
// the hold, audits it, and moves the frozen resolution warning/due points by
// exactly the working time spent paused while every other commitment field
// stays put (issue #352 S2).
func TestResumeFromHoldShiftsOnlyResolutionInstants(t *testing.T) {
	h := newHoldHarness()
	cat := h.categories.seed("Bugs")
	pausedAt := h.clock.now
	ticket := seedHeldTicket(t, h, cat.ID, pausedAt)
	frozen := holdFrozenSLA(pausedAt)
	h.seedFrozen(ticket.ID, frozen)
	actor := holdActor(42, domain.RoleAdmin)

	const pause = 3*time.Hour + 30*time.Minute
	h.clock.Advance(pause)

	resumed, err := h.svc.ResumeFromHold(context.Background(), actor, ticket.ID)
	if err != nil {
		t.Fatalf("ResumeFromHold: unexpected error: %v", err)
	}
	if resumed.OnHoldSince != nil || resumed.OnHoldReason != "" {
		t.Fatalf("ResumeFromHold: the hold must be cleared, got since=%v reason=%q", resumed.OnHoldSince, resumed.OnHoldReason)
	}
	if !resumed.UpdatedAt.Equal(h.clock.now) {
		t.Fatalf("ResumeFromHold: updated_at = %v, want %v", resumed.UpdatedAt, h.clock.now)
	}

	stored := h.stored(t, ticket.ID)
	if stored.OnHoldSince != nil || stored.OnHoldReason != "" || !stored.UpdatedAt.Equal(h.clock.now) {
		t.Fatalf("ResumeFromHold: persisted ticket must be off hold, got %+v", stored)
	}

	events := h.auditEvents(t, ticket.ID)
	if len(events) != 1 {
		t.Fatalf("ResumeFromHold: exactly one audit event expected, got %+v", events)
	}
	assertHoldAudit(t, events[0], domain.ActionResume, nil, actor, h.clock.now)

	want := frozen
	want.WarnResolveAt = frozen.WarnResolveAt.Add(pause)
	want.DueResolveAt = frozen.DueResolveAt.Add(pause)
	assertSLAEquals(t, "ResumeFromHold", h.frozen(t, ticket.ID), want)
	if h.slaStore.insertTicketSLACalls != 0 {
		t.Fatalf("ResumeFromHold: a shift must never freeze a new commitment, got %d inserts", h.slaStore.insertTicketSLACalls)
	}
	if h.tx.updateHoldCalls != 1 {
		t.Fatalf("ResumeFromHold: must issue exactly one hold write, got %d", h.tx.updateHoldCalls)
	}
}

// TestResumeFromHoldWithoutFrozenSLAShiftsNothing proves a ticket with no
// commitment resumes normally and the hold write carries no shift: absence of
// an SLA is a state, never a fabricated commitment.
func TestResumeFromHoldWithoutFrozenSLAShiftsNothing(t *testing.T) {
	h := newHoldHarness()
	cat := h.categories.seed("Bugs")
	ticket := seedHeldTicket(t, h, cat.ID, h.clock.now)
	before := h.stored(t, ticket.ID)
	actor := holdActor(42, domain.RoleAdmin)
	h.clock.Advance(time.Hour)

	resumed, err := h.svc.ResumeFromHold(context.Background(), actor, ticket.ID)
	if err != nil {
		t.Fatalf("ResumeFromHold: unexpected error: %v", err)
	}
	if resumed.OnHoldSince != nil {
		t.Fatalf("ResumeFromHold: the hold must be cleared, got %v", resumed.OnHoldSince)
	}
	if got := h.frozen(t, before.ID); got != nil {
		t.Fatalf("ResumeFromHold: a ticket with no commitment must stay without one, got %+v", got)
	}
	if h.slaStore.insertTicketSLACalls != 0 {
		t.Fatalf("ResumeFromHold: must not create a commitment, got %d inserts", h.slaStore.insertTicketSLACalls)
	}
	events := h.auditEvents(t, ticket.ID)
	if len(events) != 1 || events[0].Action != domain.ActionResume {
		t.Fatalf("ResumeFromHold: want one resume audit, got %+v", events)
	}
}

// TestResumeFromHoldRejectionsWriteNothing proves refusing a resume of a ticket
// that is not on hold, a role user, and an out-of-scope agent writes nothing
// (issue #352): the fake's stored ticket, its events and the SLA stay put.
func TestResumeFromHoldRejectionsWriteNothing(t *testing.T) {
	otherAgent := int64(777)
	cases := []struct {
		name       string
		onHold     bool
		actor      domain.User
		assignee   *int64
		wantNoRead bool
		assertErr  func(*testing.T, error)
	}{
		{
			name:      "not on hold",
			actor:     holdActor(42, domain.RoleAdmin),
			assertErr: func(t *testing.T, err error) { requireValidationKey(t, err, "error.ticket_not_on_hold") },
		},
		{
			name: "user role cannot edit", onHold: true, actor: holdActor(43, domain.RoleUser),
			wantNoRead: true,
			assertErr:  func(t *testing.T, err error) { requireForbidden(t, err, domain.ErrMsgUserCannotEdit) },
		},
		{
			name: "agent out of scope", onHold: true, actor: holdActor(44, domain.RoleAgent), assignee: &otherAgent,
			assertErr: func(t *testing.T, err error) {
				if !errors.Is(err, domain.ErrNotFound) {
					t.Fatalf("agent outside the ticket scope must get ErrNotFound, got %v", err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHoldHarness()
			cat := h.categories.seed("Bugs")
			ticket := seededTicket(h.tickets, cat.ID, domain.StateInProgress)
			if tc.onHold {
				if _, err := ticket.PutOnHold("waiting", h.clock.now); err != nil {
					t.Fatalf("arrange hold: %v", err)
				}
			}
			ticket.UserID = tc.assignee
			h.storeTicket(t, ticket)
			before := h.stored(t, ticket.ID)
			frozen := holdFrozenSLA(h.clock.now)
			h.seedFrozen(ticket.ID, frozen)
			frozenBefore := h.frozen(t, ticket.ID)
			readsBefore := len(h.tickets.getByIDCalls)

			_, err := h.svc.ResumeFromHold(context.Background(), tc.actor, ticket.ID)
			tc.assertErr(t, err)
			if tc.wantNoRead && len(h.tickets.getByIDCalls) != readsBefore {
				t.Fatalf("a denied role must be refused BEFORE the read, got %d reads", len(h.tickets.getByIDCalls)-readsBefore)
			}
			h.assertNothingWritten(t, before, frozenBefore)
		})
	}
}

// TestHoldWithoutSLAWiringShiftsNothing proves the legacy NewTicketService
// wiring (no SLA collaborator) still holds and resumes a ticket: there is no
// commitment to shift, and nothing panics.
func TestHoldWithoutSLAWiringShiftsNothing(t *testing.T) {
	h := newHoldHarnessNoSLA()
	cat := h.categories.seed("Bugs")
	ticket := seededTicket(h.tickets, cat.ID, domain.StateInProgress)
	actor := holdActor(42, domain.RoleAdmin)

	held, err := h.svc.PutOnHold(context.Background(), actor, ticket.ID, "waiting")
	if err != nil {
		t.Fatalf("PutOnHold: unexpected error: %v", err)
	}
	if held.OnHoldSince == nil {
		t.Fatal("PutOnHold: the hold must be stored without an SLA collaborator")
	}
	h.clock.Advance(time.Hour)
	if _, err := h.svc.ResumeFromHold(context.Background(), actor, ticket.ID); err != nil {
		t.Fatalf("ResumeFromHold: unexpected error: %v", err)
	}
	events := h.auditEvents(t, ticket.ID)
	if len(events) != 2 || events[0].Action != domain.ActionHold || events[1].Action != domain.ActionResume {
		t.Fatalf("want hold then resume audits, got %+v", events)
	}
}

// --- S5: terminal states end the hold ------------------------------------

// wantShiftsResolution returns the commitment with its resolution instants
// moved by pause, the expected outcome of a hold that lasted pause.
func wantShiftsResolution(frozen domain.TicketSLA, pause time.Duration) domain.TicketSLA {
	frozen.WarnResolveAt = frozen.WarnResolveAt.Add(pause)
	frozen.DueResolveAt = frozen.DueResolveAt.Add(pause)
	return frozen
}

// assertHoldEndedInOneWrite asserts the exact two-event batch a terminal
// transition on a held ticket persists: the resume first, then the transition,
// both stamped with the acting session and the same instant (issue #352 S5).
func assertHoldEndedInOneWrite(t *testing.T, h *holdHarness, ticketID int64, from, to domain.State, actor domain.User) {
	t.Helper()
	events := h.auditEvents(t, ticketID)
	if len(events) != 2 {
		t.Fatalf("want exactly [resume, transition], got %+v", events)
	}
	assertHoldAudit(t, events[0], domain.ActionResume, nil, actor, h.clock.now)
	tr := events[1]
	if tr.Action != domain.ActionTransition {
		t.Fatalf("second event action = %q, want %q", tr.Action, domain.ActionTransition)
	}
	if tr.Field == nil || *tr.Field != "state" {
		t.Fatalf("transition event field = %v, want state", tr.Field)
	}
	if tr.FromValue == nil || *tr.FromValue != string(from) || tr.ToValue == nil || *tr.ToValue != string(to) {
		t.Fatalf("transition event = %v -> %v, want %q -> %q", tr.FromValue, tr.ToValue, from, to)
	}
	if tr.Actor != actor.Name || tr.ActorUserID == nil || *tr.ActorUserID != actor.ID {
		t.Fatalf("transition event must carry the session actor, got %+v", tr)
	}
	if !tr.CreatedAt.Equal(h.clock.now) {
		t.Fatalf("transition event created_at = %v, want %v", tr.CreatedAt, h.clock.now)
	}
	if h.tx.updateHoldCalls != 1 {
		t.Fatalf("a hold-ending terminal change must be ONE hold write, got %d", h.tx.updateHoldCalls)
	}
}

// TestTransitionResolveEndsHoldInOneWrite proves resolving a held ticket ends
// the hold in the SAME write: the resolution instants shift at the transition
// instant, the state becomes resolved, and the timeline reads resume then
// transition (issue #352 S5).
func TestTransitionResolveEndsHoldInOneWrite(t *testing.T) {
	h := newHoldHarness()
	cat := h.categories.seed("Bugs")
	pausedAt := h.clock.now
	ticket := seedHeldTicket(t, h, cat.ID, pausedAt)
	frozen := holdFrozenSLA(pausedAt)
	h.seedFrozen(ticket.ID, frozen)
	actor := holdActor(42, domain.RoleAdmin)

	const pause = 2 * time.Hour
	h.clock.Advance(pause)

	updated, err := h.svc.Transition(context.Background(), actor, ticket.ID, domain.StateResolved, "")
	if err != nil {
		t.Fatalf("Transition: unexpected error: %v", err)
	}
	if updated.State != domain.StateResolved || updated.ResolvedAt == nil || !updated.ResolvedAt.Equal(h.clock.now) {
		t.Fatalf("Transition: want resolved at %v, got %q resolved_at=%v", h.clock.now, updated.State, updated.ResolvedAt)
	}
	if updated.OnHoldSince != nil || updated.OnHoldReason != "" {
		t.Fatalf("Transition: a resolved ticket must not stay on hold, got since=%v", updated.OnHoldSince)
	}

	stored := h.stored(t, ticket.ID)
	if stored.State != domain.StateResolved || stored.OnHoldSince != nil || stored.OnHoldReason != "" {
		t.Fatalf("Transition: persisted ticket = %+v, want resolved off hold", stored)
	}
	assertHoldEndedInOneWrite(t, h, ticket.ID, domain.StateInProgress, domain.StateResolved, actor)
	assertSLAEquals(t, "Transition(resolved)", h.frozen(t, ticket.ID), wantShiftsResolution(frozen, pause))
}

// TestTransitionCancelEndsHoldInOneWrite proves cancelling a held ticket ends
// the hold the same way (issue #352 S5 covers cancelling explicitly).
func TestTransitionCancelEndsHoldInOneWrite(t *testing.T) {
	h := newHoldHarness()
	cat := h.categories.seed("Bugs")
	pausedAt := h.clock.now
	ticket := seedHeldTicket(t, h, cat.ID, pausedAt)
	frozen := holdFrozenSLA(pausedAt)
	h.seedFrozen(ticket.ID, frozen)
	actor := holdActor(42, domain.RoleAdmin)

	const pause = 90 * time.Minute
	h.clock.Advance(pause)

	updated, err := h.svc.Transition(context.Background(), actor, ticket.ID, domain.StateCancelled, "duplicate request")
	if err != nil {
		t.Fatalf("Transition: unexpected error: %v", err)
	}
	if updated.State != domain.StateCancelled || updated.OnHoldSince != nil || updated.OnHoldReason != "" {
		t.Fatalf("Transition: want cancelled off hold, got %+v", updated)
	}
	stored := h.stored(t, ticket.ID)
	if stored.State != domain.StateCancelled || stored.OnHoldSince != nil {
		t.Fatalf("Transition: persisted ticket = %+v, want cancelled off hold", stored)
	}
	assertHoldEndedInOneWrite(t, h, ticket.ID, domain.StateInProgress, domain.StateCancelled, actor)
	assertSLAEquals(t, "Transition(cancelled)", h.frozen(t, ticket.ID), wantShiftsResolution(frozen, pause))
	// A cancellation reason was never persisted on the transition audit (only a
	// reopen-from-closed note is), and ending the hold must not change that: the
	// reason stays on the resume event's side of the batch as nothing.
	events := h.auditEvents(t, ticket.ID)
	if events[1].Note != nil {
		t.Fatalf("transition note = %v, want none", *events[1].Note)
	}
}

// TestTransitionOfTicketNotOnHoldKeepsUpdatePath pins the behaviour the hold
// change must not alter: an ordinary transition of a ticket that is NOT on hold
// still persists through Update with exactly one transition audit, never
// touching the hold write or the frozen SLA (issue #352).
func TestTransitionOfTicketNotOnHoldKeepsUpdatePath(t *testing.T) {
	h := newHoldHarness()
	cat := h.categories.seed("Bugs")
	ticket := seededTicket(h.tickets, cat.ID, domain.StateInProgress)
	frozen := holdFrozenSLA(h.clock.now)
	h.seedFrozen(ticket.ID, frozen)
	frozenBefore := h.frozen(t, ticket.ID)
	actor := holdActor(42, domain.RoleAdmin)
	h.clock.Advance(time.Minute)

	updated, err := h.svc.Transition(context.Background(), actor, ticket.ID, domain.StateResolved, "")
	if err != nil {
		t.Fatalf("Transition: unexpected error: %v", err)
	}
	if updated.State != domain.StateResolved || updated.OnHoldSince != nil {
		t.Fatalf("Transition: want resolved and never held, got %+v", updated)
	}
	events := h.auditEvents(t, ticket.ID)
	if len(events) != 1 || events[0].Action != domain.ActionTransition {
		t.Fatalf("Transition: want exactly one transition audit, got %+v", events)
	}
	if h.tx.updateHoldCalls != 0 {
		t.Fatalf("a ticket not on hold must keep using Update, got %d hold writes", h.tx.updateHoldCalls)
	}
	if got := h.frozen(t, ticket.ID); !reflect.DeepEqual(got, frozenBefore) {
		t.Fatalf("a ticket not on hold must never shift its SLA: got %+v, want %+v", got, frozenBefore)
	}
}

// --- issue #352: the compare-and-set refusal -----------------------------

// staleHoldStore answers reads from a SNAPSHOT taken before the hold changed,
// so the service compares against an outdated hold instant and the store's
// compare-and-set must refuse the write (issue #352).
type staleHoldStore struct {
	*fakeTicketStore
	snapshot domain.Ticket
}

func (s staleHoldStore) GetByID(_ context.Context, _ int64, _ application.TicketQuery) (*domain.Ticket, error) {
	cp := s.snapshot
	return &cp, nil
}

// TestHoldConflictFromStorePropagatesUnchanged proves a stale read surfaces the
// store's *domain.HoldConflictError untouched and writes nothing — neither the
// ticket, nor the timeline, nor the frozen SLA (issue #352).
func TestHoldConflictFromStorePropagatesUnchanged(t *testing.T) {
	actor := holdActor(42, domain.RoleAdmin)
	cases := []struct {
		name string
		call func(*application.TicketService, int64) error
		// stale applies the outdated facts the losing read observed.
		stale func(*domain.Ticket, time.Time)
	}{
		{
			// The resume read the hold that began at 10:00; the stored row was
			// re-held at 10:30 by another actor.
			name: "resume against a superseded hold instant",
			call: func(svc *application.TicketService, id int64) error {
				_, err := svc.ResumeFromHold(context.Background(), actor, id)
				return err
			},
			stale: func(t *domain.Ticket, earlier time.Time) {
				t.OnHoldSince = &earlier
				t.OnHoldReason = "waiting"
			},
		},
		{
			// The hold read an un-held ticket; somebody else held it meanwhile.
			name: "hold against a ticket held meanwhile",
			call: func(svc *application.TicketService, id int64) error {
				_, err := svc.PutOnHold(context.Background(), actor, id, "waiting")
				return err
			},
			stale: func(t *domain.Ticket, _ time.Time) {
				t.OnHoldSince = nil
				t.OnHoldReason = ""
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHoldHarness()
			cat := h.categories.seed("Bugs")
			stalePause := h.clock.now
			h.clock.Advance(30 * time.Minute)
			ticket := seedHeldTicket(t, h, cat.ID, h.clock.now)
			frozen := holdFrozenSLA(h.clock.now)
			h.seedFrozen(ticket.ID, frozen)
			before := h.stored(t, ticket.ID)
			frozenBefore := h.frozen(t, ticket.ID)

			snapshot := before
			tc.stale(&snapshot, stalePause)
			svc := h.withTicketStore(staleHoldStore{fakeTicketStore: h.tickets, snapshot: snapshot})

			err := tc.call(svc, ticket.ID)
			var conflict *domain.HoldConflictError
			if !errors.As(err, &conflict) {
				t.Fatalf("want *domain.HoldConflictError, got %v", err)
			}
			if conflict.TicketID != ticket.ID {
				t.Fatalf("conflict ticket id = %d, want %d", conflict.TicketID, ticket.ID)
			}
			if h.tx.updateHoldCalls != 1 {
				t.Fatalf("the losing write must reach the store once, got %d calls", h.tx.updateHoldCalls)
			}
			if got := h.stored(t, ticket.ID); !reflect.DeepEqual(got, before) {
				t.Fatalf("a refused compare-and-set must write nothing:\n got %+v\nwant %+v", got, before)
			}
			if events := h.auditEvents(t, ticket.ID); len(events) != 0 {
				t.Fatalf("a refused compare-and-set must not be audited, got %+v", events)
			}
			if got := h.frozen(t, ticket.ID); !reflect.DeepEqual(got, frozenBefore) {
				t.Fatalf("a refused compare-and-set must not shift the SLA: got %+v, want %+v", got, frozenBefore)
			}
		})
	}
}

// --- L8: a workflow terminal step cannot close a held ticket --------------

// TestWorkflowTerminalStepOnHeldTicketIsRefused proves the L8 decision: a
// workflow completion whose plan reaches a resolve or close step on a ticket
// that is on hold is refused with error.ticket_on_hold and writes nothing — the
// agent resumes the ticket first. No workflow-runner change is involved: the
// refusal is the domain transition's own guard, hit by applyTerminal.
func TestWorkflowTerminalStepOnHeldTicketIsRefused(t *testing.T) {
	cases := []struct {
		name string
		def  domain.WorkflowDefinition
	}{
		{"resolve step", wf(res())},
		{"close step", wf(clo())},
		{"human step then resolve", wf(man(), res())},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHoldHarness()
			cat := h.categories.seed("Bugs")
			ticket := seedHeldTicket(t, h, cat.ID, h.clock.now)
			ticket.UserID = ptr(int64(5))
			h.storeTicket(t, ticket)
			before := h.stored(t, ticket.ID)
			frozen := holdFrozenSLA(h.clock.now)
			h.seedFrozen(ticket.ID, frozen)
			frozenBefore := h.frozen(t, ticket.ID)

			snap := application.WorkflowExecutionSnapshot{
				Ticket:   &before,
				Run:      &application.WorkflowRun{TicketID: ticket.ID, CurrentStepIndex: 0, Status: "active", StartedAt: h.clock.now},
				Workflow: tc.def,
			}
			cmd := application.CompleteWorkflowCommand{
				TicketID: ticket.ID, ActorUserID: 5, ActorName: "Ana", ExpectedPosition: 1,
			}

			plan, err := h.runner.PlanComplete(context.Background(), snap, cmd)
			requireValidationKey(t, err, "error.ticket_on_hold")
			var ve *domain.ValidationError
			if !errors.As(err, &ve) || ve.Field != "state" {
				t.Fatalf("want the state ValidationError, got %v", err)
			}
			if !reflect.DeepEqual(plan, application.WorkflowMutationPlan{}) {
				t.Fatalf("a refused completion must plan nothing, got %+v", plan)
			}
			h.assertNothingWritten(t, before, frozenBefore)
		})
	}
}
