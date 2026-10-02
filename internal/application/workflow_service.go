package application

import (
	"context"
	"errors"

	"github.com/giulianotesta7/tkt/internal/domain"
)

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

func (s *WorkflowService) GetForBuilder(ctx context.Context, actor domain.User, categoryID int64) (domain.WorkflowDefinition, error) {
	if err := s.requireManage(actor); err != nil {
		return nil, err
	}
	raw, err := s.store.GetDraft(ctx, categoryID)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return domain.WorkflowDefinition{}, nil
	}
	return domain.ParseWorkflowDefinition(raw)
}

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

func (s *WorkflowService) Preview(ctx context.Context, actor domain.User, _ int64, draft domain.WorkflowDefinition) (domain.WorkflowDefinition, []domain.WorkflowValidationIssue, error) {
	if err := s.requireManage(actor); err != nil {
		return nil, nil, err
	}
	b, err := canonicalBytes(draft)
	if err != nil {
		return nil, []domain.WorkflowValidationIssue{{Step: 1, Field: "steps", Message: err.Error()}}, nil
	}
	def, err := domain.ParseWorkflowDefinition(b)
	if err != nil {
		return nil, []domain.WorkflowValidationIssue{{Step: 1, Field: "steps", Message: err.Error()}}, nil
	}
	return def, def.Validate(), nil
}

func (s *WorkflowService) Publish(ctx context.Context, actor domain.User, categoryID int64, draft domain.WorkflowDefinition) ([]domain.WorkflowValidationIssue, error) {
	if err := s.requireManage(actor); err != nil {
		return nil, err
	}
	b, err := canonicalBytes(draft)
	if err != nil {
		return []domain.WorkflowValidationIssue{{Step: 1, Field: "steps", Message: err.Error()}}, nil
	}
	def, err := domain.ParseWorkflowDefinition(b)
	if err != nil {
		return []domain.WorkflowValidationIssue{{Step: 1, Field: "steps", Message: err.Error()}}, nil
	}
	if iss := def.Validate(); len(iss) > 0 {
		return iss, nil
	}
	if len(def) == 0 {
		return []domain.WorkflowValidationIssue{{Step: 1, Field: "steps", Message: "workflow must have at least one step"}}, nil
	}
	by := actor.ID
	_, iss, err := s.store.Publish(ctx, categoryID, b, &by)
	return iss, err
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
// The check runs before any write, so a refused clone cannot partially apply.
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
	existing, err := s.store.GetDraft(ctx, targetCategoryID)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return &domain.ValidationError{Field: "target_category_id", Message: "the target category already has a draft; cloning would overwrite it"}
	}
	b, err := canonicalBytes(published.Workflow)
	if err != nil {
		return err
	}
	return s.store.UpsertDraft(ctx, targetCategoryID, b)
}
