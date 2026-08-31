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

type terminalClock struct{ now time.Time }

func (c terminalClock) Now() time.Time { return c.now }

func terminalDefinition(kind domain.StepType) domain.WorkflowDefinition {
	return domain.WorkflowDefinition{{Type: kind}}
}

func terminalTicket(t *testing.T, s *Store, state domain.State, versionID, requester int64) domain.Ticket {
	t.Helper()
	now := testClock
	ticket := domain.Ticket{
		Number:            41,
		Title:             "terminal",
		RequesterName:     "Requester",
		RequesterEmail:    "requester@example.test",
		RequesterUserID:   &requester,
		CategoryID:        1,
		Priority:          domain.PriorityMedium,
		State:             state,
		CreatedAt:         now,
		UpdatedAt:         now,
		WorkflowVersionID: &versionID,
	}
	if state == domain.StateResolved || state == domain.StateClosed {
		ticket.ResolvedAt = &now
	}
	if state == domain.StateClosed {
		ticket.ClosedAt = &now
	}
	return seedPinnedTicket(t, s, ticket)
}

func TestWorkflowUoW_TerminalPersistedMatrix(t *testing.T) {
	cases := []struct {
		name       string
		step       domain.StepType
		state      domain.State
		wantState  domain.State
		wantAudits []string
		wantErr    bool
	}{
		{"resolve new", domain.StepResolve, domain.StateNew, domain.StateResolved, []string{"transition|new|resolved"}, false},
		{"resolve in progress", domain.StepResolve, domain.StateInProgress, domain.StateResolved, []string{"transition|in_progress|resolved"}, false},
		{"resolve resolved no-op", domain.StepResolve, domain.StateResolved, domain.StateResolved, nil, false},
		{"resolve closed no-op", domain.StepResolve, domain.StateClosed, domain.StateClosed, nil, false},
		{"resolve cancelled rejects", domain.StepResolve, domain.StateCancelled, domain.StateCancelled, nil, true},
		{"close new", domain.StepClose, domain.StateNew, domain.StateClosed, []string{"transition|new|resolved", "transition|resolved|closed"}, false},
		{"close in progress", domain.StepClose, domain.StateInProgress, domain.StateClosed, []string{"transition|in_progress|resolved", "transition|resolved|closed"}, false},
		{"close resolved", domain.StepClose, domain.StateResolved, domain.StateClosed, []string{"transition|resolved|closed"}, false},
		{"close closed no-op", domain.StepClose, domain.StateClosed, domain.StateClosed, nil, false},
		{"close cancelled rejects", domain.StepClose, domain.StateCancelled, domain.StateCancelled, nil, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestDB(t)
			cat := seedCategory(t, s, "terminal")
			req := seedUser(t, s, "Requester", "requester@example.test", true)
			def := terminalDefinition(tc.step)
			versionID := seedPublished(t, s, cat, def)
			ticket := terminalTicket(t, s, tc.state, versionID, req)
			ticket.CategoryID = cat
			if _, err := s.db.Exec(`UPDATE tickets SET category_id=? WHERE id=?`, cat, ticket.ID); err != nil {
				t.Fatalf("set category: %v", err)
			}
			seedRun(t, s, ticket.ID, 0, "active", testClock)

			runner := application.NewWorkflowRunner(terminalClock{now: testClock})
			snapshot := application.WorkflowExecutionSnapshot{Ticket: &ticket, Run: &application.WorkflowRun{TicketID: ticket.ID, Status: "active", StartedAt: testClock}, Workflow: def}
			plan, err := runner.PlanComplete(context.Background(), snapshot, application.CompleteWorkflowCommand{TicketID: ticket.ID, ActorUserID: req, ExpectedPosition: 1})
			if tc.wantErr {
				if err == nil {
					t.Fatal("cancelled terminal plan must reject")
				}
				return
			}
			if err != nil {
				t.Fatalf("plan terminal: %v", err)
			}
			result, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan)
			if err != nil {
				t.Fatalf("apply terminal: %v", err)
			}
			if result.Ticket == nil || result.Ticket.State != tc.wantState || result.Ticket.WorkflowVersionID == nil || *result.Ticket.WorkflowVersionID != versionID {
				t.Fatalf("refreshed ticket = %+v, want state=%s pinned=%d", result.Ticket, tc.wantState, versionID)
			}
			if !result.Ticket.CreatedAt.Equal(testClock) || !result.Ticket.UpdatedAt.Equal(testClock) {
				t.Fatalf("refreshed ticket timestamps = created %s updated %s, want %s", result.Ticket.CreatedAt, result.Ticket.UpdatedAt, testClock)
			}
			if result.Run == nil || result.Run.CurrentStepIndex != 1 || result.Run.Status != "completed" || result.Run.CompletedAt == nil || !result.Run.CompletedAt.Equal(testClock) {
				t.Fatalf("refreshed run = %+v", result.Run)
			}
			if got := auditActionOrder(t, s, ticket.ID); strings.Join(got, ";") != strings.Join(tc.wantAudits, ";") {
				t.Fatalf("audits = %v, want %v", got, tc.wantAudits)
			}
			var actor string
			var actorID *int64
			var note *string
			var closureVia *string
			if len(tc.wantAudits) > 0 {
				if err := s.db.QueryRow(`SELECT actor, actor_user_id, note, closure_via FROM audit_events WHERE ticket_id=? ORDER BY id LIMIT 1`, ticket.ID).Scan(&actor, &actorID, &note, &closureVia); err != nil {
					t.Fatalf("read terminal audit: %v", err)
				}
				if actor != "workflow" || actorID != nil || note != nil {
					t.Fatalf("terminal audit = actor %q actorID %v note %v", actor, actorID, note)
				}
				// Workflow-terminal closures are attributed by the workflow actor
				// convention ONLY — closure_via stays NULL (issue #55, audit-log
				// delta: transition audit events keep the existing workflow actor
				// convention).
				if closureVia != nil {
					t.Fatalf("terminal audit closure_via = %q, want NULL (workflow actor convention)", *closureVia)
				}
			}
		})
	}
}

// TestWorkflowUoW_RejectsClosureViaStampedTransitionAudit pins the additive
// attribution guard (issue #55, design D1.3): a plan whose workflow transition
// audit carries a non-nil ClosureVia is rejected with a typed
// ErrWorkflowPositionConflict before ANY write — closure_via stamping belongs
// exclusively to the manual confirmation/agent paths, never to workflow plans.
func TestWorkflowUoW_RejectsClosureViaStampedTransitionAudit(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "via-stamped")
	req := seedUser(t, s, "Requester", "requester@example.test", true)
	def := terminalDefinition(domain.StepClose)
	versionID := seedPublished(t, s, cat, def)
	ticket := terminalTicket(t, s, domain.StateResolved, versionID, req)
	if _, err := s.db.Exec(`UPDATE tickets SET category_id=? WHERE id=?`, cat, ticket.ID); err != nil {
		t.Fatalf("set category: %v", err)
	}
	seedRun(t, s, ticket.ID, 0, "active", testClock)

	runner := application.NewWorkflowRunner(terminalClock{now: testClock})
	snapshot := application.WorkflowExecutionSnapshot{Ticket: &ticket, Run: &application.WorkflowRun{TicketID: ticket.ID, Status: "active", StartedAt: testClock}, Workflow: def}
	plan, err := runner.PlanComplete(context.Background(), snapshot, application.CompleteWorkflowCommand{TicketID: ticket.ID, ActorUserID: req, ExpectedPosition: 1})
	if err != nil {
		t.Fatalf("plan close: %v", err)
	}

	// Stamp closure_via onto the workflow's closure transition audit — exactly
	// the accident the validator must refuse.
	stamped := false
	for i, op := range plan.Operations {
		tr, ok := op.(application.TransitionOperation)
		if !ok || tr.Audit.ToValue == nil || *tr.Audit.ToValue != string(domain.StateClosed) {
			continue
		}
		via := domain.ClosureViaRequesterConfirmation
		tr.Audit.ClosureVia = &via
		plan.Operations[i] = tr
		stamped = true
	}
	if !stamped {
		t.Fatal("close plan must contain a closure transition operation to stamp")
	}

	_, err = newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan)
	var conflict *domain.WorkflowPositionConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("apply with closure_via-stamped workflow audit = %v, want typed workflow position conflict", err)
	}
	assertApplyNoWrites(t, s, ticket)
}

func TestWorkflowUoW_FormThenTerminalRollsBackAndRetriesOnce(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "responses")
	req := seedUser(t, s, "Requester", "requester@example.test", true)
	def := domain.WorkflowDefinition{
		{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{{Key: "secret", Label: "Secret", Kind: domain.FieldShortText, Required: true}}}},
		{Type: domain.StepClose},
	}
	versionID := seedPublished(t, s, cat, def)
	ticket := terminalTicket(t, s, domain.StateNew, versionID, req)
	if _, err := s.db.Exec(`UPDATE tickets SET category_id=? WHERE id=?`, cat, ticket.ID); err != nil {
		t.Fatalf("set category: %v", err)
	}
	seedRun(t, s, ticket.ID, 0, "active", testClock)

	runner := application.NewWorkflowRunner(terminalClock{now: testClock})
	plan, err := runner.PlanComplete(context.Background(), application.WorkflowExecutionSnapshot{Ticket: &ticket, Run: &application.WorkflowRun{TicketID: ticket.ID, Status: "active", StartedAt: testClock}, Workflow: def}, application.CompleteWorkflowCommand{TicketID: ticket.ID, ActorUserID: req, ActorName: "Requester", ExpectedPosition: 1, RawAnswers: application.RawPositionalValues{{Position: 0, Values: []string{"private answer"}}}})
	if err != nil {
		t.Fatalf("plan form then close: %v", err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_terminal_audit BEFORE INSERT ON audit_events WHEN NEW.action = 'transition' BEGIN SELECT RAISE(ABORT, 'injected terminal audit failure'); END`); err != nil {
		t.Fatalf("install failure trigger: %v", err)
	}
	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan); err == nil {
		t.Fatal("injected terminal audit failure must roll back")
	}
	assertApplyNoWrites(t, s, ticket)
	if _, err := s.db.Exec(`DROP TRIGGER reject_terminal_audit`); err != nil {
		t.Fatalf("remove failure trigger: %v", err)
	}
	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan); err != nil {
		t.Fatalf("retry after rollback: %v", err)
	}
	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan); !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("completed plan retry = %v, want typed conflict", err)
	}
	var answer string
	if err := s.db.QueryRow(`SELECT answers_json FROM ticket_form_answers WHERE ticket_id=?`, ticket.ID).Scan(&answer); err != nil || answer != `["private answer"]` {
		t.Fatalf("typed answer = %q err=%v", answer, err)
	}
	var summaries int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE ticket_id=? AND (COALESCE(note, '') LIKE '%private answer%' OR COALESCE(from_value, '') LIKE '%private answer%' OR COALESCE(to_value, '') LIKE '%private answer%')`, ticket.ID).Scan(&summaries); err != nil || summaries != 0 {
		t.Fatalf("answer leaked into audit summaries: count=%d err=%v", summaries, err)
	}
}

func TestWorkflowRunner_ManualTaskRejectsClosedTickets(t *testing.T) {
	for _, state := range []domain.State{domain.StateResolved, domain.StateClosed} {
		t.Run(string(state), func(t *testing.T) {
			now := testClock
			assignee := int64(7)
			snapshot := application.WorkflowExecutionSnapshot{
				Ticket:   &domain.Ticket{ID: 1, State: state, UserID: &assignee, CreatedAt: now, UpdatedAt: now},
				Run:      &application.WorkflowRun{TicketID: 1, Status: "active", StartedAt: now},
				Workflow: domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "do it"}}},
			}
			plan, err := application.NewWorkflowRunner(terminalClock{now: now}).PlanComplete(context.Background(), snapshot, application.CompleteWorkflowCommand{TicketID: 1, ActorUserID: assignee, ExpectedPosition: 1})
			var validation *domain.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("manual completion on %s = %v, want existing validation rejection", state, err)
			}
			if len(plan.Operations) != 0 {
				t.Fatalf("manual completion on %s produced %d operations, want no-write plan", state, len(plan.Operations))
			}
		})
	}
}

func TestTicketReadsAndWorkflowResultPreserveWorkflowVersionID(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "pins")
	req := seedUser(t, s, "Requester", "requester@example.test", true)
	def := terminalDefinition(domain.StepResolve)
	versionID := seedPublished(t, s, cat, def)
	pinned := terminalTicket(t, s, domain.StateNew, versionID, req)
	if _, err := s.db.Exec(`UPDATE tickets SET category_id=? WHERE id=?`, cat, pinned.ID); err != nil {
		t.Fatalf("set category: %v", err)
	}
	legacy := seedTicket(t, s, domain.Ticket{Number: 42, Title: "legacy", CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: testClock, UpdatedAt: testClock})

	check := func(name string, tickets []domain.Ticket) {
		t.Helper()
		for _, ticket := range tickets {
			if ticket.ID == pinned.ID && (ticket.WorkflowVersionID == nil || *ticket.WorkflowVersionID != versionID) {
				t.Fatalf("%s lost pinned version: %+v", name, ticket.WorkflowVersionID)
			}
			if ticket.ID == legacy.ID && ticket.WorkflowVersionID != nil {
				t.Fatalf("%s changed legacy NULL pin: %v", name, *ticket.WorkflowVersionID)
			}
		}
	}
	ctx := context.Background()
	got, err := s.TicketStore().GetByID(ctx, pinned.ID, application.TicketQuery{Scope: application.ScopeAll})
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	check("GetByID", []domain.Ticket{*got})
	list, err := s.TicketStore().List(ctx, application.TicketQuery{Scope: application.ScopeAll}, application.Page{Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	check("List", list)
	found, err := s.SearchStore().Search(ctx, application.TicketQuery{Scope: application.ScopeAll}, application.Page{Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	check("Search", found)
}
