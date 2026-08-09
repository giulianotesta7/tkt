package application_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// ticketHarness wires a TicketService against ONE coherent set of fakes —
// including the unit-of-work (C1) and the view builder (C2) — so mutation
// tests and read tests share the same stores and cannot silently diverge.
type ticketHarness struct {
	svc        *application.TicketService
	tickets    *fakeTicketStore
	users      *fakeUserStore
	categories *fakeCategoryStore
	comments   *fakeCommentStore
	audits     *fakeAuditStore
	tx         *fakeUnitOfWork
	clock      *fakeClock
}

// newTicketHarness returns a service whose every port reads the same fakes:
// the ticket store, the audit store, and the unit-of-work wrapping both.
func newTicketHarness() *ticketHarness {
	clock := fixedClock()
	users := newFakeUserStore()
	categories := newFakeCategoryStore()
	tickets := newFakeTicketStore()
	comments := newFakeCommentStore()
	audits := newFakeAuditStore()
	tx := newFakeUnitOfWork(tickets, audits)
	builder := application.NewViewBuilder(tickets, users, categories, comments, audits)
	svc := application.NewTicketService(tickets, users, categories, tx, builder, clock)
	return &ticketHarness{
		svc: svc, tickets: tickets, users: users, categories: categories,
		comments: comments, audits: audits, tx: tx, clock: clock,
	}
}

func validCreateInput(catID int64, userID *int64) application.CreateTicketInput {
	return application.CreateTicketInput{
		Title:       "Fix login redirect",
		Description: "After login the user lands on the wrong page",
		CategoryID:  catID,
		UserID:      userID,
		Priority:    domain.PriorityHigh,
	}
}

func TestCreateStoresTicketWithNumberAndStateNew(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	user := h.users.seed("Ana", "ana@example.com", true)
	actor := domain.User{Name: "Ada", Email: "ada@example.com"}

	ticket, err := h.svc.Create(context.Background(), actor, validCreateInput(cat.ID, ptr(user.ID)))
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
	if !ticket.CreatedAt.Equal(h.clock.now) || !ticket.UpdatedAt.Equal(h.clock.now) {
		t.Fatalf("Create: timestamps must come from the injected clock, got created=%v updated=%v", ticket.CreatedAt, ticket.UpdatedAt)
	}
	if ticket.UserID == nil || *ticket.UserID != user.ID {
		t.Fatalf("Create: assigned user must be stored, got %v", ticket.UserID)
	}

	// MAX+1 numbering is a store concern (D8): the second ticket follows.
	second, err := h.svc.Create(context.Background(), actor, validCreateInput(cat.ID, nil))
	if err != nil {
		t.Fatalf("Create (second): unexpected error: %v", err)
	}
	if second.Number != 2 {
		t.Fatalf("Create: second ticket must be numbered 2 (MAX+1), got %d", second.Number)
	}

	// Creation is audited with the actor from the session.
	events, err := h.audits.ListByTicket(context.Background(), ticket.ID)
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
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	actor := domain.User{Name: "Ada"}

	in := validCreateInput(cat.ID, nil)
	in.Title = "  "
	_, err := h.svc.Create(context.Background(), actor, in)

	var verr *domain.ValidationError
	if !errors.As(err, &verr) || verr.Field != "title" {
		t.Fatalf("Create: missing title must be a ValidationError on field title, got %v", err)
	}
	if len(h.tickets.tickets) != 0 {
		t.Fatal("Create: rejected ticket must not be stored")
	}
	if len(h.audits.events) != 0 {
		t.Fatal("Create: rejected ticket must not be audited")
	}
}

func TestCreateRejectsInactiveUserAssignment(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	inactive := h.users.seed("Ana", "ana@example.com", false)
	actor := domain.User{Name: "Ada"}

	_, err := h.svc.Create(context.Background(), actor, validCreateInput(cat.ID, ptr(inactive.ID)))

	var ierr *domain.InactiveUserError
	if !errors.As(err, &ierr) {
		t.Fatalf("Create: inactive assignment must be an InactiveUserError, got %v", err)
	}
	if len(h.tickets.tickets) != 0 {
		t.Fatal("Create: rejected ticket must not be stored")
	}
	if len(h.audits.events) != 0 {
		t.Fatal("Create: rejected ticket must not be audited")
	}
}

func TestCreateRejectsUnknownCategory(t *testing.T) {
	h := newTicketHarness()
	actor := domain.User{Name: "Ada"}

	_, err := h.svc.Create(context.Background(), actor, validCreateInput(999, nil))

	var nerr *domain.NotFoundError
	if !errors.As(err, &nerr) || nerr.Kind != "category" {
		t.Fatalf("Create: unknown category must be a NotFoundError(kind=category), got %v", err)
	}
	if len(h.tickets.tickets) != 0 {
		t.Fatal("Create: rejected ticket must not be stored")
	}
	if len(h.audits.events) != 0 {
		t.Fatal("Create: rejected ticket must not be audited")
	}
}

func TestCreateRejectsUnknownAssignedUser(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	actor := domain.User{Name: "Ada"}

	_, err := h.svc.Create(context.Background(), actor, validCreateInput(cat.ID, ptr(int64(999))))

	var nerr *domain.NotFoundError
	if !errors.As(err, &nerr) || nerr.Kind != "user" {
		t.Fatalf("Create: unknown assigned user must be a NotFoundError(kind=user), got %v", err)
	}
	if len(h.tickets.tickets) != 0 {
		t.Fatal("Create: rejected ticket must not be stored")
	}
	if len(h.audits.events) != 0 {
		t.Fatal("Create: rejected ticket must not be audited")
	}
}

func TestCreateRejectsInvalidPriority(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	actor := domain.User{Name: "Ada"}

	in := validCreateInput(cat.ID, nil)
	in.Priority = domain.Priority("urgent")
	_, err := h.svc.Create(context.Background(), actor, in)

	var ierr *domain.InvalidPriorityError
	if !errors.As(err, &ierr) {
		t.Fatalf("Create: invalid priority must be an InvalidPriorityError, got %v", err)
	}
	if len(h.tickets.tickets) != 0 {
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
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	ticket := seededTicket(h.tickets, cat.ID, domain.StateNew)
	actor := domain.User{Name: "Ada", Email: "ada@example.com"}
	h.clock.Advance(timeMinute)

	updated, err := h.svc.Transition(context.Background(), actor, ticket.ID, domain.StateInProgress, "")
	if err != nil {
		t.Fatalf("Transition: unexpected error: %v", err)
	}
	if updated.State != domain.StateInProgress {
		t.Fatalf("Transition: state must be in_progress, got %q", updated.State)
	}
	if !updated.UpdatedAt.Equal(h.clock.now) {
		t.Fatalf("Transition: updated_at must be refreshed, got %v want %v", updated.UpdatedAt, h.clock.now)
	}

	events, err := h.audits.ListByTicket(context.Background(), ticket.ID)
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
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	ticket := seededTicket(h.tickets, cat.ID, domain.StateNew)
	actor := domain.User{Name: "Ada"}

	_, err := h.svc.Transition(context.Background(), actor, ticket.ID, domain.StateClosed, "")
	var terr *domain.InvalidTransitionError
	if !errors.As(err, &terr) {
		t.Fatalf("Transition: new -> closed must be an InvalidTransitionError, got %v", err)
	}

	stored, _ := h.tickets.GetByID(context.Background(), ticket.ID)
	if stored.State != domain.StateNew {
		t.Fatalf("Transition: rejected move must not change state, got %q", stored.State)
	}
	if len(h.audits.events) != 0 {
		t.Fatal("Transition: rejected move must not be audited")
	}
}

func TestTransitionReopenClosedRequiresReason(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	ticket := seededTicket(h.tickets, cat.ID, domain.StateClosed)
	actor := domain.User{Name: "Ada"}

	_, err := h.svc.Transition(context.Background(), actor, ticket.ID, domain.StateInProgress, "")
	var rerr *domain.ReopenReasonRequiredError
	if !errors.As(err, &rerr) {
		t.Fatalf("Transition: closed reopen without reason must be a ReopenReasonRequiredError, got %v", err)
	}
	if len(h.audits.events) != 0 {
		t.Fatal("Transition: rejected reopen must not be audited")
	}
}

func TestTransitionReopenClosedWithReasonRecordsNote(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	ticket := seededTicket(h.tickets, cat.ID, domain.StateClosed)
	actor := domain.User{Name: "Ada"}

	updated, err := h.svc.Transition(context.Background(), actor, ticket.ID, domain.StateInProgress, "customer came back")
	if err != nil {
		t.Fatalf("Transition: closed reopen with reason: unexpected error: %v", err)
	}
	if updated.State != domain.StateInProgress {
		t.Fatalf("Transition: reopened ticket must be in_progress, got %q", updated.State)
	}
	events, _ := h.audits.ListByTicket(context.Background(), ticket.ID)
	if len(events) != 1 || events[0].Note == nil || *events[0].Note != "customer came back" {
		t.Fatalf("Transition: reopen reason must be recorded in the audit note, got %+v", events)
	}
}

func TestTransitionUnknownTicket(t *testing.T) {
	h := newTicketHarness()
	actor := domain.User{Name: "Ada"}

	_, err := h.svc.Transition(context.Background(), actor, 4242, domain.StateInProgress, "")
	var nerr *domain.NotFoundError
	if !errors.As(err, &nerr) || nerr.Kind != "ticket" {
		t.Fatalf("Transition: unknown ticket must be a NotFoundError(kind=ticket), got %v", err)
	}
}

func TestUpdateAppliesChangedFieldsAndAuditsEach(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	ticket := seededTicket(h.tickets, cat.ID, domain.StateInProgress)
	actor := domain.User{Name: "Ada"}
	h.clock.Advance(timeMinute)

	resolvedAt := h.clock.now
	ticket.ResolvedAt = &resolvedAt
	h.tickets.Update(context.Background(), &ticket)

	newTitle := "Fix login redirect (v2)"
	newPriority := domain.PriorityCritical
	updated, err := h.svc.Update(context.Background(), actor, ticket.ID, domain.TicketUpdate{
		Title:    &newTitle,
		Priority: &newPriority,
	})
	if err != nil {
		t.Fatalf("Update: unexpected error: %v", err)
	}
	if updated.Title != newTitle || updated.Priority != newPriority {
		t.Fatalf("Update: changed fields must be applied, got title=%q priority=%q", updated.Title, updated.Priority)
	}
	if !updated.UpdatedAt.Equal(h.clock.now) {
		t.Fatalf("Update: updated_at must be refreshed, got %v want %v", updated.UpdatedAt, h.clock.now)
	}
	// Lifecycle timestamps belong to the state machine alone.
	if updated.ResolvedAt == nil || !updated.ResolvedAt.Equal(resolvedAt) {
		t.Fatal("Update: resolved_at must remain untouched by field edits")
	}

	events, _ := h.audits.ListByTicket(context.Background(), ticket.ID)
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
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	ticket := seededTicket(h.tickets, cat.ID, domain.StateInProgress)
	actor := domain.User{Name: "Ada"}
	before, _ := h.tickets.GetByID(context.Background(), ticket.ID)

	bad := domain.Priority("urgent")
	_, err := h.svc.Update(context.Background(), actor, ticket.ID, domain.TicketUpdate{Priority: &bad})
	var ierr *domain.InvalidPriorityError
	if !errors.As(err, &ierr) {
		t.Fatalf("Update: invalid priority must be an InvalidPriorityError, got %v", err)
	}

	after, _ := h.tickets.GetByID(context.Background(), ticket.ID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("Update: rejected edit must leave the stored ticket untouched")
	}
	if len(h.audits.events) != 0 {
		t.Fatal("Update: rejected edit must not be audited")
	}
}

func TestUpdateValidatesCategoryAndAssignedUser(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	// The inactive user MUST live in the same store the service reads from
	// (C3): seeding a fresh store fails the lookup before the active check.
	inactive := h.users.seed("Ana", "ana@example.com", false)
	ticket := seededTicket(h.tickets, cat.ID, domain.StateInProgress)
	actor := domain.User{Name: "Ada"}

	// Category edits validate existence as in creation.
	missingCat := int64(999)
	_, err := h.svc.Update(context.Background(), actor, ticket.ID, domain.TicketUpdate{CategoryID: &missingCat})
	var nerr *domain.NotFoundError
	if !errors.As(err, &nerr) || nerr.Kind != "category" {
		t.Fatalf("Update: unknown category must be a NotFoundError(kind=category), got %v", err)
	}
	// User edits validate existence AND active state as in creation: the
	// inactive user reaches the active-state check, so the failure MUST be
	// an InactiveUserError, not a missing-user error.
	_, err = h.svc.Update(context.Background(), actor, ticket.ID, domain.TicketUpdate{UserID: &inactive.ID})
	var ierr *domain.InactiveUserError
	if !errors.As(err, &ierr) {
		t.Fatalf("Update: assigning an inactive user must be an InactiveUserError, got %v", err)
	}
	if len(h.audits.events) != 0 {
		t.Fatal("Update: rejected edits must not be audited")
	}
}

func TestEveryMutationAuditedInOccurrenceOrder(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	user := h.users.seed("Ana", "ana@example.com", true)
	actor := domain.User{Name: "Ada", Email: "ada@example.com"}

	// GIVEN a ticket (audit-log scenario: one transition and two field edits).
	ticket, err := h.svc.Create(context.Background(), actor, validCreateInput(cat.ID, ptr(user.ID)))
	if err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}
	h.clock.Advance(timeMinute)
	if _, err := h.svc.Transition(context.Background(), actor, ticket.ID, domain.StateInProgress, ""); err != nil {
		t.Fatalf("Transition: unexpected error: %v", err)
	}
	h.clock.Advance(timeMinute)
	newTitle := "Edited title"
	newPriority := domain.PriorityCritical
	if _, err := h.svc.Update(context.Background(), actor, ticket.ID, domain.TicketUpdate{
		Title:    &newTitle,
		Priority: &newPriority,
	}); err != nil {
		t.Fatalf("Update: unexpected error: %v", err)
	}

	// THEN the mutation events exist in occurrence order, each with the
	// session actor. (Creation appended its own created event first.)
	events, _ := h.audits.ListByTicket(context.Background(), ticket.ID)
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

// TestCreateRollsBackTicketWhenAuditAppendFails proves the no-silent-mutations
// atomicity contract (C1): when the audit half of the unit-of-work fails, the
// ticket mutation must NOT be persisted and the failure must reach the caller.
func TestCreateRollsBackTicketWhenAuditAppendFails(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	user := h.users.seed("Ana", "ana@example.com", true)
	actor := domain.User{Name: "Ada"}
	h.tx.failAuditAppend = true

	_, err := h.svc.Create(context.Background(), actor, validCreateInput(cat.ID, ptr(user.ID)))
	if !errors.Is(err, errAuditAppendFailed) {
		t.Fatalf("Create: audit append failure must propagate to the caller, got %v", err)
	}
	if len(h.tickets.tickets) != 0 {
		t.Fatal("Create: ticket must NOT be persisted when the audit append fails")
	}
	if len(h.audits.events) != 0 {
		t.Fatal("Create: no audit event may be persisted when the append fails")
	}
}

// TestTransitionRollsBackStateWhenAuditAppendFails proves the rollback half
// of the unit-of-work on the Update path (C1): a failed audit append must
// restore the pre-transition ticket state.
func TestTransitionRollsBackStateWhenAuditAppendFails(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	ticket := seededTicket(h.tickets, cat.ID, domain.StateNew)
	actor := domain.User{Name: "Ada"}
	h.tx.failAuditAppend = true

	_, err := h.svc.Transition(context.Background(), actor, ticket.ID, domain.StateInProgress, "")
	if !errors.Is(err, errAuditAppendFailed) {
		t.Fatalf("Transition: audit append failure must propagate to the caller, got %v", err)
	}
	stored, err := h.tickets.GetByID(context.Background(), ticket.ID)
	if err != nil {
		t.Fatalf("GetByID after rollback: unexpected error: %v", err)
	}
	if stored.State != domain.StateNew {
		t.Fatalf("Transition: rolled-back ticket must keep state %q, got %q", domain.StateNew, stored.State)
	}
	if len(h.audits.events) != 0 {
		t.Fatal("Transition: no audit event may be persisted when the append fails")
	}
}

// TestGetByIDReturnsComposedView covers the read contract (C2): GetByID
// returns the composed TicketView — ticket, resolved category name, resolved
// assigned-user name, and the chronological comment timeline — not the raw
// domain aggregate.
func TestGetByIDReturnsComposedView(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	user := h.users.seed("Ana", "ana@example.com", true)
	ticket := seededTicket(h.tickets, cat.ID, domain.StateNew)
	ticket.UserID = ptr(user.ID)
	if err := h.tickets.Update(context.Background(), &ticket); err != nil {
		t.Fatalf("seed assignment: unexpected error: %v", err)
	}
	for _, body := range []string{"first", "second"} {
		if err := h.comments.Add(context.Background(), &domain.Comment{
			TicketID: ticket.ID, Author: "Ada", Body: body, CreatedAt: h.clock.Now(),
		}); err != nil {
			t.Fatalf("seed comment %q: unexpected error: %v", body, err)
		}
	}

	view, err := h.svc.GetByID(context.Background(), ticket.ID)
	if err != nil {
		t.Fatalf("GetByID: unexpected error: %v", err)
	}
	if view.Ticket == nil || view.Ticket.ID != ticket.ID || view.Ticket.Title != ticket.Title {
		t.Fatalf("GetByID: view must carry the stored ticket, got %+v", view.Ticket)
	}
	if view.Category == nil || view.Category.Name != "Bugs" {
		t.Fatalf("GetByID: view must resolve the category name, got %+v", view.Category)
	}
	if view.AssignedUser == nil || view.AssignedUser.Name != "Ana" {
		t.Fatalf("GetByID: view must resolve the assigned user name, got %+v", view.AssignedUser)
	}
	if len(view.Comments) != 2 || view.Comments[0].Body != "first" || view.Comments[1].Body != "second" {
		t.Fatalf("GetByID: view must carry the comment timeline in creation order, got %+v", view.Comments)
	}

	_, err = h.svc.GetByID(context.Background(), 4242)
	var nerr *domain.NotFoundError
	if !errors.As(err, &nerr) || nerr.Kind != "ticket" {
		t.Fatalf("GetByID: unknown id must be a NotFoundError(kind=ticket), got %v", err)
	}
}
