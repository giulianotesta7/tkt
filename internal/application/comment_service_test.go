package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestAddCommentStoresWithSessionAuthor(t *testing.T) {
	clock := fixedClock()
	tickets := newFakeTicketStore()
	comments := newFakeCommentStore()
	cat := newFakeCategoryStore().seed("Bugs")
	ticket := tickets.seed(domain.Ticket{
		Title: "Seeded", CategoryID: cat.ID, Priority: domain.PriorityLow,
		State: domain.StateNew, CreatedAt: clock.now, UpdatedAt: clock.now,
	})
	svc := application.NewCommentService(tickets, comments, clock)
	actor := domain.User{Name: "Ada", Email: "ada@example.com"}
	clock.Advance(timeMinute)

	c, err := svc.Add(context.Background(), actor, ticket.ID, "The redirect is broken")
	if err != nil {
		t.Fatalf("Add: unexpected error: %v", err)
	}
	if c.ID == 0 {
		t.Fatal("Add: comment must receive an ID from the store")
	}
	if c.Author != actor.Name {
		t.Fatalf("Add: author must come from the session, got %q", c.Author)
	}
	if !c.CreatedAt.Equal(clock.now) {
		t.Fatalf("Add: timestamp must come from the injected clock, got %v", c.CreatedAt)
	}

	stored := comments.comments[ticket.ID]
	if len(stored) != 1 || stored[0].Body != "The redirect is broken" {
		t.Fatalf("Add: comment must be stored, got %+v", stored)
	}
}

func TestAddCommentRejectsEmptyBodyWithoutStoreCall(t *testing.T) {
	clock := fixedClock()
	tickets := newFakeTicketStore()
	comments := newFakeCommentStore()
	cat := newFakeCategoryStore().seed("Bugs")
	ticket := tickets.seed(domain.Ticket{
		Title: "Seeded", CategoryID: cat.ID, Priority: domain.PriorityLow,
		State: domain.StateNew, CreatedAt: clock.now, UpdatedAt: clock.now,
	})
	svc := application.NewCommentService(tickets, comments, clock)

	_, err := svc.Add(context.Background(), domain.User{Name: "Ada"}, ticket.ID, "   ")
	var verr *domain.ValidationError
	if !errors.As(err, &verr) || verr.Field != "body" {
		t.Fatalf("Add: empty body must be a ValidationError on field body, got %v", err)
	}
	if len(comments.comments[ticket.ID]) != 0 {
		t.Fatal("Add: rejected comment must not be stored")
	}
}

func TestAddCommentUnknownTicket(t *testing.T) {
	clock := fixedClock()
	svc := application.NewCommentService(newFakeTicketStore(), newFakeCommentStore(), clock)

	_, err := svc.Add(context.Background(), domain.User{Name: "Ada"}, 4242, "hello")
	var nerr *domain.NotFoundError
	if !errors.As(err, &nerr) || nerr.Kind != "ticket" {
		t.Fatalf("Add: unknown ticket must be a NotFoundError(kind=ticket), got %v", err)
	}
}

func TestAddCommentOnClosedTicketAccepted(t *testing.T) {
	clock := fixedClock()
	tickets := newFakeTicketStore()
	comments := newFakeCommentStore()
	cat := newFakeCategoryStore().seed("Bugs")
	now := clock.Now()
	ticket := tickets.seed(domain.Ticket{
		Title: "Closed ticket", CategoryID: cat.ID, Priority: domain.PriorityLow,
		State: domain.StateClosed, CreatedAt: now, UpdatedAt: now,
		ClosedAt: &now,
	})
	svc := application.NewCommentService(tickets, comments, clock)

	c, err := svc.Add(context.Background(), domain.User{Name: "Ada"}, ticket.ID, "Still relevant after closure")
	if err != nil {
		t.Fatalf("Add: comments on closed tickets must be accepted, got %v", err)
	}
	if c.Body != "Still relevant after closure" {
		t.Fatalf("Add: comment must be stored, got %+v", c)
	}
}

// TestListByTicketCreationOrder covers the chronological timeline (ASC):
// three comments created at increasing times render in creation order.
func TestListByTicketCreationOrder(t *testing.T) {
	clock := fixedClock()
	tickets := newFakeTicketStore()
	comments := newFakeCommentStore()
	cat := newFakeCategoryStore().seed("Bugs")
	ticket := tickets.seed(domain.Ticket{
		Title: "Seeded", CategoryID: cat.ID, Priority: domain.PriorityLow,
		State: domain.StateNew, CreatedAt: clock.now, UpdatedAt: clock.now,
	})
	svc := application.NewCommentService(tickets, comments, clock)
	actor := domain.User{Name: "Ada"}

	for _, body := range []string{"first", "second", "third"} {
		clock.Advance(timeMinute)
		if _, err := svc.Add(context.Background(), actor, ticket.ID, body); err != nil {
			t.Fatalf("Add(%q): unexpected error: %v", body, err)
		}
	}

	list, err := svc.ListByTicket(context.Background(), ticket.ID)
	if err != nil {
		t.Fatalf("ListByTicket: unexpected error: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("ListByTicket: 3 comments expected, got %d", len(list))
	}
	for i, want := range []string{"first", "second", "third"} {
		if list[i].Body != want {
			t.Fatalf("ListByTicket: comment %d must be %q (creation order), got %q", i, want, list[i].Body)
		}
	}
}
