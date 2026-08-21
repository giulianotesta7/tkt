package application_test

import (
	"context"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

type fakeWorkflowResponseStore struct {
	responses []application.WorkflowResponse
	calls     *int
}

func (f fakeWorkflowResponseStore) ListWorkflowResponses(_ context.Context, _ int64) ([]application.WorkflowResponse, error) {
	if f.calls != nil {
		(*f.calls)++
	}
	return f.responses, nil
}

func TestViews_WorkflowTimeline(t *testing.T) {
	clock := fixedClock()
	tickets := newFakeTicketStore()
	users := newFakeUserStore()
	categories := newFakeCategoryStore()
	comments := newFakeCommentStore()
	audits := newFakeAuditStore()
	requester := users.seed("Requester", "requester@example.com", true)
	category := categories.seed("Network")
	ticket := tickets.seed(domain.Ticket{
		Title: "Hostile <script>alert(1)</script>", CategoryID: category.ID,
		RequesterUserID: ptr(requester.ID), Priority: domain.PriorityMedium,
		State: domain.StateResolved, CreatedAt: clock.now, UpdatedAt: clock.now,
	})
	audits.Append(context.Background(), domain.AuditEvent{
		TicketID: ticket.ID, Actor: "workflow", Action: domain.ActionTransition,
		Field: ptr("state"), FromValue: ptr("in_progress"), ToValue: ptr("resolved"), CreatedAt: clock.now,
	})
	responses := fakeWorkflowResponseStore{responses: []application.WorkflowResponse{{
		StepIndex: 0, SubmittedAt: clock.now, Fields: []application.WorkflowResponseField{
			{Label: "Server <name>", Value: "<b>api-01</b>"},
			{Label: "Approved", Value: "true"},
		},
	}}}

	builder := application.NewViewBuilder(tickets, users, categories, comments, audits, responses)
	view, err := builder.TicketView(context.Background(), ticket.ID, application.TicketQuery{Scope: application.ScopeAll}, true)
	if err != nil {
		t.Fatalf("TicketView: %v", err)
	}
	if len(view.WorkflowResponses) != 1 || len(view.WorkflowResponses[0].Fields) != 2 {
		t.Fatalf("responses = %+v", view.WorkflowResponses)
	}
	if got := view.Timeline[0].ActorLabel; got != "Workflow" {
		t.Fatalf("workflow actor label = %q, want Workflow", got)
	}
	if got := view.Timeline[0].Summary; got != "Ticket resolved" {
		t.Fatalf("workflow transition summary = %q", got)
	}
	if view.WorkflowResponses[0].SubmittedAt != time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC) {
		t.Fatalf("response ordering timestamp = %v", view.WorkflowResponses[0].SubmittedAt)
	}
}

func TestViews_WorkflowResponsesFollowScopedTicketRead(t *testing.T) {
	calls := 0
	builder := application.NewViewBuilder(newFakeTicketStore(), newFakeUserStore(), newFakeCategoryStore(), newFakeCommentStore(), newFakeAuditStore(), fakeWorkflowResponseStore{calls: &calls})
	if _, err := builder.TicketView(context.Background(), 404, application.TicketQuery{Scope: application.ScopeAll}, true); err == nil {
		t.Fatal("out-of-scope/missing ticket read succeeded")
	}
	if calls != 0 {
		t.Fatalf("response store calls = %d, want 0 before scoped ticket read succeeds", calls)
	}
}
