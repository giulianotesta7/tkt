package application

import (
	"context"
	"errors"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// ErrDraftRevisionConflict reports that a guarded draft write was refused
// because the stored draft revision no longer matched the one the writer
// expected (issue #254). It is the sentinel a store returns from
// SaveDraftIfRevision; the HTTP layer recognizes it to render the refusal
// instead of overwriting the newer draft.
var ErrDraftRevisionConflict = errors.New("workflow draft revision conflict")

// WorkflowDraftRevisionStore is the optimistic-lock capability of a workflow
// store (issue #254). The base WorkflowStore port is unchanged so existing
// stores and fakes keep compiling; a store that cannot answer the revision
// questions is discovered by type assertion, exactly like the
// WorkflowVersionStore resolver Clone already uses. A store WITHOUT this
// capability cannot perform a guarded write: SaveDraftAtRevision fails closed
// rather than risk an unguarded overwrite.
type WorkflowDraftRevisionStore interface {
	// GetDraftWithRevision reads the draft bytes AND their revision in one
	// statement, so the pair a builder renders is always internally consistent
	// (a separate read could render revision N+1 next to revision N's bytes and
	// silently revert the newer change on submit).
	GetDraftWithRevision(ctx context.Context, categoryID int64) ([]byte, int64, error)
	// SaveDraftIfRevision writes draft atomically IF AND ONLY IF the stored
	// revision equals expectedRevision, and reports the new revision. A
	// mismatch writes NOTHING and returns ErrDraftRevisionConflict.
	SaveDraftIfRevision(ctx context.Context, categoryID int64, expectedRevision int64, draft []byte) (int64, error)
}

type WorkflowService struct{ store WorkflowStore }

func NewWorkflowService(store WorkflowStore) *WorkflowService { return &WorkflowService{store: store} }

func (s *WorkflowService) requireManage(actor domain.User) error {
	if !NewPolicy().Capabilities(actor.Role).Require(CapManageCategories) {
		return domain.NewForbiddenError("category management is not permitted")
	}
	return nil
}

func canonicalBytes(draft domain.WorkflowDefinition) ([]byte, error) {
	if draft == nil {
		draft = domain.WorkflowDefinition{}
	}
	return draft.MarshalCanonical()
}

// GetForBuilder returns the category's draft together with the revision the
// rendered builder must carry back (issue #254). The pair comes from one
// store read when the store supports revisions; a store that does not answers
// revision 0, which is the correct value for a store with no lock at all (and
// whose guarded writes fail closed anyway).
func (s *WorkflowService) GetForBuilder(ctx context.Context, actor domain.User, categoryID int64) (domain.WorkflowDefinition, int64, error) {
	if err := s.requireManage(actor); err != nil {
		return nil, 0, err
	}
	raw, revision, err := s.readDraft(ctx, categoryID)
	if err != nil {
		return nil, 0, err
	}
	if len(raw) == 0 {
		return domain.WorkflowDefinition{}, revision, nil
	}
	def, err := domain.ParseWorkflowDefinition(raw)
	if err != nil {
		return nil, 0, err
	}
	return def, revision, nil
}

// readDraft resolves the draft bytes and their revision. The revision-capable
// store answers both in one statement; a legacy store answers bytes only and
// reports revision 0.
func (s *WorkflowService) readDraft(ctx context.Context, categoryID int64) ([]byte, int64, error) {
	if revisions, ok := s.store.(WorkflowDraftRevisionStore); ok {
		return revisions.GetDraftWithRevision(ctx, categoryID)
	}
	raw, err := s.store.GetDraft(ctx, categoryID)
	return raw, 0, err
}

// SaveDraftAtRevision is the guarded draft write (issue #254): it persists
// the draft ONLY when the stored revision still equals expectedRevision, and
// returns the revision the write produced. A stale tab — one whose form
// carried a revision another writer already advanced — is refused with
// ErrDraftRevisionConflict and its bytes are never written, so the first
// writer's work survives.
//
// It is the production authoring path and the ONLY draft write the builder
// uses. A mutation that carries no usable revision is refused by the HTTP
// layer before it reaches the store, so no authoring flow can ever overwrite a
// newer draft.
func (s *WorkflowService) SaveDraftAtRevision(ctx context.Context, actor domain.User, categoryID int64, draft domain.WorkflowDefinition, expectedRevision int64) (int64, error) {
	if err := s.requireManage(actor); err != nil {
		return 0, err
	}
	b, err := canonicalBytes(draft)
	if err != nil {
		return 0, err
	}
	revisions, ok := s.store.(WorkflowDraftRevisionStore)
	if !ok {
		return 0, errors.New("workflow store cannot guard draft revisions")
	}
	return revisions.SaveDraftIfRevision(ctx, categoryID, expectedRevision, b)
}

// SaveDraft is the UNGUARDED draft write. It is no longer an authoring path:
// no production code calls it (the builder uses SaveDraftAtRevision), and it is
// retained only for test arrangement (handlers_amendment4_test.go,
// handlers_catalog_unavailable_test.go, handlers_category_workflows_test.go)
// and for the legacy wrappers deliberately exercised by
// TestWorkflowService_Mutating_Canonicalizes and TestWorkflowService_Publish.
// AddStep, MoveUp and RemoveStep wrap it for the same reason and also have no
// production caller. A new handler must use SaveDraftAtRevision instead.
func (s *WorkflowService) SaveDraft(ctx context.Context, actor domain.User, categoryID int64, draft domain.WorkflowDefinition) error {
	if err := s.requireManage(actor); err != nil {
		return err
	}
	b, err := canonicalBytes(draft)
	if err != nil {
		return err
	}
	return s.store.UpsertDraft(ctx, categoryID, b)
}

func (s *WorkflowService) AddStep(ctx context.Context, actor domain.User, categoryID int64, draft domain.WorkflowDefinition, step domain.WorkflowStep) error {
	nd := append(append(domain.WorkflowDefinition(nil), draft...), step)
	return s.SaveDraft(ctx, actor, categoryID, nd)
}

func (s *WorkflowService) MoveUp(ctx context.Context, actor domain.User, categoryID int64, draft domain.WorkflowDefinition, idx int) error {
	if idx <= 0 || idx >= len(draft) {
		return s.SaveDraft(ctx, actor, categoryID, draft)
	}
	nd := append(domain.WorkflowDefinition(nil), draft...)
	nd[idx], nd[idx-1] = nd[idx-1], nd[idx]
	return s.SaveDraft(ctx, actor, categoryID, nd)
}

func (s *WorkflowService) RemoveStep(ctx context.Context, actor domain.User, categoryID int64, draft domain.WorkflowDefinition, idx int) error {
	if idx < 0 || idx >= len(draft) {
		return s.SaveDraft(ctx, actor, categoryID, draft)
	}
	nd := append(domain.WorkflowDefinition(nil), draft[:idx]...)
	nd = append(nd, draft[idx+1:]...)
	return s.SaveDraft(ctx, actor, categoryID, nd)
}

// publishDraft canonicalizes and validates a draft for publishing. A non-empty
// issue list short-circuits the publish; the returned bytes are the canonical
// form the store persists and versions.
func publishDraft(draft domain.WorkflowDefinition) ([]byte, []domain.WorkflowValidationIssue) {
	b, err := canonicalBytes(draft)
	if err != nil {
		return nil, []domain.WorkflowValidationIssue{{Step: 1, Field: "steps", Message: err.Error()}}
	}
	def, err := domain.ParseWorkflowDefinition(b)
	if err != nil {
		return nil, []domain.WorkflowValidationIssue{{Step: 1, Field: "steps", Message: err.Error()}}
	}
	if iss := def.Validate(); len(iss) > 0 {
		return nil, iss
	}
	if len(def) == 0 {
		return nil, []domain.WorkflowValidationIssue{{Step: 1, Field: "steps", Message: "workflow must have at least one step"}}
	}
	return b, nil
}

// Publish is the historical, unguarded publish. It publishes against the
// category's CURRENT revision and still advances it in the same transaction,
// but it carries no revision to compare against, so it cannot refuse a stale
// tab. It is retained for the seed tooling and fixtures that publish a fresh
// category's draft directly; the HTTP authoring path uses PublishAtRevision.
func (s *WorkflowService) Publish(ctx context.Context, actor domain.User, categoryID int64, draft domain.WorkflowDefinition) ([]domain.WorkflowValidationIssue, error) {
	if err := s.requireManage(actor); err != nil {
		return nil, err
	}
	b, iss := publishDraft(draft)
	if len(iss) > 0 {
		return iss, nil
	}
	by := actor.ID
	_, iss, err := s.store.Publish(ctx, categoryID, b, &by)
	return iss, err
}

// PublishAtRevision is the guarded publish (issue #254): expectedRevision must
// still be the stored revision, or the publish writes NOTHING and returns
// ErrDraftRevisionConflict for the HTTP layer to render as a refusal. On
// success it returns the ADVANCED revision, so the caller re-renders a page
// whose next save carries the right expectation.
func (s *WorkflowService) PublishAtRevision(ctx context.Context, actor domain.User, categoryID int64, draft domain.WorkflowDefinition, expectedRevision int64) (int64, []domain.WorkflowValidationIssue, error) {
	if err := s.requireManage(actor); err != nil {
		return 0, nil, err
	}
	b, iss := publishDraft(draft)
	if len(iss) > 0 {
		return 0, iss, nil
	}
	by := actor.ID
	_, revision, iss, err := s.store.PublishAtRevision(ctx, categoryID, b, expectedRevision, &by)
	return revision, iss, err
}

func (s *WorkflowService) ListSummaries(ctx context.Context, actor domain.User) ([]WorkflowSummary, error) {
	if err := s.requireManage(actor); err != nil {
		return nil, err
	}
	return s.store.ListSummaries(ctx)
}

// ListRequesterSummaries is ListSummaries for the REQUESTER-facing surfaces —
// the ticket picker and, later, the create form's category select. Those pages
// must not demand an operator capability, and they must not answer "can this
// category be used?" from a different rule either: the admin's categories
// screen and the requester's picker read the same computation, so they cannot
// disagree about a category's state.
//
// It is deliberately not a rename of ListAvailableCategories, which answers a
// narrower question ("is anything published?") for the form's select. That
// narrower rule still lets a published-but-unrunnable category be chosen there,
// which the create guard refuses with 409 — see issue #239.
func (s *WorkflowService) ListRequesterSummaries(ctx context.Context) ([]WorkflowSummary, error) {
	return s.store.ListSummaries(ctx)
}

func (s *WorkflowService) ListAvailableCategories(ctx context.Context) ([]domain.Category, error) {
	return s.store.ListAvailableCategories(ctx)
}

// errCloneTargetHasDraft is the #257 refusal: cloning never merges and never
// overwrites an existing target draft. The SAME error is returned whether the
// draft was already present at the emptiness check or a concurrent writer
// landed it between the check and the guarded write, so both callers see one
// status/message path.
func errCloneTargetHasDraft() error {
	return &domain.ValidationError{Field: "target_category_id", Message: "the target category already has a draft; cloning would overwrite it"}
}

// Clone reuses a workflow: it copies the SOURCE category's CURRENT PUBLISHED
// version into the TARGET category's draft, so an admin does not rebuild the
// same sequence by hand (issue #257).
//
// It is authoring, not publishing: the target keeps no current version and
// cannot move a ticket until an operator publishes the cloned draft
// deliberately, exactly like any other builder change.
//
// An existing target draft is PROTECTED. When the target already has draft
// bytes the clone is refused with a comprehensible ValidationError and writes
// NOTHING — it never merges and never overwrites work someone else may own.
//
// The protection is atomic (issue #299): the emptiness check and the write are
// not two independent steps. The target's bytes and revision are read in ONE
// statement, and the write goes through the SAME revision compare-and-swap the
// builder uses, so two concurrent clones cannot both observe an empty target.
// A clone that loses the race rechecks the revision inside the store's
// transaction, writes NOTHING, and is refused on the same #257 path; the
// winner's bytes survive byte-for-byte. A store without the revision
// capability fails closed rather than risk an unguarded overwrite.
//
// The published-version read is composed from the existing WorkflowVersionStore
// method (GetCurrentVersion) rather than a new port: the production sqlite
// workflowStore implements both ports on one value, and the optional-capability
// type assertion is this codebase's established discovery pattern (see
// AgentQueueContextStore). A store that cannot resolve published versions fails
// closed instead of guessing.
func (s *WorkflowService) Clone(ctx context.Context, actor domain.User, sourceCategoryID, targetCategoryID int64) error {
	if err := s.requireManage(actor); err != nil {
		return err
	}
	versions, ok := s.store.(WorkflowVersionStore)
	if !ok {
		return errors.New("workflow store cannot resolve a published version")
	}
	published, err := versions.GetCurrentVersion(ctx, sourceCategoryID)
	if err != nil {
		return err
	}
	if published == nil {
		return &domain.ValidationError{Field: "source_category_id", Message: "the source category has no published workflow to clone"}
	}
	// The emptiness check and the guarded write must share ONE compare-and-swap.
	// Reading bytes and revision together means a concurrent clone that writes
	// between this read and SaveDraftIfRevision advances the revision, so the
	// loser's in-statement comparison updates zero rows and writes NOTHING.
	existing, revision, err := s.readDraft(ctx, targetCategoryID)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return errCloneTargetHasDraft()
	}
	b, err := canonicalBytes(published.Workflow)
	if err != nil {
		return err
	}
	revisions, ok := s.store.(WorkflowDraftRevisionStore)
	if !ok {
		return errors.New("workflow store cannot guard draft revisions")
	}
	if _, err := revisions.SaveDraftIfRevision(ctx, targetCategoryID, revision, b); err != nil {
		if errors.Is(err, ErrDraftRevisionConflict) {
			return errCloneTargetHasDraft()
		}
		return err
	}
	return nil
}
