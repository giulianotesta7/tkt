package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// res and clo build the two automatic terminal steps. Terminal steps are final
// and carry no config (domain validation enforces final-only and at-most-one).
func res() domain.WorkflowStep { return domain.WorkflowStep{Type: domain.StepResolve} }
func clo() domain.WorkflowStep { return domain.WorkflowStep{Type: domain.StepClose} }

// stampedSnap returns a snapshot whose ticket carries the lifecycle timestamps a
// persisted ticket would have in that state: resolved and closed tickets stamp
// ResolvedAt, a closed ticket additionally stamps ClosedAt (set only by
// Ticket.Transition — ticket-management spec).
func stampedSnap(st domain.State, def domain.WorkflowDefinition) application.WorkflowExecutionSnapshot {
	s := snap(st, 0, def)
	at := s.Run.StartedAt
	switch st {
	case domain.StateResolved:
		s.Ticket.ResolvedAt = &at
	case domain.StateClosed:
		s.Ticket.ResolvedAt = &at
		s.Ticket.ClosedAt = &at
	}
	return s
}

func wantValidation(t *testing.T, err error) {
	t.Helper()
	var ve *domain.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want *ValidationError got %v", err)
	}
}

// completedRun asserts the run completed facts: status completed, cursor at the
// end of the definition, and the exact completion timestamp.
func completedRun(t *testing.T, pl application.WorkflowMutationPlan, wantCursor int, wantEnd time.Time) {
	t.Helper()
	if pl.NextCursor != wantCursor || pl.NextRunStatus != "completed" {
		t.Fatalf("run completion cursor/status = %d/%s, want %d/completed", pl.NextCursor, pl.NextRunStatus, wantCursor)
	}
	if pl.Result.Run.Status != "completed" || pl.Result.Run.CurrentStepIndex != wantCursor {
		t.Fatalf("result run %+v", pl.Result.Run)
	}
	if pl.Result.Run.CompletedAt == nil || !pl.Result.Run.CompletedAt.Equal(wantEnd) {
		t.Fatalf("completed_at = %v, want %v", pl.Result.Run.CompletedAt, wantEnd)
	}
}

// TestWorkflowRunner_TerminalMatrix proves the full resolve/close state matrix
// through domain.Ticket.Transition on an in-memory copy: transition facts,
// workflow audit stamping (actor "workflow", NULL user id, exact from/to/time/
// order), no-op completion for already lifecycle-closed states, cancelled
// rejection with no writes, and completed-run results.
func TestWorkflowRunner_TerminalMatrix(t *testing.T) {
	r := application.NewWorkflowRunner(fixedClock())
	now := fixedClock().Now()
	const last = 1 // every matrix workflow has exactly one (final) step
	cases := []struct {
		name    string
		s       application.WorkflowExecutionSnapshot
		cmd     application.CompleteWorkflowCommand
		kinds   []string
		wantErr bool
		verify  func(*testing.T, application.WorkflowMutationPlan)
	}{
		{
			name:  "resolve from new transitions and completes",
			s:     stampedSnap(domain.StateNew, wf(res())),
			cmd:   cmdFor(1, nil),
			kinds: []string{"TransitionOperation"},
			verify: func(t *testing.T, pl application.WorkflowMutationPlan) {
				assertTransition(t, pl.Operations[0].(application.TransitionOperation), "new", "resolved")
				if pl.NextTicketState != domain.StateResolved || pl.Result.Ticket.State != domain.StateResolved {
					t.Fatalf("state %s/%s", pl.NextTicketState, pl.Result.Ticket.State)
				}
				if pl.Result.Ticket.ResolvedAt == nil || !pl.Result.Ticket.ResolvedAt.Equal(now) {
					t.Fatalf("resolved_at must be stamped by Transition, got %v", pl.Result.Ticket.ResolvedAt)
				}
				if pl.Result.Ticket.ClosedAt != nil {
					t.Fatalf("closed_at must stay nil after resolve")
				}
				completedRun(t, pl, last, now)
			},
		},
		{
			name:  "resolve from in_progress transitions and completes",
			s:     stampedSnap(domain.StateInProgress, wf(res())),
			cmd:   cmdFor(1, nil),
			kinds: []string{"TransitionOperation"},
			verify: func(t *testing.T, pl application.WorkflowMutationPlan) {
				assertTransition(t, pl.Operations[0].(application.TransitionOperation), "in_progress", "resolved")
				if pl.NextTicketState != domain.StateResolved {
					t.Fatalf("state %s", pl.NextTicketState)
				}
				completedRun(t, pl, last, now)
			},
		},
		{
			name: "resolve from resolved is a completed no-op",
			s:    stampedSnap(domain.StateResolved, wf(res())),
			cmd:  cmdFor(1, nil),
			verify: func(t *testing.T, pl application.WorkflowMutationPlan) {
				if len(pl.Operations) != 0 {
					t.Fatalf("no-op must plan zero operations, got %v", opKinds(pl))
				}
				if pl.NextTicketState != domain.StateResolved || pl.Result.Ticket.State != domain.StateResolved {
					t.Fatalf("state must stay resolved, got %s/%s", pl.NextTicketState, pl.Result.Ticket.State)
				}
				if pl.Result.Ticket.ResolvedAt == nil {
					t.Fatalf("existing resolved_at must be preserved")
				}
				completedRun(t, pl, last, now)
			},
		},
		{
			name: "resolve from closed is a completed no-op",
			s:    stampedSnap(domain.StateClosed, wf(res())),
			cmd:  cmdFor(1, nil),
			verify: func(t *testing.T, pl application.WorkflowMutationPlan) {
				if len(pl.Operations) != 0 {
					t.Fatalf("no-op must plan zero operations, got %v", opKinds(pl))
				}
				if pl.NextTicketState != domain.StateClosed || pl.Result.Ticket.ClosedAt == nil {
					t.Fatalf("closed state/facts must be preserved: %+v", pl.Result.Ticket)
				}
				completedRun(t, pl, last, now)
			},
		},
		{
			name:    "resolve from cancelled rejects with no writes",
			s:       stampedSnap(domain.StateCancelled, wf(res())),
			cmd:     cmdFor(1, nil),
			wantErr: true,
		},
		{
			name:  "close from new orders resolved then closed",
			s:     stampedSnap(domain.StateNew, wf(clo())),
			cmd:   cmdFor(1, nil),
			kinds: []string{"TransitionOperation", "TransitionOperation"},
			verify: func(t *testing.T, pl application.WorkflowMutationPlan) {
				assertTransition(t, pl.Operations[0].(application.TransitionOperation), "new", "resolved")
				assertTransition(t, pl.Operations[1].(application.TransitionOperation), "resolved", "closed")
				if pl.NextTicketState != domain.StateClosed || pl.Result.Ticket.State != domain.StateClosed {
					t.Fatalf("state %s/%s", pl.NextTicketState, pl.Result.Ticket.State)
				}
				if pl.Result.Ticket.ResolvedAt == nil || !pl.Result.Ticket.ResolvedAt.Equal(now) {
					t.Fatalf("resolved_at %v", pl.Result.Ticket.ResolvedAt)
				}
				if pl.Result.Ticket.ClosedAt == nil || !pl.Result.Ticket.ClosedAt.Equal(now) {
					t.Fatalf("closed_at %v", pl.Result.Ticket.ClosedAt)
				}
				completedRun(t, pl, last, now)
			},
		},
		{
			name:  "close from in_progress orders resolved then closed",
			s:     stampedSnap(domain.StateInProgress, wf(clo())),
			cmd:   cmdFor(1, nil),
			kinds: []string{"TransitionOperation", "TransitionOperation"},
			verify: func(t *testing.T, pl application.WorkflowMutationPlan) {
				assertTransition(t, pl.Operations[0].(application.TransitionOperation), "in_progress", "resolved")
				assertTransition(t, pl.Operations[1].(application.TransitionOperation), "resolved", "closed")
				if pl.NextTicketState != domain.StateClosed {
					t.Fatalf("state %s", pl.NextTicketState)
				}
				completedRun(t, pl, last, now)
			},
		},
		{
			name:  "close from resolved transitions once",
			s:     stampedSnap(domain.StateResolved, wf(clo())),
			cmd:   cmdFor(1, nil),
			kinds: []string{"TransitionOperation"},
			verify: func(t *testing.T, pl application.WorkflowMutationPlan) {
				assertTransition(t, pl.Operations[0].(application.TransitionOperation), "resolved", "closed")
				if pl.NextTicketState != domain.StateClosed || pl.Result.Ticket.ClosedAt == nil {
					t.Fatalf("close from resolved must stamp closed: %+v", pl.Result.Ticket)
				}
				if pl.Result.Ticket.ResolvedAt == nil {
					t.Fatalf("existing resolved_at must be preserved")
				}
				completedRun(t, pl, last, now)
			},
		},
		{
			name: "close from closed is a completed no-op",
			s:    stampedSnap(domain.StateClosed, wf(clo())),
			cmd:  cmdFor(1, nil),
			verify: func(t *testing.T, pl application.WorkflowMutationPlan) {
				if len(pl.Operations) != 0 {
					t.Fatalf("no-op must plan zero operations, got %v", opKinds(pl))
				}
				if pl.NextTicketState != domain.StateClosed || pl.Result.Ticket.ClosedAt == nil {
					t.Fatalf("closed facts must be preserved: %+v", pl.Result.Ticket)
				}
				completedRun(t, pl, last, now)
			},
		},
		{
			name:    "close from cancelled rejects with no writes",
			s:       stampedSnap(domain.StateCancelled, wf(clo())),
			cmd:     cmdFor(1, nil),
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pl, err := r.PlanComplete(context.Background(), tc.s, tc.cmd)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want rejection")
				}
				wantValidation(t, err)
				return
			}
			if err != nil {
				t.Fatalf("unexpected %v", err)
			}
			if tc.kinds != nil {
				wantOps(t, pl, tc.kinds...)
			}
			if tc.verify != nil {
				tc.verify(t, pl)
			}
		})
	}
}

// TestWorkflowRunner_AutoAdvance proves the finite closed auto-advance loop: one
// human completion plans its operation(s), then the walker immediately plans the
// following automatic least_loaded/resolve/close steps without extra user
// action; claim/form/manual_task stop pending human input; reaching the end of
// the definition completes the run without an unrelated state mutation; and the
// operation/audit/cursor ordering is literal and persistable.
func TestWorkflowRunner_AutoAdvance(t *testing.T) {
	r := application.NewWorkflowRunner(fixedClock())
	now := fixedClock().Now()
	text := []domain.FormField{{Key: "name", Label: "Name", Kind: domain.FieldShortText, Required: true}}
	raw := application.RawPositionalValues{{Position: 0, Values: []string{"api-01"}}}
	requester := ptr(int64(1))
	assignee := ptr(int64(9))
	cases := []struct {
		name       string
		s          application.WorkflowExecutionSnapshot
		cmd        application.CompleteWorkflowCommand
		kinds      []string
		wantCursor int
		wantStatus string
		wantState  domain.State
		wantErr    bool
		verify     func(*testing.T, application.WorkflowMutationPlan)
	}{
		{
			// form[requester] → automatic least_loaded (new→in_progress
			// consequence) → manual_task stops the loop with one pending action.
			name:       "human form then automatic least_loaded stops at manual",
			s:          snapWith(domain.StateNew, 0, wf(fm(domain.FormActorRequester, text...), least(5), man(), res()), requester, nil),
			cmd:        cmdFor(1, raw),
			kinds:      []string{"FormAnswerOperation", "WorkflowStepOperation", "LeastLoadedAssignmentOperation", "TransitionOperation"},
			wantCursor: 2,
			wantStatus: "active",
			wantState:  domain.StateInProgress,
			verify: func(t *testing.T, pl application.WorkflowMutationPlan) {
				if pl.Operations[2].(application.LeastLoadedAssignmentOperation).StepIndex != 1 || pl.Operations[2].(application.LeastLoadedAssignmentOperation).DeskID != 5 {
					t.Fatalf("automatic least_loaded facts %+v", pl.Operations[2])
				}
				// The required new→in_progress consequence of person routing.
				assertTransition(t, pl.Operations[3].(application.TransitionOperation), "new", "in_progress")
				if pl.Operations[3].(application.TransitionOperation).StepIndex != 1 {
					t.Fatalf("least transition must carry the automatic step index")
				}
				if pl.Result.Ticket.State != domain.StateInProgress {
					t.Fatalf("ticket consequence %s", pl.Result.Ticket.State)
				}
			},
		},
		{
			// The same workflow continues: the human completes the manual task at
			// position 3 and the immediately following automatic resolve_ticket
			// runs in the same plan (in_progress → resolved), completing the run.
			name:       "human manual then automatic resolve completes",
			s:          snapWith(domain.StateInProgress, 2, wf(fm(domain.FormActorRequester, text...), least(5), man(), res()), requester, assignee),
			cmd:        application.CompleteWorkflowCommand{TicketID: 1, ActorUserID: 9, ActorName: actorName(9), ExpectedPosition: 3},
			kinds:      []string{"WorkflowStepOperation", "TransitionOperation"},
			wantCursor: 4,
			wantStatus: "completed",
			wantState:  domain.StateResolved,
			verify: func(t *testing.T, pl application.WorkflowMutationPlan) {
				if pl.Operations[0].(application.WorkflowStepOperation).StepIndex != 2 {
					t.Fatalf("manual step index %d", pl.Operations[0].(application.WorkflowStepOperation).StepIndex)
				}
				assertTransition(t, pl.Operations[1].(application.TransitionOperation), "in_progress", "resolved")
				if pl.Operations[1].(application.TransitionOperation).StepIndex != 3 {
					t.Fatalf("resolve must carry the terminal step index")
				}
				completedRun(t, pl, 4, now)
			},
		},
		{
			// Automatic close_ticket chain after a human form: least_loaded
			// consequence + two ordered workflow transition audits, run completed.
			name:       "human form then automatic close chain completes in closed",
			s:          snapWith(domain.StateNew, 0, wf(fm(domain.FormActorRequester, text...), least(7), clo()), requester, nil),
			cmd:        cmdFor(1, raw),
			kinds:      []string{"FormAnswerOperation", "WorkflowStepOperation", "LeastLoadedAssignmentOperation", "TransitionOperation", "TransitionOperation", "TransitionOperation"},
			wantCursor: 3,
			wantStatus: "completed",
			wantState:  domain.StateClosed,
			verify: func(t *testing.T, pl application.WorkflowMutationPlan) {
				assertTransition(t, pl.Operations[3].(application.TransitionOperation), "new", "in_progress")
				assertTransition(t, pl.Operations[4].(application.TransitionOperation), "in_progress", "resolved")
				assertTransition(t, pl.Operations[5].(application.TransitionOperation), "resolved", "closed")
				if pl.Operations[3].(application.TransitionOperation).StepIndex != 1 ||
					pl.Operations[4].(application.TransitionOperation).StepIndex != 2 ||
					pl.Operations[5].(application.TransitionOperation).StepIndex != 2 {
					t.Fatalf("step indexes lost in the chain")
				}
				if pl.Result.Ticket.ClosedAt == nil || pl.Result.Ticket.ResolvedAt == nil {
					t.Fatalf("lifecycle stamps %+v", pl.Result.Ticket)
				}
				completedRun(t, pl, 3, now)
			},
		},
		{
			// Claim is the submitted human step; the automatic least_loaded and
			// resolve still follow in the same plan (ticket already in_progress
			// after the claim: no redundant transition is planned).
			name:       "human claim then automatic least_loaded and resolve",
			s:          snapWith(domain.StateNew, 0, wf(claim(42), least(5), res()), nil, nil),
			cmd:        cmdFor(7, nil),
			kinds:      []string{"ClaimAssignmentOperation", "TransitionOperation", "WorkflowStepOperation", "LeastLoadedAssignmentOperation", "TransitionOperation"},
			wantCursor: 3,
			wantStatus: "completed",
			wantState:  domain.StateResolved,
			verify: func(t *testing.T, pl application.WorkflowMutationPlan) {
				assertTransition(t, pl.Operations[1].(application.TransitionOperation), "new", "in_progress")
				assertTransition(t, pl.Operations[4].(application.TransitionOperation), "in_progress", "resolved")
				if pl.Operations[3].(application.LeastLoadedAssignmentOperation).StepIndex != 1 {
					t.Fatalf("automatic least_loaded index")
				}
				if pl.Operations[4].(application.TransitionOperation).StepIndex != 2 {
					t.Fatalf("automatic resolve index")
				}
				completedRun(t, pl, 3, now)
			},
		},
		{
			// A following claim step is a human decision: the loop stops with it
			// pending and does not fabricate an assignee or audit.
			name:       "following claim stops pending human input",
			s:          snapWith(domain.StateNew, 0, wf(fm(domain.FormActorRequester, text...), claim(42)), requester, nil),
			cmd:        cmdFor(1, raw),
			kinds:      []string{"FormAnswerOperation", "WorkflowStepOperation"},
			wantCursor: 1,
			wantStatus: "active",
			wantState:  domain.StateNew,
		},
		{
			// A following form stops pending the next human submission.
			name:       "following form stops pending human input",
			s:          snapWith(domain.StateNew, 0, wf(fm(domain.FormActorRequester, text...), fm(domain.FormActorRequester, text...)), requester, nil),
			cmd:        cmdFor(1, raw),
			kinds:      []string{"FormAnswerOperation", "WorkflowStepOperation"},
			wantCursor: 1,
			wantStatus: "active",
			wantState:  domain.StateNew,
		},
		{
			// in_progress least_loaded inside the loop: no redundant transition.
			name:       "in_progress least_loaded after human plans no redundant transition",
			s:          snapWith(domain.StateInProgress, 0, wf(man(), least(5)), nil, assignee),
			cmd:        cmdFor(9, nil),
			kinds:      []string{"WorkflowStepOperation", "LeastLoadedAssignmentOperation"},
			wantCursor: 2,
			wantStatus: "completed",
			wantState:  domain.StateInProgress,
			verify: func(t *testing.T, pl application.WorkflowMutationPlan) {
				if pl.Operations[1].(application.LeastLoadedAssignmentOperation).StepIndex != 1 {
					t.Fatalf("automatic least_loaded index")
				}
				completedRun(t, pl, 2, now)
			},
		},
		{
			// End-of-definition completion without a terminal step: the run
			// completes and the ticket keeps the state its completed steps
			// produced (new here: workflow completion does not imply resolution).
			name:       "form-only end of definition completes without state mutation",
			s:          snapWith(domain.StateNew, 0, wf(fm(domain.FormActorRequester, text...)), requester, nil),
			cmd:        cmdFor(1, raw),
			kinds:      []string{"FormAnswerOperation", "WorkflowStepOperation"},
			wantCursor: 1,
			wantStatus: "completed",
			wantState:  domain.StateNew,
			verify: func(t *testing.T, pl application.WorkflowMutationPlan) {
				completedRun(t, pl, 1, now)
				if pl.Result.Ticket.ResolvedAt != nil || pl.Result.Ticket.ClosedAt != nil {
					t.Fatalf("no unrelated lifecycle stamps: %+v", pl.Result.Ticket)
				}
			},
		},
		{
			// Assignment-only workflow preserves the reached in_progress state.
			name:       "claim-only run completes in in_progress",
			s:          snapWith(domain.StateNew, 0, wf(claim(42)), nil, nil),
			cmd:        cmdFor(7, nil),
			kinds:      []string{"ClaimAssignmentOperation", "TransitionOperation", "WorkflowStepOperation"},
			wantCursor: 1,
			wantStatus: "completed",
			wantState:  domain.StateInProgress,
			verify: func(t *testing.T, pl application.WorkflowMutationPlan) {
				completedRun(t, pl, 1, now)
			},
		},
		{
			// An automatic least_loaded at the current cursor followed by the
			// automatic resolve step: the whole automatic chain is planned in one
			// request and the run ends resolved.
			name:       "automatic least_loaded at cursor then resolve completes",
			s:          snapWith(domain.StateNew, 0, wf(least(5), res()), nil, nil),
			cmd:        cmdFor(7, nil),
			kinds:      []string{"LeastLoadedAssignmentOperation", "TransitionOperation", "TransitionOperation"},
			wantCursor: 2,
			wantStatus: "completed",
			wantState:  domain.StateResolved,
			verify: func(t *testing.T, pl application.WorkflowMutationPlan) {
				assertTransition(t, pl.Operations[1].(application.TransitionOperation), "new", "in_progress")
				assertTransition(t, pl.Operations[2].(application.TransitionOperation), "in_progress", "resolved")
				completedRun(t, pl, 2, now)
			},
		},
		{
			// A failing human completion must reject before the loop starts: no
			// automatic step is planned and the plan stays empty.
			name:    "human validation failure plans no automatics",
			s:       snapWith(domain.StateNew, 0, wf(fm(domain.FormActorRequester, text...), least(5), res()), requester, nil),
			cmd:     cmdFor(1, nil), // required text answer missing
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pl, err := r.PlanComplete(context.Background(), tc.s, tc.cmd)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want rejection")
				}
				wantValidation(t, err)
				return
			}
			if err != nil {
				t.Fatalf("unexpected %v", err)
			}
			if tc.kinds != nil {
				wantOps(t, pl, tc.kinds...)
			}
			if tc.wantCursor != pl.NextCursor || tc.wantStatus != pl.NextRunStatus || tc.wantState != pl.NextTicketState {
				t.Fatalf("cursor/status/state = %d/%s/%s, want %d/%s/%s", pl.NextCursor, pl.NextRunStatus, pl.NextTicketState, tc.wantCursor, tc.wantStatus, tc.wantState)
			}
			if tc.verify != nil {
				tc.verify(t, pl)
			}
		})
	}
}

// TestWorkflowRunner_TerminalSnapshotImmutability proves the terminal matrix
// plans on an in-memory ticket copy: the snapshot ticket (pointer identity and
// pointee values, state, timestamps) and run (status, cursor) are unchanged.
func TestWorkflowRunner_TerminalSnapshotImmutability(t *testing.T) {
	r := application.NewWorkflowRunner(fixedClock())
	s := stampedSnap(domain.StateNew, wf(res()))
	s.Run.StartedAt = time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	s.Ticket.ResolvedAt = ptr(time.Date(2026, 8, 20, 11, 30, 0, 0, time.UTC))
	s.Ticket.ClosedAt = ptr(time.Date(2026, 8, 20, 12, 30, 0, 0, time.UTC))
	resPtr, resVal := s.Ticket.ResolvedAt, *s.Ticket.ResolvedAt
	cloPtr, cloVal := s.Ticket.ClosedAt, *s.Ticket.ClosedAt
	tState, tUpdated := s.Ticket.State, s.Ticket.UpdatedAt
	rStatus, rCursor, rStarted := s.Run.Status, s.Run.CurrentStepIndex, s.Run.StartedAt
	if _, err := r.PlanComplete(context.Background(), s, cmdFor(1, nil)); err != nil {
		t.Fatalf("unexpected %v", err)
	}
	if s.Ticket.State != tState || !s.Ticket.UpdatedAt.Equal(tUpdated) ||
		s.Ticket.ResolvedAt != resPtr || *s.Ticket.ResolvedAt != resVal ||
		s.Ticket.ClosedAt != cloPtr || *s.Ticket.ClosedAt != cloVal {
		t.Fatalf("snapshot ticket mutated: %+v", *s.Ticket)
	}
	if s.Run.Status != rStatus || s.Run.CurrentStepIndex != rCursor || !s.Run.StartedAt.Equal(rStarted) {
		t.Fatalf("snapshot run mutated: %+v", *s.Run)
	}
}
