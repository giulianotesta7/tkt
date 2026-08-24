package application_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Contextual workflow timeline (presentation half): a workflow_assignment
// event resolves into STRUCTURED TimelineItem presentation facts — person and
// desk labels plus the assignment branch — so the template renders one exact
// main line without deriving markup from raw audit values.

func assignmentEvent(field, from, to string, deskID *int64) domain.AuditEvent {
	return domain.AuditEvent{Actor: "workflow", Action: domain.ActionWorkflowAssignment,
		Field: &field, FromValue: &from, ToValue: &to, DeskID: deskID}
}

// assignmentFixture carries the seeded stores a workflow_assignment view test
// arranges against. Tests append their own audit events so each case controls
// the exact backend facts (ToValue user id, DeskID).
type assignmentFixture struct {
	builder *application.ViewBuilder
	users   *fakeUserStore
	desks   *fakeDeskStore
	audits  *fakeAuditStore
	ticket  domain.Ticket
	user    domain.User
	desk    domain.Desk
	clock   *fakeClock
}

func seedAssignmentFixture(t *testing.T) assignmentFixture {
	t.Helper()
	clock := fixedClock()
	tickets := newFakeTicketStore()
	users := newFakeUserStore()
	categories := newFakeCategoryStore()
	comments := newFakeCommentStore()
	audits := newFakeAuditStore()
	desks := newFakeDeskStore()

	user := users.seed("Beto", "beto@example.com", true)
	cat := categories.seed("Bugs")
	desk := &domain.Desk{Name: "Network"}
	if err := desks.Create(context.Background(), desk); err != nil {
		t.Fatalf("seed desk: %v", err)
	}
	ticket := tickets.seed(domain.Ticket{
		Title: "Seeded", CategoryID: cat.ID, Priority: domain.PriorityLow,
		State: domain.StateInProgress, CreatedAt: clock.now, UpdatedAt: clock.now,
	})
	return assignmentFixture{
		builder: application.NewViewBuilder(tickets, users, categories, comments, audits, desks),
		users:   users, desks: desks, audits: audits,
		ticket: ticket, user: user, desk: *desk, clock: clock,
	}
}

func (f assignmentFixture) append(t *testing.T, ev domain.AuditEvent) {
	t.Helper()
	ev.TicketID = f.ticket.ID
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = f.clock.now
	}
	if err := f.audits.Append(context.Background(), ev); err != nil {
		t.Fatalf("append audit: %v", err)
	}
}

func (f assignmentFixture) items(t *testing.T) []application.TimelineItem {
	t.Helper()
	view, err := f.builder.TicketView(context.Background(), f.ticket.ID, application.TicketQuery{Scope: application.ScopeAll}, true)
	if err != nil {
		t.Fatalf("TicketView: unexpected error: %v", err)
	}
	return view.Timeline
}

func singleItem(t *testing.T, items []application.TimelineItem) application.TimelineItem {
	t.Helper()
	if len(items) != 1 {
		t.Fatalf("timeline entries = %d, want 1", len(items))
	}
	return items[0]
}

func TestTicketViewWorkflowAssignmentStructuredLabels(t *testing.T) {
	f := seedAssignmentFixture(t)
	to := strconv.FormatInt(f.user.ID, 10)
	deskID := f.desk.ID
	f.append(t, assignmentEvent("user", "", to, &deskID))

	item := singleItem(t, f.items(t))
	if !item.IsWorkflowAssignment {
		t.Fatalf("workflow_assignment must set the structured assignment branch, got %+v", item)
	}
	if item.AssignmentPerson != f.user.Name {
		t.Errorf("AssignmentPerson = %q, want %q", item.AssignmentPerson, f.user.Name)
	}
	if item.AssignmentDesk != f.desk.Name {
		t.Errorf("AssignmentDesk = %q, want %q", item.AssignmentDesk, f.desk.Name)
	}
	if item.Summary != "Assigned to" {
		t.Errorf("Summary = %q, want the exact prefix %q", item.Summary, "Assigned to")
	}
	// least_loaded auto-assignment stamps actor "workflow"; per the audit-log
	// spec the timeline omits actor text for it entirely instead of a label.
	if item.ActorLabel != "" {
		t.Errorf("ActorLabel = %q, want no actor text for the persisted workflow actor", item.ActorLabel)
	}
}

func TestTicketViewWorkflowAssignmentHumanClaimActorStaysPerson(t *testing.T) {
	f := seedAssignmentFixture(t)
	to := strconv.FormatInt(f.user.ID, 10)
	deskID := f.desk.ID
	ev := assignmentEvent("user", "", to, &deskID)
	ev.Actor = "Ada"
	f.append(t, ev)

	item := singleItem(t, f.items(t))
	if item.ActorLabel != "Ada" {
		t.Errorf("human claim ActorLabel = %q, want the person name Ada", item.ActorLabel)
	}
}

func TestTicketViewWorkflowAssignmentUnknownUserAndDesk(t *testing.T) {
	f := seedAssignmentFixture(t)
	missingUser, missingDesk := "999", int64(4242)
	var noDesk *int64

	// Deleted desk (ON DELETE SET NULL leaves DeskID nil) AND missing assignee.
	f.append(t, assignmentEvent("user", "", missingUser, noDesk))
	item := singleItem(t, f.items(t))
	if item.AssignmentPerson != "Unknown user" {
		t.Errorf("missing assignee must degrade to Unknown user, got %q", item.AssignmentPerson)
	}
	if item.AssignmentDesk != "Unknown desk" {
		t.Errorf("nil desk must degrade to Unknown desk, got %q", item.AssignmentDesk)
	}

	// A stale desk id that no longer resolves degrades the same way.
	f.append(t, assignmentEvent("user", "", strconv.FormatInt(f.user.ID, 10), &missingDesk))
	items := f.items(t)
	if len(items) != 2 {
		t.Fatalf("timeline entries = %d, want 2", len(items))
	}
	stale := items[0] // newest first
	if stale.AssignmentDesk != "Unknown desk" {
		t.Errorf("stale desk id must degrade to Unknown desk, got %q", stale.AssignmentDesk)
	}
	if stale.AssignmentPerson != f.user.Name {
		t.Errorf("AssignmentPerson = %q, want %q", stale.AssignmentPerson, f.user.Name)
	}
}

func TestTicketViewWorkflowAssignmentResolvesRenamedAndInactiveUser(t *testing.T) {
	f := seedAssignmentFixture(t)
	to := strconv.FormatInt(f.user.ID, 10)
	deskID := f.desk.ID
	f.append(t, assignmentEvent("user", "", to, &deskID))

	// Rename + deactivate AFTER the event: historical display resolves the
	// current stored label (user-management spec), never the raw id.
	renamed, err := f.users.GetByID(context.Background(), f.user.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	renamed.Name = "Beto Ruiz"
	renamed.Active = false
	if err := f.users.Update(context.Background(), renamed); err != nil {
		t.Fatalf("rename/deactivate: %v", err)
	}

	item := singleItem(t, f.items(t))
	if item.AssignmentPerson != "Beto Ruiz" {
		t.Errorf("renamed user must resolve to the current stored name, got %q", item.AssignmentPerson)
	}
	if item.AssignmentDesk != f.desk.Name {
		t.Errorf("AssignmentDesk = %q, want %q", item.AssignmentDesk, f.desk.Name)
	}
}

func TestTicketViewDetailSuppressionMatrix(t *testing.T) {
	f := seedAssignmentFixture(t)
	note := "secret answer content"
	reason := "handover to second-line"
	fieldState, fieldUser := "state", "user"
	fromEmpty := ""
	toSelf := strconv.FormatInt(f.user.ID, 10)
	cases := []struct {
		name         string
		ev           domain.AuditEvent
		wantSuppress bool
	}{
		{"legacy workflow_step suppresses note", domain.AuditEvent{Action: domain.ActionWorkflowStep, Note: &note}, true},
		{"manual task suppresses note", domain.AuditEvent{Action: domain.ActionWorkflowManualTask, Note: &note}, true},
		{"requester form suppresses note", domain.AuditEvent{Action: domain.ActionWorkflowRequesterForm, Note: &note}, true},
		{"assignee form suppresses note", domain.AuditEvent{Action: domain.ActionWorkflowAssigneeForm, Note: &note}, true},
		{"transition keeps reopen reason visible", domain.AuditEvent{Action: domain.ActionTransition, Field: &fieldState, FromValue: ptr("closed"), ToValue: ptr("in_progress"), Note: &note}, false},
		{"legacy update keeps reassignment reason visible", domain.AuditEvent{Action: domain.ActionUpdate, Field: &fieldUser, FromValue: &fromEmpty, ToValue: &toSelf, Reason: &reason}, false},
		{"workflow_assignment keeps its validated reason visible", assignmentEvent("user", fromEmpty, toSelf, nil), false},
	}
	for i := range cases {
		cases[i].ev.CreatedAt = f.clock.now.Add(time.Duration(i) * timeMinute)
		f.append(t, cases[i].ev)
	}

	items := f.items(t)
	if len(items) != len(cases) {
		t.Fatalf("timeline entries = %d, want %d", len(items), len(cases))
	}
	for i, tc := range cases {
		// newest first: reverse index
		got := items[len(items)-1-i].SuppressDetail
		if got != tc.wantSuppress {
			t.Errorf("%s: SuppressDetail = %v, want %v", tc.name, got, tc.wantSuppress)
		}
	}
}
