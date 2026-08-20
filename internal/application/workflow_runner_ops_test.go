package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func snapWith(st domain.State, cur int, def domain.WorkflowDefinition, requester, assignee *int64) application.WorkflowExecutionSnapshot {
	s := snap(st, cur, def)
	s.Ticket.RequesterUserID = requester
	s.Ticket.UserID = assignee
	return s
}

func formAnswerOf(t *testing.T, pl application.WorkflowMutationPlan) application.FormAnswerOperation {
	t.Helper()
	for _, op := range pl.Operations {
		if fa, ok := op.(application.FormAnswerOperation); ok {
			return fa
		}
	}
	t.Fatalf("no FormAnswerOperation in plan")
	return application.FormAnswerOperation{}
}

func wantForbidden(t *testing.T, err error) {
	t.Helper()
	var fe *domain.ForbiddenError
	if !errors.As(err, &fe) {
		t.Fatalf("want ForbiddenError got %v", err)
	}
}

func opKinds(pl application.WorkflowMutationPlan) []string {
	ks := make([]string, len(pl.Operations))
	for i, op := range pl.Operations {
		ks[i] = reflect.TypeOf(op).Name()
	}
	return ks
}

func wantOps(t *testing.T, pl application.WorkflowMutationPlan, want ...string) {
	t.Helper()
	if got := opKinds(pl); !reflect.DeepEqual(got, want) {
		t.Fatalf("op sequence %v, want %v", got, want)
	}
}

func assertAssignAudit(t *testing.T, a domain.AuditEvent, actor int64, from, to, reason string) {
	t.Helper()
	got := ""
	if a.Reason != nil {
		got = *a.Reason
	}
	if a.TicketID != 1 || a.Actor != actorName(actor) || a.ActorUserID == nil || *a.ActorUserID != actor || a.Action != domain.ActionUpdate ||
		a.Field == nil || *a.Field != "user" || a.FromValue == nil || *a.FromValue != from ||
		a.ToValue == nil || *a.ToValue != to || got != reason || !a.CreatedAt.Equal(fixedClock().Now()) {
		t.Fatalf("assignment audit %+v", a)
	}
}

func assertTransition(t *testing.T, tr application.TransitionOperation, from, to string) {
	t.Helper()
	// The audit is exactly the domain.Ticket.Transition event (action
	// transition, field state, actual from/to, ticket id, timestamp) with the
	// workflow actor stamp (audit-log spec: automatic workflow transition uses
	// actor "workflow" and a NULL user id).
	if tr.Audit.TicketID != 1 || tr.Audit.Action != domain.ActionTransition || tr.Audit.Actor != "workflow" ||
		tr.Audit.ActorUserID != nil || tr.Audit.Note != nil || tr.Audit.Field == nil || *tr.Audit.Field != "state" ||
		tr.Audit.FromValue == nil || *tr.Audit.FromValue != from || tr.Audit.ToValue == nil || *tr.Audit.ToValue != to ||
		!tr.Audit.CreatedAt.Equal(fixedClock().Now()) {
		t.Fatalf("transition op %+v", tr.Audit)
	}
}

func cmdFor(actor int64, raw application.RawPositionalValues) application.CompleteWorkflowCommand {
	return application.CompleteWorkflowCommand{TicketID: 1, ActorUserID: actor, ActorName: actorName(actor), ExpectedPosition: 1, RawAnswers: raw}
}

// actorName derives a stable human display name for a command actor id, so
// human audits can assert the exact actor string in lockstep with the id
// (audit-log spec: application stamps both Actor and ActorUserID).
func actorName(id int64) string { return fmt.Sprintf("human-%d", id) }

func cmdReason(actor int64, reason string) application.CompleteWorkflowCommand {
	c := cmdFor(actor, nil)
	c.Reason = reason
	return c
}

// TestWorkflowRunner_OrderedOperations proves the closed ordered operation
// contract: every human-supported step emits exactly its literal operation
// sequence with complete persistence facts (step index, identity, timestamp,
// typed answers, required audits).
func TestWorkflowRunner_OrderedOperations(t *testing.T) {
	r := application.NewWorkflowRunner(fixedClock())
	now := fixedClock().Now()
	nameFields := []domain.FormField{{Key: "name", Label: "Name", Kind: domain.FieldShortText, Required: true}, {Key: "agree", Label: "Agree", Kind: domain.FieldCheckbox, Required: true}}
	formRaw := application.RawPositionalValues{{Position: 0, Values: []string{"  api  "}}, {Position: 1, Values: []string{"on"}}}
	reassignErr := func(t *testing.T, err error) {
		t.Helper()
		var rerr *domain.ReassignReasonRequiredError
		if !errors.As(err, &rerr) {
			t.Fatalf("want ReassignReasonRequiredError got %v", err)
		}
	}
	cases := []struct {
		name    string
		s       application.WorkflowExecutionSnapshot
		cmd     application.CompleteWorkflowCommand
		kinds   []string
		wantErr bool
		errChk  func(*testing.T, error)
		verify  func(*testing.T, application.WorkflowMutationPlan)
	}{
		{
			name:  "form answer then workflow step",
			s:     snapWith(domain.StateNew, 0, wf(fm(domain.FormActorRequester, nameFields...)), ptr(int64(1)), nil),
			cmd:   cmdFor(1, formRaw),
			kinds: []string{"FormAnswerOperation", "WorkflowStepOperation"},
			verify: func(t *testing.T, pl application.WorkflowMutationPlan) {
				fa, ok := pl.Operations[0].(application.FormAnswerOperation)
				if !ok || fa.StepIndex != 0 || fa.SubmittedByUserID != 1 || !fa.SubmittedAt.Equal(now) {
					t.Fatalf("form facts %+v", fa)
				}
				var arr []any
				if err := json.Unmarshal(fa.AnswersJSON, &arr); err != nil || len(arr) != 2 || arr[0] != "api" || arr[1] != true {
					t.Fatalf("typed answers %v %v", arr, err)
				}
				ws := pl.Operations[1].(application.WorkflowStepOperation)
				if ws.StepIndex != 0 || ws.Audit.Actor != actorName(1) || ws.Audit.Action != domain.ActionWorkflowStep || ws.Audit.ActorUserID == nil ||
					*ws.Audit.ActorUserID != 1 || !ws.Audit.CreatedAt.Equal(now) {
					t.Fatalf("workflow_step audit %+v", ws.Audit)
				}
			},
		},
		{
			name:  "manual workflow step only",
			s:     snapWith(domain.StateInProgress, 0, wf(man()), nil, ptr(int64(9))),
			cmd:   cmdFor(9, nil),
			kinds: []string{"WorkflowStepOperation"},
			verify: func(t *testing.T, pl application.WorkflowMutationPlan) {
				ws := pl.Operations[0].(application.WorkflowStepOperation)
				if ws.StepIndex != 0 || ws.Audit.Actor != actorName(9) || ws.Audit.ActorUserID == nil || *ws.Audit.ActorUserID != 9 {
					t.Fatalf("manual step %+v", ws)
				}
			},
		},
		{
			name:  "claim new unassigned orders assignment transition workflow step",
			s:     snapWith(domain.StateNew, 0, wf(claim(42)), nil, nil),
			cmd:   cmdFor(7, nil),
			kinds: []string{"ClaimAssignmentOperation", "TransitionOperation", "WorkflowStepOperation"},
			verify: func(t *testing.T, pl application.WorkflowMutationPlan) {
				ca := pl.Operations[0].(application.ClaimAssignmentOperation)
				if ca.StepIndex != 0 || ca.DeskID != 42 || ca.AssigneeUserID != 7 || ca.Reason != "" {
					t.Fatalf("claim op %+v", ca)
				}
				assertAssignAudit(t, ca.AssignmentAudit, 7, "", "7", "")
				assertTransition(t, pl.Operations[1].(application.TransitionOperation), "new", "in_progress")
			},
		},
		{
			name:    "claim reassignment requires reason",
			s:       snapWith(domain.StateInProgress, 0, wf(claim(1)), nil, ptr(int64(5))),
			cmd:     cmdReason(7, "   "),
			wantErr: true,
			errChk:  reassignErr,
		},
		{
			name:  "claim reassignment propagates trimmed reason and audit",
			s:     snapWith(domain.StateInProgress, 0, wf(claim(9)), nil, ptr(int64(5))),
			cmd:   cmdReason(7, "  handoff  "),
			kinds: []string{"ClaimAssignmentOperation", "WorkflowStepOperation"},
			verify: func(t *testing.T, pl application.WorkflowMutationPlan) {
				ca := pl.Operations[0].(application.ClaimAssignmentOperation)
				if ca.Reason != "handoff" {
					t.Fatalf("claim reason %+v", ca)
				}
				assertAssignAudit(t, ca.AssignmentAudit, 7, "5", "7", "handoff")
			},
		},
		{
			name:  "same person claim workflow step only",
			s:     snapWith(domain.StateInProgress, 0, wf(claim(1)), nil, ptr(int64(7))),
			cmd:   cmdFor(7, nil),
			kinds: []string{"WorkflowStepOperation"},
		},
		{
			name:  "same person claim from new transitions without assignment",
			s:     snapWith(domain.StateNew, 0, wf(claim(1)), nil, ptr(int64(7))),
			cmd:   cmdFor(7, nil),
			kinds: []string{"TransitionOperation", "WorkflowStepOperation"},
			verify: func(t *testing.T, pl application.WorkflowMutationPlan) {
				if pl.NextTicketState != domain.StateInProgress {
					t.Fatalf("same-person claim on new must transition, got %s", pl.NextTicketState)
				}
				assertTransition(t, pl.Operations[0].(application.TransitionOperation), "new", "in_progress")
			},
		},
		{
			name:  "least loaded new is explicit intent plus transition",
			s:     snapWith(domain.StateNew, 0, wf(least(5)), nil, nil),
			cmd:   cmdFor(7, nil),
			kinds: []string{"LeastLoadedAssignmentOperation", "TransitionOperation"},
			verify: func(t *testing.T, pl application.WorkflowMutationPlan) {
				lo := pl.Operations[0].(application.LeastLoadedAssignmentOperation)
				if lo.StepIndex != 0 || lo.DeskID != 5 {
					t.Fatalf("least op %+v", lo)
				}
			},
		},
		{
			name:  "least loaded in progress is intent only",
			s:     snapWith(domain.StateInProgress, 0, wf(least(5)), nil, nil),
			cmd:   cmdFor(7, nil),
			kinds: []string{"LeastLoadedAssignmentOperation"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pl, err := r.PlanComplete(context.Background(), tc.s, tc.cmd)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error")
				}
				tc.errChk(t, err)
				return
			}
			if err != nil {
				t.Fatalf("unexpected %v", err)
			}
			wantOps(t, pl, tc.kinds...)
			if tc.verify != nil {
				tc.verify(t, pl)
			}
		})
	}
}

// TestWorkflowRunner_ActorAndClaim proves strict actor identity: forms and
// manual steps accept only their required human (admin/root have no bypass),
// claim always assigns the actor, and identity never comes from RawAnswers.
func TestWorkflowRunner_ActorAndClaim(t *testing.T) {
	r := application.NewWorkflowRunner(fixedClock())
	fields := []domain.FormField{{Key: "k", Label: "L", Kind: domain.FieldShortText, Required: true}}
	raw := application.RawPositionalValues{{Position: 0, Values: []string{"x"}}}
	reqForm := snapWith(domain.StateNew, 0, wf(fm(domain.FormActorRequester, fields...)), ptr(int64(1)), nil)
	reqFormAssigned := snapWith(domain.StateNew, 0, wf(fm(domain.FormActorRequester, fields...)), ptr(int64(1)), ptr(int64(2)))
	asgForm := snapWith(domain.StateInProgress, 0, wf(fm(domain.FormActorAssignee, fields...)), nil, ptr(int64(9)))
	manual := snapWith(domain.StateInProgress, 0, wf(man()), nil, ptr(int64(9)))
	manualFree := snapWith(domain.StateInProgress, 0, wf(man()), nil, nil)
	claimVerify := func(t *testing.T, pl application.WorkflowMutationPlan) {
		ca, ok := pl.Operations[0].(application.ClaimAssignmentOperation)
		if !ok || ca.AssigneeUserID != 7 {
			t.Fatalf("claim must assign the actor, got %+v", pl.Operations[0])
		}
	}
	rawVerify := func(t *testing.T, pl application.WorkflowMutationPlan) {
		fa := formAnswerOf(t, pl)
		if fa.SubmittedByUserID != 1 {
			t.Fatalf("identity from command only, got %d", fa.SubmittedByUserID)
		}
		var a []string
		if err := json.Unmarshal(fa.AnswersJSON, &a); err != nil || a[0] != "hacker" {
			t.Fatalf("value maps to pinned field %v %v", a, err)
		}
	}
	cases := []struct {
		name   string
		s      application.WorkflowExecutionSnapshot
		cmd    application.CompleteWorkflowCommand
		forbid bool
		verify func(*testing.T, application.WorkflowMutationPlan)
	}{
		{name: "requester form only requester", s: reqForm, cmd: cmdFor(1, raw)},
		{name: "admin cannot complete requester form", s: reqForm, cmd: cmdFor(2, raw), forbid: true},
		{name: "root cannot complete requester form", s: reqForm, cmd: cmdFor(3, raw), forbid: true},
		{name: "current assignee cannot complete requester form", s: reqFormAssigned, cmd: cmdFor(2, raw), forbid: true},
		{name: "assignee form only assignee", s: asgForm, cmd: cmdFor(9, raw)},
		{name: "non-assignee cannot complete assignee form", s: asgForm, cmd: cmdFor(2, raw), forbid: true},
		{name: "manual only current assignee", s: manual, cmd: cmdFor(9, nil)},
		{name: "admin cannot complete manual", s: manual, cmd: cmdFor(2, nil), forbid: true},
		{name: "manual unassigned rejected", s: manualFree, cmd: cmdFor(2, nil), forbid: true},
		{name: "claim always assigns actor", s: snapWith(domain.StateNew, 0, wf(claim(1)), nil, nil), cmd: cmdFor(7, application.RawPositionalValues{{Position: 0, Values: []string{"999"}}}), verify: claimVerify},
		{name: "raw answers cannot impersonate", s: reqForm, cmd: cmdFor(1, application.RawPositionalValues{{Position: 0, Values: []string{"hacker"}}}), verify: rawVerify},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pl, err := r.PlanComplete(context.Background(), tc.s, tc.cmd)
			if tc.forbid {
				wantForbidden(t, err)
				return
			}
			if err != nil {
				t.Fatalf("%s denied: %v", tc.name, err)
			}
			if tc.verify != nil {
				tc.verify(t, pl)
			}
		})
	}
}

// TestWorkflowRunner_SnapshotImmutability proves PlanComplete never mutates the
// snapshot: the full ticket (pointer identity AND pointee values, state,
// timestamps), the run (status, times, cursor), and the pinned workflow
// definition (deep copy) are unchanged afterwards. Pointer identities are
// captured separately because a shallow struct copy would alias the snapshot's
// pointers and could mask an in-place pointee mutation.
func TestWorkflowRunner_SnapshotImmutability(t *testing.T) {
	r := application.NewWorkflowRunner(fixedClock())
	def := domain.WorkflowDefinition{
		claim(1),
		fm(domain.FormActorRequester, domain.FormField{Key: "k", Label: "L", Kind: domain.FieldShortText, Required: true}),
		man(),
	}
	s := snapWith(domain.StateNew, 0, def, ptr(int64(1)), ptr(int64(1)))
	s.Run.StartedAt = time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	// Distinct non-nil pointee values so the immutability checks genuinely
	// exercise pointer dereference (also via the same-person claim from new,
	// which still exercises the transition path).
	s.Ticket.ResolvedAt = ptr(time.Date(2026, 8, 20, 11, 30, 0, 0, time.UTC))
	s.Ticket.ClosedAt = ptr(time.Date(2026, 8, 20, 12, 30, 0, 0, time.UTC))
	s.Run.CompletedAt = ptr(time.Date(2026, 8, 20, 8, 30, 0, 0, time.UTC))
	derefI := func(p *int64) int64 {
		if p == nil {
			return 0
		}
		return *p
	}
	derefT := func(p *time.Time) time.Time {
		if p == nil {
			return time.Time{}
		}
		return *p
	}
	reqPtr, reqVal := s.Ticket.RequesterUserID, derefI(s.Ticket.RequesterUserID)
	asgPtr, asgVal := s.Ticket.UserID, derefI(s.Ticket.UserID)
	resPtr, resVal := s.Ticket.ResolvedAt, derefT(s.Ticket.ResolvedAt)
	cloPtr, cloVal := s.Ticket.ClosedAt, derefT(s.Ticket.ClosedAt)
	tState, tCreated, tUpdated := s.Ticket.State, s.Ticket.CreatedAt, s.Ticket.UpdatedAt
	rStatus, rCursor, rStarted := s.Run.Status, s.Run.CurrentStepIndex, s.Run.StartedAt
	rCompPtr, rCompVal := s.Run.CompletedAt, derefT(s.Run.CompletedAt)
	deepW, err := deepCopyWorkflow(s.Workflow)
	if err != nil {
		t.Fatalf("deep copy: %v", err)
	}
	if _, err := r.PlanComplete(context.Background(), s, cmdFor(1, nil)); err != nil {
		t.Fatalf("unexpected %v", err)
	}
	if s.Ticket.RequesterUserID != reqPtr || derefI(s.Ticket.RequesterUserID) != reqVal ||
		s.Ticket.UserID != asgPtr || derefI(s.Ticket.UserID) != asgVal ||
		s.Ticket.ResolvedAt != resPtr || derefT(s.Ticket.ResolvedAt) != resVal ||
		s.Ticket.ClosedAt != cloPtr || derefT(s.Ticket.ClosedAt) != cloVal ||
		s.Ticket.State != tState || !s.Ticket.CreatedAt.Equal(tCreated) || !s.Ticket.UpdatedAt.Equal(tUpdated) {
		t.Fatalf("ticket mutated: %+v", *s.Ticket)
	}
	if s.Run.Status != rStatus || s.Run.CurrentStepIndex != rCursor || !s.Run.StartedAt.Equal(rStarted) ||
		s.Run.CompletedAt != rCompPtr || derefT(s.Run.CompletedAt) != rCompVal {
		t.Fatalf("run mutated: %+v", *s.Run)
	}
	if !reflect.DeepEqual(deepW, s.Workflow) {
		t.Fatalf("workflow mutated: %+v -> %+v", deepW, s.Workflow)
	}
}

// deepCopyWorkflow builds an independent copy of the pinned definition so an
// in-place mutation through a shared step config pointer is detectable.
func deepCopyWorkflow(w domain.WorkflowDefinition) (domain.WorkflowDefinition, error) {
	b, err := json.Marshal(w)
	if err != nil {
		return nil, err
	}
	var out domain.WorkflowDefinition
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// TestWorkflowRunner_NoFunctionsDeep recursively inspects every nested payload
// field of each plan — including the concrete operation structs and their audit
// events — and proves the contract carries only data: no function fields, no
// callbacks, no registries.
func TestWorkflowRunner_NoFunctionsDeep(t *testing.T) {
	r := application.NewWorkflowRunner(fixedClock())
	fields := []domain.FormField{{Key: "k", Label: "L", Kind: domain.FieldShortText, Required: true}}
	for _, tc := range []struct {
		name string
		s    application.WorkflowExecutionSnapshot
		cmd  application.CompleteWorkflowCommand
	}{
		{"form", snapWith(domain.StateNew, 0, wf(fm(domain.FormActorRequester, fields...)), ptr(int64(1)), nil), cmdFor(1, application.RawPositionalValues{{Position: 0, Values: []string{"x"}}})},
		{"claim", snapWith(domain.StateNew, 0, wf(claim(42)), nil, nil), cmdFor(7, nil)},
		{"least loaded", snapWith(domain.StateNew, 0, wf(least(5)), nil, nil), cmdFor(7, nil)},
		{"terminal resolve", snapWith(domain.StateNew, 0, wf(res()), nil, nil), cmdFor(1, nil)},
		{"terminal close", snapWith(domain.StateNew, 0, wf(clo()), nil, nil), cmdFor(1, nil)},
		{"auto chain", snapWith(domain.StateNew, 0, wf(fm(domain.FormActorRequester, fields...), least(5), man(), res()), ptr(int64(1)), nil), cmdFor(1, application.RawPositionalValues{{Position: 0, Values: []string{"x"}}})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pl, err := r.PlanComplete(context.Background(), tc.s, tc.cmd)
			if err != nil {
				t.Fatalf("unexpected %v", err)
			}
			if len(pl.Operations) == 0 {
				t.Fatalf("plan has no operations")
			}
			assertNoFunctions(t, reflect.ValueOf(pl))
		})
	}
}

func assertNoFunctions(t *testing.T, v reflect.Value) {
	t.Helper()
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Func:
		t.Fatalf("function value in plan: %s", v.Type())
	case reflect.Interface, reflect.Ptr:
		if !v.IsNil() {
			assertNoFunctions(t, v.Elem())
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			assertNoFunctions(t, v.Field(i))
		}
	case reflect.Slice, reflect.Array:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return // byte payload (typed answers JSON)
		}
		for i := 0; i < v.Len(); i++ {
			assertNoFunctions(t, v.Index(i))
		}
	}
}
