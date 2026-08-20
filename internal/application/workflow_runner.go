package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/giulianotesta7/tkt/internal/domain"
)

type WorkflowRunner struct{ clock domain.Clock }

func NewWorkflowRunner(c domain.Clock) *WorkflowRunner { return &WorkflowRunner{clock: c} }

func (r *WorkflowRunner) PlanComplete(_ context.Context, snap WorkflowExecutionSnapshot, cmd CompleteWorkflowCommand) (WorkflowMutationPlan, error) {
	if cmd.ExpectedPosition <= 0 || snap.Run == nil || snap.Ticket == nil || snap.Run.Status != "active" || len(snap.Workflow) == 0 {
		return WorkflowMutationPlan{}, domain.NewWorkflowPositionConflictError("workflow position conflict")
	}
	exp := cmd.ExpectedPosition - 1
	if exp != snap.Run.CurrentStepIndex || exp < 0 || exp >= len(snap.Workflow) {
		return WorkflowMutationPlan{}, domain.NewWorkflowPositionConflictError("workflow position conflict")
	}
	step := snap.Workflow[exp]
	if domain.IsClosed(snap.Ticket.State) {
		return WorkflowMutationPlan{}, &domain.ValidationError{Field: "state", Message: "workflow step cannot complete in current ticket state"}
	}
	ticket := *snap.Ticket
	now := r.clock.Now()
	var plan WorkflowMutationPlan
	plan.TicketID = ticket.ID
	plan.ExpectedCursor = snap.Run.CurrentStepIndex
	plan.ExpectedRunStatus = snap.Run.Status
	plan.TicketBeforeState = snap.Ticket.State
	nextCursor := snap.Run.CurrentStepIndex
	nextState := ticket.State
	var audits []domain.AuditEvent
	switch step.Type {
	case domain.StepAssignToDesk:
		if step.AssignToDesk == nil {
			return WorkflowMutationPlan{}, &domain.ValidationError{Field: "type", Message: "assign_to_desk requires config"}
		}
		switch step.AssignToDesk.Strategy {
		case domain.StrategyClaim:
			uid := cmd.ActorUserID
			plan.Assignment = &AssignmentRequest{DeskID: step.AssignToDesk.DeskID, Strategy: domain.StrategyClaim, AssigneeUserID: &uid}
		case domain.StrategyLeastLoaded:
			plan.Assignment = &AssignmentRequest{DeskID: step.AssignToDesk.DeskID, Strategy: domain.StrategyLeastLoaded}
		default:
			return WorkflowMutationPlan{}, &domain.ValidationError{Field: "strategy", Message: "unknown strategy"}
		}
		if ticket.State == domain.StateNew {
			if ev, err := ticket.Transition(domain.StateInProgress, "", now); err == nil {
				ev.Actor = "workflow"
				audits = append(audits, *ev)
				nextState = ticket.State
			}
		}
		nextCursor++
	case domain.StepForm:
		if step.Form == nil {
			return WorkflowMutationPlan{}, &domain.ValidationError{Field: "type", Message: "form requires config"}
		}
		b, err := decodePositionalAnswers(step.Form.Fields, cmd.RawAnswers)
		if err != nil {
			return WorkflowMutationPlan{}, err
		}
		plan.AnswersJSON = b
		idx := exp
		plan.AnswersStepIndex = &idx
		nextCursor++
	case domain.StepManualTask:
		if step.ManualTask == nil {
			return WorkflowMutationPlan{}, &domain.ValidationError{Field: "type", Message: "manual_task requires config"}
		}
		nextCursor++
	case domain.StepResolve, domain.StepClose:
		return WorkflowMutationPlan{}, &domain.ValidationError{Field: "type", Message: fmt.Sprintf("terminal step %q not supported in this slice", step.Type)}
	default:
		return WorkflowMutationPlan{}, &domain.ValidationError{Field: "type", Message: fmt.Sprintf("unknown step type %q", step.Type)}
	}
	status := "active"
	if nextCursor >= len(snap.Workflow) {
		status = "completed"
	}
	plan.NextCursor = nextCursor
	plan.NextRunStatus = status
	plan.NextTicketState = nextState
	plan.Audits = audits
	plan.Result = WorkflowExecutionResult{Ticket: &ticket, Run: &WorkflowRun{TicketID: ticket.ID, CurrentStepIndex: nextCursor, Status: status, StartedAt: snap.Run.StartedAt}}
	if status == "completed" {
		ct := now
		plan.Result.Run.CompletedAt = &ct
	}
	return plan, nil
}

func decodePositionalAnswers(fields []domain.FormField, raw RawPositionalValues) ([]byte, error) {
	seen := map[int]bool{}
	for _, rv := range raw {
		if rv.Position < 0 || rv.Position >= len(fields) {
			return nil, &domain.ValidationError{Field: "answers", Message: fmt.Sprintf("unknown position %d", rv.Position)}
		}
		if seen[rv.Position] {
			return nil, &domain.ValidationError{Field: "answers", Message: fmt.Sprintf("duplicate position %d", rv.Position)}
		}
		seen[rv.Position] = true
		if len(rv.Values) > 1 {
			return nil, &domain.ValidationError{Field: "answers", Message: fmt.Sprintf("ambiguous values for position %d", rv.Position)}
		}
	}
	m := map[int]string{}
	has := map[int]bool{}
	for _, rv := range raw {
		if len(rv.Values) == 1 {
			m[rv.Position] = rv.Values[0]
			has[rv.Position] = true
		}
	}
	out := make([]any, len(fields))
	for i, f := range fields {
		v := m[i]
		present := has[i]
		switch f.Kind {
		case domain.FieldCheckbox:
			b := false
			if present && strings.TrimSpace(v) != "" {
				s := strings.TrimSpace(v)
				if s == "on" || s == "true" {
					b = true
				} else {
					return nil, &domain.ValidationError{Field: f.Key, Message: fmt.Sprintf("Step %d: invalid checkbox value", i+1)}
				}
			}
			if f.Required && !b {
				return nil, &domain.ValidationError{Field: f.Key, Message: fmt.Sprintf("Step %d: %s is required", i+1, f.Key)}
			}
			out[i] = b
		case domain.FieldShortText, domain.FieldLongText:
			s := ""
			if present {
				s = strings.TrimSpace(v)
			}
			if f.Required && s == "" {
				return nil, &domain.ValidationError{Field: f.Key, Message: fmt.Sprintf("Step %d: %s is required", i+1, f.Key)}
			}
			out[i] = s
		case domain.FieldSingleSelect:
			s := ""
			if present {
				s = v
			}
			if s == "" {
				if f.Required {
					return nil, &domain.ValidationError{Field: f.Key, Message: fmt.Sprintf("Step %d: %s is required", i+1, f.Key)}
				}
				out[i] = ""
				continue
			}
			ok := false
			for _, o := range f.Options {
				if s == o {
					ok = true
					break
				}
			}
			if !ok {
				return nil, &domain.ValidationError{Field: f.Key, Message: fmt.Sprintf("Step %d: invalid option %q", i+1, s)}
			}
			out[i] = s
		default:
			return nil, &domain.ValidationError{Field: f.Key, Message: "unknown field kind"}
		}
	}
	return json.Marshal(out)
}
