package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// seededCommentTimeline arranges a ticket with comments and audit events at
// increasing times through the fakes (D13: the view resolves refs).
func seededCommentTimeline(t *testing.T) (*application.ViewBuilder, *fakeTicketStore, *fakeUserStore, domain.Ticket, domain.User, domain.Category) {
	t.Helper()
	clock := fixedClock()
	tickets := newFakeTicketStore()
	users := newFakeUserStore()
	categories := newFakeCategoryStore()
	comments := newFakeCommentStore()
	audits := newFakeAuditStore()

	user := users.seed("Ana", "ana@example.com", true)
	cat := categories.seed("Bugs")
	ticket := tickets.seed(domain.Ticket{
		Title: "Seeded", CategoryID: cat.ID, Priority: domain.PriorityLow,
		State: domain.StateInProgress, CreatedAt: clock.now, UpdatedAt: clock.now,
		UserID: ptr(user.ID),
	})

	for _, body := range []string{"first", "second", "third"} {
		clock.Advance(timeMinute)
		comments.Add(context.Background(), &domain.Comment{
			TicketID: ticket.ID, Author: "Ada", Body: body, CreatedAt: clock.now,
		})
	}
	clock.Advance(timeMinute)
	audits.Append(context.Background(), domain.AuditEvent{
		TicketID: ticket.ID, Actor: "Ada", Action: domain.ActionCreated, CreatedAt: clock.now,
	})

	builder := application.NewViewBuilder(tickets, users, categories, comments, audits)
	return builder, tickets, users, ticket, user, cat
}

func TestTicketViewComposesRefsAndOrderedTimelines(t *testing.T) {
	builder, _, _, ticket, user, cat := seededCommentTimeline(t)

	view, err := builder.TicketView(context.Background(), ticket.ID)
	if err != nil {
		t.Fatalf("TicketView: unexpected error: %v", err)
	}
	if view.Ticket == nil || view.Ticket.ID != ticket.ID {
		t.Fatalf("TicketView: ticket must be the stored one, got %+v", view.Ticket)
	}
	if view.Category == nil || view.Category.ID != cat.ID || view.Category.Name != "Bugs" {
		t.Fatalf("TicketView: category ref must be resolved, got %+v", view.Category)
	}
	if view.AssignedUser == nil || view.AssignedUser.ID != user.ID {
		t.Fatalf("TicketView: assigned user ref must be resolved, got %+v", view.AssignedUser)
	}
	if len(view.Comments) != 3 {
		t.Fatalf("TicketView: 3 comments expected, got %d", len(view.Comments))
	}
	for i, want := range []string{"first", "second", "third"} {
		if view.Comments[i].Body != want {
			t.Fatalf("TicketView: comment %d must be %q in creation order, got %q", i, want, view.Comments[i].Body)
		}
	}
	if len(view.AuditEvents) != 1 || view.AuditEvents[0].Action != domain.ActionCreated {
		t.Fatalf("TicketView: audit timeline must be in occurrence order, got %+v", view.AuditEvents)
	}
}

func TestTicketViewUnassignedUserIsNil(t *testing.T) {
	clock := fixedClock()
	tickets := newFakeTicketStore()
	categories := newFakeCategoryStore()
	cat := categories.seed("Bugs")
	ticket := tickets.seed(domain.Ticket{
		Title: "Unassigned", CategoryID: cat.ID, Priority: domain.PriorityLow,
		State: domain.StateNew, CreatedAt: clock.now, UpdatedAt: clock.now,
	})
	builder := application.NewViewBuilder(tickets, newFakeUserStore(), categories, newFakeCommentStore(), newFakeAuditStore())

	view, err := builder.TicketView(context.Background(), ticket.ID)
	if err != nil {
		t.Fatalf("TicketView: unexpected error: %v", err)
	}
	if view.AssignedUser != nil {
		t.Fatalf("TicketView: unassigned ticket must resolve to nil user, got %+v", view.AssignedUser)
	}
}

// TestTicketViewShowsInactiveAssignedUser covers the user-management
// historical display: a deactivated user stays visible on their tickets.
func TestTicketViewShowsInactiveAssignedUser(t *testing.T) {
	builder, _, users, ticket, user, _ := seededCommentTimeline(t)

	deactivated, err := users.GetByID(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("GetByID: unexpected error: %v", err)
	}
	deactivated.Active = false
	if err := users.Update(context.Background(), deactivated); err != nil {
		t.Fatalf("deactivate: unexpected error: %v", err)
	}

	view, err := builder.TicketView(context.Background(), ticket.ID)
	if err != nil {
		t.Fatalf("TicketView: unexpected error: %v", err)
	}
	if view.AssignedUser == nil || view.AssignedUser.Active {
		t.Fatalf("TicketView: deactivated user must still be shown as assigned, got %+v", view.AssignedUser)
	}
}

func TestTicketViewUnknownTicket(t *testing.T) {
	builder := application.NewViewBuilder(newFakeTicketStore(), newFakeUserStore(), newFakeCategoryStore(), newFakeCommentStore(), newFakeAuditStore())

	_, err := builder.TicketView(context.Background(), 4242)
	var nerr *domain.NotFoundError
	if !errors.As(err, &nerr) || nerr.Kind != "ticket" {
		t.Fatalf("TicketView: unknown ticket must be a NotFoundError(kind=ticket), got %v", err)
	}
}
