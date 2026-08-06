package application_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// newTicketService wires a TicketService against fresh fakes.
func newTicketService() (*application.TicketService, *fakeTicketStore, *fakeUserStore, *fakeCategoryStore, *fakeAuditStore, *fakeClock) {
	clock := fixedClock()
	users := newFakeUserStore()
	categories := newFakeCategoryStore()
	tickets := newFakeTicketStore()
	audits := newFakeAuditStore()
	svc := application.NewTicketService(tickets, users, categories, audits, clock)
	return svc, tickets, users, categories, audits, clock
}

func validCreateInput(catID int64, userID *int64) application.CreateTicketInput {
	return application.CreateTicketInput{
		Title:          "Fix login redirect",
		Description:    "After login the user lands on the wrong page",
		RequesterName:  "Bob",
		RequesterEmail: "bob@example.com",
		CategoryID:     catID,
		UserID:         userID,
		Priority:       domain.PriorityHigh,
	}
}

func TestCreateStoresTicketWithNumberAndStateNew(t *testing.T) {
	svc, _, users, categories, audits, clock := newTicketService()
	cat := categories.seed("Bugs")
	user := users.seed("Ana", "ana@example.com", true)
	actor := domain.User{Name: "Ada", Email: "ada@example.com"}

	ticket, err := svc.Create(context.Background(), actor, validCreateInput(cat.ID, ptr(user.ID)))
	if err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}
	if ticket.ID == 0 {
		t.Fatal("Create: ticket must receive an ID from the store")
	}
	if ticket.Number != 1 {
		t.Fatalf("Create: first ticket must be numbered 1, got %d", ticket.Number)
	}
	if ticket.State != domain.StateNew {
		t.Fatalf("Create: new ticket must start in state %q, got %q", domain.StateNew, ticket.State)
	}
	if !ticket.CreatedAt.Equal(clock.now) || !ticket.UpdatedAt.Equal(clock.now) {
		t.Fatalf("Create: timestamps must come from the injected clock, got created=%v updated=%v", ticket.CreatedAt, ticket.UpdatedAt)
	}
	if ticket.UserID == nil || *ticket.UserID != user.ID {
		t.Fatalf("Create: assigned user must be stored, got %v", ticket.UserID)
	}

	// MAX+1 numbering is a store concern (D8): the second ticket follows.
	second, err := svc.Create(context.Background(), actor, validCreateInput(cat.ID, nil))
	if err != nil {
		t.Fatalf("Create (second): unexpected error: %v", err)
	}
	if second.Number != 2 {
		t.Fatalf("Create: second ticket must be numbered 2 (MAX+1), got %d", second.Number)
	}

	// Creation is audited with the actor from the session.
	events, err := audits.ListByTicket(context.Background(), ticket.ID)
	if err != nil {
		t.Fatalf("ListByTicket: unexpected error: %v", err)
	}
	if len(events) != 1 || events[0].Action != domain.ActionCreated {
		t.Fatalf("Create: expected one created audit event, got %d events", len(events))
	}
	if events[0].Actor != actor.Name {
		t.Fatalf("Create: audit actor must come from the session, got %q", events[0].Actor)
	}
}

func TestCreateRejectsMissingTitle(t *testing.T) {
	svc, tickets, _, categories, audits, _ := newTicketService()
	cat := categories.seed("Bugs")
	actor := domain.User{Name: "Ada"}

	in := validCreateInput(cat.ID, nil)
	in.Title = "  "
	_, err := svc.Create(context.Background(), actor, in)

	var verr *domain.ValidationError
	if !errors.As(err, &verr) || verr.Field != "title" {
		t.Fatalf("Create: missing title must be a ValidationError on field title, got %v", err)
	}
	if len(tickets.tickets) != 0 {
		t.Fatal("Create: rejected ticket must not be stored")
	}
	if len(audits.events) != 0 {
		t.Fatal("Create: rejected ticket must not be audited")
	}
}

func TestCreateRejectsInactiveUserAssignment(t *testing.T) {
	svc, tickets, users, categories, audits, _ := newTicketService()
	cat := categories.seed("Bugs")
	inactive := users.seed("Ana", "ana@example.com", false)
	actor := domain.User{Name: "Ada"}

	_, err := svc.Create(context.Background(), actor, validCreateInput(cat.ID, ptr(inactive.ID)))

	var ierr *domain.InactiveUserError
	if !errors.As(err, &ierr) {
		t.Fatalf("Create: inactive assignment must be an InactiveUserError, got %v", err)
	}
	if len(tickets.tickets) != 0 {
		t.Fatal("Create: rejected ticket must not be stored")
	}
	if len(audits.events) != 0 {
		t.Fatal("Create: rejected ticket must not be audited")
	}
}

func TestCreateRejectsUnknownCategory(t *testing.T) {
	svc, tickets, _, _, audits, _ := newTicketService()
	actor := domain.User{Name: "Ada"}

	_, err := svc.Create(context.Background(), actor, validCreateInput(999, nil))

	var nerr *domain.NotFoundError
	if !errors.As(err, &nerr) || nerr.Kind != "category" {
		t.Fatalf("Create: unknown category must be a NotFoundError(kind=category), got %v", err)
	}
	if len(tickets.tickets) != 0 {
		t.Fatal("Create: rejected ticket must not be stored")
	}
	if len(audits.events) != 0 {
		t.Fatal("Create: rejected ticket must not be audited")
	}
}

func TestCreateRejectsUnknownAssignedUser(t *testing.T) {
	svc, tickets, _, categories, audits, _ := newTicketService()
	cat := categories.seed("Bugs")
	actor := domain.User{Name: "Ada"}

	_, err := svc.Create(context.Background(), actor, validCreateInput(cat.ID, ptr(int64(999))))

	var nerr *domain.NotFoundError
	if !errors.As(err, &nerr) || nerr.Kind != "user" {
		t.Fatalf("Create: unknown assigned user must be a NotFoundError(kind=user), got %v", err)
	}
	if len(tickets.tickets) != 0 {
		t.Fatal("Create: rejected ticket must not be stored")
	}
	if len(audits.events) != 0 {
		t.Fatal("Create: rejected ticket must not be audited")
	}
}

func TestCreateRejectsInvalidPriority(t *testing.T) {
	svc, tickets, _, categories, _, _ := newTicketService()
	cat := categories.seed("Bugs")
	actor := domain.User{Name: "Ada"}

	in := validCreateInput(cat.ID, nil)
	in.Priority = domain.Priority("urgent")
	_, err := svc.Create(context.Background(), actor, in)

	var ierr *domain.InvalidPriorityError
	if !errors.As(err, &ierr) {
		t.Fatalf("Create: invalid priority must be an InvalidPriorityError, got %v", err)
	}
	if len(tickets.tickets) != 0 {
		t.Fatal("Create: rejected ticket must not be stored")
	}
}

// seededTicket arranges a stored ticket (bypassing the service) for
// transition/update tests.
func seededTicket(store *fakeTicketStore, catID int64, state domain.State) domain.Ticket {
	return store.seed(domain.Ticket{
		Title:          "Seeded ticket",
		Description:    "",
		RequesterName:  "Bob",
		RequesterEmail: "bob@example.com",
		CategoryID:     catID,
		Priority:       domain.PriorityMedium,
		State:          state,
		CreatedAt:      fixedClock().now,
		UpdatedAt:      fixedClock().now,
	})
}

func TestTransitionAppliesAndAuditsWithSessionActor(t *testing.T) {
	svc, tickets, _, categories, audits, clock := newTicketService()
	cat := categories.seed("Bugs")
	ticket := seededTicket(tickets, cat.ID, domain.StateNew)
	actor := domain.User{Name: "Ada", Email: "ada@example.com"}
	clock.Advance(timeMinute)

	updated, err := svc.Transition(context.Background(), actor, ticket.ID, domain.StateInProgress, "")
	if err != nil {
		t.Fatalf("Transition: unexpected error: %v", err)
	}
	if updated.State != domain.StateInProgress {
		t.Fatalf("Transition: state must be in_progress, got %q", updated.State)
	}
	if !updated.UpdatedAt.Equal(clock.now) {
		t.Fatalf("Transition: updated_at must be refreshed, got %v want %v", updated.UpdatedAt, clock.now)
	}

	events, err := audits.ListByTicket(context.Background(), ticket.ID)
	if err != nil {
		t.Fatalf("ListByTicket: unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("Transition: exactly one audit event expected, got %d", len(events))
	}
	ev := events[0]
	if ev.Action != domain.ActionTransition {
		t.Fatalf("Transition: audit action must be %q, got %q", domain.ActionTransition, ev.Action)
	}
	if ev.Field == nil || *ev.Field != "state" {
		t.Fatalf("Transition: audit field must be %q, got %v", "state", ev.Field)
	}
	if ev.FromValue == nil || *ev.FromValue != string(domain.StateNew) ||
		ev.ToValue == nil || *ev.ToValue != string(domain.StateInProgress) {
		t.Fatalf("Transition: audit from/to must be new -> in_progress, got %v -> %v", ev.FromValue, ev.ToValue)
	}
	if ev.Actor != actor.Name {
		t.Fatalf("Transition: audit actor must come from the session, got %q", ev.Actor)
	}
}

func TestTransitionRejectsInvalidMoveWithoutMutations(t *testing.T) {
	svc, tickets, _, categories, audits, _ := newTicketService()
	cat := categories.seed("Bugs")
	ticket := seededTicket(tickets, cat.ID, domain.StateNew)
	actor := domain.User{Name: "Ada"}

	_, err := svc.Transition(context.Background(), actor, ticket.ID, domain.StateClosed, "")
	var terr *domain.InvalidTransitionError
	if !errors.As(err, &terr) {
		t.Fatalf("Transition: new -> closed must be an InvalidTransitionError, got %v", err)
	}

	stored, _ := tickets.GetByID(context.Background(), ticket.ID)
	if stored.State != domain.StateNew {
		t.Fatalf("Transition: rejected move must not change state, got %q", stored.State)
	}
	if len(audits.events) != 0 {
		t.Fatal("Transition: rejected move must not be audited")
	}
}

func TestTransitionReopenClosedRequiresReason(t *testing.T) {
	svc, tickets, _, categories, audits, _ := newTicketService()
	cat := categories.seed("Bugs")
	ticket := seededTicket(tickets, cat.ID, domain.StateClosed)
	actor := domain.User{Name: "Ada"}

	_, err := svc.Transition(context.Background(), actor, ticket.ID, domain.StateInProgress, "")
	var rerr *domain.ReopenReasonRequiredError
	if !errors.As(err, &rerr) {
		t.Fatalf("Transition: closed reopen without reason must be a ReopenReasonRequiredError, got %v", err)
	}
	if len(audits.events) != 0 {
		t.Fatal("Transition: rejected reopen must not be audited")
	}
}

func TestTransitionReopenClosedWithReasonRecordsNote(t *testing.T) {
	svc, tickets, _, categories, audits, _ := newTicketService()
	cat := categories.seed("Bugs")
	ticket := seededTicket(tickets, cat.ID, domain.StateClosed)
	actor := domain.User{Name: "Ada"}

	updated, err := svc.Transition(context.Background(), actor, ticket.ID, domain.StateInProgress, "customer came back")
	if err != nil {
		t.Fatalf("Transition: closed reopen with reason: unexpected error: %v", err)
	}
	if updated.State != domain.StateInProgress {
		t.Fatalf("Transition: reopened ticket must be in_progress, got %q", updated.State)
	}
	events, _ := audits.ListByTicket(context.Background(), ticket.ID)
	if len(events) != 1 || events[0].Note == nil || *events[0].Note != "customer came back" {
		t.Fatalf("Transition: reopen reason must be recorded in the audit note, got %+v", events)
	}
}

func TestTransitionUnknownTicket(t *testing.T) {
	svc, _, _, _, _, _ := newTicketService()
	actor := domain.User{Name: "Ada"}

	_, err := svc.Transition(context.Background(), actor, 4242, domain.StateInProgress, "")
	var nerr *domain.NotFoundError
	if !errors.As(err, &nerr) || nerr.Kind != "ticket" {
		t.Fatalf("Transition: unknown ticket must be a NotFoundError(kind=ticket), got %v", err)
	}
}

func TestUpdateAppliesChangedFieldsAndAuditsEach(t *testing.T) {
	svc, tickets, _, categories, audits, clock := newTicketService()
	cat := categories.seed("Bugs")
	ticket := seededTicket(tickets, cat.ID, domain.StateInProgress)
	actor := domain.User{Name: "Ada"}
	clock.Advance(timeMinute)

	resolvedAt := clock.now
	ticket.ResolvedAt = &resolvedAt
	tickets.Update(context.Background(), &ticket)

	newTitle := "Fix login redirect (v2)"
	newPriority := domain.PriorityCritical
	updated, err := svc.Update(context.Background(), actor, ticket.ID, domain.TicketUpdate{
		Title:    &newTitle,
		Priority: &newPriority,
	})
	if err != nil {
		t.Fatalf("Update: unexpected error: %v", err)
	}
	if updated.Title != newTitle || updated.Priority != newPriority {
		t.Fatalf("Update: changed fields must be applied, got title=%q priority=%q", updated.Title, updated.Priority)
	}
	if !updated.UpdatedAt.Equal(clock.now) {
		t.Fatalf("Update: updated_at must be refreshed, got %v want %v", updated.UpdatedAt, clock.now)
	}
	// Lifecycle timestamps belong to the state machine alone.
	if updated.ResolvedAt == nil || !updated.ResolvedAt.Equal(resolvedAt) {
		t.Fatal("Update: resolved_at must remain untouched by field edits")
	}

	events, _ := audits.ListByTicket(context.Background(), ticket.ID)
	if len(events) != 2 {
		t.Fatalf("Update: one audit event per changed field expected (2), got %d", len(events))
	}
	if events[0].Field == nil || *events[0].Field != "title" || events[1].Field == nil || *events[1].Field != "priority" {
		t.Fatalf("Update: audit fields must be title then priority, got %v / %v", events[0].Field, events[1].Field)
	}
	if events[0].Actor != actor.Name || events[1].Actor != actor.Name {
		t.Fatalf("Update: audit actors must come from the session, got %q / %q", events[0].Actor, events[1].Actor)
	}
}

func TestUpdateRejectsInvalidPriorityWithoutChanges(t *testing.T) {
	svc, tickets, _, categories, audits, _ := newTicketService()
	cat := categories.seed("Bugs")
	ticket := seededTicket(tickets, cat.ID, domain.StateInProgress)
	actor := domain.User{Name: "Ada"}
	before, _ := tickets.GetByID(context.Background(), ticket.ID)

	bad := domain.Priority("urgent")
	_, err := svc.Update(context.Background(), actor, ticket.ID, domain.TicketUpdate{Priority: &bad})
	var ierr *domain.InvalidPriorityError
	if !errors.As(err, &ierr) {
		t.Fatalf("Update: invalid priority must be an InvalidPriorityError, got %v", err)
	}

	after, _ := tickets.GetByID(context.Background(), ticket.ID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("Update: rejected edit must leave the stored ticket untouched")
	}
	if len(audits.events) != 0 {
		t.Fatal("Update: rejected edit must not be audited")
	}
}

func TestUpdateValidatesCategoryAndAssignedUser(t *testing.T) {
	svc, tickets, _, categories, audits, _ := newTicketService()
	cat := categories.seed("Bugs")
	inactive := newFakeUserStore().seed("Ana", "ana@example.com", false)
	ticket := seededTicket(tickets, cat.ID, domain.StateInProgress)
	actor := domain.User{Name: "Ada"}

	// Category edits validate existence as in creation.
	missingCat := int64(999)
	if _, err := svc.Update(context.Background(), actor, ticket.ID, domain.TicketUpdate{CategoryID: &missingCat}); err == nil {
		t.Fatal("Update: unknown category must be rejected")
	}
	// User edits validate existence AND active state as in creation.
	if _, err := svc.Update(context.Background(), actor, ticket.ID, domain.TicketUpdate{UserID: &inactive.ID}); err == nil {
		t.Fatal("Update: assigning an inactive user must be rejected")
	}
	if len(audits.events) != 0 {
		t.Fatal("Update: rejected edits must not be audited")
	}
}

func TestEveryMutationAuditedInOccurrenceOrder(t *testing.T) {
	svc, _, users, categories, audits, clock := newTicketService()
	cat := categories.seed("Bugs")
	user := users.seed("Ana", "ana@example.com", true)
	actor := domain.User{Name: "Ada", Email: "ada@example.com"}

	// GIVEN a ticket (audit-log scenario: one transition and two field edits).
	ticket, err := svc.Create(context.Background(), actor, validCreateInput(cat.ID, ptr(user.ID)))
	if err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}
	clock.Advance(timeMinute)
	if _, err := svc.Transition(context.Background(), actor, ticket.ID, domain.StateInProgress, ""); err != nil {
		t.Fatalf("Transition: unexpected error: %v", err)
	}
	clock.Advance(timeMinute)
	newTitle := "Edited title"
	newPriority := domain.PriorityCritical
	if _, err := svc.Update(context.Background(), actor, ticket.ID, domain.TicketUpdate{
		Title:    &newTitle,
		Priority: &newPriority,
	}); err != nil {
		t.Fatalf("Update: unexpected error: %v", err)
	}

	// THEN the mutation events exist in occurrence order, each with the
	// session actor. (Creation appended its own created event first.)
	events, _ := audits.ListByTicket(context.Background(), ticket.ID)
	if len(events) < 3 {
		t.Fatalf("audit trail: expected at least 3 events, got %d", len(events))
	}
	mutations := events[len(events)-3:]
	want := []string{domain.ActionTransition, domain.ActionUpdate, domain.ActionUpdate}
	for i, ev := range mutations {
		if ev.Action != want[i] {
			t.Fatalf("audit trail: event %d must be %q, got %q", i, want[i], ev.Action)
		}
		if ev.Actor != actor.Name {
			t.Fatalf("audit trail: event %d actor must come from the session, got %q", i, ev.Actor)
		}
	}
}

func TestGetByID(t *testing.T) {
	svc, tickets, _, categories, _, _ := newTicketService()
	cat := categories.seed("Bugs")
	ticket := seededTicket(tickets, cat.ID, domain.StateNew)

	got, err := svc.GetByID(context.Background(), ticket.ID)
	if err != nil {
		t.Fatalf("GetByID: unexpected error: %v", err)
	}
	if got.ID != ticket.ID || got.Title != ticket.Title {
		t.Fatalf("GetByID: must return the stored ticket, got %+v", got)
	}

	_, err = svc.GetByID(context.Background(), 4242)
	var nerr *domain.NotFoundError
	if !errors.As(err, &nerr) || nerr.Kind != "ticket" {
		t.Fatalf("GetByID: unknown id must be a NotFoundError(kind=ticket), got %v", err)
	}
}
