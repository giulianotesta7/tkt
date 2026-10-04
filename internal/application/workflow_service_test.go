package application_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestWorkflowService_GetForBuilder_RequiresCapability(t *testing.T) {
	ws := newFakeWorkflowStore()
	svc := application.NewWorkflowService(ws)
	for _, tc := range []struct {
		name string
		role domain.Role
		ok   bool
	}{
		{"user denied", domain.RoleUser, false},
		{"agent denied", domain.RoleAgent, false},
		{"admin allowed", domain.RoleAdmin, true},
		{"root allowed", domain.RoleRoot, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actor := domain.User{ID: 10, Role: tc.role, Active: true}
			def, revision, err := svc.GetForBuilder(context.Background(), actor, 1)
			if tc.ok {
				if err != nil {
					t.Fatalf("allowed %s err %v", tc.role, err)
				}
				if len(def) != 0 {
					t.Fatalf("want empty got %d", len(def))
				}
				if revision != 0 {
					t.Fatalf("a store without the revision capability must read revision 0, got %d", revision)
				}
				if len(ws.upsertCalls) != 0 {
					t.Fatal("GetForBuilder must not write")
				}
				if len(ws.getCalls) != 1 {
					t.Fatalf("GetDraft once got %d", len(ws.getCalls))
				}
			} else {
				if err == nil {
					t.Fatal("want forbidden")
				}
				if len(ws.getCalls) != 0 {
					t.Fatal("denied must not reach store")
				}
			}
			ws.getCalls = nil
			ws.upsertCalls = nil
		})
	}
}

func TestWorkflowService_GetForBuilder_EmptyWhenAbsent(t *testing.T) {
	ws := newFakeWorkflowStore()
	svc := application.NewWorkflowService(ws)
	admin := domain.User{ID: 1, Role: domain.RoleAdmin, Active: true}
	def, revision, err := svc.GetForBuilder(context.Background(), admin, 99)
	if err != nil {
		t.Fatal(err)
	}
	if len(def) != 0 {
		t.Fatalf("want empty got %v", def)
	}
	if revision != 0 {
		t.Fatalf("absent draft revision = %d, want 0", revision)
	}
	if len(ws.upsertCalls) != 0 {
		t.Fatal("must not upsert")
	}
}

func TestWorkflowService_Mutating_Canonicalizes(t *testing.T) {
	ws := newFakeWorkflowStore()
	svc := application.NewWorkflowService(ws)
	admin := domain.User{ID: 1, Role: domain.RoleAdmin, Active: true}
	draft := domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "  Do it  "}}}
	if err := svc.SaveDraft(context.Background(), admin, 1, draft); err != nil {
		t.Fatal(err)
	}
	if len(ws.upsertCalls) != 1 {
		t.Fatalf("want 1 upsert got %d", len(ws.upsertCalls))
	}
	def, _ := domain.ParseWorkflowDefinition(ws.upsertCalls[0].draft)
	if def[0].ManualTask.Instructions != "Do it" {
		t.Fatalf("want trimmed got %q", def[0].ManualTask.Instructions)
	}
	ws.upsertCalls = nil
	step := domain.WorkflowStep{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Second"}}
	if err := svc.AddStep(context.Background(), admin, 1, draft, step); err != nil {
		t.Fatal(err)
	}
	def2, _ := domain.ParseWorkflowDefinition(ws.upsertCalls[0].draft)
	if len(def2) != 2 {
		t.Fatalf("AddStep want 2 got %d", len(def2))
	}
	ws.upsertCalls = nil
	two := domain.WorkflowDefinition{
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "A"}},
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "B"}},
	}
	if err := svc.RemoveStep(context.Background(), admin, 1, two, 0); err != nil {
		t.Fatal(err)
	}
	def3, _ := domain.ParseWorkflowDefinition(ws.upsertCalls[0].draft)
	if len(def3) != 1 || def3[0].ManualTask.Instructions != "B" {
		t.Fatalf("RemoveStep wrong %v", def3)
	}
	ws.upsertCalls = nil
	if err := svc.MoveUp(context.Background(), admin, 1, two, 1); err != nil {
		t.Fatal(err)
	}
	def4, _ := domain.ParseWorkflowDefinition(ws.upsertCalls[0].draft)
	if def4[0].ManualTask.Instructions != "B" || def4[1].ManualTask.Instructions != "A" {
		t.Fatalf("MoveUp wrong %v", def4)
	}
}

func TestWorkflowService_Mutating_Denied(t *testing.T) {
	ws := newFakeWorkflowStore()
	svc := application.NewWorkflowService(ws)
	agent := domain.User{ID: 2, Role: domain.RoleAgent, Active: true}
	draft := domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "x"}}}
	if err := svc.SaveDraft(context.Background(), agent, 1, draft); err == nil {
		t.Fatal("want denied")
	}
	if len(ws.upsertCalls) != 0 {
		t.Fatal("denied should not call store")
	}
}

func TestWorkflowService_Publish(t *testing.T) {
	ws := newFakeWorkflowStore()
	svc := application.NewWorkflowService(ws)
	admin := domain.User{ID: 1, Role: domain.RoleAdmin, Active: true}
	agent := domain.User{ID: 2, Role: domain.RoleAgent, Active: true}
	empty := domain.WorkflowDefinition{}
	if _, err := svc.Publish(context.Background(), agent, 1, empty); err == nil {
		t.Fatal("agent publish denied")
	}
	iss, err := svc.Publish(context.Background(), admin, 1, empty)
	if err != nil || len(iss) == 0 {
		t.Fatalf("empty publish want issues got %v err %v", iss, err)
	}
	if len(ws.publishCalls) != 0 {
		t.Fatal("invalid must not call store")
	}
	valid := domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Do"}}}
	iss2, err := svc.Publish(context.Background(), admin, 1, valid)
	if err != nil || len(iss2) != 0 {
		t.Fatalf("valid publish failed %v %v", err, iss2)
	}
	if len(ws.publishCalls) != 1 {
		t.Fatalf("want 1 publish got %d", len(ws.publishCalls))
	}
	other := domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Other"}}}
	if err := svc.SaveDraft(context.Background(), admin, 1, other); err != nil {
		t.Fatal(err)
	}
	if ws.published == nil || len(*ws.published) != 1 {
		t.Fatal("published should stay after draft edit")
	}
}

func TestWorkflowService_PublishInvalidNoWrite(t *testing.T) {
	ws := newFakeWorkflowStore()
	svc := application.NewWorkflowService(ws)
	admin := domain.User{ID: 1, Role: domain.RoleAdmin, Active: true}
	invalid := domain.WorkflowDefinition{{Type: "unknown"}}
	iss, _ := svc.Publish(context.Background(), admin, 1, invalid)
	if len(iss) == 0 {
		t.Fatal("want issues")
	}
	if len(ws.publishCalls) != 0 {
		t.Fatal("invalid must not reach store")
	}
}

func TestWorkflowService_ListSummaries_RequiresCapability(t *testing.T) {
	ws := newFakeWorkflowStore()
	svc := application.NewWorkflowService(ws)
	if _, err := svc.ListSummaries(context.Background(), domain.User{ID: 3, Role: domain.RoleUser}); err == nil {
		t.Fatal("user denied")
	}
	if _, err := svc.ListSummaries(context.Background(), domain.User{ID: 1, Role: domain.RoleAdmin}); err != nil {
		t.Fatal(err)
	}
}

// cloneStore composes the two ports the Clone use case reads on one value,
// exactly as the production sqlite workflowStore does: the draft/publish port
// (WorkflowStore) plus the published-version resolver (WorkflowVersionStore).
// Clone discovers the resolver by type assertion, so a fake that implements
// only WorkflowStore cannot clone.
type cloneStore struct {
	*fakeWorkflowStore
	versions *fakeWorkflowVersionStore
}

func newCloneStore() *cloneStore {
	return &cloneStore{fakeWorkflowStore: newFakeWorkflowStore(), versions: newFakeWorkflowVersionStore()}
}

func (c *cloneStore) GetCurrentVersion(ctx context.Context, categoryID int64) (*application.PublishedWorkflow, error) {
	return c.versions.GetCurrentVersion(ctx, categoryID)
}

// interceptingCloneStore models the interleaving the clone guard must survive:
// another writer commits a target draft AFTER Clone's emptiness check returns
// but BEFORE its write lands. Both reads lie and report the target empty, while
// the store already holds someone else's bytes at revision 1. An unguarded
// check-then-write (the #257 defect) clobbers those bytes and reports success;
// a clone that routes its write through the revision compare-and-swap is
// refused with ZERO writes and the other writer's bytes survive byte-for-byte.
type interceptingCloneStore struct {
	*cloneStore
	lieCategoryID int64
}

func (c *interceptingCloneStore) GetDraft(ctx context.Context, categoryID int64) ([]byte, error) {
	if categoryID == c.lieCategoryID {
		return nil, nil
	}
	return c.cloneStore.GetDraft(ctx, categoryID)
}

func (c *interceptingCloneStore) GetDraftWithRevision(ctx context.Context, categoryID int64) ([]byte, int64, error) {
	if categoryID == c.lieCategoryID {
		return nil, 0, nil
	}
	return c.cloneStore.GetDraftWithRevision(ctx, categoryID)
}

// Clone writes the SOURCE's current published definition into the TARGET's
// draft. It is authoring, not publishing: the target keeps no current version
// until an operator publishes it deliberately.
func TestWorkflowService_Clone_WritesTargetDraftWithoutPublishing(t *testing.T) {
	store := newCloneStore()
	svc := application.NewWorkflowService(store)
	admin := domain.User{ID: 1, Role: domain.RoleAdmin, Active: true}
	source := domain.WorkflowDefinition{
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Clone me"}},
		{Type: domain.StepResolve},
	}
	store.versions.publish(7, source)

	if err := svc.Clone(context.Background(), admin, 7, 9); err != nil {
		t.Fatalf("clone: %v", err)
	}
	if len(store.upsertCalls) != 1 || store.upsertCalls[0].cat != 9 {
		t.Fatalf("clone must write exactly the target draft, got %+v", store.upsertCalls)
	}
	got, err := domain.ParseWorkflowDefinition(store.upsertCalls[0].draft)
	if err != nil || len(got) != 2 || got[0].ManualTask.Instructions != "Clone me" || got[1].Type != domain.StepResolve {
		t.Fatalf("cloned draft wrong: %v err %v", got, err)
	}
	if len(store.publishCalls) != 0 {
		t.Fatal("clone must never publish")
	}
}

// The maintainer's decision: an existing target draft is protected. A refused
// clone returns a comprehensible error and leaves the target's bytes EXACTLY
// as they were — it never merges and never overwrites.
func TestWorkflowService_Clone_RefusesExistingTargetDraftUntouched(t *testing.T) {
	store := newCloneStore()
	svc := application.NewWorkflowService(store)
	admin := domain.User{ID: 1, Role: domain.RoleAdmin, Active: true}
	store.versions.publish(7, domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Source"}}})
	existing := domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Someone else's work"}}}
	before, err := existing.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveDraft(context.Background(), admin, 9, existing); err != nil {
		t.Fatal(err)
	}
	store.upsertCalls = nil // ignore the arrangement write

	err = svc.Clone(context.Background(), admin, 7, 9)
	if err == nil {
		t.Fatal("clone into a category that already has a draft must be refused")
	}
	if !strings.Contains(err.Error(), "already has a draft") {
		t.Fatalf("refusal must explain the existing draft, got %q", err.Error())
	}
	if len(store.upsertCalls) != 0 {
		t.Fatalf("refused clone must write nothing, got %+v", store.upsertCalls)
	}
	after, err := store.GetDraft(context.Background(), 9)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("target draft changed on a refused clone:\n got  %s\n want %s", after, before)
	}
	if len(store.publishCalls) != 0 {
		t.Fatal("refused clone must not publish")
	}
}

// A clone into a genuinely empty target writes the source's published bytes and
// advances the target draft revision by exactly one. The revision is the state
// the subsequent builder page renders, so the cloned draft is guarded from its
// very next edit like any other draft.
func TestWorkflowService_Clone_EmptyTargetAdvancesRevision(t *testing.T) {
	store := newCloneStore()
	svc := application.NewWorkflowService(store)
	admin := domain.User{ID: 1, Role: domain.RoleAdmin, Active: true}
	source := domain.WorkflowDefinition{
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Clone me"}},
		{Type: domain.StepResolve},
	}
	store.versions.publish(7, source)

	if err := svc.Clone(context.Background(), admin, 7, 9); err != nil {
		t.Fatalf("clone into an empty target: %v", err)
	}
	if got := store.revisions[9]; got != 1 {
		t.Fatalf("clone must advance the target draft revision to 1, got %d", got)
	}
	want, err := source.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.GetDraft(context.Background(), 9)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("cloned bytes = %s, want %s", got, want)
	}
	if len(store.publishCalls) != 0 {
		t.Fatal("clone must never publish")
	}
}

// Falsification for the atomic clone: the target draft is created BETWEEN the
// emptiness check and the write, so a check-then-write implementation observes
// an empty target and then overwrites the winner's bytes. The guarded write
// must instead be refused with the #257 message and leave the winner's bytes
// byte-for-byte intact. This fails if the clone guard is a no-op.
func TestWorkflowService_Clone_RefusesDraftLandedBetweenCheckAndWrite(t *testing.T) {
	base := newCloneStore()
	store := &interceptingCloneStore{cloneStore: base, lieCategoryID: 9}
	svc := application.NewWorkflowService(store)
	admin := domain.User{ID: 1, Role: domain.RoleAdmin, Active: true}
	ctx := context.Background()
	store.versions.publish(7, domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Source"}}})

	// The concurrent winner lands at revision 1 after Clone's read would have
	// reported the target empty.
	other := domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Someone else's work"}}}
	otherBytes, err := other.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := base.SaveDraftIfRevision(ctx, 9, 0, otherBytes); err != nil {
		t.Fatalf("arrange concurrent winner: %v", err)
	}
	base.upsertCalls = nil // ignore the arrangement write

	err = svc.Clone(ctx, admin, 7, 9)
	if err == nil {
		t.Fatal("a draft landed between the check and the write must make the clone refuse")
	}
	if !strings.Contains(err.Error(), "already has a draft") {
		t.Fatalf("refusal must use the #257 message, got %q", err.Error())
	}
	after, err := base.GetDraft(ctx, 9)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, otherBytes) {
		t.Fatalf("the winner's bytes were clobbered:\n got  %s\n want %s", after, otherBytes)
	}
	if len(base.publishCalls) != 0 {
		t.Fatal("refused clone must not publish")
	}
}

func TestWorkflowService_Clone_SourceWithoutPublishedVersion(t *testing.T) {
	store := newCloneStore()
	svc := application.NewWorkflowService(store)
	admin := domain.User{ID: 1, Role: domain.RoleAdmin, Active: true}
	if err := svc.Clone(context.Background(), admin, 7, 9); err == nil {
		t.Fatal("a source with no published workflow must be refused")
	}
	if len(store.upsertCalls) != 0 {
		t.Fatal("must not write when the source has nothing published")
	}
}

func TestWorkflowService_Clone_RequiresCapability(t *testing.T) {
	store := newCloneStore()
	svc := application.NewWorkflowService(store)
	store.versions.publish(7, domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Source"}}})
	for _, role := range []domain.Role{domain.RoleUser, domain.RoleAgent} {
		if err := svc.Clone(context.Background(), domain.User{ID: 2, Role: role, Active: true}, 7, 9); err == nil {
			t.Fatalf("%s clone must be denied", role)
		}
	}
	if len(store.upsertCalls) != 0 {
		t.Fatal("denied clone must not write")
	}
}

// bareWorkflowStore implements ONLY the base WorkflowStore port. Embedding the
// INTERFACE (not the concrete fake) is what hides the fake's extra
// optimistic-lock methods, which is exactly the shape SaveDraftAtRevision must
// fail closed on.
type bareWorkflowStore struct{ application.WorkflowStore }

// TestWorkflowService_SaveDraftAtRevision_RefusesStaleTab is the issue #254
// falsification test at the application layer: two writers carry the SAME
// expected revision (the stale tab and the tab that saved first). The stale
// write must be refused with ErrDraftRevisionConflict and the winner's bytes
// must survive. Deterministic and sequential on purpose: no sleeps, no
// polling, no t.Parallel — the two sequential service calls ARE the stale tab.
func TestWorkflowService_SaveDraftAtRevision_RefusesStaleTab(t *testing.T) {
	store := newFakeWorkflowStore()
	svc := application.NewWorkflowService(store)
	admin := domain.User{ID: 1, Role: domain.RoleAdmin, Active: true}
	ctx := context.Background()

	winner := domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "winner"}}}
	stale := domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "stale tab"}}}

	_, revision, err := svc.GetForBuilder(ctx, admin, 1)
	if err != nil {
		t.Fatal(err)
	}
	if revision != 0 {
		t.Fatalf("fresh category revision = %d, want 0", revision)
	}

	if next, err := svc.SaveDraftAtRevision(ctx, admin, 1, winner, revision); err != nil || next != 1 {
		t.Fatalf("first writer: next=%d err=%v, want 1 and nil", next, err)
	}
	// The stale tab still carries revision 0.
	if _, err := svc.SaveDraftAtRevision(ctx, admin, 1, stale, 0); !errors.Is(err, application.ErrDraftRevisionConflict) {
		t.Fatalf("stale write err = %v, want ErrDraftRevisionConflict", err)
	}
	got, revisionAfter, err := svc.GetForBuilder(ctx, admin, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ManualTask == nil || got[0].ManualTask.Instructions != "winner" {
		t.Fatalf("stored draft = %+v, want the winner's bytes", got)
	}
	if revisionAfter != 1 {
		t.Fatalf("stored revision = %d, want 1 (the refused write must not advance it)", revisionAfter)
	}
}

// The positive path: the correct revision succeeds and advances the revision.
func TestWorkflowService_SaveDraftAtRevision_AdvancesOnCorrectRevision(t *testing.T) {
	store := newFakeWorkflowStore()
	svc := application.NewWorkflowService(store)
	admin := domain.User{ID: 1, Role: domain.RoleAdmin, Active: true}
	ctx := context.Background()
	first := domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "one"}}}
	second := domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "two"}}}

	if next, err := svc.SaveDraftAtRevision(ctx, admin, 1, first, 0); err != nil || next != 1 {
		t.Fatalf("first write next=%d err=%v, want 1 and nil", next, err)
	}
	if next, err := svc.SaveDraftAtRevision(ctx, admin, 1, second, 1); err != nil || next != 2 {
		t.Fatalf("second write next=%d err=%v, want 2 and nil", next, err)
	}
	got, revision, err := svc.GetForBuilder(ctx, admin, 1)
	if err != nil {
		t.Fatal(err)
	}
	if revision != 2 || len(got) != 1 || got[0].ManualTask == nil || got[0].ManualTask.Instructions != "two" {
		t.Fatalf("after two guarded writes: revision=%d draft=%+v, want 2 and 'two'", revision, got)
	}
}

// A store without the revision capability must fail closed: a guarded write is
// refused with an error and nothing is written.
func TestWorkflowService_SaveDraftAtRevision_StoreWithoutCapabilityFailsClosed(t *testing.T) {
	base := newFakeWorkflowStore()
	store := &bareWorkflowStore{WorkflowStore: base}
	svc := application.NewWorkflowService(store)
	admin := domain.User{ID: 1, Role: domain.RoleAdmin, Active: true}
	draft := domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "x"}}}
	if _, err := svc.SaveDraftAtRevision(context.Background(), admin, 1, draft, 0); err == nil {
		t.Fatal("a store without the revision capability must refuse a guarded write")
	}
	if len(base.upsertCalls) != 0 {
		t.Fatal("a refused guarded write must write nothing")
	}
}

// TestWorkflowService_Publish_RefusesStaleRevision is the publish-side
// falsification test at the application layer: a publish carrying a revision
// another writer already advanced is refused and writes nothing.
func TestWorkflowService_Publish_RefusesStaleRevision(t *testing.T) {
	store := newFakeWorkflowStore()
	svc := application.NewWorkflowService(store)
	admin := domain.User{ID: 1, Role: domain.RoleAdmin, Active: true}
	ctx := context.Background()
	draft := domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Do"}}}

	// A writer advances the draft to revision 1 before the stale publish.
	if next, err := svc.SaveDraftAtRevision(ctx, admin, 1, draft, 0); err != nil || next != 1 {
		t.Fatalf("advance revision: next=%d err=%v, want 1 and nil", next, err)
	}

	// The stale tab still carries revision 0.
	if _, _, err := svc.PublishAtRevision(ctx, admin, 1, draft, 0); !errors.Is(err, application.ErrDraftRevisionConflict) {
		t.Fatalf("stale publish err = %v, want ErrDraftRevisionConflict", err)
	}
	if len(store.publishCalls) != 0 {
		t.Fatalf("a refused publish must write nothing, got %d publish calls", len(store.publishCalls))
	}
}

// The positive path: publishing at the correct revision succeeds, advances the
// revision, and leaves a later save at the pre-publish revision refused.
func TestWorkflowService_Publish_AdvancesAndBlocksOldRevision(t *testing.T) {
	store := newFakeWorkflowStore()
	svc := application.NewWorkflowService(store)
	admin := domain.User{ID: 1, Role: domain.RoleAdmin, Active: true}
	ctx := context.Background()
	draft := domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Do"}}}

	revision, iss, err := svc.PublishAtRevision(ctx, admin, 1, draft, 0)
	if err != nil || len(iss) != 0 || revision != 1 {
		t.Fatalf("publish: revision=%d iss=%v err=%v, want 1, none, nil", revision, iss, err)
	}
	if _, err := svc.SaveDraftAtRevision(ctx, admin, 1, draft, 0); !errors.Is(err, application.ErrDraftRevisionConflict) {
		t.Fatalf("save at the pre-publish revision err = %v, want ErrDraftRevisionConflict", err)
	}
	if next, err := svc.SaveDraftAtRevision(ctx, admin, 1, draft, revision); err != nil || next != 2 {
		t.Fatalf("save at the published revision: next=%d err=%v, want 2 and nil", next, err)
	}
}
