package application_test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestTicketViewEnrichesAuditTimelineLabels(t *testing.T) {
	type refs struct {
		ana, bruno    domain.User
		bugs, support domain.Category
	}
	tests := []struct {
		name      string
		field     string
		from      func(refs) string
		to        func(refs) string
		wantField string
		wantFrom  string
		wantTo    string
	}{
		{
			name:      "user assignment resolves empty and numeric values",
			field:     "user",
			from:      func(refs) string { return "" },
			to:        func(r refs) string { return strconv.FormatInt(r.bruno.ID, 10) },
			wantField: "Assigned To", wantFrom: "Unassigned", wantTo: "Bruno",
		},
		{
			name:      "user_id alias resolves both users",
			field:     "user_id",
			from:      func(r refs) string { return strconv.FormatInt(r.ana.ID, 10) },
			to:        func(r refs) string { return strconv.FormatInt(r.bruno.ID, 10) },
			wantField: "Assigned To", wantFrom: "Ana", wantTo: "Bruno",
		},
		{
			name:      "category resolves names",
			field:     "category",
			from:      func(r refs) string { return strconv.FormatInt(r.bugs.ID, 10) },
			to:        func(r refs) string { return strconv.FormatInt(r.support.ID, 10) },
			wantField: "Category", wantFrom: "Bugs", wantTo: "Support",
		},
		{
			name:      "category_id alias resolves names",
			field:     "category_id",
			from:      func(r refs) string { return strconv.FormatInt(r.support.ID, 10) },
			to:        func(r refs) string { return strconv.FormatInt(r.bugs.ID, 10) },
			wantField: "Category", wantFrom: "Support", wantTo: "Bugs",
		},
		{
			name:      "state is human readable",
			field:     "state",
			from:      func(refs) string { return "new" },
			to:        func(refs) string { return "in_progress" },
			wantField: "State", wantFrom: "New", wantTo: "In Progress",
		},
		{
			name:      "priority is human readable",
			field:     "priority",
			from:      func(refs) string { return "low" },
			to:        func(refs) string { return "high" },
			wantField: "Priority", wantFrom: "Low", wantTo: "High",
		},
		{
			name:      "known text field keeps values literal",
			field:     "description",
			from:      func(refs) string { return "old_value" },
			to:        func(refs) string { return "new_value" },
			wantField: "Description", wantFrom: "old_value", wantTo: "new_value",
		},
		{
			name:      "unknown field falls back safely",
			field:     "external_reference",
			from:      func(refs) string { return "ABC_1" },
			to:        func(refs) string { return "XYZ_2" },
			wantField: "External Reference", wantFrom: "ABC_1", wantTo: "XYZ_2",
		},
		{
			name:      "missing user never leaks id",
			field:     "user_id",
			from:      func(refs) string { return "999" },
			to:        func(refs) string { return "" },
			wantField: "Assigned To", wantFrom: "Unknown user", wantTo: "Unassigned",
		},
		{
			name:      "missing category never leaks id",
			field:     "category_id",
			from:      func(refs) string { return "999" },
			to:        func(refs) string { return "invalid" },
			wantField: "Category", wantFrom: "Unknown category", wantTo: "Unknown category",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := fixedClock()
			tickets := newFakeTicketStore()
			users := newFakeUserStore()
			categories := newFakeCategoryStore()
			comments := newFakeCommentStore()
			audits := newFakeAuditStore()
			r := refs{
				ana:     users.seed("Ana", "ana@example.com", true),
				bruno:   users.seed("Bruno", "bruno@example.com", true),
				bugs:    categories.seed("Bugs"),
				support: categories.seed("Support"),
			}
			ticket := tickets.seed(domain.Ticket{
				Title: "Seeded", CategoryID: r.bugs.ID, UserID: ptr(r.ana.ID),
				Priority: domain.PriorityLow, State: domain.StateNew,
				CreatedAt: clock.now, UpdatedAt: clock.now,
			})
			audits.Append(context.Background(), domain.AuditEvent{
				TicketID: ticket.ID, Actor: "Ada", Action: domain.ActionUpdate,
				Field: ptr(tt.field), FromValue: ptr(tt.from(r)), ToValue: ptr(tt.to(r)), CreatedAt: clock.now,
			})

			builder := application.NewViewBuilder(tickets, users, categories, comments, audits)
			view, err := builder.TicketView(context.Background(), ticket.ID, application.TicketQuery{Scope: application.ScopeAll})
			if err != nil {
				t.Fatalf("TicketView: unexpected error: %v", err)
			}
			if len(view.Timeline) != 1 {
				t.Fatalf("timeline entries = %d, want 1", len(view.Timeline))
			}
			item := view.Timeline[0]
			if item.ActionLabel != "Update" || item.FieldLabel != tt.wantField || item.FromLabel != tt.wantFrom || item.ToLabel != tt.wantTo {
				t.Errorf("labels = action %q field %q from %q to %q, want Update / %q / %q / %q", item.ActionLabel, item.FieldLabel, item.FromLabel, item.ToLabel, tt.wantField, tt.wantFrom, tt.wantTo)
			}
		})
	}
}

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

	view, err := builder.TicketView(context.Background(), ticket.ID, application.TicketQuery{Scope: application.ScopeAll})
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
	// Merged timeline: 4 entries, newest first (comment-timeline spec).
	if len(view.Timeline) != 4 {
		t.Fatalf("TicketView: merged timeline must have 4 entries, got %d", len(view.Timeline))
	}
	// Events (created, last mutation) come before the older comments.
	if view.Timeline[0].IsComment || view.Timeline[0].Event == nil || view.Timeline[0].Event.Action != domain.ActionCreated {
		t.Fatalf("TicketView: newest timeline entry must be the created event, got %+v", view.Timeline[0])
	}
	wantOrder := []string{"third", "second", "first"}
	for i, want := range wantOrder {
		item := view.Timeline[i+1]
		if !item.IsComment || item.Comment.Body != want {
			t.Fatalf("TicketView: timeline[%d] must be comment %q, got %+v", i+1, want, item)
		}
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

	view, err := builder.TicketView(context.Background(), ticket.ID, application.TicketQuery{Scope: application.ScopeAll})
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

	view, err := builder.TicketView(context.Background(), ticket.ID, application.TicketQuery{Scope: application.ScopeAll})
	if err != nil {
		t.Fatalf("TicketView: unexpected error: %v", err)
	}
	if view.AssignedUser == nil || view.AssignedUser.Active {
		t.Fatalf("TicketView: deactivated user must still be shown as assigned, got %+v", view.AssignedUser)
	}
}

func TestTicketViewUnknownTicket(t *testing.T) {
	builder := application.NewViewBuilder(newFakeTicketStore(), newFakeUserStore(), newFakeCategoryStore(), newFakeCommentStore(), newFakeAuditStore())

	_, err := builder.TicketView(context.Background(), 4242, application.TicketQuery{Scope: application.ScopeAll})
	var nerr *domain.NotFoundError
	if !errors.As(err, &nerr) || nerr.Kind != "ticket" {
		t.Fatalf("TicketView: unknown ticket must be a NotFoundError(kind=ticket), got %v", err)
	}
}
