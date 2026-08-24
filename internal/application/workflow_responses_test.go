package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// PR10 task 10.2 — step-indexed view join + merged timeline. A semantic
// completion event binds to its pinned step context ONLY through the persisted
// audit_events.step_index (never timestamps or order); missing, out-of-range,
// or incompatible contexts degrade to the safe summary; automatic events omit
// actor text while human events keep their attributed names.

type fakeWorkflowStepStore struct {
	byIndex map[int]*application.WorkflowStepContext
	missing map[int]bool // indexes that degrade to nil,nil (no persisted row)
	corrupt map[int]bool // indexes whose persisted answers are corrupt (fail closed)
	calls   int
}

func (f *fakeWorkflowStepStore) WorkflowStepContext(_ context.Context, _ int64, stepIndex int) (*application.WorkflowStepContext, error) {
	f.calls++
	if f.corrupt[stepIndex] {
		return nil, errors.New("sqlite: corrupt persisted form answers")
	}
	if f.missing[stepIndex] {
		return nil, nil
	}
	if ctx, ok := f.byIndex[stepIndex]; ok {
		return ctx, nil
	}
	return nil, nil
}

// ListWorkflowResponses is a compile-seam compatibility stub only: the
// ViewBuilder still consumes WorkflowResponseStore until task 10.2 rewires the
// timeline to WorkflowStepContextStore. It returns nil so existing behavior is
// untouched and the task-10.2 RED assertions below stay intact.
func (f *fakeWorkflowStepStore) ListWorkflowResponses(_ context.Context, _ int64) ([]application.WorkflowResponse, error) {
	return nil, nil
}

func newFakeWorkflowStepStore() *fakeWorkflowStepStore {
	return &fakeWorkflowStepStore{byIndex: map[int]*application.WorkflowStepContext{}, missing: map[int]bool{}, corrupt: map[int]bool{}}
}

type stepTimelineFixture struct {
	builder   *application.ViewBuilder
	responses *fakeWorkflowStepStore
	audits    *fakeAuditStore
	ticket    domain.Ticket
	clock     time.Time
}

func seedStepTimelineFixture(t *testing.T) stepTimelineFixture {
	t.Helper()
	tickets := newFakeTicketStore()
	users := newFakeUserStore()
	categories := newFakeCategoryStore()
	comments := newFakeCommentStore()
	audits := newFakeAuditStore()
	requester := users.seed("Requester", "requester@example.com", true)
	category := categories.seed("Network")
	ticket := tickets.seed(domain.Ticket{
		Title: "Merged timeline", CategoryID: category.ID,
		RequesterUserID: ptr(requester.ID), Priority: domain.PriorityMedium,
		State: domain.StateInProgress, CreatedAt: fixedClock().now, UpdatedAt: fixedClock().now,
	})
	responses := newFakeWorkflowStepStore()
	builder := application.NewViewBuilder(tickets, users, categories, comments, audits, newFakeDeskStore(), responses)
	return stepTimelineFixture{builder: builder, responses: responses, audits: audits, ticket: ticket, clock: fixedClock().now}
}

// append persists through the fixture's own fake audit store directly — the
// production ViewBuilder exposes no write API and none may exist solely for
// tests.
func (f stepTimelineFixture) append(t *testing.T, ev domain.AuditEvent) {
	t.Helper()
	ev.TicketID = f.ticket.ID
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = f.clock
	}
	if err := f.audits.Append(context.Background(), ev); err != nil {
		t.Fatalf("append audit: %v", err)
	}
}

func (f stepTimelineFixture) view(t *testing.T) *application.TicketView {
	t.Helper()
	view, err := f.builder.TicketView(context.Background(), f.ticket.ID, application.TicketQuery{Scope: application.ScopeAll}, true)
	if err != nil {
		t.Fatalf("TicketView: %v", err)
	}
	return view
}

// TestViews_FormEventBindsByExactStepIndex proves identical-timestamp events
// with different persisted step indexes each bind only through their own index.
func TestViews_FormEventBindsByExactStepIndex(t *testing.T) {
	f := seedStepTimelineFixture(t)
	f.responses.byIndex[0] = &application.WorkflowStepContext{
		Kind: "form", FormActor: domain.FormActorRequester,
		Fields: []application.WorkflowResponseField{{Label: "Server <name>", Value: "<b>api-01</b>"}},
	}
	f.responses.byIndex[2] = &application.WorkflowStepContext{
		Kind: "form", FormActor: domain.FormActorAssignee,
		Fields: []application.WorkflowResponseField{{Label: "Work done", Value: "racked"}},
	}
	fieldState := "state"
	// All three share ONE timestamp: correlation must come from the persisted
	// indexes, never from timestamps or ordering.
	f.append(t, domain.AuditEvent{Actor: "Ada", Action: domain.ActionWorkflowRequesterForm, StepIndex: ptr(0)})
	f.append(t, domain.AuditEvent{Actor: "workflow", Action: domain.ActionTransition, Field: &fieldState, FromValue: ptr("new"), ToValue: ptr("in_progress")})
	f.append(t, domain.AuditEvent{Actor: "Beto", Action: domain.ActionWorkflowAssigneeForm, StepIndex: ptr(2)})

	items := f.view(t).Timeline
	if len(items) != 3 {
		t.Fatalf("timeline entries = %d, want 3", len(items))
	}
	if items[0].Summary != "Submitted work details" || len(items[0].StepFields) != 1 || items[0].StepFields[0].Label != "Work done" {
		t.Errorf("assignee form item = %+v, want its own index-2 fields", items[0])
	}
	if items[1].StepFields != nil || items[1].Summary != "Ticket in progress" {
		t.Errorf("transition item must stay summary-only, got %+v (%q)", items[1].StepFields, items[1].Summary)
	}
	if items[2].Summary != "Submitted request details" || len(items[2].StepFields) != 1 || items[2].StepFields[0].Label != "Server <name>" {
		t.Errorf("requester form item = %+v, want its own index-0 fields", items[2])
	}
	// Automatic transition omits actor text; human completions keep their names.
	if items[1].ActorLabel != "" {
		t.Errorf("automatic transition ActorLabel = %q, want empty (no actor text)", items[1].ActorLabel)
	}
	if items[0].ActorLabel != "Beto" || items[2].ActorLabel != "Ada" {
		t.Errorf("human actors must keep their names, got %q and %q", items[0].ActorLabel, items[2].ActorLabel)
	}
}

// TestViews_MissingOrIncompatibleContextDegradesToSummary proves no labels,
// values, or instructions are fabricated when context is absent or mismatched.
func TestViews_MissingOrIncompatibleContextDegradesToSummary(t *testing.T) {
	f := seedStepTimelineFixture(t)
	// Index 3 has NO stored answers row; index 4 resolves to a MANUAL step but
	// the event claims a requester form (incompatible).
	f.responses.missing[3] = true
	f.responses.byIndex[4] = &application.WorkflowStepContext{Kind: "manual", Instruction: "Rack it"}
	f.responses.byIndex[5] = &application.WorkflowStepContext{
		Kind: "form", FormActor: domain.FormActorAssignee,
		Fields: []application.WorkflowResponseField{{Label: "Work done", Value: "x"}},
	}
	f.append(t, domain.AuditEvent{Actor: "Ada", Action: domain.ActionWorkflowRequesterForm, StepIndex: ptr(3), CreatedAt: f.clock.Add(time.Minute)})
	f.append(t, domain.AuditEvent{Actor: "Ada", Action: domain.ActionWorkflowRequesterForm, StepIndex: ptr(4), CreatedAt: f.clock.Add(2 * time.Minute)})
	f.append(t, domain.AuditEvent{Actor: "Beto", Action: domain.ActionWorkflowManualTask, StepIndex: ptr(5), CreatedAt: f.clock.Add(3 * time.Minute)})

	items := f.view(t).Timeline
	for i, wantSummary := range []string{"Completed task", "Submitted request details", "Submitted request details"} {
		item := items[i]
		if item.Summary != wantSummary || item.StepFields != nil || item.StepInstruction != "" {
			t.Errorf("degraded item %d = summary %q fields %v instruction %q, want bare %q", i, item.Summary, item.StepFields, item.StepInstruction, wantSummary)
		}
	}
}

// TestViews_ManualEventRendersPinnedInstruction proves a manual completion item
// carries its contextual pinned instruction joined by the exact index.
func TestViews_ManualEventRendersPinnedInstruction(t *testing.T) {
	f := seedStepTimelineFixture(t)
	f.responses.byIndex[1] = &application.WorkflowStepContext{Kind: "manual", Instruction: "Rack the server"}
	f.append(t, domain.AuditEvent{Actor: "Ada", Action: domain.ActionWorkflowManualTask, StepIndex: ptr(1)})

	item := singleItem(t, f.view(t).Timeline)
	if item.StepInstruction != "Rack the server" {
		t.Errorf("StepInstruction = %q, want the pinned instruction", item.StepInstruction)
	}
}

// TestViews_LegacyNullIndexStaysCompletedStep proves a legacy workflow_step row
// with NULL step context reads as `Completed step` and consults no store.
func TestViews_LegacyNullIndexStaysCompletedStep(t *testing.T) {
	f := seedStepTimelineFixture(t)
	note := "secret legacy note"
	f.append(t, domain.AuditEvent{Actor: "Legacy", Action: domain.ActionWorkflowStep, Note: &note})

	item := singleItem(t, f.view(t).Timeline)
	if item.Summary != "Completed step" || item.StepFields != nil || item.StepInstruction != "" {
		t.Errorf("legacy event = %+v, want bare Completed step", item)
	}
	if !item.SuppressDetail {
		t.Error("legacy workflow_step detail must stay suppressed")
	}
	if f.responses.calls != 0 {
		t.Errorf("NULL-index lookups = %d, want 0 (correlation is index-only)", f.responses.calls)
	}
}

// TestViews_CorruptStepContextFailsClosed proves corrupt persisted answers
// fail the view closed instead of degrading to a fabricated render.
func TestViews_CorruptStepContextFailsClosed(t *testing.T) {
	f := seedStepTimelineFixture(t)
	f.responses.corrupt[0] = true
	f.append(t, domain.AuditEvent{Actor: "Ada", Action: domain.ActionWorkflowRequesterForm, StepIndex: ptr(0)})

	if _, err := f.builder.TicketView(context.Background(), f.ticket.ID, application.TicketQuery{Scope: application.ScopeAll}, true); err == nil {
		t.Fatal("corrupt persisted answers must fail closed, got a successful view")
	}
}

// TestViews_StepContextFollowsScopedTicketRead preserves the authorization
// boundary: no response projection happens before the scoped ticket read wins.
func TestViews_StepContextFollowsScopedTicketRead(t *testing.T) {
	f := seedStepTimelineFixture(t)
	if _, err := f.builder.TicketView(context.Background(), 404, application.TicketQuery{Scope: application.ScopeAll}, true); err == nil {
		t.Fatal("out-of-scope/missing ticket read succeeded")
	}
	if f.responses.calls != 0 {
		t.Fatalf("step-context calls = %d, want 0 before scoped ticket read succeeds", f.responses.calls)
	}
}
