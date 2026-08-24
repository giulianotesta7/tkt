package application_test

import (
	"context"
	"errors"
	"reflect"
	"strconv"
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
	builder := application.NewViewBuilder(tickets, users, categories, comments, audits, newFakeDeskStore())
	svc := application.NewTicketService(tickets, users, categories, tx, builder, clock)
	return &ticketHarness{
		svc: svc, tickets: tickets, users: users, categories: categories,
		comments: comments, audits: audits, tx: tx, clock: clock,
	}
}

func validCreateInput(catID int64) application.CreateTicketInput {
	return application.CreateTicketInput{
		Title:       "Fix login redirect",
		Description: "After login the user lands on the wrong page",
		CategoryID:  catID,
		Priority:    domain.PriorityHigh,
	}
}

func TestCreateStoresTicketWithNumberAndStateNew(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	actor := domain.User{Name: "Ada", Email: "ada@example.com", Role: domain.RoleAdmin}

	ticket, err := h.svc.Create(context.Background(), actor, validCreateInput(cat.ID))
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
	if ticket.UserID != nil {
		t.Fatalf("Create: every ticket starts unassigned (Amendment 2), got assignee %v", *ticket.UserID)
	}

	// MAX+1 numbering is a store concern (D8): the second ticket follows.
	second, err := h.svc.Create(context.Background(), actor, validCreateInput(cat.ID))
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

// TestCreateStoresSessionRequester proves the creating session user is
// persisted as the immutable requester_user_id and a user-role actor's
// ticket starts unassigned (ticket-access spec: creator persisted, assignee
// empty).
func TestCreateStoresSessionRequester(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	actor := domain.User{ID: 7, Name: "Ada", Email: "ada@example.com", Role: domain.RoleUser}

	ticket, err := h.svc.Create(context.Background(), actor, validCreateInput(cat.ID))
	if err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}
	if ticket.RequesterUserID == nil || *ticket.RequesterUserID != actor.ID {
		t.Fatalf("Create: requester_user_id = %v, want session user id %d", ticket.RequesterUserID, actor.ID)
	}
	if ticket.UserID != nil {
		t.Fatalf("Create: a user-role actor's ticket must start unassigned, got assignee %d", *ticket.UserID)
	}
}

// TestCreateAlwaysStartsUnassigned proves creation structurally stamps an
// EMPTY assignee for EVERY role (Amendment 2): CreateTicketInput carries no
// assignee at all, so no application path can smuggle a creation-time one.
// Person assignment happens only later through the audited Assign flow.
func TestCreateAlwaysStartsUnassigned(t *testing.T) {
	for _, role := range []domain.Role{domain.RoleUser, domain.RoleAgent, domain.RoleAdmin, domain.RoleRoot} {
		h := newTicketHarness()
		cat := h.categories.seed("Bugs")
		actor := domain.User{ID: 9, Name: "Beto", Email: "beto@example.com", Role: role}

		ticket, err := h.svc.Create(context.Background(), actor, validCreateInput(cat.ID))
		if err != nil {
			t.Fatalf("Create (role %s): unexpected error: %v", role, err)
		}
		if ticket.UserID != nil {
			t.Fatalf("Create (role %s): ticket must start unassigned, got assignee %v", role, *ticket.UserID)
		}
		if ticket.RequesterUserID == nil || *ticket.RequesterUserID != actor.ID {
			t.Fatalf("Create (role %s): requester_user_id = %v, want session user id %d", role, ticket.RequesterUserID, actor.ID)
		}
	}
}

func TestCreateRejectsMissingTitle(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	actor := domain.User{Name: "Ada", Role: domain.RoleAdmin}

	in := validCreateInput(cat.ID)
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

func TestCreateRejectsUnknownCategory(t *testing.T) {
	h := newTicketHarness()
	actor := domain.User{Name: "Ada", Role: domain.RoleAdmin}

	_, err := h.svc.Create(context.Background(), actor, validCreateInput(999))

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

func TestCreateRejectsInvalidPriority(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	actor := domain.User{Name: "Ada", Role: domain.RoleAdmin}

	in := validCreateInput(cat.ID)
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

// --- S4: assignment (ticket-access-assignment "Person-Only Assignment") ---

// TestAssignInitialAssignmentNoReasonRequired proves the initial assignment
// (unassigned → person) succeeds WITHOUT any reason and records an audit
// event with the session actor as actor (spec: "Initial assignment without
// reason"). An agent may claim an unassigned ticket (design: "Agents may
// claim unassigned→self").
func TestAssignInitialAssignmentNoReasonRequired(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	ticket := seededTicket(h.tickets, cat.ID, domain.StateNew)
	// The assignee is a real stored user: Assign validates the target
	// through the user store, so the self-assign target must exist there.
	agent := h.users.seedRole("Beto", "beto@example.com", domain.RoleAgent, true)
	actor := domain.User{ID: agent.ID, Name: agent.Name, Email: agent.Email, Role: domain.RoleAgent}
	h.clock.Advance(timeMinute)

	updated, err := h.svc.Assign(context.Background(), actor, ticket.ID, ptr(agent.ID), "")
	if err != nil {
		t.Fatalf("Assign: initial self-assignment must not require a reason, got %v", err)
	}
	if updated.UserID == nil || *updated.UserID != agent.ID {
		t.Fatalf("Assign: assignee = %v, want %d", updated.UserID, agent.ID)
	}
	if !updated.UpdatedAt.Equal(h.clock.now) {
		t.Fatalf("Assign: updated_at must be refreshed, got %v want %v", updated.UpdatedAt, h.clock.now)
	}

	events, _ := h.audits.ListByTicket(context.Background(), ticket.ID)
	if len(events) != 1 {
		t.Fatalf("Assign: exactly one audit event expected, got %d", len(events))
	}
	ev := events[0]
	if ev.Action != domain.ActionUpdate || ev.Field == nil || *ev.Field != "user" {
		t.Fatalf("Assign: audit must be an update on field user, got %+v", ev)
	}
	if ev.FromValue == nil || *ev.FromValue != "" || ev.ToValue == nil || *ev.ToValue != strconv.FormatInt(agent.ID, 10) {
		t.Fatalf("Assign: audit from/to = %v -> %v, want unassigned -> %d", ev.FromValue, ev.ToValue, agent.ID)
	}
	if ev.Reason != nil {
		t.Fatalf("Assign: initial assignment must not record a reason, got %q", *ev.Reason)
	}
	if ev.Actor != agent.Name {
		t.Fatalf("Assign: audit actor must be the session actor, got %q", ev.Actor)
	}
	if ev.ActorUserID == nil || *ev.ActorUserID != agent.ID {
		t.Fatalf("Assign: audit ActorUserID = %v, want %d", ev.ActorUserID, agent.ID)
	}
}

// TestAssignReassignRequiresReason proves a reassignment (person A → person
// B) is rejected WITHOUT a non-empty reason and succeeds with one, which is
// recorded in the audit event with the session actor (spec: "Reassignment
// requires reason"; approved decision: reason required only for
// reassignment, never for the initial assignment).
func TestAssignReassignRequiresReason(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	agentA := h.users.seedRole("Ana", "ana@example.com", domain.RoleAgent, true)
	agentB := h.users.seedRole("Beto", "beto@example.com", domain.RoleAgent, true)
	ticket := seededTicket(h.tickets, cat.ID, domain.StateNew)
	ticket.UserID = ptr(agentA.ID)
	if err := h.tickets.Update(context.Background(), &ticket); err != nil {
		t.Fatalf("seed assignment: %v", err)
	}

	// Reassignment without a reason: rejected, nothing changes.
	_, err := h.svc.Assign(context.Background(), domain.User{ID: agentA.ID, Name: agentA.Name, Role: domain.RoleAgent}, ticket.ID, ptr(agentB.ID), "   ")
	var rerr *domain.ReassignReasonRequiredError
	if !errors.As(err, &rerr) {
		t.Fatalf("Assign: reassign without reason must be a ReassignReasonRequiredError, got %v", err)
	}
	stored, _ := h.tickets.GetByID(context.Background(), ticket.ID, application.TicketQuery{Scope: application.ScopeAll})
	if stored.UserID == nil || *stored.UserID != agentA.ID {
		t.Fatalf("Assign: rejected reassignment must keep assignee A, got %v", stored.UserID)
	}
	if len(h.audits.events) != 0 {
		t.Fatal("Assign: rejected reassignment must not be audited")
	}

	// Reassignment with a reason: succeeds, reason + session actor recorded.
	updated, err := h.svc.Assign(context.Background(), domain.User{ID: agentA.ID, Name: agentA.Name, Role: domain.RoleAgent}, ticket.ID, ptr(agentB.ID), "handoff to second-line")
	if err != nil {
		t.Fatalf("Assign: reassign with reason: unexpected error: %v", err)
	}
	if updated.UserID == nil || *updated.UserID != agentB.ID {
		t.Fatalf("Assign: reassigned assignee = %v, want %d", updated.UserID, agentB.ID)
	}
	events, _ := h.audits.ListByTicket(context.Background(), ticket.ID)
	if len(events) != 1 {
		t.Fatalf("Assign: one audit event expected for the reassignment, got %d", len(events))
	}
	if events[0].Reason == nil || *events[0].Reason != "handoff to second-line" {
		t.Fatalf("Assign: reason must be recorded in the audit event, got %v", events[0].Reason)
	}
	if events[0].Actor != agentA.Name || events[0].ActorUserID == nil || *events[0].ActorUserID != agentA.ID {
		t.Fatalf("Assign: audit actor must be the session actor, got %q / %v", events[0].Actor, events[0].ActorUserID)
	}
}

// TestAssignSameAssigneeIsNoop proves assigning the ticket to its current
// assignee changes nothing and mints no audit event.
func TestAssignSameAssigneeIsNoop(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	agent := h.users.seedRole("Ana", "ana@example.com", domain.RoleAgent, true)
	ticket := seededTicket(h.tickets, cat.ID, domain.StateNew)
	ticket.UserID = ptr(agent.ID)
	if err := h.tickets.Update(context.Background(), &ticket); err != nil {
		t.Fatalf("seed assignment: %v", err)
	}
	before, _ := h.tickets.GetByID(context.Background(), ticket.ID, application.TicketQuery{Scope: application.ScopeAll})

	updated, err := h.svc.Assign(context.Background(), domain.User{ID: agent.ID, Name: agent.Name, Role: domain.RoleAgent}, ticket.ID, ptr(agent.ID), "")
	if err != nil {
		t.Fatalf("Assign: same assignee must be a no-op, got %v", err)
	}
	if updated.UserID == nil || *updated.UserID != agent.ID {
		t.Fatalf("Assign: assignee must stay %d, got %v", agent.ID, updated.UserID)
	}
	if !updated.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatal("Assign: no-op must not refresh updated_at")
	}
	if len(h.audits.events) != 0 {
		t.Fatal("Assign: no-op must not be audited")
	}
}

// TestAssignUserRoleCannotAssign proves role user cannot assign or change
// assignment (spec: "User role cannot assign").
func TestAssignUserRoleCannotAssign(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	ticket := seededTicket(h.tickets, cat.ID, domain.StateNew)
	user := domain.User{ID: 41, Name: "Ula", Role: domain.RoleUser}

	_, err := h.svc.Assign(context.Background(), user, ticket.ID, ptr(int64(1)), "")
	var verr *domain.ValidationError
	if !errors.As(err, &verr) || verr.Field != "user" || verr.Message != domain.ErrMsgUserRoleCannotAssign {
		t.Fatalf("Assign: user-role actor must be denied with ErrMsgUserRoleCannotAssign, got %v", err)
	}
	if len(h.audits.events) != 0 {
		t.Fatal("Assign: denied actor must not be audited")
	}
}

// TestAssignTargetMustBeAgentPlus proves the assignment target must be an
// active agent-plus person (spec: "Assignment target must be agent-plus"):
// an active user-role account is rejected, admin/root targets are accepted.
func TestAssignTargetMustBeAgentPlus(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	ticket := seededTicket(h.tickets, cat.ID, domain.StateNew)
	actor := domain.User{ID: 51, Name: "Ada", Role: domain.RoleAdmin}
	userRole := h.users.seedRole("Ula", "ula@example.com", domain.RoleUser, true)
	adminTarget := h.users.seedRole("Cami", "cami@example.com", domain.RoleAdmin, true)

	_, err := h.svc.Assign(context.Background(), actor, ticket.ID, ptr(userRole.ID), "")
	var verr *domain.ValidationError
	if !errors.As(err, &verr) || verr.Field != "user" || verr.Message != domain.ErrMsgAssignTargetRole {
		t.Fatalf("Assign: user-role target must be rejected with ErrMsgAssignTargetRole, got %v", err)
	}
	stored, _ := h.tickets.GetByID(context.Background(), ticket.ID, application.TicketQuery{Scope: application.ScopeAll})
	if stored.UserID != nil {
		t.Fatalf("Assign: rejected target must leave the ticket unassigned, got %v", stored.UserID)
	}

	// Triangulation: an admin target is a legal assignment.
	updated, err := h.svc.Assign(context.Background(), actor, ticket.ID, ptr(adminTarget.ID), "")
	if err != nil {
		t.Fatalf("Assign: admin target must be assignable, got %v", err)
	}
	if updated.UserID == nil || *updated.UserID != adminTarget.ID {
		t.Fatalf("Assign: assignee = %v, want admin %d", updated.UserID, adminTarget.ID)
	}
}

// TestAssignTargetInactive proves an inactive target is rejected with the
// inactive-user error (user-management spec: deactivated users cannot be
// assigned to new tickets).
func TestAssignTargetInactive(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	ticket := seededTicket(h.tickets, cat.ID, domain.StateNew)
	actor := domain.User{ID: 61, Name: "Ada", Role: domain.RoleAdmin}
	inactive := h.users.seedRole("Noa", "noa@example.com", domain.RoleAgent, false)

	_, err := h.svc.Assign(context.Background(), actor, ticket.ID, ptr(inactive.ID), "")
	var ierr *domain.InactiveUserError
	if !errors.As(err, &ierr) {
		t.Fatalf("Assign: inactive target must be an InactiveUserError, got %v", err)
	}
}

// TestAssignAgentMayOnlyClaimUnassignedTicketForSelf proves that an agent's
// initial assignment is a self-claim; only admin/root may assign another user.
func TestAssignAgentMayOnlyClaimUnassignedTicketForSelf(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	ticket := seededTicket(h.tickets, cat.ID, domain.StateNew)
	actor := h.users.seedRole("Agent A", "a@example.com", domain.RoleAgent, true)
	target := h.users.seedRole("Agent B", "b@example.com", domain.RoleAgent, true)

	if _, err := h.svc.Assign(context.Background(), actor, ticket.ID, ptr(target.ID), ""); err == nil {
		t.Fatal("agent must not initially assign an unassigned ticket to another agent")
	}
	stored, err := h.tickets.GetByID(context.Background(), ticket.ID, application.TicketQuery{Scope: application.ScopeAll})
	if err != nil || stored.UserID != nil {
		t.Fatalf("denied claim must leave ticket unassigned, ticket=%+v err=%v", stored, err)
	}
	if _, err := h.svc.Assign(context.Background(), actor, ticket.ID, ptr(actor.ID), ""); err != nil {
		t.Fatalf("agent self-claim: %v", err)
	}
}

// TestAssignAgentCannotReassignOthersTicket proves an agent can only claim
// an unassigned ticket or reassign THEIR OWN ticket: another agent's
// assigned ticket is out of scope (ErrNotFound, no existence leak — spec:
// "Agent transitions only assigned tickets"; the assignment read applies the
// same scoping discipline).
func TestAssignAgentCannotReassignOthersTicket(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	agentY := h.users.seedRole("Yara", "yara@example.com", domain.RoleAgent, true)
	agentX := domain.User{ID: 71, Name: "Xavi", Role: domain.RoleAgent}
	ticket := seededTicket(h.tickets, cat.ID, domain.StateNew)
	ticket.UserID = ptr(agentY.ID)
	if err := h.tickets.Update(context.Background(), &ticket); err != nil {
		t.Fatalf("seed assignment: %v", err)
	}

	_, err := h.svc.Assign(context.Background(), agentX, ticket.ID, ptr(agentX.ID), "")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Assign: agent X on agent Y's ticket must be ErrNotFound, got %v", err)
	}
	stored, _ := h.tickets.GetByID(context.Background(), ticket.ID, application.TicketQuery{Scope: application.ScopeAll})
	if stored.UserID == nil || *stored.UserID != agentY.ID {
		t.Fatalf("Assign: denied agent must not change the assignment, got %v", stored.UserID)
	}
}

// TestAssignUnassignClearsAssignment proves clearing the assignment (person
// → unassigned) is allowed without a reason and audited (from = person,
// to = "").
func TestAssignUnassignClearsAssignment(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	agent := h.users.seedRole("Ana", "ana@example.com", domain.RoleAgent, true)
	actor := domain.User{ID: 81, Name: "Ada", Role: domain.RoleAdmin}
	ticket := seededTicket(h.tickets, cat.ID, domain.StateNew)
	ticket.UserID = ptr(agent.ID)
	if err := h.tickets.Update(context.Background(), &ticket); err != nil {
		t.Fatalf("seed assignment: %v", err)
	}

	updated, err := h.svc.Assign(context.Background(), actor, ticket.ID, nil, "")
	if err != nil {
		t.Fatalf("Assign: unassign: unexpected error: %v", err)
	}
	if updated.UserID != nil {
		t.Fatalf("Assign: ticket must be unassigned, got %v", updated.UserID)
	}
	events, _ := h.audits.ListByTicket(context.Background(), ticket.ID)
	if len(events) != 1 || events[0].FromValue == nil || *events[0].FromValue != strconv.FormatInt(agent.ID, 10) ||
		events[0].ToValue == nil || *events[0].ToValue != "" {
		t.Fatalf("Assign: unassign audit must be %d -> \"\", got %+v", agent.ID, events)
	}
}

// TestAssignUnknownTarget proves assigning to an unknown user is a
// NotFoundError(kind=user).
func TestAssignUnknownTarget(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	ticket := seededTicket(h.tickets, cat.ID, domain.StateNew)
	actor := domain.User{ID: 91, Name: "Ada", Role: domain.RoleAdmin}

	_, err := h.svc.Assign(context.Background(), actor, ticket.ID, ptr(int64(999)), "")
	var nerr *domain.NotFoundError
	if !errors.As(err, &nerr) || nerr.Kind != "user" {
		t.Fatalf("Assign: unknown target must be a NotFoundError(kind=user), got %v", err)
	}
}

// --- S4: state transition authorization (ticket-state-machine spec) -------

// TestTransitionUserRoleDenied proves role user MUST NOT perform transitions
// of ANY ticket — including their own — with the state unchanged and nothing
// audited (spec: "User role cannot transition").
func TestTransitionUserRoleDenied(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	owner := domain.User{ID: 111, Name: "Ula", Role: domain.RoleUser}
	ticket := h.tickets.seed(domain.Ticket{
		Title: "own", CategoryID: cat.ID, RequesterUserID: ptr(owner.ID),
		Priority: domain.PriorityLow, State: domain.StateNew,
		CreatedAt: h.clock.now, UpdatedAt: h.clock.now,
	})

	_, err := h.svc.Transition(context.Background(), owner, ticket.ID, domain.StateInProgress, "")
	var ferr *domain.ForbiddenError
	if !errors.As(err, &ferr) {
		t.Fatalf("Transition: user-role actor must be denied with a ForbiddenError, got %v", err)
	}
	stored, _ := h.tickets.GetByID(context.Background(), ticket.ID, application.TicketQuery{Scope: application.ScopeAll})
	if stored.State != domain.StateNew {
		t.Fatalf("Transition: denied user must not change the state, got %q", stored.State)
	}
	if len(h.audits.events) != 0 {
		t.Fatal("Transition: denied user must not be audited")
	}
}

// TestTransitionAgentTransitionsOwnTicket proves an agent transitions their
// own assigned ticket through a legal move (spec: "Agent transitions only
// assigned tickets" — the allowed half).
func TestTransitionAgentTransitionsOwnTicket(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	agent := h.users.seedRole("Ana", "ana@example.com", domain.RoleAgent, true)
	ticket := seededTicket(h.tickets, cat.ID, domain.StateNew)
	ticket.UserID = ptr(agent.ID)
	if err := h.tickets.Update(context.Background(), &ticket); err != nil {
		t.Fatalf("seed assignment: %v", err)
	}
	actor := domain.User{ID: agent.ID, Name: agent.Name, Role: domain.RoleAgent}

	updated, err := h.svc.Transition(context.Background(), actor, ticket.ID, domain.StateInProgress, "")
	if err != nil {
		t.Fatalf("Transition: agent on own ticket: unexpected error: %v", err)
	}
	if updated.State != domain.StateInProgress {
		t.Fatalf("Transition: state = %q, want in_progress", updated.State)
	}
}

// TestTransitionAgentCannotTransitionOthersTicket proves an agent cannot
// transition a ticket assigned to a different agent: the scoped read denies
// it as ErrNotFound before any state change (spec: "Agent transitions only
// assigned tickets" — the denied half).
func TestTransitionAgentCannotTransitionOthersTicket(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	agentY := h.users.seedRole("Yara", "yara@example.com", domain.RoleAgent, true)
	agentX := domain.User{ID: 121, Name: "Xavi", Role: domain.RoleAgent}
	ticket := seededTicket(h.tickets, cat.ID, domain.StateNew)
	ticket.UserID = ptr(agentY.ID)
	if err := h.tickets.Update(context.Background(), &ticket); err != nil {
		t.Fatalf("seed assignment: %v", err)
	}

	_, err := h.svc.Transition(context.Background(), agentX, ticket.ID, domain.StateInProgress, "")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Transition: agent X on agent Y's ticket must be ErrNotFound, got %v", err)
	}
	stored, _ := h.tickets.GetByID(context.Background(), ticket.ID, application.TicketQuery{Scope: application.ScopeAll})
	if stored.State != domain.StateNew {
		t.Fatalf("Transition: denied agent must not change the state, got %q", stored.State)
	}
}

// TestUpdateUserRoleDenied proves role user cannot edit ANY ticket, even
// their own (design route policy: POST /tickets/{id}/edit requires an
// assigned agent or admin/root).
func TestUpdateUserRoleDenied(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	owner := domain.User{ID: 131, Name: "Ula", Role: domain.RoleUser}
	ticket := h.tickets.seed(domain.Ticket{
		Title: "own", CategoryID: cat.ID, RequesterUserID: ptr(owner.ID),
		Priority: domain.PriorityLow, State: domain.StateNew,
		CreatedAt: h.clock.now, UpdatedAt: h.clock.now,
	})
	newPriority := domain.PriorityHigh

	_, err := h.svc.Update(context.Background(), owner, ticket.ID, domain.TicketUpdate{Priority: &newPriority})
	var ferr *domain.ForbiddenError
	if !errors.As(err, &ferr) {
		t.Fatalf("Update: user-role actor must be denied with a ForbiddenError, got %v", err)
	}
	stored, _ := h.tickets.GetByID(context.Background(), ticket.ID, application.TicketQuery{Scope: application.ScopeAll})
	if stored.Priority != domain.PriorityLow {
		t.Fatalf("Update: denied user must not change the ticket, got priority %q", stored.Priority)
	}
}

// TestUpdateRejectsAssignmentFields proves assignment changes are handled
// ONLY by the assign use case: Update rejects user assignment fields so the
// reassignment-reason rule cannot be bypassed through a generic edit
// (design: POST /tickets/{id}/assign is the single assignment path).
func TestUpdateRejectsAssignmentFields(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	agent := h.users.seedRole("Ana", "ana@example.com", domain.RoleAgent, true)
	ticket := seededTicket(h.tickets, cat.ID, domain.StateNew)
	actor := domain.User{ID: 141, Name: "Ada", Role: domain.RoleAdmin}
	before, _ := h.tickets.GetByID(context.Background(), ticket.ID, application.TicketQuery{Scope: application.ScopeAll})

	_, err := h.svc.Update(context.Background(), actor, ticket.ID, domain.TicketUpdate{UserID: ptr(agent.ID)})
	var verr *domain.ValidationError
	if !errors.As(err, &verr) || verr.Field != "user" || verr.Message != domain.ErrMsgAssignmentViaAssign {
		t.Fatalf("Update: assignment via Update must be rejected with ErrMsgAssignmentViaAssign, got %v", err)
	}
	_, err = h.svc.Update(context.Background(), actor, ticket.ID, domain.TicketUpdate{ClearUserID: true})
	if !errors.As(err, &verr) || verr.Message != domain.ErrMsgAssignmentViaAssign {
		t.Fatalf("Update: clearing assignment via Update must be rejected, got %v", err)
	}
	after, _ := h.tickets.GetByID(context.Background(), ticket.ID, application.TicketQuery{Scope: application.ScopeAll})
	if !reflect.DeepEqual(before, after) {
		t.Fatal("Update: rejected assignment fields must leave the ticket untouched")
	}
	if len(h.audits.events) != 0 {
		t.Fatal("Update: rejected assignment fields must not be audited")
	}
}

// TestUpdateClosedTicketRejected proves a closed ticket (resolved, closed,
// or cancelled) is read-only: Update refuses any field edit with a
// ForbiddenError before any store mutation (closed-ticket read-only spec —
// only the state transition remains mutable, and cancelled is terminal).
func TestUpdateClosedTicketRejected(t *testing.T) {
	for _, state := range []domain.State{domain.StateResolved, domain.StateClosed, domain.StateCancelled} {
		t.Run(string(state), func(t *testing.T) {
			h := newTicketHarness()
			cat := h.categories.seed("Bugs")
			ticket := seededTicket(h.tickets, cat.ID, state)
			actor := domain.User{Name: "Ada", Role: domain.RoleAdmin}
			newTitle := "Renamed"

			_, err := h.svc.Update(context.Background(), actor, ticket.ID, domain.TicketUpdate{Title: &newTitle})
			var ferr *domain.ForbiddenError
			if !errors.As(err, &ferr) || ferr.Message != domain.ErrMsgClosedTicketReadOnly {
				t.Fatalf("Update: closed ticket must be denied with %q, got %v", domain.ErrMsgClosedTicketReadOnly, err)
			}
			stored, _ := h.tickets.GetByID(context.Background(), ticket.ID, application.TicketQuery{Scope: application.ScopeAll})
			if stored.Title != ticket.Title {
				t.Fatalf("Update: denied edit must not change the title, got %q", stored.Title)
			}
			if len(h.audits.events) != 0 {
				t.Fatal("Update: denied edit must not be audited")
			}
		})
	}
}

// TestAssignClosedTicketRejected proves assignment is also read-only on a
// closed ticket: Assign refuses with a ForbiddenError before any store
// mutation (closed-ticket read-only spec).
func TestAssignClosedTicketRejected(t *testing.T) {
	for _, state := range []domain.State{domain.StateResolved, domain.StateClosed, domain.StateCancelled} {
		t.Run(string(state), func(t *testing.T) {
			h := newTicketHarness()
			cat := h.categories.seed("Bugs")
			ticket := seededTicket(h.tickets, cat.ID, state)
			assignee := h.users.seedRole("Ana", "ana@example.com", domain.RoleAgent, true)
			actor := domain.User{Name: "Ada", Role: domain.RoleAdmin}

			_, err := h.svc.Assign(context.Background(), actor, ticket.ID, ptr(assignee.ID), "")
			var ferr *domain.ForbiddenError
			if !errors.As(err, &ferr) || ferr.Message != domain.ErrMsgClosedTicketReadOnly {
				t.Fatalf("Assign: closed ticket must be denied with %q, got %v", domain.ErrMsgClosedTicketReadOnly, err)
			}
			stored, _ := h.tickets.GetByID(context.Background(), ticket.ID, application.TicketQuery{Scope: application.ScopeAll})
			if stored.UserID != nil {
				t.Fatalf("Assign: denied assign must not set the assignee, got %v", stored.UserID)
			}
			if len(h.audits.events) != 0 {
				t.Fatal("Assign: denied assign must not be audited")
			}
		})
	}
}

func TestTransitionAppliesAndAuditsWithSessionActor(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	ticket := seededTicket(h.tickets, cat.ID, domain.StateNew)
	actor := domain.User{Name: "Ada", Email: "ada@example.com", Role: domain.RoleAdmin}
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
	actor := domain.User{Name: "Ada", Role: domain.RoleAdmin}

	_, err := h.svc.Transition(context.Background(), actor, ticket.ID, domain.StateClosed, "")
	var terr *domain.InvalidTransitionError
	if !errors.As(err, &terr) {
		t.Fatalf("Transition: new -> closed must be an InvalidTransitionError, got %v", err)
	}

	stored, _ := h.tickets.GetByID(context.Background(), ticket.ID, application.TicketQuery{Scope: application.ScopeAll})
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
	actor := domain.User{Name: "Ada", Role: domain.RoleAdmin}

	_, err := h.svc.Transition(context.Background(), actor, ticket.ID, domain.StateInProgress, "")
	var rerr *domain.ReopenReasonRequiredError
	if !errors.As(err, &rerr) {
		t.Fatalf("Transition: closed reopen without reason must be a ReopenReasonRequiredError, got %v", err)
	}
	if len(h.audits.events) != 0 {
		t.Fatal("Transition: rejected reopen must not be audited")
	}
}

func TestTransitionReopenResolvedRequiresReason(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	ticket := seededTicket(h.tickets, cat.ID, domain.StateResolved)
	actor := domain.User{Name: "Ada", Role: domain.RoleAdmin}

	_, err := h.svc.Transition(context.Background(), actor, ticket.ID, domain.StateInProgress, "")
	var rerr *domain.ReopenReasonRequiredError
	if !errors.As(err, &rerr) {
		t.Fatalf("Transition: resolved reopen without reason must be a ReopenReasonRequiredError, got %v", err)
	}
	if len(h.audits.events) != 0 {
		t.Fatal("Transition: rejected reopen must not be audited")
	}
}

func TestTransitionReopenClosedWithReasonRecordsNote(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	ticket := seededTicket(h.tickets, cat.ID, domain.StateClosed)
	actor := domain.User{Name: "Ada", Role: domain.RoleAdmin}

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
	actor := domain.User{Name: "Ada", Role: domain.RoleAdmin}

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
	actor := domain.User{Name: "Ada", Role: domain.RoleAdmin}
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

// TestUpdateNeverTouchesDescription proves the description is immutable
// after creation: the update input exposes no description field (it was
// removed from TicketUpdate), so any Update leaves the stored description
// byte-for-byte unchanged and appends no description audit event. The
// description exists on the aggregate only as a creation-time value.
func TestUpdateNeverTouchesDescription(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	ticket := seededTicket(h.tickets, cat.ID, domain.StateInProgress)
	ticket.Description = "Original description"
	h.tickets.Update(context.Background(), &ticket)
	actor := domain.User{Name: "Ada", Role: domain.RoleAdmin}
	h.clock.Advance(timeMinute)

	newTitle := "Renamed title"
	newPriority := domain.PriorityHigh
	updated, err := h.svc.Update(context.Background(), actor, ticket.ID, domain.TicketUpdate{
		Title:    &newTitle,
		Priority: &newPriority,
	})
	if err != nil {
		t.Fatalf("Update: unexpected error: %v", err)
	}
	if updated.Description != "Original description" {
		t.Fatalf("Update: description must stay immutable, got %q", updated.Description)
	}
	events, _ := h.audits.ListByTicket(context.Background(), ticket.ID)
	if len(events) != 2 {
		t.Fatalf("Update: exactly the title+priority audits expected, got %d", len(events))
	}
	for _, ev := range events {
		if ev.Field != nil && *ev.Field == "description" {
			t.Fatalf("Update: no description audit may exist, got %+v", events)
		}
	}
}

func TestUpdateRejectsInvalidPriorityWithoutChanges(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	ticket := seededTicket(h.tickets, cat.ID, domain.StateInProgress)
	actor := domain.User{Name: "Ada", Role: domain.RoleAdmin}
	before, _ := h.tickets.GetByID(context.Background(), ticket.ID, application.TicketQuery{Scope: application.ScopeAll})

	bad := domain.Priority("urgent")
	_, err := h.svc.Update(context.Background(), actor, ticket.ID, domain.TicketUpdate{Priority: &bad})
	var ierr *domain.InvalidPriorityError
	if !errors.As(err, &ierr) {
		t.Fatalf("Update: invalid priority must be an InvalidPriorityError, got %v", err)
	}

	after, _ := h.tickets.GetByID(context.Background(), ticket.ID, application.TicketQuery{Scope: application.ScopeAll})
	if !reflect.DeepEqual(before, after) {
		t.Fatal("Update: rejected edit must leave the stored ticket untouched")
	}
	if len(h.audits.events) != 0 {
		t.Fatal("Update: rejected edit must not be audited")
	}
}

// TestUpdateNeverTouchesCategory proves the category is immutable after
// creation, mirroring the description: the update input exposes no category
// field (it was removed from TicketUpdate), so any Update leaves the stored
// category unchanged and appends no category audit event. The category
// exists on the aggregate only as a creation-time value.
func TestUpdateNeverTouchesCategory(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	ticket := seededTicket(h.tickets, cat.ID, domain.StateInProgress)
	actor := domain.User{Name: "Ada", Role: domain.RoleAdmin}
	h.clock.Advance(timeMinute)

	newTitle := "Renamed title"
	newPriority := domain.PriorityHigh
	updated, err := h.svc.Update(context.Background(), actor, ticket.ID, domain.TicketUpdate{
		Title:    &newTitle,
		Priority: &newPriority,
	})
	if err != nil {
		t.Fatalf("Update: unexpected error: %v", err)
	}
	if updated.CategoryID != cat.ID {
		t.Fatalf("Update: category must stay immutable, got %d want %d", updated.CategoryID, cat.ID)
	}
	events, _ := h.audits.ListByTicket(context.Background(), ticket.ID)
	if len(events) != 2 {
		t.Fatalf("Update: exactly the title+priority audits expected, got %d", len(events))
	}
	for _, ev := range events {
		if ev.Field != nil && *ev.Field == "category" {
			t.Fatalf("Update: no category audit may exist, got %+v", events)
		}
	}
}

func TestEveryMutationAuditedInOccurrenceOrder(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	actor := domain.User{Name: "Ada", Email: "ada@example.com", Role: domain.RoleAdmin}

	// GIVEN a ticket (audit-log scenario: one transition and two field edits).
	ticket, err := h.svc.Create(context.Background(), actor, validCreateInput(cat.ID))
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

	// THEN creation plus exactly three mutation events exist in occurrence
	// order, each with the session actor.
	events, _ := h.audits.ListByTicket(context.Background(), ticket.ID)
	if len(events) != 4 {
		t.Fatalf("audit trail: expected exactly 4 events (created + 3 mutations), got %d", len(events))
	}
	wantActions := []string{domain.ActionCreated, domain.ActionTransition, domain.ActionUpdate, domain.ActionUpdate}
	wantFields := []*string{nil, ptr("state"), ptr("title"), ptr("priority")}
	for i, ev := range events {
		if ev.Action != wantActions[i] {
			t.Fatalf("audit trail: event %d must be %q, got %q", i, wantActions[i], ev.Action)
		}
		if ev.Actor != actor.Name {
			t.Fatalf("audit trail: event %d actor must come from the session, got %q", i, ev.Actor)
		}
		if !reflect.DeepEqual(ev.Field, wantFields[i]) {
			t.Fatalf("audit trail: event %d field = %v, want %v", i, ev.Field, wantFields[i])
		}
	}
}

// TestCreateRollsBackTicketWhenAuditAppendFails proves the no-silent-mutations
// atomicity contract (C1): when the audit half of the unit-of-work fails, the
// ticket mutation must NOT be persisted and the failure must reach the caller.
func TestCreateRollsBackTicketWhenAuditAppendFails(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	actor := domain.User{Name: "Ada", Role: domain.RoleAdmin}
	h.tx.failAuditAppend = true

	_, err := h.svc.Create(context.Background(), actor, validCreateInput(cat.ID))
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
	actor := domain.User{Name: "Ada", Role: domain.RoleAdmin}
	h.tx.failAuditAppend = true

	_, err := h.svc.Transition(context.Background(), actor, ticket.ID, domain.StateInProgress, "")
	if !errors.Is(err, errAuditAppendFailed) {
		t.Fatalf("Transition: audit append failure must propagate to the caller, got %v", err)
	}
	stored, err := h.tickets.GetByID(context.Background(), ticket.ID, application.TicketQuery{Scope: application.ScopeAll})
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

// TestMutationAuditEventsCarryActorUserID proves the S4 audit contract
// (design: "Events store session actor ID/snapshot"): every mutation event
// the service mints — create, transition, update — carries the session
// actor's user id alongside the name snapshot.
func TestMutationAuditEventsCarryActorUserID(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	actor := domain.User{ID: 11, Name: "Ada", Email: "ada@example.com", Role: domain.RoleAdmin}

	ticket, err := h.svc.Create(context.Background(), actor, validCreateInput(cat.ID))
	if err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}
	h.clock.Advance(timeMinute)
	if _, err := h.svc.Transition(context.Background(), actor, ticket.ID, domain.StateInProgress, ""); err != nil {
		t.Fatalf("Transition: unexpected error: %v", err)
	}
	h.clock.Advance(timeMinute)
	newTitle := "Renamed"
	if _, err := h.svc.Update(context.Background(), actor, ticket.ID, domain.TicketUpdate{Title: &newTitle}); err != nil {
		t.Fatalf("Update: unexpected error: %v", err)
	}

	events, err := h.audits.ListByTicket(context.Background(), ticket.ID)
	if err != nil {
		t.Fatalf("ListByTicket: unexpected error: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events (created + transition + update), got %d", len(events))
	}
	for i, ev := range events {
		if ev.Actor != actor.Name {
			t.Errorf("event[%d] Actor = %q, want session name %q", i, ev.Actor, actor.Name)
		}
		if ev.ActorUserID == nil || *ev.ActorUserID != actor.ID {
			t.Errorf("event[%d] ActorUserID = %v, want session user id %d", i, ev.ActorUserID, actor.ID)
		}
	}
}

// TestGetByIDReturnsComposedView covers the read contract (C2): GetByID
// returns the composed TicketView — ticket, resolved category name, resolved
// assigned-user name, and the chronological comment timeline — not the raw
// domain aggregate.
func TestGetByIDReturnsComposedView(t *testing.T) {
	h := newTicketHarness()
	actor := domain.User{Name: "Ada", Role: domain.RoleAdmin}
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

	view, err := h.svc.GetByID(context.Background(), actor, ticket.ID)
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

	_, err = h.svc.GetByID(context.Background(), actor, 4242)
	var nerr *domain.NotFoundError
	if !errors.As(err, &nerr) || nerr.Kind != "ticket" {
		t.Fatalf("GetByID: unknown id must be a NotFoundError(kind=ticket), got %v", err)
	}
}

// TestGetByIDUserCannotReadOthersTicket proves direct lookup is scoped: a
// user-role actor can read their own ticket but B's ticket is
// indistinguishable from a missing one (ticket-access spec: direct request
// for another's ticket is denied).
func TestGetByIDUserCannotReadOthersTicket(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Bugs")
	owner := domain.User{ID: 1, Name: "Owner", Role: domain.RoleUser}
	other := domain.User{ID: 2, Name: "Other", Role: domain.RoleUser}

	own := h.tickets.seed(domain.Ticket{Title: "own", CategoryID: cat.ID, RequesterUserID: ptr(owner.ID),
		Priority: domain.PriorityLow, State: domain.StateNew, CreatedAt: h.clock.now, UpdatedAt: h.clock.now})
	others := h.tickets.seed(domain.Ticket{Title: "other", CategoryID: cat.ID, RequesterUserID: ptr(other.ID),
		Priority: domain.PriorityLow, State: domain.StateNew, CreatedAt: h.clock.now, UpdatedAt: h.clock.now})

	view, err := h.svc.GetByID(context.Background(), owner, own.ID)
	if err != nil {
		t.Fatalf("GetByID(own): unexpected error: %v", err)
	}
	if view.Ticket.ID != own.ID {
		t.Fatalf("GetByID(own) = ticket %d, want %d", view.Ticket.ID, own.ID)
	}

	_, err = h.svc.GetByID(context.Background(), owner, others.ID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByID(other's ticket) err = %v, want ErrNotFound", err)
	}
}

// --- PR5 S5: atomic create+pin+run (application orchestration, tasks 5.1-5.2) ---
//
// Batch A proves the TicketService orchestration contract against fakes: the
// service resolves the category's current published version, pins that exact
// version, plans the initial automatic advancement through the WorkflowRunner,
// and submits ONE CreateTicketWithRun plan to the WorkflowUnitOfWork. The
// SQLite adapter (WorkflowVersionStore/WorkflowUnitOfWork implementations) and
// its atomicity proof arrive with PR5 Batch B; this batch asserts the exact
// plan and error propagation, never simulated atomicity.

// errWorkflowUoWFailed is the simulated WorkflowUnitOfWork failure the fake
// returns when failCreate is set: the service must propagate it untouched and
// must never fall back to the legacy TicketUnitOfWork create path.
var errWorkflowUoWFailed = errors.New("workflow create unit of work failed")

// workflowCreateHarness wires a TicketService with the workflow create path
// (NewTicketServiceWithWorkflowCreate): a fake WorkflowVersionStore holding
// published versions, the REAL WorkflowRunner planning initial automatic
// advancement, and a fake WorkflowUnitOfWork recording the exact plan.
type workflowCreateHarness struct {
	svc        *application.TicketService
	tickets    *fakeTicketStore
	users      *fakeUserStore
	categories *fakeCategoryStore
	comments   *fakeCommentStore
	audits     *fakeAuditStore
	tx         *fakeUnitOfWork
	clock      *fakeClock
	versions   *fakeWorkflowVersionStore
	runner     *application.WorkflowRunner
	wfTx       *fakeWorkflowUnitOfWork
}

func newWorkflowCreateHarness() *workflowCreateHarness {
	clock := fixedClock()
	users := newFakeUserStore()
	categories := newFakeCategoryStore()
	tickets := newFakeTicketStore()
	comments := newFakeCommentStore()
	audits := newFakeAuditStore()
	tx := newFakeUnitOfWork(tickets, audits)
	versions := newFakeWorkflowVersionStore()
	runner := application.NewWorkflowRunner(clock)
	wfTx := newFakeWorkflowUnitOfWork(tickets, audits)
	builder := application.NewViewBuilder(tickets, users, categories, comments, audits, newFakeDeskStore())
	svc := application.NewTicketServiceWithWorkflowCreate(tickets, users, categories, tx, builder, clock, versions, runner, wfTx)
	return &workflowCreateHarness{
		svc: svc, tickets: tickets, users: users, categories: categories,
		comments: comments, audits: audits, tx: tx, clock: clock,
		versions: versions, runner: runner, wfTx: wfTx,
	}
}

// TestTicketService_CreateWithWorkflow_UnpublishedCategoryIsUnavailable proves a
// category without a published workflow version returns the EXACT 422 category
// message and performs no create or unit-of-work writes (the service must not
// fall back to the legacy create path).
func TestTicketService_CreateWithWorkflow_UnpublishedCategoryIsUnavailable(t *testing.T) {
	h := newWorkflowCreateHarness()
	cat := h.categories.seed("Bugs")
	actor := domain.User{ID: 7, Name: "Ada", Email: "ada@example.com", Role: domain.RoleAdmin}

	_, err := h.svc.Create(context.Background(), actor, validCreateInput(cat.ID))
	var verr *domain.ValidationError
	if !errors.As(err, &verr) || verr.Field != "category" || verr.Message != domain.ErrMsgCategoryWorkflowUnavailable {
		t.Fatalf("Create: unavailable category must be ValidationError{Field: category, Message: %q}, got %v", domain.ErrMsgCategoryWorkflowUnavailable, err)
	}
	if len(h.wfTx.calls) != 0 {
		t.Fatalf("Create: unavailable category must NOT submit a CreateTicketWithRun plan, got %d calls", len(h.wfTx.calls))
	}
	if len(h.tickets.tickets) != 0 || len(h.audits.events) != 0 {
		t.Fatal("Create: unavailable category must not persist a ticket or an audit")
	}
}

// TestTicketService_CreateWithWorkflow_PinsVersionAndPlansInitialAutomatic
// proves the full orchestration contract for a published category: the service
// resolves the immutable current version ONCE, submits exactly ONE
// CreateTicketWithRun plan carrying the expected version identity (adapter
// recheck), the pinned definition, the created audit with the session actor,
// and the runner-planned initial automatic advancement (least_loaded intent +
// exact new→in_progress workflow transition), and persists the pinned ticket.
func TestTicketService_CreateWithWorkflow_PinsVersionAndPlansInitialAutomatic(t *testing.T) {
	h := newWorkflowCreateHarness()
	cat := h.categories.seed("Bugs")
	// [assign_to_desk[least_loaded], manual_task]: creation auto-plans the
	// least_loaded assignment and the new→in_progress transition, then the run
	// waits at the human manual step.
	def := domain.WorkflowDefinition{
		{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: 3, Strategy: domain.StrategyLeastLoaded}},
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Provision the server"}},
	}
	versionID := h.versions.publish(cat.ID, def)
	actor := domain.User{ID: 7, Name: "Ada", Email: "ada@example.com", Role: domain.RoleUser}

	ticket, err := h.svc.Create(context.Background(), actor, validCreateInput(cat.ID))
	if err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}
	if len(h.versions.calls) != 1 || h.versions.calls[0] != cat.ID {
		t.Fatalf("Create: must resolve the current version exactly once for the category, got %v", h.versions.calls)
	}
	if len(h.wfTx.calls) != 1 {
		t.Fatalf("Create: must submit exactly one CreateTicketWithRun plan, got %d", len(h.wfTx.calls))
	}
	in := h.wfTx.calls[0]
	if in.CategoryID != cat.ID || in.ExpectedVersionID != versionID {
		t.Fatalf("Create: plan must carry the expected category/current-version identity for the adapter recheck, got cat=%d version=%d", in.CategoryID, in.ExpectedVersionID)
	}
	if !reflect.DeepEqual(in.Workflow, def) {
		t.Fatal("Create: plan must carry the pinned immutable workflow definition")
	}
	if in.Ticket == nil || in.Ticket.WorkflowVersionID == nil || *in.Ticket.WorkflowVersionID != versionID {
		t.Fatalf("Create: ticket must pin the exact current version id %d, got %v", versionID, in.Ticket)
	}
	if in.Ticket.State != domain.StateNew {
		t.Fatalf("Create: ticket must be planned in state new, got %q", in.Ticket.State)
	}
	if in.CreatedAudit.Action != domain.ActionCreated || in.CreatedAudit.Actor != actor.Name ||
		in.CreatedAudit.ActorUserID == nil || *in.CreatedAudit.ActorUserID != actor.ID {
		t.Fatalf("Create: created audit must carry the creating session actor, got %+v", in.CreatedAudit)
	}
	if !in.CreatedAudit.CreatedAt.Equal(h.clock.now) || !in.StartedAt.Equal(h.clock.now) {
		t.Fatal("Create: created audit and run start must use the injected clock")
	}
	if in.ExpectedCursor != 0 || in.ExpectedRunStatus != "active" {
		t.Fatalf("Create: fresh run must start active at cursor 0, got cursor %d status %q", in.ExpectedCursor, in.ExpectedRunStatus)
	}
	// Initial automatic advancement: least_loaded intent + exact workflow
	// new→in_progress transition, then the run waits at the manual step.
	if len(in.Operations) != 2 {
		t.Fatalf("Create: plan must carry exactly 2 initial automatic operations, got %d", len(in.Operations))
	}
	least, ok := in.Operations[0].(application.LeastLoadedAssignmentOperation)
	if !ok || least.StepIndex != 0 || least.DeskID != 3 {
		t.Fatalf("Create: first automatic op must be the least_loaded intent at step 0 for desk 3, got %#v", in.Operations[0])
	}
	tr, ok := in.Operations[1].(application.TransitionOperation)
	if !ok || tr.Audit.Action != domain.ActionTransition || tr.Audit.FromValue == nil || *tr.Audit.FromValue != string(domain.StateNew) ||
		tr.Audit.ToValue == nil || *tr.Audit.ToValue != string(domain.StateInProgress) {
		t.Fatalf("Create: second automatic op must be the exact new→in_progress transition audit, got %#v", in.Operations[1])
	}
	if tr.Audit.Actor != "workflow" || tr.Audit.ActorUserID != nil {
		t.Fatalf("Create: automatic transition audit must stamp actor workflow with no user id, got %+v", tr.Audit)
	}
	if in.NextCursor != 1 || in.NextRunStatus != "active" || in.NextTicketState != domain.StateInProgress {
		t.Fatalf("Create: plan must advance to cursor 1 / active / in_progress, got cursor %d status %q state %q", in.NextCursor, in.NextRunStatus, in.NextTicketState)
	}
	if in.CompletedAt != nil {
		t.Fatal("Create: an active run must not carry a completion timestamp")
	}
	if ticket.ID == 0 || ticket.Number != 1 || ticket.WorkflowVersionID == nil || *ticket.WorkflowVersionID != versionID {
		t.Fatalf("Create: returned ticket must be persisted with id/number and the pinned version, got %+v", ticket)
	}
}

// TestTicketService_CreateWithWorkflow_HumanFirstStepStopsInitialAdvancement
// proves automatic advancement stops at the FIRST human-pending step: a form at
// cursor 0 yields an empty operations list and a run waiting at cursor 0 in new.
func TestTicketService_CreateWithWorkflow_HumanFirstStepStopsInitialAdvancement(t *testing.T) {
	h := newWorkflowCreateHarness()
	cat := h.categories.seed("Bugs")
	def := domain.WorkflowDefinition{
		{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{{Key: "reason", Label: "Reason", Kind: domain.FieldShortText, Required: true}}}},
		{Type: domain.StepResolve},
	}
	versionID := h.versions.publish(cat.ID, def)
	actor := domain.User{ID: 7, Name: "Ada", Email: "ada@example.com", Role: domain.RoleUser}

	ticket, err := h.svc.Create(context.Background(), actor, validCreateInput(cat.ID))
	if err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}
	in := h.wfTx.calls[0]
	if len(in.Operations) != 0 {
		t.Fatalf("Create: a run starting with a human form must not auto-advance, got %d operations", len(in.Operations))
	}
	if in.NextCursor != 0 || in.NextRunStatus != "active" || in.NextTicketState != domain.StateNew {
		t.Fatalf("Create: run must wait at cursor 0 active in new, got cursor %d status %q state %q", in.NextCursor, in.NextRunStatus, in.NextTicketState)
	}
	if ticket.State != domain.StateNew {
		t.Fatalf("Create: human-first workflow must leave the ticket new, got %q", ticket.State)
	}
	if ticket.WorkflowVersionID == nil || *ticket.WorkflowVersionID != versionID {
		t.Fatalf("Create: ticket must pin version %d, got %v", versionID, ticket.WorkflowVersionID)
	}
}

// TestTicketService_CreateWithWorkflow_AllAutomaticCompletesAtCreation proves an
// entirely automatic workflow resolves/advances within the same atomic create
// unit: [resolve_ticket] alone plans the exact workflow transition, completes
// the run at creation (CompletedAt from the injected clock), and the ticket is
// stored resolved.
func TestTicketService_CreateWithWorkflow_AllAutomaticCompletesAtCreation(t *testing.T) {
	h := newWorkflowCreateHarness()
	cat := h.categories.seed("Bugs")
	versionID := h.versions.publish(cat.ID, domain.WorkflowDefinition{{Type: domain.StepResolve}})
	actor := domain.User{ID: 7, Name: "Ada", Email: "ada@example.com", Role: domain.RoleUser}

	ticket, err := h.svc.Create(context.Background(), actor, validCreateInput(cat.ID))
	if err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}
	in := h.wfTx.calls[0]
	if len(in.Operations) != 1 {
		t.Fatalf("Create: [resolve_ticket] must plan exactly 1 terminal transition, got %d", len(in.Operations))
	}
	tr, ok := in.Operations[0].(application.TransitionOperation)
	if !ok || tr.Audit.ToValue == nil || *tr.Audit.ToValue != string(domain.StateResolved) {
		t.Fatalf("Create: terminal op must be the exact resolved transition, got %#v", in.Operations[0])
	}
	if tr.Audit.Actor != "workflow" || tr.Audit.ActorUserID != nil {
		t.Fatalf("Create: terminal transition audit must stamp actor workflow, got %+v", tr.Audit)
	}
	if in.NextCursor != 1 || in.NextRunStatus != "completed" || in.NextTicketState != domain.StateResolved {
		t.Fatalf("Create: run must complete at creation in resolved, got cursor %d status %q state %q", in.NextCursor, in.NextRunStatus, in.NextTicketState)
	}
	if in.CompletedAt == nil || !in.CompletedAt.Equal(h.clock.now) {
		t.Fatal("Create: a run completed at creation must carry the injected completion time")
	}
	if ticket.State != domain.StateResolved {
		t.Fatalf("Create: ticket must be stored resolved, got %q", ticket.State)
	}
	if ticket.WorkflowVersionID == nil || *ticket.WorkflowVersionID != versionID {
		t.Fatalf("Create: ticket must pin version %d, got %v", versionID, ticket.WorkflowVersionID)
	}
}

// TestTicketService_CreateWithWorkflow_PropagatesUnitOfWorkFailure proves the
// service never simulates atomicity: a WorkflowUnitOfWork failure propagates
// untouched, exactly one plan is submitted, nothing is partially persisted, and
// the service does NOT fall back to the legacy TicketUnitOfWork create.
func TestTicketService_CreateWithWorkflow_PropagatesUnitOfWorkFailure(t *testing.T) {
	h := newWorkflowCreateHarness()
	cat := h.categories.seed("Bugs")
	h.versions.publish(cat.ID, domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Do the thing"}}})
	actor := domain.User{ID: 7, Name: "Ada", Email: "ada@example.com", Role: domain.RoleUser}
	h.wfTx.failCreate = true
	h.wfTx.failWith = errWorkflowUoWFailed

	_, err := h.svc.Create(context.Background(), actor, validCreateInput(cat.ID))
	if !errors.Is(err, errWorkflowUoWFailed) {
		t.Fatalf("Create: the WorkflowUnitOfWork failure must propagate untouched, got %v", err)
	}
	if len(h.wfTx.calls) != 1 {
		t.Fatalf("Create: exactly one CreateTicketWithRun attempt expected, got %d", len(h.wfTx.calls))
	}
	if len(h.tickets.tickets) != 0 || len(h.audits.events) != 0 {
		t.Fatal("Create: a failed unit must leave no partial ticket or audit — the service never retries or falls back")
	}
	if h.tx.createCalls != 0 {
		t.Fatal("Create: the service must NOT fall back to the legacy TicketUnitOfWork create path")
	}
}

// TestTicketService_CreateWithWorkflow_LaterPublicationDoesNotChangePinnedVersion
// proves the pin immutability contract (spec "In-flight ticket keeps its pinned
// version"): a later publication switches the category's current version, but the
// in-flight ticket and its already-submitted plan keep the originally resolved
// version id; a NEW create pins the newer version.
func TestTicketService_CreateWithWorkflow_LaterPublicationDoesNotChangePinnedVersion(t *testing.T) {
	h := newWorkflowCreateHarness()
	cat := h.categories.seed("Bugs")
	v1 := h.versions.publish(cat.ID, domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "First pass"}}})
	actor := domain.User{ID: 7, Name: "Ada", Email: "ada@example.com", Role: domain.RoleUser}
	ticket, err := h.svc.Create(context.Background(), actor, validCreateInput(cat.ID))
	if err != nil {
		t.Fatalf("Create (v1): unexpected error: %v", err)
	}

	// A later publication switches the category's current version...
	v2 := h.versions.publish(cat.ID, domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Second pass"}}})
	if v2 == v1 {
		t.Fatal("arrange: a later publish must allocate a new version id")
	}

	// ...but the in-flight ticket, its stored row, and the submitted plan keep v1.
	if ticket.WorkflowVersionID == nil || *ticket.WorkflowVersionID != v1 {
		t.Fatalf("Create: in-flight ticket must keep pinned version %d after a later publish, got %v", v1, ticket.WorkflowVersionID)
	}
	stored, err := h.tickets.GetByID(context.Background(), ticket.ID, application.TicketQuery{Scope: application.ScopeAll})
	if err != nil {
		t.Fatalf("GetByID after later publish: %v", err)
	}
	if stored.WorkflowVersionID == nil || *stored.WorkflowVersionID != v1 {
		t.Fatalf("Create: stored ticket must keep pinned version %d, got %v", v1, stored.WorkflowVersionID)
	}
	if in := h.wfTx.calls[0]; in.ExpectedVersionID != v1 {
		t.Fatalf("Create: plan must carry the originally resolved version %d, got %d", v1, in.ExpectedVersionID)
	}

	// A new create pins the newer version.
	second, err := h.svc.Create(context.Background(), actor, validCreateInput(cat.ID))
	if err != nil {
		t.Fatalf("Create (v2): unexpected error: %v", err)
	}
	if second.WorkflowVersionID == nil || *second.WorkflowVersionID != v2 {
		t.Fatalf("Create: a new ticket after the later publish must pin version %d, got %v", v2, second.WorkflowVersionID)
	}
	if len(h.wfTx.calls) != 2 {
		t.Fatalf("Create: two creates must submit exactly two plans, got %d", len(h.wfTx.calls))
	}
}

// TestTicketService_CreateWithWorkflow_LegacyNullPinTicketsUnchanged proves the
// workflow wiring does not change legacy behavior: a NULL-pin (pre-workflow)
// ticket stays readable through the existing scopes, and the non-workflow
// mutation paths (Assign, Transition) keep their exact behavior.
func TestTicketService_CreateWithWorkflow_LegacyNullPinTicketsUnchanged(t *testing.T) {
	h := newWorkflowCreateHarness()
	cat := h.categories.seed("Bugs")
	legacy := seededTicket(h.tickets, cat.ID, domain.StateNew) // WorkflowVersionID nil = legacy pre-workflow ticket
	admin := domain.User{Name: "Ada", Role: domain.RoleAdmin}
	agent := h.users.seedRole("Ana", "ana@example.com", domain.RoleAgent, true)

	view, err := h.svc.GetByID(context.Background(), admin, legacy.ID)
	if err != nil {
		t.Fatalf("GetByID: legacy NULL-pin ticket must remain readable, got %v", err)
	}
	if view.Ticket.WorkflowVersionID != nil {
		t.Fatalf("GetByID: legacy ticket must keep a NULL pin, got %v", view.Ticket.WorkflowVersionID)
	}

	updated, err := h.svc.Assign(context.Background(), agent, legacy.ID, ptr(agent.ID), "")
	if err != nil {
		t.Fatalf("Assign: legacy ticket must remain assignable, got %v", err)
	}
	if updated.UserID == nil || *updated.UserID != agent.ID {
		t.Fatalf("Assign: assignee = %v, want %d", updated.UserID, agent.ID)
	}
	trans, err := h.svc.Transition(context.Background(), admin, legacy.ID, domain.StateInProgress, "")
	if err != nil {
		t.Fatalf("Transition: legacy ticket must remain transitionable, got %v", err)
	}
	if trans.State != domain.StateInProgress {
		t.Fatalf("Transition: state = %q, want in_progress", trans.State)
	}
}
