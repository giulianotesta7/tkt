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

// errVersionStoreFailed is the simulated WorkflowVersionStore failure the fake
// returns when failWith is set: the service must propagate it untouched and must
// not plan, submit a unit-of-work plan, persist a ticket/audit, or fall back to
// the legacy create path.
var errVersionStoreFailed = errors.New("workflow version store failed")

// --- PR5 Batch A gatekeeper correction: published-definition deep snapshot ---
//
// The guard blocker: PublishedWorkflow.Workflow was shallow-aliased into the
// WorkflowRunner planning call AND the CreateTicketWithRunInput capture, so a
// caller/store-owned definition mutated after lookup could alter the supposedly
// immutable captured plan. These regressions prove the corrected contract:
// the application deep-snapshots the published definition at the trust boundary
// BEFORE both initial runner planning and CreateTicketWithRunInput capture, and
// the fakes are alias-safe enough to expose (never hide) that contract.

// workflowCreateMutationFixture returns the all-shapes workflow definition used
// by the alias/immutability regressions: an automatic least_loaded assignment, a
// requester form with nested Fields (short_text + single_select with Options),
// and a manual step — together touching every nested pointer/slice shape of the
// closed WorkflowDefinition union (step type, AssignToDesk, Form, Fields,
// Options, ManualTask). It plans exactly 2 initial automatic operations
// (least_loaded step 0 + the new→in_progress workflow transition), then stops
// pending human input at the form.
func workflowCreateMutationFixture() domain.WorkflowDefinition {
	return domain.WorkflowDefinition{
		{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: 3, Strategy: domain.StrategyLeastLoaded}},
		{Type: domain.StepForm, Form: &domain.FormStep{
			Actor: domain.FormActorRequester,
			Fields: []domain.FormField{
				{Key: "reason", Label: "Reason", Kind: domain.FieldShortText, Required: true},
				{Key: "priority", Label: "Priority", Kind: domain.FieldSingleSelect, Options: []string{"low", "high"}},
			},
		}},
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Provision the server"}},
	}
}

// mutateWorkflowAllShapes mutates every nested pointer/slice shape of the closed
// WorkflowDefinition union in place: each step's Type, the AssignToDesk payload
// fields, the Form actor plus its nested Fields slice (element fields and a
// slice append) and an Options slice element, and the ManualTask payload. A
// deep-snapshotting boundary must make ALL of these invisible to a plan it
// captured before (or regardless of) the mutation.
func mutateWorkflowAllShapes(wf domain.WorkflowDefinition) {
	wf[0].Type = domain.StepManualTask
	wf[0].AssignToDesk.DeskID = 999
	wf[0].AssignToDesk.Strategy = domain.StrategyClaim

	wf[1].Type = domain.StepResolve
	wf[1].Form.Actor = domain.FormActorAssignee
	wf[1].Form.Fields[0].Key = "hacked"
	wf[1].Form.Fields[0].Label = "Hacked"
	wf[1].Form.Fields[0].Kind = domain.FieldCheckbox
	wf[1].Form.Fields[0].Required = false
	wf[1].Form.Fields[0].Options = append(wf[1].Form.Fields[0].Options, "leaked")
	wf[1].Form.Fields[1].Options[0] = "hacked"
	wf[1].Form.Fields = append(wf[1].Form.Fields, domain.FormField{Key: "extra", Label: "Extra", Kind: domain.FieldShortText})

	wf[2].Type = domain.StepAssignToDesk
	wf[2].ManualTask.Instructions = "HACKED"
}

// TestTicketService_CreateWithWorkflow_CapturedPlanImmuneToStoreMutation is the
// gatekeeper RED/GREEN regression: a plan captured at Create must be a deep,
// caller/store-independent snapshot. After Create returns, the test mutates the
// STORE-owned published definition (the object GetCurrentVersion hands out) in
// every nested shape — the alias vector the gatekeeper found — plus the current
// version id, and asserts the runner/UoW-captured definition, the planned
// operations, and the pinned version are all unchanged. RED: the service aliases
// pv.Workflow into the runner and CreateTicketWithRunInput, so the recorded plan
// observes the mutations. GREEN: the application deep-snapshots the published
// definition at the trust boundary before BOTH planning and capture.
func TestTicketService_CreateWithWorkflow_CapturedPlanImmuneToStoreMutation(t *testing.T) {
	h := newWorkflowCreateHarness()
	cat := h.categories.seed("Bugs")
	want := workflowCreateMutationFixture()
	versionID := h.versions.publish(cat.ID, want)
	actor := domain.User{ID: 7, Name: "Ada", Email: "ada@example.com", Role: domain.RoleUser}

	if _, err := h.svc.Create(context.Background(), actor, validCreateInput(cat.ID)); err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}

	// Mutate the store-owned published definition AFTER the plan was captured:
	// every closed shape of the union, plus the store's current version id.
	stored := h.versions.versions[cat.ID]
	mutateWorkflowAllShapes(stored.Workflow)
	stored.VersionID = 777

	in := h.wfTx.calls[0]
	if !reflect.DeepEqual(in.Workflow, workflowCreateMutationFixture()) {
		t.Fatal("Create: the captured plan must be a deep snapshot — mutating the store-owned definition after capture must not alter the pinned workflow")
	}
	if in.ExpectedVersionID != versionID {
		t.Fatalf("Create: the expected current version must be captured by value, got %d want %d", in.ExpectedVersionID, versionID)
	}
	if in.Ticket == nil || in.Ticket.WorkflowVersionID == nil || *in.Ticket.WorkflowVersionID != versionID {
		t.Fatalf("Create: the ticket pin must be captured by value — store mutation after capture must not change it, got %v", in.Ticket.WorkflowVersionID)
	}
	// The runner-planned initial automatic advancement is also stable: the two
	// ops were planned from the pre-mutation snapshot (least_loaded intent at
	// step 0 for desk 3, then the exact new→in_progress workflow transition).
	if len(in.Operations) != 2 {
		t.Fatalf("Create: plan must carry exactly 2 initial automatic operations, got %d", len(in.Operations))
	}
	least, ok := in.Operations[0].(application.LeastLoadedAssignmentOperation)
	if !ok || least.StepIndex != 0 || least.DeskID != 3 {
		t.Fatalf("Create: least_loaded op must keep step 0 / desk 3, got %#v", in.Operations[0])
	}
	tr, ok := in.Operations[1].(application.TransitionOperation)
	if !ok || tr.Audit.ToValue == nil || *tr.Audit.ToValue != string(domain.StateInProgress) {
		t.Fatalf("Create: transition op must keep the exact new→in_progress audit, got %#v", in.Operations[1])
	}
}

// TestTicketService_CreateWithWorkflow_OriginalDefMutationAfterPublishDoesNotLeak
// proves the fake version store's SETUP is alias-safe (requirement 3) and the
// store contract behind it: publish() persists an immutable snapshot of the
// definition, so mutating the caller's own definition after configuring the
// published workflow but before Create must not corrupt the store's current
// version or the captured plan. RED (shallow publish): the store serves the
// mutated caller slice and the plan captures it. GREEN (deep-copy publish plus
// trust-boundary snapshot): the captured plan equals the originally published
// definition.
func TestTicketService_CreateWithWorkflow_OriginalDefMutationAfterPublishDoesNotLeak(t *testing.T) {
	h := newWorkflowCreateHarness()
	cat := h.categories.seed("Bugs")
	def := workflowCreateMutationFixture()
	want := workflowCreateMutationFixture()
	h.versions.publish(cat.ID, def)

	// Mutate the caller-owned definition after publish, before Create...
	mutateWorkflowAllShapes(def)

	if _, err := h.svc.Create(context.Background(), domain.User{ID: 7, Name: "Ada", Email: "ada@example.com", Role: domain.RoleUser}, validCreateInput(cat.ID)); err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}
	in := h.wfTx.calls[0]
	if !reflect.DeepEqual(in.Workflow, want) {
		t.Fatal("Create: a mutation of the caller's definition after publish must not leak into the captured plan")
	}
	if in.ExpectedVersionID == 0 {
		t.Fatal("Create: the plan must still carry the published version id")
	}
}

// TestTicketService_CreateWithWorkflow_CapturesUntrustedSourceOnce proves the
// capture-once contract (PR5 second-attempt gate blocker 3/4): after
// GetCurrentVersion the service must read the untrusted published definition
// EXACTLY ONCE (trustedSnapshot := pv.Workflow.Clone()) and derive the two
// independent runner/persistence clones from that snapshot — never clone or
// read pv.Workflow again. The deterministic TEMPORAL hook is an existing
// dependency boundary the runner already consults (the injected domain.Clock),
// not a production callback, sleep, or race: a publisherClock swaps the
// version store's owned workflow memory in place exactly once, landing between
// the runner-planning clock read and the CreateTicketWithRunInput capture —
// precisely between the two pv.Workflow reads of the violating double-read
// shape. RED (double read): the second clone observes the mutated store memory
// (persisted workflow snapshot B) while the plan came from pristine snapshot A.
// GREEN (capture once): both derive from the same trusted snapshot, so the
// mid-flight store mutation stays invisible.
func TestTicketService_CreateWithWorkflow_CapturesUntrustedSourceOnce(t *testing.T) {
	clock := &publisherClock{now: fixedClock().now}
	users := newFakeUserStore()
	categories := newFakeCategoryStore()
	tickets := newFakeTicketStore()
	comments := newFakeCommentStore()
	audits := newFakeAuditStore()
	tx := newFakeUnitOfWork(tickets, audits)
	versions := newFakeWorkflowVersionStore()
	runner := application.NewWorkflowRunner(clock)
	wfTx := newFakeWorkflowUnitOfWork(tickets, audits)
	builder := application.NewViewBuilder(tickets, users, categories, comments, audits, newFakeDeskStore())
	svc := application.NewTicketServiceWithWorkflowCreate(tickets, users, categories, tx, builder, clock, versions, runner, wfTx)

	cat := categories.seed("Bugs")
	want := workflowCreateMutationFixture()
	versionID := versions.publish(cat.ID, want)
	// The store-owned PublishedWorkflow GetCurrentVersion hands out is the
	// untrusted, mutable memory the trust boundary must capture once; the
	// publisherClock aliases it exactly as a concurrent publisher would.
	clock.published = versions.versions[cat.ID]
	actor := domain.User{ID: 7, Name: "Ada", Email: "ada@example.com", Role: domain.RoleUser}

	if _, err := svc.Create(context.Background(), actor, validCreateInput(cat.ID)); err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}
	if len(wfTx.calls) != 1 {
		t.Fatalf("Create: exactly one CreateTicketWithRun plan expected, got %d", len(wfTx.calls))
	}
	in := wfTx.calls[0]
	if !reflect.DeepEqual(in.Workflow, want) {
		t.Fatal("Create: the persisted workflow must be the SAME definition the runner planned against — a mid-flight mutation of the untrusted source must not produce a divergent persisted snapshot B")
	}
	if in.ExpectedVersionID != versionID {
		t.Fatalf("Create: plan must keep the originally resolved version %d, got %d", versionID, in.ExpectedVersionID)
	}
	least, ok := in.Operations[0].(application.LeastLoadedAssignmentOperation)
	if !ok || least.StepIndex != 0 || least.DeskID != 3 {
		t.Fatalf("Create: the runner plan must come from the same trusted snapshot (least_loaded step 0 / desk 3), got %#v", in.Operations[0])
	}
	// Arrange proof: the publisher clock MUST have exercised the temporal window
	// (store-owned memory actually mutated between planning and capture), so a
	// green result cannot be a vacuous pass.
	if !clock.swapped || versions.versions[cat.ID].Workflow[0].AssignToDesk.DeskID != 999 {
		t.Fatal("arrange: the publisher clock must have mutated the store-owned definition inside the planning window — the regression is not exercising the double-read hazard")
	}
}

// publisherClock is the deterministic, bounded temporal hook of the
// snapshot capture-once regression: it implements the injected domain.Clock
// the WorkflowRunner ALREADY consults (an existing dependency boundary — no
// production callback, sleep, or race), and its Now() swaps the version
// store's owned workflow memory once, on the RUNNER-PLANNING READ (the second
// Now() call in the create flow: the first is the service's pre-lookup `now`
// capture). That read lands exactly between snapshot A and snapshot B capture
// of a violating double-read implementation — the temporal window where a
// concurrent publisher can replace the definition.
type publisherClock struct {
	now time.Time
	// published is the store-owned PublishedWorkflow that GetCurrentVersion
	// hands out (its Workflow aliases the store's backing memory); mutating it
	// simulates a concurrent publisher replacing the definition between reads.
	published *application.PublishedWorkflow
	// reads counts Now() calls; swapped records that the one-shot swap fired.
	reads   int
	swapped bool
}

func (c *publisherClock) Now() time.Time {
	c.reads++
	if c.reads == 2 && !c.swapped && c.published != nil {
		c.swapped = true
		mutateWorkflowAllShapes(c.published.Workflow)
	}
	return c.now
}

// TestTicketService_CreateWithWorkflow_VersionStoreErrorPropagates proves a
// WorkflowVersionStore lookup failure propagates UNCHANGED and short-circuits
// everything downstream: no initial-automatic planning (no plan), no
// CreateTicketWithRun submission, no persisted ticket or created audit, and no
// fallback to the legacy TicketUnitOfWork create. The runner has no persistence
// surface, and a zero UoW-call count proves no plan was produced at all.
func TestTicketService_CreateWithWorkflow_VersionStoreErrorPropagates(t *testing.T) {
	h := newWorkflowCreateHarness()
	cat := h.categories.seed("Bugs")
	h.versions.publish(cat.ID, workflowCreateMutationFixture())
	h.versions.failWith = errVersionStoreFailed
	actor := domain.User{ID: 7, Name: "Ada", Email: "ada@example.com", Role: domain.RoleUser}

	_, err := h.svc.Create(context.Background(), actor, validCreateInput(cat.ID))
	if !errors.Is(err, errVersionStoreFailed) {
		t.Fatalf("Create: the version-store failure must propagate untouched, got %v", err)
	}
	if len(h.wfTx.calls) != 0 {
		t.Fatalf("Create: a failed lookup must not submit a CreateTicketWithRun plan, got %d calls", len(h.wfTx.calls))
	}
	if len(h.tickets.tickets) != 0 || len(h.audits.events) != 0 {
		t.Fatal("Create: a failed lookup must not persist a ticket or an audit")
	}
	if h.tx.createCalls != 0 {
		t.Fatal("Create: a failed lookup must NOT fall back to the legacy TicketUnitOfWork create path")
	}
}
