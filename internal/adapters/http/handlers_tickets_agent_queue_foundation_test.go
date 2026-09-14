package httpadapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestAgentQueueSectionsBatchesRowContextIntoWrapper(t *testing.T) {
	h := newHarness(t)
	agent := seedUserRole(t, h.store, "Ava", "ava@example.com", domain.RoleAgent)
	desk, err := h.desks.Create(t.Context(), *h.admin, "Queue desk")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}
	if err := h.desks.AddMember(t.Context(), *h.admin, desk.ID, agent.ID); err != nil {
		t.Fatalf("add desk member: %v", err)
	}
	cat, err := h.categories.Create(t.Context(), "Queue context")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	h.publishWorkflow(t, cat.ID, domain.WorkflowDefinition{{
		Type:         domain.StepAssignToDesk,
		AssignToDesk: &domain.AssignToDeskStep{DeskID: desk.ID, Strategy: domain.StrategyClaim},
	}})
	if _, err := h.tickets.Create(t.Context(), *h.admin, application.CreateTicketInput{
		Title: "Claimable row", CategoryID: cat.ID, Priority: domain.PriorityMedium,
	}); err != nil {
		t.Fatalf("create claimable ticket: %v", err)
	}
	assignedTicket := h.seedTicket(t, "Assigned row", nil)
	h.assignTicket(t, assignedTicket.ID, agent.ID)

	req := httptest.NewRequest(http.MethodGet, "/tickets", nil).
		WithContext(context.WithValue(context.Background(), ctxKeyUser{}, agent))
	queue := NewTicketHandlers(h.tickets, h.comments, h.search, h.categories, h.users,
		h.store.DeskStore(), h.workflows, application.NewWorkflowRunner(h.clock),
		h.store.WorkflowRunStore(), h.store.WorkflowUnitOfWork(), h.renderer)
	assigned, claimable, total, err := queue.agentQueueSections(req, filterState{}, 1, 1)
	if err != nil {
		t.Fatalf("agentQueueSections: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if len(assigned.Tickets) != 1 || assigned.Tickets[0].Title != "Assigned row" {
		t.Fatalf("assigned rows = %+v, want the embedded assigned ticket", assigned.Tickets)
	}
	row := assigned.Tickets[0]
	if row.Context.DeskName != "" {
		t.Errorf("manually assigned desk = %q, want empty (no desk audit)", row.Context.DeskName)
	}
	if row.Context.CurrentTask != "handle" {
		t.Errorf("assigned current task = %q, want the pinned manual instruction", row.Context.CurrentTask)
	}
	if row.Context.Position != nil {
		t.Errorf("assigned row must carry no claim position, got %d", *row.Context.Position)
	}
	if len(claimable.Tickets) != 1 || claimable.Tickets[0].Title != "Claimable row" {
		t.Fatalf("claimable rows = %+v, want the embedded claimable ticket", claimable.Tickets)
	}
	claim := claimable.Tickets[0]
	if claim.Context.DeskName != "Queue desk" {
		t.Errorf("claim desk = %q, want the current pinned claim desk", claim.Context.DeskName)
	}
	if claim.Context.Position == nil || *claim.Context.Position != 1 {
		t.Errorf("claim position = %v, want 1 (1-based current step)", claim.Context.Position)
	}
	if claim.Context.CurrentTask != "Waiting for claim" {
		t.Errorf("claim current task = %q, want %q", claim.Context.CurrentTask, "Waiting for claim")
	}
}
