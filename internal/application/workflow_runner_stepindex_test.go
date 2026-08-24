package application_test

import (
	"context"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// PR10 task 10.1 — the runner seals the zero-based pinned step index onto every
// SEMANTIC audit it plans (requester/assignee form, manual task, claim
// assignment). Transition audits keep a NULL index: they are lifecycle facts,
// not step completions.
func TestWorkflowRunner_StepIndexSealedOnSemanticAudits(t *testing.T) {
	assignee := int64(9)
	ticket := domain.Ticket{
		ID: 1, Title: "T", State: domain.StateInProgress, UserID: &assignee,
		RequesterUserID: &assignee, WorkflowVersionID: ptr(int64(5)),
	}
	def := domain.WorkflowDefinition{
		{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{{Key: "host", Label: "Host", Kind: domain.FieldShortText}}}},
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "do it"}},
	}
	now := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: now}
	runner := application.NewWorkflowRunner(clock)
	snap := application.WorkflowExecutionSnapshot{
		Ticket:   &ticket,
		Run:      &application.WorkflowRun{TicketID: ticket.ID, CurrentStepIndex: 0, Status: "active", StartedAt: now},
		Workflow: def,
	}

	plan, err := runner.PlanComplete(context.Background(), snap, application.CompleteWorkflowCommand{
		TicketID: 1, ActorUserID: assignee, ActorName: "Requester", ExpectedPosition: 1,
		RawAnswers: application.RawPositionalValues{{Position: 0, Values: []string{"api-01"}}},
	})
	if err != nil {
		t.Fatalf("plan requester form: %v", err)
	}
	var formAudit *domain.AuditEvent
	for _, op := range plan.Operations {
		if ws, ok := op.(application.WorkflowStepOperation); ok && ws.Audit.Action == domain.ActionWorkflowRequesterForm {
			formAudit = &ws.Audit
		}
	}
	if formAudit == nil || formAudit.StepIndex == nil || *formAudit.StepIndex != 0 {
		t.Fatalf("requester form audit StepIndex = %v, want sealed 0", formAudit)
	}

	snap.Run.CurrentStepIndex = 1
	plan, err = runner.PlanComplete(context.Background(), snap, application.CompleteWorkflowCommand{
		TicketID: 1, ActorUserID: assignee, ActorName: "Assignee", ExpectedPosition: 2,
	})
	if err != nil {
		t.Fatalf("plan manual: %v", err)
	}
	var manualAudit *domain.AuditEvent
	for _, op := range plan.Operations {
		if ws, ok := op.(application.WorkflowStepOperation); ok && ws.Audit.Action == domain.ActionWorkflowManualTask {
			manualAudit = &ws.Audit
		}
	}
	if manualAudit == nil || manualAudit.StepIndex == nil || *manualAudit.StepIndex != 1 {
		t.Fatalf("manual task audit StepIndex = %v, want sealed 1", manualAudit)
	}

	// A claim assignment seals its own step index; its planned new->in_progress
	// transition keeps NULL (state transitions are not step completions).
	claimTicket := ticket
	claimTicket.ID = 2
	claimTicket.State = domain.StateNew
	claimTicket.UserID = nil
	desk := int64(42)
	claimDef := domain.WorkflowDefinition{{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: desk, Strategy: domain.StrategyClaim}}}
	claimSnap := application.WorkflowExecutionSnapshot{
		Ticket:   &claimTicket,
		Run:      &application.WorkflowRun{TicketID: 2, CurrentStepIndex: 0, Status: "active", StartedAt: now},
		Workflow: claimDef,
	}
	plan, err = runner.PlanComplete(context.Background(), claimSnap, application.CompleteWorkflowCommand{
		TicketID: 2, ActorUserID: assignee, ActorName: "Agent", ExpectedPosition: 1,
	})
	if err != nil {
		t.Fatalf("plan claim: %v", err)
	}
	var claimIdx, transitionIdx *int
	for _, op := range plan.Operations {
		switch v := op.(type) {
		case application.ClaimAssignmentOperation:
			if v.AssignmentAudit.StepIndex != nil {
				idx := *v.AssignmentAudit.StepIndex
				claimIdx = &idx
			}
		case application.TransitionOperation:
			transitionIdx = v.Audit.StepIndex
		}
	}
	if claimIdx == nil || *claimIdx != 0 {
		t.Fatalf("claim assignment StepIndex = %v, want sealed 0", claimIdx)
	}
	if transitionIdx != nil {
		t.Fatalf("assignment-triggered transition must keep StepIndex NULL, got %d", *transitionIdx)
	}
}
