package application

import (
	"context"

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
	if raw == nil || len(raw) == 0 {
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

func (s *WorkflowService) ListAvailableCategories(ctx context.Context) ([]domain.Category, error) {
	return s.store.ListAvailableCategories(ctx)
}
