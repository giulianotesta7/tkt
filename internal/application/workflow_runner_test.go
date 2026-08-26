package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func wf(s ...domain.WorkflowStep) domain.WorkflowDefinition { return s }
func claim(d int64) domain.WorkflowStep {
	return domain.WorkflowStep{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: d, Strategy: domain.StrategyClaim}}
}
func least(d int64) domain.WorkflowStep {
	return domain.WorkflowStep{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: d, Strategy: domain.StrategyLeastLoaded}}
}
func fm(a domain.FormActor, f ...domain.FormField) domain.WorkflowStep {
	return domain.WorkflowStep{Type: domain.StepForm, Form: &domain.FormStep{Actor: a, Fields: f}}
}
func man() domain.WorkflowStep {
	return domain.WorkflowStep{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "do"}}
}
func snap(st domain.State, cur int, def domain.WorkflowDefinition) application.WorkflowExecutionSnapshot {
	n := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	t := &domain.Ticket{ID: 1, State: st, CreatedAt: n, UpdatedAt: n, RequesterUserID: ptr(int64(1))}
	return application.WorkflowExecutionSnapshot{Ticket: t, Run: &application.WorkflowRun{TicketID: 1, CurrentStepIndex: cur, Status: "active", StartedAt: n}, Workflow: def}
}
func TestWorkflowRunner_PositionConflict(t *testing.T) {
	r := application.NewWorkflowRunner(fixedClock())
	def := wf(claim(1), fm(domain.FormActorRequester, domain.FormField{Key: "k", Label: "l", Kind: domain.FieldShortText}))
	base := snap(domain.StateNew, 0, def)
	for _, tc := range []struct {
		name string
		s    application.WorkflowExecutionSnapshot
		p    int
	}{{"zero", base, 0}, {"negative", base, -1}, {"stale", base, 2}, {"mismatch", snap(domain.StateNew, 1, def), 1}, {"empty", snap(domain.StateNew, 0, nil), 1}} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.PlanComplete(context.Background(), tc.s, application.CompleteWorkflowCommand{TicketID: 1, ActorUserID: 9, ExpectedPosition: tc.p})
			if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
				t.Fatalf("want conflict got %v", err)
			}
		})
	}
	t.Run("nil guards", func(t *testing.T) {
		s := snap(domain.StateNew, 0, def)
		s.Run = nil
		_, err := r.PlanComplete(context.Background(), s, application.CompleteWorkflowCommand{TicketID: 1, ActorUserID: 1, ExpectedPosition: 1})
		if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
			t.Fatalf("want conflict")
		}
		s = snap(domain.StateNew, 0, def)
		s.Ticket = nil
		_, err = r.PlanComplete(context.Background(), s, application.CompleteWorkflowCommand{TicketID: 1, ActorUserID: 1, ExpectedPosition: 1})
		if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
			t.Fatalf("want conflict")
		}
	})
}
func TestWorkflowRunner_LifecycleAndAssignment(t *testing.T) {
	r := application.NewWorkflowRunner(fixedClock())
	for _, st := range []domain.State{domain.StateResolved, domain.StateClosed, domain.StateCancelled} {
		t.Run("lifecycle "+string(st), func(t *testing.T) {
			_, err := r.PlanComplete(context.Background(), snap(st, 0, wf(claim(1))), application.CompleteWorkflowCommand{TicketID: 1, ActorUserID: 5, ExpectedPosition: 1})
			if err == nil || errors.Is(err, domain.ErrWorkflowPositionConflict) {
				t.Fatalf("want validation got %v", err)
			}
		})
	}
	t.Run("claim new transitions", func(t *testing.T) {
		pl, err := r.PlanComplete(context.Background(), snap(domain.StateNew, 0, wf(claim(42))), application.CompleteWorkflowCommand{TicketID: 1, ActorUserID: 7, ExpectedPosition: 1})
		if err != nil || len(pl.Operations) != 2 || pl.NextTicketState != domain.StateInProgress || pl.Result.Ticket.State != domain.StateInProgress {
			t.Fatalf("claim %+v %v", pl, err)
		}
		ca, ok := pl.Operations[0].(application.ClaimAssignmentOperation)
		if !ok || ca.DeskID != 42 || ca.AssigneeUserID != 7 {
			t.Fatalf("claim op %+v", pl.Operations[0])
		}
	})
	t.Run("claim in_progress no re-transition", func(t *testing.T) {
		pl, _ := r.PlanComplete(context.Background(), snap(domain.StateInProgress, 0, wf(claim(1))), application.CompleteWorkflowCommand{TicketID: 1, ActorUserID: 7, ExpectedPosition: 1})
		if pl.NextTicketState != domain.StateInProgress || len(pl.Operations) != 1 {
			t.Fatalf("no re-transition %+v", pl)
		}
		for _, op := range pl.Operations {
			if _, ok := op.(application.TransitionOperation); ok {
				t.Fatalf("unexpected transition op %+v", op)
			}
		}
	})
	t.Run("least_loaded no sql", func(t *testing.T) {
		pl, _ := r.PlanComplete(context.Background(), snap(domain.StateNew, 0, wf(least(5))), application.CompleteWorkflowCommand{TicketID: 1, ActorUserID: 7, ExpectedPosition: 1})
		if len(pl.Operations) != 2 {
			t.Fatalf("least ops %+v", pl.Operations)
		}
		lo, ok := pl.Operations[0].(application.LeastLoadedAssignmentOperation)
		if !ok || lo.DeskID != 5 {
			t.Fatalf("least op %+v", pl.Operations[0])
		}
	})
	t.Run("manual advances", func(t *testing.T) {
		s := snap(domain.StateNew, 0, wf(man()))
		s.Ticket.UserID = ptr(int64(1))
		pl, err := r.PlanComplete(context.Background(), s, application.CompleteWorkflowCommand{TicketID: 1, ActorUserID: 1, ExpectedPosition: 1})
		if err != nil || pl.NextCursor != 1 || len(pl.Operations) != 1 {
			t.Fatalf("manual %v %+v", err, pl.Operations)
		}
		if _, ok := pl.Operations[0].(application.WorkflowStepOperation); !ok {
			t.Fatalf("manual op %+v", pl.Operations[0])
		}
	})
	t.Run("terminal resolve from new completes", func(t *testing.T) {
		pl, err := r.PlanComplete(context.Background(), snap(domain.StateNew, 0, wf(domain.WorkflowStep{Type: domain.StepResolve})), application.CompleteWorkflowCommand{TicketID: 1, ActorUserID: 1, ExpectedPosition: 1})
		if err != nil {
			t.Fatalf("terminal resolve from new must plan: %v", err)
		}
		if len(pl.Operations) != 1 || pl.NextRunStatus != "completed" || pl.NextTicketState != domain.StateResolved {
			t.Fatalf("terminal resolve %+v", pl)
		}
	})
}
func TestWorkflowRunner_FormDecoding(t *testing.T) {
	r := application.NewWorkflowRunner(fixedClock())
	fields := []domain.FormField{{Key: "name", Label: "Name", Kind: domain.FieldShortText, Required: true}, {Key: "agree", Label: "Agree", Kind: domain.FieldCheckbox, Required: true}, {Key: "region", Label: "Region", Kind: domain.FieldSingleSelect, Required: false, Options: []string{"eu-west-1", "us-east-1"}}, {Key: "notes", Label: "Notes", Kind: domain.FieldLongText, Required: false}}
	def := wf(fm(domain.FormActorRequester, fields...))
	s0 := snap(domain.StateNew, 0, def)
	for _, tc := range []struct {
		name    string
		raw     application.RawPositionalValues
		wantErr bool
	}{{"checkbox absent required", application.RawPositionalValues{{Position: 0, Values: []string{"hi"}}}, false}, {"checkbox empty required", application.RawPositionalValues{{Position: 0, Values: []string{"a"}}, {Position: 1, Values: []string{""}}}, false}, {"checkbox on", application.RawPositionalValues{{Position: 0, Values: []string{"hi"}}, {Position: 1, Values: []string{"on"}}}, false}, {"checkbox true", application.RawPositionalValues{{Position: 0, Values: []string{"hi"}}, {Position: 1, Values: []string{"true"}}}, false}, {"checkbox invalid", application.RawPositionalValues{{Position: 0, Values: []string{"hi"}}, {Position: 1, Values: []string{"yes"}}}, true}, {"text trimmed", application.RawPositionalValues{{Position: 0, Values: []string{"  hello  "}}, {Position: 1, Values: []string{"on"}}}, false}, {"text blank required", application.RawPositionalValues{{Position: 0, Values: []string{"  "}}, {Position: 1, Values: []string{"on"}}}, true}, {"select optional empty", application.RawPositionalValues{{Position: 0, Values: []string{"x"}}, {Position: 1, Values: []string{"on"}}}, false}, {"select valid", application.RawPositionalValues{{Position: 0, Values: []string{"x"}}, {Position: 1, Values: []string{"on"}}, {Position: 2, Values: []string{"eu-west-1"}}}, false}, {"select invalid", application.RawPositionalValues{{Position: 0, Values: []string{"x"}}, {Position: 1, Values: []string{"on"}}, {Position: 2, Values: []string{"bad"}}}, true}, {"select padded rejected", application.RawPositionalValues{{Position: 0, Values: []string{"x"}}, {Position: 1, Values: []string{"on"}}, {Position: 2, Values: []string{" eu-west-1 "}}}, true}, {"unknown pos", application.RawPositionalValues{{Position: 0, Values: []string{"x"}}, {Position: 1, Values: []string{"on"}}, {Position: 5, Values: []string{"hi"}}}, true}, {"duplicate pos", application.RawPositionalValues{{Position: 0, Values: []string{"a"}}, {Position: 0, Values: []string{"b"}}}, true}, {"ambiguous", application.RawPositionalValues{{Position: 0, Values: []string{"a", "b"}}}, true}, {"extra beyond pinned", application.RawPositionalValues{{Position: 0, Values: []string{"x"}}, {Position: 1, Values: []string{"on"}}, {Position: 4, Values: []string{"hi"}}}, true}, {"typed array", application.RawPositionalValues{{Position: 0, Values: []string{"api-01"}}, {Position: 1, Values: []string{"true"}}, {Position: 2, Values: []string{"eu-west-1"}}, {Position: 3, Values: []string{""}}}, false}} {
		t.Run(tc.name, func(t *testing.T) {
			pl, err := r.PlanComplete(context.Background(), s0, application.CompleteWorkflowCommand{TicketID: 1, ActorUserID: 1, ExpectedPosition: 1, RawAnswers: tc.raw})
			if tc.wantErr && err == nil {
				t.Fatalf("want error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected %v", err)
			}
			if !tc.wantErr {
				fa := formAnswerOf(t, pl)
				var raw []json.RawMessage
				if err := json.Unmarshal(fa.AnswersJSON, &raw); err != nil || len(raw) != len(fields) {
					t.Fatalf("typed array %v", err)
				}
			}
		})
	}
	t.Run("trim and typed values", func(t *testing.T) {
		pl, _ := r.PlanComplete(context.Background(), s0, application.CompleteWorkflowCommand{TicketID: 1, ActorUserID: 1, ExpectedPosition: 1, RawAnswers: application.RawPositionalValues{{Position: 0, Values: []string{"  hello  "}}, {Position: 1, Values: []string{"on"}}}})
		fa := formAnswerOf(t, pl)
		var a []json.RawMessage
		_ = json.Unmarshal(fa.AnswersJSON, &a)
		var s string
		_ = json.Unmarshal(a[0], &s)
		if s != "hello" {
			t.Fatalf("trim %q", s)
		}
		pl2, _ := r.PlanComplete(context.Background(), s0, application.CompleteWorkflowCommand{TicketID: 1, ActorUserID: 1, ExpectedPosition: 1, RawAnswers: application.RawPositionalValues{{Position: 0, Values: []string{"api-01"}}, {Position: 1, Values: []string{"true"}}, {Position: 2, Values: []string{"eu-west-1"}}, {Position: 3, Values: []string{""}}}})
		fa2 := formAnswerOf(t, pl2)
		var arr []any
		_ = json.Unmarshal(fa2.AnswersJSON, &arr)
		if arr[0] != "api-01" || arr[1] != true {
			t.Fatalf("typed %v", arr)
		}
	})
	t.Run("checkbox absent stores false", func(t *testing.T) {
		pl, err := r.PlanComplete(context.Background(), s0, application.CompleteWorkflowCommand{TicketID: 1, ActorUserID: 1, ExpectedPosition: 1, RawAnswers: application.RawPositionalValues{{Position: 0, Values: []string{"x"}}}})
		if err != nil {
			t.Fatalf("%v", err)
		}
		var a []any
		if err := json.Unmarshal(formAnswerOf(t, pl).AnswersJSON, &a); err != nil || len(a) != 4 || a[1] != false {
			t.Fatalf("absent checkbox must store false, got %v (%v)", a, err)
		}
	})
	t.Run("checkbox on stores true", func(t *testing.T) {
		pl, err := r.PlanComplete(context.Background(), s0, application.CompleteWorkflowCommand{TicketID: 1, ActorUserID: 1, ExpectedPosition: 1, RawAnswers: application.RawPositionalValues{{Position: 0, Values: []string{"x"}}, {Position: 1, Values: []string{"on"}}}})
		if err != nil {
			t.Fatalf("%v", err)
		}
		var a []any
		if err := json.Unmarshal(formAnswerOf(t, pl).AnswersJSON, &a); err != nil || len(a) != 4 || a[1] != true {
			t.Fatalf("checked checkbox must store true, got %v (%v)", a, err)
		}
	})
	t.Run("required select empty", func(t *testing.T) {
		f := []domain.FormField{{Key: "k", Label: "L", Kind: domain.FieldSingleSelect, Required: true, Options: []string{"a", "b"}}}
		s := snap(domain.StateNew, 0, wf(fm(domain.FormActorRequester, f...)))
		if _, err := r.PlanComplete(context.Background(), s, application.CompleteWorkflowCommand{TicketID: 1, ActorUserID: 1, ExpectedPosition: 1}); err == nil {
			t.Fatalf("want required")
		}
	})
	t.Run("no impersonation", func(t *testing.T) {
		raw := application.RawPositionalValues{{Position: 0, Values: []string{"imp"}}}
		pl, err := r.PlanComplete(context.Background(), snap(domain.StateNew, 0, wf(fm(domain.FormActorRequester, domain.FormField{Key: "real", Label: "Real", Kind: domain.FieldShortText, Required: true}))), application.CompleteWorkflowCommand{TicketID: 1, ActorUserID: 1, ExpectedPosition: 1, RawAnswers: raw})
		if err != nil {
			t.Fatalf("%v", err)
		}
		fa := formAnswerOf(t, pl)
		var a []string
		_ = json.Unmarshal(fa.AnswersJSON, &a)
		if a[0] != "imp" {
			t.Fatalf("pinned")
		}
	})
}
