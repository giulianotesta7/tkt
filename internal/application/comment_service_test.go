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
	actor := domain.User{Name: "Ada", Email: "ada@example.com", Role: domain.RoleAdmin}
	clock.Advance(timeMinute)

	c, err := svc.Add(context.Background(), actor, ticket.ID, "The redirect is broken", "public")
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

	_, err := svc.Add(context.Background(), domain.User{Name: "Ada", Role: domain.RoleAdmin}, ticket.ID, "   ", "public")
	var verr *domain.ValidationError
	if !errors.As(err, &verr) || verr.Field != "body" {
		t.Fatalf("Add: empty body must be a ValidationError on field body, got %v", err)
	}
	if len(comments.comments[ticket.ID]) != 0 {
		t.Fatal("Add: rejected comment must not be stored")
	}
	if len(tickets.getByIDCalls) != 0 {
		t.Fatalf("Add: empty body must be rejected before ticket lookup, got calls %v", tickets.getByIDCalls)
	}
	if len(comments.addCalls) != 0 {
		t.Fatalf("Add: empty body must be rejected before comment persistence, got calls %+v", comments.addCalls)
	}
}

func TestAddCommentUnknownTicket(t *testing.T) {
	clock := fixedClock()
	svc := application.NewCommentService(newFakeTicketStore(), newFakeCommentStore(), clock)

	_, err := svc.Add(context.Background(), domain.User{Name: "Ada", Role: domain.RoleAdmin}, 4242, "hello", "public")
	var nerr *domain.NotFoundError
	if !errors.As(err, &nerr) || nerr.Kind != "ticket" {
		t.Fatalf("Add: unknown ticket must be a NotFoundError(kind=ticket), got %v", err)
	}
}

// TestAddCommentOnClosedTicketRejected proves a closed ticket (resolved,
// closed, or cancelled) rejects a new comment with a ForbiddenError BEFORE
// any comment store call (closed-ticket read-only spec): only the state
// transition remains mutable on a closed ticket, and cancelled is terminal.
func TestAddCommentOnClosedTicketRejected(t *testing.T) {
	cases := []struct {
		name  string
		state domain.State
	}{
		{name: "resolved", state: domain.StateResolved},
		{name: "closed", state: domain.StateClosed},
		{name: "cancelled", state: domain.StateCancelled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := fixedClock()
			tickets := newFakeTicketStore()
			comments := newFakeCommentStore()
			cat := newFakeCategoryStore().seed("Bugs")
			now := clock.Now()
			ticket := tickets.seed(domain.Ticket{
				Title: tc.name + " ticket", CategoryID: cat.ID, Priority: domain.PriorityLow,
				State: tc.state, CreatedAt: now, UpdatedAt: now,
			})
			svc := application.NewCommentService(tickets, comments, clock)

			_, err := svc.Add(context.Background(), domain.User{Name: "Ada", Role: domain.RoleAdmin}, ticket.ID, "Still relevant after closure", "public")
			var ferr *domain.ForbiddenError
			if !errors.As(err, &ferr) || ferr.Message != domain.ErrMsgCommentOnClosedTicket {
				t.Fatalf("Add: closed ticket must be denied with %q, got %v", domain.ErrMsgCommentOnClosedTicket, err)
			}
			if len(comments.comments[ticket.ID]) != 0 {
				t.Fatal("Add: rejected comment must not be stored")
			}
			if len(comments.addCalls) != 0 {
				t.Fatalf("Add: denial must fire before the comment store call, got %+v", comments.addCalls)
			}
		})
	}
}

// TestAddCommentOnOpenTicketAccepted proves open tickets (new, in_progress)
// still accept comments after the closed-ticket guard (regression guard).
func TestAddCommentOnOpenTicketAccepted(t *testing.T) {
	for _, state := range []domain.State{domain.StateNew, domain.StateInProgress} {
		t.Run(string(state), func(t *testing.T) {
			clock := fixedClock()
			tickets := newFakeTicketStore()
			comments := newFakeCommentStore()
			cat := newFakeCategoryStore().seed("Bugs")
			now := clock.Now()
			ticket := tickets.seed(domain.Ticket{
				Title: "Open ticket", CategoryID: cat.ID, Priority: domain.PriorityLow,
				State: state, CreatedAt: now, UpdatedAt: now,
			})
			svc := application.NewCommentService(tickets, comments, clock)

			c, err := svc.Add(context.Background(), domain.User{Name: "Ada", Role: domain.RoleAdmin}, ticket.ID, "A note", "public")
			if err != nil {
				t.Fatalf("Add: comments on %s tickets must be accepted, got %v", state, err)
			}
			if c.Body != "A note" {
				t.Fatalf("Add: comment must be stored, got %+v", c)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// S5: comment visibility (comment-visibility spec). Role user creates ONLY
// public comments; roles agent+ create both public and internal. Internal
// comments are staff-only: a user-role actor is denied before any query.
// ---------------------------------------------------------------------------

// seededOwnedTicket arranges a ticket owned by the user-role actor (the
// actor is the requester), so a user-role actor can both see it and comment
// on it within scope.
func seededOwnedTicket(t *testing.T, clock *fakeClock, tickets *fakeTicketStore, categories *fakeCategoryStore, user domain.User) domain.Ticket {
	t.Helper()
	cat := categories.seed("Bugs")
	return tickets.seed(domain.Ticket{
		Title: "Seeded", CategoryID: cat.ID, Priority: domain.PriorityLow,
		State: domain.StateNew, CreatedAt: clock.now, UpdatedAt: clock.now,
		RequesterUserID: &user.ID,
	})
}

// TestAddCommentUserPublicOnly proves the user-role actor may create public
// comments but is denied internal ones BEFORE any store call
// (comment-visibility spec: "User creates public comment" + "User cannot
// create internal").
func TestAddCommentUserPublicOnly(t *testing.T) {
	clock := fixedClock()
	tickets := newFakeTicketStore()
	comments := newFakeCommentStore()
	user := domain.User{ID: 1, Name: "Ula", Email: "ula@example.com", Role: domain.RoleUser}
	ticket := seededOwnedTicket(t, clock, tickets, newFakeCategoryStore(), user)
	svc := application.NewCommentService(tickets, comments, clock)

	// User adds a public comment: accepted, stored with visibility public.
	c, err := svc.Add(context.Background(), user, ticket.ID, "Visible note", "public")
	if err != nil {
		t.Fatalf("Add(public): user must be allowed a public comment, got %v", err)
	}
	if c.Visibility != domain.CommentPublic {
		t.Fatalf("Add(public): visibility must be public, got %q", c.Visibility)
	}
	stored := comments.comments[ticket.ID]
	if len(stored) != 1 || stored[0].Visibility != domain.CommentPublic || stored[0].Body != "Visible note" {
		t.Fatalf("Add(public): comment must be stored as public, got %+v", stored)
	}

	// User adds an internal comment: denied, nothing stored, no query made.
	_, err = svc.Add(context.Background(), user, ticket.ID, "Staff secret", "internal")
	var ferr *domain.ForbiddenError
	if !errors.As(err, &ferr) || ferr.Message != domain.ErrMsgUserCannotCommentInternal {
		t.Fatalf("Add(internal): user must be denied with %q, got %v", domain.ErrMsgUserCannotCommentInternal, err)
	}
	if len(comments.comments[ticket.ID]) != 1 {
		t.Fatal("Add(internal): denied comment must not be stored")
	}
	if len(tickets.getByIDCalls) != 1 {
		t.Fatalf("Add(internal): denial must fire before the ticket lookup (1 call for the public add), got calls %v", tickets.getByIDCalls)
	}
}

// TestAddCommentAgentCreatesBothVisibilities proves an agent-role actor may
// create public AND internal comments within scope, each stored with its own
// visibility (comment-visibility spec: "Agent creates internal comment").
func TestAddCommentAgentCreatesBothVisibilities(t *testing.T) {
	clock := fixedClock()
	tickets := newFakeTicketStore()
	comments := newFakeCommentStore()
	agent := domain.User{ID: 1, Name: "Xylo", Email: "xylo@example.com", Role: domain.RoleAgent}
	cat := newFakeCategoryStore().seed("Bugs")
	ticket := tickets.seed(domain.Ticket{
		Title: "Seeded", CategoryID: cat.ID, Priority: domain.PriorityLow,
		State: domain.StateNew, CreatedAt: clock.now, UpdatedAt: clock.now,
		UserID: &agent.ID,
	})
	svc := application.NewCommentService(tickets, comments, clock)

	pub, err := svc.Add(context.Background(), agent, ticket.ID, "Public note", "public")
	if err != nil {
		t.Fatalf("Add(public): unexpected error: %v", err)
	}
	internal, err := svc.Add(context.Background(), agent, ticket.ID, "Staff only", "internal")
	if err != nil {
		t.Fatalf("Add(internal): agent must be allowed internal comments, got %v", err)
	}
	if pub.Visibility != domain.CommentPublic || internal.Visibility != domain.CommentInternal {
		t.Fatalf("visibilities = %q / %q, want public / internal", pub.Visibility, internal.Visibility)
	}
	stored := comments.comments[ticket.ID]
	if len(stored) != 2 {
		t.Fatalf("stored = %d comments, want 2", len(stored))
	}
	if stored[0].Visibility != domain.CommentPublic || stored[1].Visibility != domain.CommentInternal {
		t.Fatalf("stored visibilities = %q / %q, want public / internal", stored[0].Visibility, stored[1].Visibility)
	}
}

// TestAddCommentVisibilityValidation proves forged visibility values are
// rejected (fail closed) while an omitted visibility defaults to public
// (migration 0003 default: legacy comments backfill to public).
func TestAddCommentVisibilityValidation(t *testing.T) {
	clock := fixedClock()
	tickets := newFakeTicketStore()
	comments := newFakeCommentStore()
	admin := domain.User{Name: "Ada", Email: "ada@example.com", Role: domain.RoleAdmin}
	cat := newFakeCategoryStore().seed("Bugs")
	ticket := tickets.seed(domain.Ticket{
		Title: "Seeded", CategoryID: cat.ID, Priority: domain.PriorityLow,
		State: domain.StateNew, CreatedAt: clock.now, UpdatedAt: clock.now,
	})
	svc := application.NewCommentService(tickets, comments, clock)

	// Unknown visibility: rejected before any query or store call.
	_, err := svc.Add(context.Background(), admin, ticket.ID, "Forged", "secret")
	var verr *domain.ValidationError
	if !errors.As(err, &verr) || verr.Field != "visibility" {
		t.Fatalf("Add(secret): must be a ValidationError on field visibility, got %v", err)
	}
	if len(comments.comments[ticket.ID]) != 0 {
		t.Fatal("Add(secret): rejected comment must not be stored")
	}
	if len(tickets.getByIDCalls) != 0 {
		t.Fatalf("Add(secret): rejection must fire before the ticket lookup, got calls %v", tickets.getByIDCalls)
	}

	// Omitted visibility defaults to public (legacy form posts, backfill).
	c, err := svc.Add(context.Background(), admin, ticket.ID, "Legacy note", "")
	if err != nil {
		t.Fatalf("Add(\"\"): omitted visibility must default to public, got %v", err)
	}
	if c.Visibility != domain.CommentPublic {
		t.Fatalf("Add(\"\"): visibility must default to public, got %q", c.Visibility)
	}
}

// commentUpdater and commentDeleter mirror the edit/delete operations the
// MVP MUST NOT provide (comment-timeline spec, append-only). The negative
// type assertions in TestAppendOnlyCommentsNoUpdateOrDelete are the runtime
// guard: if the port or a use case ever grows one of these operations, this
// test fails at the contract boundary.
type commentUpdater interface {
	Update(context.Context, domain.Comment) error
}

type commentDeleter interface {
	Delete(context.Context, int64) error
}

// TestAppendOnlyCommentsNoUpdateOrDelete is the runtime evidence for the
// append-only scenario: neither the CommentStore port (via the fake) nor
// the CommentService use case exposes an update or delete operation, and
// the timeline returns exactly what was added, unchanged.
func TestAppendOnlyCommentsNoUpdateOrDelete(t *testing.T) {
	clock := fixedClock()
	store := newFakeCommentStore()
	tickets := newFakeTicketStore()
	cat := newFakeCategoryStore().seed("Bugs")
	ticket := tickets.seed(domain.Ticket{
		Title: "Seeded", CategoryID: cat.ID, Priority: domain.PriorityLow,
		State: domain.StateNew, CreatedAt: clock.now, UpdatedAt: clock.now,
	})
	svc := application.NewCommentService(tickets, store, clock)

	// The store port must not expose edit/delete operations...
	if upd, ok := any(store).(commentUpdater); ok {
		t.Fatalf("append-only violated: CommentStore must not implement Update, got %T", upd)
	}
	if del, ok := any(store).(commentDeleter); ok {
		t.Fatalf("append-only violated: CommentStore must not implement Delete, got %T", del)
	}
	// ...and neither may the use case boundary.
	if upd, ok := any(svc).(commentUpdater); ok {
		t.Fatalf("append-only violated: CommentService must not implement Update, got %T", upd)
	}
	if del, ok := any(svc).(commentDeleter); ok {
		t.Fatalf("append-only violated: CommentService must not implement Delete, got %T", del)
	}

	// The timeline returns exactly the added comments, in creation order:
	// with no edit/delete path, nothing can change or remove them.
	actor := domain.User{Name: "Ada", Role: domain.RoleAdmin}
	for _, body := range []string{"first", "second"} {
		clock.Advance(timeMinute)
		if _, err := svc.Add(context.Background(), actor, ticket.ID, body, "public"); err != nil {
			t.Fatalf("Add(%q): unexpected error: %v", body, err)
		}
	}

	list, err := svc.ListByTicket(context.Background(), ticket.ID, true)
	if err != nil {
		t.Fatalf("ListByTicket: unexpected error: %v", err)
	}
	if len(list) != 2 || list[0].Body != "first" || list[1].Body != "second" {
		t.Fatalf("ListByTicket: exactly the added comments, in order, got %+v", list)
	}
}

// ---------------------------------------------------------------------------
// Comment carve-out on resolved (comment-timeline delta): while a ticket is
// resolved, ONLY its requester may comment on it; requester-NULL resolved
// tickets accept no comments from anyone; closed/cancelled still reject
// everyone; new/in_progress rows are unchanged.
// ---------------------------------------------------------------------------

// TestAddCommentRequesterCarveOutOnResolved pins the resolved-state comment
// carve-out: the requester keeps an active voice (public only, role rule
// intact) on their own resolved ticket, while every non-requester actor and
// every requester-NULL resolved ticket stays denied before any comment write
// (closed/cancelled regression rows below stay green for everyone).
func TestAddCommentRequesterCarveOutOnResolved(t *testing.T) {
	t.Run("requester public comment on own resolved ticket", func(t *testing.T) {
		clock := fixedClock()
		tickets := newFakeTicketStore()
		comments := newFakeCommentStore()
		requester := domain.User{ID: 1, Name: "Bob", Email: "bob@example.com", Role: domain.RoleUser}
		cat := newFakeCategoryStore().seed("Bugs")
		now := clock.Now()
		ticket := tickets.seed(domain.Ticket{
			Title: "Resolved ticket", CategoryID: cat.ID, Priority: domain.PriorityLow,
			State: domain.StateResolved, CreatedAt: now, UpdatedAt: now,
			RequesterUserID: &requester.ID,
		})
		svc := application.NewCommentService(tickets, comments, clock)
		clock.Advance(timeMinute)

		c, err := svc.Add(context.Background(), requester, ticket.ID, "Still relevant, one question", "public")
		if err != nil {
			t.Fatalf("Add: requester public comment on own resolved ticket must be accepted, got %v", err)
		}
		if c.Visibility != domain.CommentPublic {
			t.Fatalf("Add: visibility must be public, got %q", c.Visibility)
		}
		stored := comments.comments[ticket.ID]
		if len(stored) != 1 || stored[0].Author != requester.Name {
			t.Fatalf("Add: comment must be stored with the requester as author, got %+v", stored)
		}
	})

	t.Run("requester internal visibility still rejected", func(t *testing.T) {
		clock := fixedClock()
		tickets := newFakeTicketStore()
		comments := newFakeCommentStore()
		requester := domain.User{ID: 1, Name: "Bob", Email: "bob@example.com", Role: domain.RoleUser}
		cat := newFakeCategoryStore().seed("Bugs")
		now := clock.Now()
		ticket := tickets.seed(domain.Ticket{
			Title: "Resolved ticket", CategoryID: cat.ID, Priority: domain.PriorityLow,
			State: domain.StateResolved, CreatedAt: now, UpdatedAt: now,
			RequesterUserID: &requester.ID,
		})
		svc := application.NewCommentService(tickets, comments, clock)

		_, err := svc.Add(context.Background(), requester, ticket.ID, "Staff secret", "internal")
		var ferr *domain.ForbiddenError
		if !errors.As(err, &ferr) || ferr.Message != domain.ErrMsgUserCannotCommentInternal {
			t.Fatalf("Add(internal): role rule must stay intact with %q, got %v", domain.ErrMsgUserCannotCommentInternal, err)
		}
		if len(comments.comments[ticket.ID]) != 0 {
			t.Fatal("Add(internal): rejected comment must not be stored")
		}
	})

	t.Run("non-requester rejected on requester-owned resolved ticket", func(t *testing.T) {
		// In-scope non-requesters (assigned agent, admin/root) reach the closed-state
		// guard and get its exact Forbidden message; an unrelated role-user is
		// denied earlier by the scoped read (NotFound — no existence leak), which
		// is still a rejection with no write.
		for _, tc := range []struct {
			name  string
			role  domain.Role
			rawID int64
		}{
			{name: "agent", role: domain.RoleAgent, rawID: 9},
			{name: "admin", role: domain.RoleAdmin, rawID: 10},
			{name: "root", role: domain.RoleRoot, rawID: 11},
		} {
			t.Run(tc.name, func(t *testing.T) {
				clock := fixedClock()
				tickets := newFakeTicketStore()
				comments := newFakeCommentStore()
				requester := domain.User{ID: 1, Name: "Bob", Email: "bob@example.com", Role: domain.RoleUser}
				cat := newFakeCategoryStore().seed("Bugs")
				now := clock.Now()
				ticket := tickets.seed(domain.Ticket{
					Title: "Resolved ticket", CategoryID: cat.ID, Priority: domain.PriorityLow,
					State: domain.StateResolved, CreatedAt: now, UpdatedAt: now,
					RequesterUserID: &requester.ID,
					UserID:          &tc.rawID,
				})
				svc := application.NewCommentService(tickets, comments, clock)
				actor := domain.User{ID: tc.rawID, Name: "Staff", Role: tc.role}

				_, err := svc.Add(context.Background(), actor, ticket.ID, "Not my ticket", "public")
				var ferr *domain.ForbiddenError
				if !errors.As(err, &ferr) || ferr.Message != domain.ErrMsgCommentOnClosedTicket {
					t.Fatalf("Add: non-requester %s must be denied with %q, got %v", tc.role, domain.ErrMsgCommentOnClosedTicket, err)
				}
				if len(comments.comments[ticket.ID]) != 0 {
					t.Fatal("Add: rejected comment must not be stored")
				}
				if len(comments.addCalls) != 0 {
					t.Fatalf("Add: denial must fire before the comment store call, got %+v", comments.addCalls)
				}
			})
		}
		t.Run("other_user", func(t *testing.T) {
			clock := fixedClock()
			tickets := newFakeTicketStore()
			comments := newFakeCommentStore()
			requester := domain.User{ID: 1, Name: "Bob", Email: "bob@example.com", Role: domain.RoleUser}
			cat := newFakeCategoryStore().seed("Bugs")
			now := clock.Now()
			ticket := tickets.seed(domain.Ticket{
				Title: "Resolved ticket", CategoryID: cat.ID, Priority: domain.PriorityLow,
				State: domain.StateResolved, CreatedAt: now, UpdatedAt: now,
				RequesterUserID: &requester.ID,
			})
			svc := application.NewCommentService(tickets, comments, clock)
			other := domain.User{ID: 42, Name: "Mallory", Role: domain.RoleUser}

			_, err := svc.Add(context.Background(), other, ticket.ID, "Not my ticket", "public")
			var nerr *domain.NotFoundError
			if !errors.As(err, &nerr) {
				t.Fatalf("Add: unrelated role-user must be denied by the scoped read (NotFound), got %v", err)
			}
			if len(comments.comments[ticket.ID]) != 0 {
				t.Fatal("Add: rejected comment must not be stored")
			}
		})
	})

	t.Run("requester-NULL resolved ticket rejects every actor", func(t *testing.T) {
		// In-scope actors (assigned agent, admin) reach the closed-state guard and
		// get its exact Forbidden message — a requester-NULL resolved ticket has no
		// requester, so the identity predicate can never admit anyone. Out-of-scope
		// actors are denied earlier by the scoped read (NotFound — no existence
		// leak): same no-write outcome, earlier wall.
		for _, tc := range []struct {
			name  string
			actor domain.User
			// assigned seeds the actor as the assignee so the scoped read admits
			// them and the rejection pins the state guard, not the scope wall.
			assigned bool
		}{
			{name: "assigned_agent", actor: domain.User{ID: 9, Name: "Xylo", Role: domain.RoleAgent}, assigned: true},
			{name: "admin", actor: domain.User{ID: 10, Name: "Ada", Role: domain.RoleAdmin}},
			{name: "unassigned_agent", actor: domain.User{ID: 11, Name: "Yuki", Role: domain.RoleAgent}},
			{name: "user", actor: domain.User{ID: 42, Name: "Mallory", Role: domain.RoleUser}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				clock := fixedClock()
				tickets := newFakeTicketStore()
				comments := newFakeCommentStore()
				cat := newFakeCategoryStore().seed("Bugs")
				now := clock.Now()
				seeded := domain.Ticket{
					Title: "Resolved legacy ticket", CategoryID: cat.ID, Priority: domain.PriorityLow,
					State: domain.StateResolved, CreatedAt: now, UpdatedAt: now,
				}
				if tc.assigned {
					seeded.UserID = &tc.actor.ID
				}
				ticket := tickets.seed(seeded)
				svc := application.NewCommentService(tickets, comments, clock)

				if tc.assigned || tc.actor.Role == domain.RoleAdmin {
					// In scope (assigned agent, or admin/root ScopeAll): the
					// closed-state guard denies — a requester-NULL resolved ticket
					// has no requester, so the identity predicate admits nobody.
					_, err := svc.Add(context.Background(), tc.actor, ticket.ID, "Still relevant", "public")
					var ferr *domain.ForbiddenError
					if !errors.As(err, &ferr) || ferr.Message != domain.ErrMsgCommentOnClosedTicket {
						t.Fatalf("Add: requester-NULL resolved ticket must reject %s with %q, got %v", tc.name, domain.ErrMsgCommentOnClosedTicket, err)
					}
					if len(comments.addCalls) != 0 {
						t.Fatalf("Add: denial must fire before the comment store call, got %+v", comments.addCalls)
					}
				} else {
					// Out of scope (unassigned agent ScopeAssigned, role user
					// ScopeOwned on a requester-NULL ticket): the scoped read
					// denies before the state guard — no existence leak.
					_, err := svc.Add(context.Background(), tc.actor, ticket.ID, "Still relevant", "public")
					var nerr *domain.NotFoundError
					if !errors.As(err, &nerr) {
						t.Fatalf("Add: out-of-scope %s must be denied by the scoped read (NotFound), got %v", tc.name, err)
					}
				}
				if len(comments.comments[ticket.ID]) != 0 {
					t.Fatal("Add: rejected comment must not be stored")
				}
			})
		}
	})

	t.Run("closed and cancelled still reject everyone", func(t *testing.T) {
		for _, state := range []domain.State{domain.StateClosed, domain.StateCancelled} {
			t.Run(string(state), func(t *testing.T) {
				clock := fixedClock()
				tickets := newFakeTicketStore()
				comments := newFakeCommentStore()
				requester := domain.User{ID: 1, Name: "Bob", Email: "bob@example.com", Role: domain.RoleUser}
				cat := newFakeCategoryStore().seed("Bugs")
				now := clock.Now()
				ticket := tickets.seed(domain.Ticket{
					Title: "Terminal ticket", CategoryID: cat.ID, Priority: domain.PriorityLow,
					State: state, CreatedAt: now, UpdatedAt: now,
					RequesterUserID: &requester.ID,
				})
				svc := application.NewCommentService(tickets, comments, clock)

				_, err := svc.Add(context.Background(), requester, ticket.ID, "Too late", "public")
				var ferr *domain.ForbiddenError
				if !errors.As(err, &ferr) || ferr.Message != domain.ErrMsgCommentOnClosedTicket {
					t.Fatalf("Add: %s ticket must reject everyone with %q, got %v", state, domain.ErrMsgCommentOnClosedTicket, err)
				}
				if len(comments.comments[ticket.ID]) != 0 {
					t.Fatal("Add: rejected comment must not be stored")
				}
			})
		}
	})
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
	actor := domain.User{Name: "Ada", Role: domain.RoleAdmin}

	for _, body := range []string{"first", "second", "third"} {
		clock.Advance(timeMinute)
		if _, err := svc.Add(context.Background(), actor, ticket.ID, body, "public"); err != nil {
			t.Fatalf("Add(%q): unexpected error: %v", body, err)
		}
	}

	list, err := svc.ListByTicket(context.Background(), ticket.ID, true)
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
