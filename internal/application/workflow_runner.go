package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

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
	actor := cmd.ActorUserID
	actorName := cmd.ActorName
	var ops []WorkflowOperation
	nextState := ticket.State
	stepAudit := func() {
		ops = append(ops, WorkflowStepOperation{StepIndex: exp, Audit: domain.AuditEvent{TicketID: ticket.ID, Actor: actorName, ActorUserID: &actor, Action: domain.ActionWorkflowStep, CreatedAt: now}})
	}
	transitionNew := func() {
		if nextState == domain.StateNew {
			if ev, err := ticket.Transition(domain.StateInProgress, "", now); err == nil {
				ev.Actor = "workflow"
				ops = append(ops, TransitionOperation{StepIndex: exp, Audit: *ev})
				nextState = ticket.State
			}
		}
	}
	switch step.Type {
	case domain.StepAssignToDesk:
		if step.AssignToDesk == nil {
			return WorkflowMutationPlan{}, &domain.ValidationError{Field: "type", Message: "assign_to_desk requires config"}
		}
		switch step.AssignToDesk.Strategy {
		case domain.StrategyClaim:
			claim, err := newClaimOperation(ticket, exp, step.AssignToDesk.DeskID, actor, actorName, cmd.Reason, now)
			if err != nil {
				return WorkflowMutationPlan{}, err
			}
			if claim != nil {
				ops = append(ops, *claim)
			}
			transitionNew()
			stepAudit()
		case domain.StrategyLeastLoaded:
			ops = append(ops, LeastLoadedAssignmentOperation{StepIndex: exp, DeskID: step.AssignToDesk.DeskID})
			transitionNew()
		default:
			return WorkflowMutationPlan{}, &domain.ValidationError{Field: "strategy", Message: "unknown strategy"}
		}
	case domain.StepForm:
		if step.Form == nil {
			return WorkflowMutationPlan{}, &domain.ValidationError{Field: "type", Message: "form requires config"}
		}
		if err := requireFormActor(ticket, step.Form.Actor, actor); err != nil {
			return WorkflowMutationPlan{}, err
		}
		b, err := decodePositionalAnswers(step.Form.Fields, cmd.RawAnswers)
		if err != nil {
			return WorkflowMutationPlan{}, err
		}
		ops = append(ops, FormAnswerOperation{StepIndex: exp, AnswersJSON: b, SubmittedByUserID: actor, SubmittedAt: now})
		stepAudit()
	case domain.StepManualTask:
		if step.ManualTask == nil {
			return WorkflowMutationPlan{}, &domain.ValidationError{Field: "type", Message: "manual_task requires config"}
		}
		if ticket.UserID == nil || *ticket.UserID != actor {
			return WorkflowMutationPlan{}, domain.NewForbiddenError("manual_task requires the current assignee")
		}
		stepAudit()
	case domain.StepResolve, domain.StepClose:
		return WorkflowMutationPlan{}, &domain.ValidationError{Field: "type", Message: fmt.Sprintf("terminal step %q not supported in this slice", step.Type)}
	default:
		return WorkflowMutationPlan{}, &domain.ValidationError{Field: "type", Message: fmt.Sprintf("unknown step type %q", step.Type)}
	}
	nextCursor := snap.Run.CurrentStepIndex + 1
	status := "active"
	if nextCursor >= len(snap.Workflow) {
		status = "completed"
	}
	plan := WorkflowMutationPlan{
		TicketID:          ticket.ID,
		ExpectedCursor:    snap.Run.CurrentStepIndex,
		ExpectedRunStatus: snap.Run.Status,
		TicketBeforeState: snap.Ticket.State,
		Operations:        ops,
		NextCursor:        nextCursor,
		NextRunStatus:     status,
		NextTicketState:   nextState,
		Result: WorkflowExecutionResult{Ticket: &ticket, Run: &WorkflowRun{
			TicketID: ticket.ID, CurrentStepIndex: nextCursor, Status: status, StartedAt: snap.Run.StartedAt,
		}},
	}
	if status == "completed" {
		ct := now
		plan.Result.Run.CompletedAt = &ct
	}
	return plan, nil
}

// requireFormActor enforces strict form actor identity: requester forms accept
// only the requester, assignee forms only the current assignee, no role bypass.
func requireFormActor(t domain.Ticket, a domain.FormActor, actor int64) error {
	switch a {
	case domain.FormActorRequester:
		if t.RequesterUserID == nil || *t.RequesterUserID != actor {
			return domain.NewForbiddenError("form requires the ticket requester")
		}
	case domain.FormActorAssignee:
		if t.UserID == nil || *t.UserID != actor {
			return domain.NewForbiddenError("form requires the current assignee")
		}
	default:
		return &domain.ValidationError{Field: "actor", Message: "unknown form actor"}
	}
	return nil
}

// newClaimOperation builds the claim assignment operation for the human
// claimant. A same-person claim returns (nil, nil): no assignment operation and
// no audit. An assignment that changes the person always carries the assignment
// audit (human actor, field/from/to, timestamp); a reassignment additionally
// requires a non-blank trimmed reason, mirroring TicketService.Assign.
func newClaimOperation(t domain.Ticket, stepIndex int, deskID int64, actor int64, actorName string, reason string, now time.Time) (*ClaimAssignmentOperation, error) {
	if t.UserID != nil && *t.UserID == actor {
		return nil, nil
	}
	from := ""
	var trimmed string
	if t.UserID != nil {
		from = strconv.FormatInt(*t.UserID, 10)
		trimmed = strings.TrimSpace(reason)
		if trimmed == "" {
			return nil, domain.NewReassignReasonRequiredError()
		}
	}
	to := strconv.FormatInt(actor, 10)
	field := "user"
	audit := domain.AuditEvent{
		TicketID: t.ID, Actor: actorName, ActorUserID: &actor, Action: domain.ActionUpdate, Field: &field,
		FromValue: &from, ToValue: &to, CreatedAt: now,
	}
	if trimmed != "" {
		audit.Reason = &trimmed
	}
	return &ClaimAssignmentOperation{StepIndex: stepIndex, DeskID: deskID, AssigneeUserID: actor, Reason: trimmed, AssignmentAudit: audit}, nil
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
