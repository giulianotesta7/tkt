package application_test

import (
	"bytes"
	"context"
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
			def, err := svc.GetForBuilder(context.Background(), actor, 1)
			if tc.ok {
				if err != nil {
					t.Fatalf("allowed %s err %v", tc.role, err)
				}
				if len(def) != 0 {
					t.Fatalf("want empty got %d", len(def))
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
	def, err := svc.GetForBuilder(context.Background(), admin, 99)
	if err != nil {
		t.Fatal(err)
	}
	if len(def) != 0 {
		t.Fatalf("want empty got %v", def)
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
