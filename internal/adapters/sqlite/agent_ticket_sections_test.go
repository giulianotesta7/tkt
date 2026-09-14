package sqlite

import (
	"context"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestTicketListAgentSectionsUseCurrentClaimDeskAndExcludeOverlap(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	agent := seedUser(t, s, "Ava", "ava@example.com", true)
	other := seedUser(t, s, "Noah", "noah@example.com", true)
	deskA := seedDeskWithMemberNamed(t, s, agent, "Desk A")
	deskB := seedDeskWithMemberNamed(t, s, agent, "Desk B")
	otherDesk := seedDeskWithMemberNamed(t, s, other, "Other desk")
	catA := seedCategory(t, s, "A")
	catB := seedCategory(t, s, "B")
	catOther := seedCategory(t, s, "Other")
	catMoved := seedCategory(t, s, "Moved")

	claim := func(deskID int64) domain.WorkflowDefinition {
		return domain.WorkflowDefinition{{
			Type:         domain.StepAssignToDesk,
			AssignToDesk: &domain.AssignToDeskStep{DeskID: deskID, Strategy: domain.StrategyClaim},
		}}
	}
	versionA := seedPublished(t, s, catA, claim(deskA))
	versionB := seedPublished(t, s, catB, claim(deskB))
	versionOther := seedPublished(t, s, catOther, claim(otherDesk))
	versionMoved := seedPublished(t, s, catMoved, domain.WorkflowDefinition{
		{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: deskA, Strategy: domain.StrategyClaim}},
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "already moved"}},
	})
	seed := func(number int, title string, categoryID, versionID int64, assignee *int64) domain.Ticket {
		ticket := seedPinnedTicket(t, s, domain.Ticket{
			Number: number, Title: title, CategoryID: categoryID, WorkflowVersionID: &versionID,
			UserID: assignee, Priority: domain.PriorityMedium, State: domain.StateNew,
			CreatedAt: testClock, UpdatedAt: testClock,
		})
		seedRun(t, s, ticket.ID, 0, "active", testClock)
		return ticket
	}

	assigned := seed(1, "Assigned", catA, versionA, &agent)
	seed(2, "Claim on desk A", catA, versionA, nil)
	seed(3, "Claim on desk B", catB, versionB, nil)
	seed(4, "Other desk claim", catOther, versionOther, nil)
	seed(5, "Claim assigned to other", catA, versionA, &other)
	moved := seedPinnedTicket(t, s, domain.Ticket{
		Number: 6, Title: "Moved past claim", CategoryID: catMoved, WorkflowVersionID: &versionMoved,
		Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: testClock, UpdatedAt: testClock,
	})
	seedRun(t, s, moved.ID, 1, "active", testClock)

	personal := application.TicketQuery{
		Scope: application.ScopeAssignedOrClaimable, ActorID: agent, Section: application.TicketSectionPersonal,
	}
	got, err := s.TicketStore().List(ctx, personal, application.Page{Limit: 10})
	if err != nil {
		t.Fatalf("list personal section: %v", err)
	}
	if len(got) != 1 || got[0].ID != assigned.ID {
		t.Fatalf("personal section = %+v, want only assigned ticket %d", got, assigned.ID)
	}

	claimable := personal
	claimable.Section = application.TicketSectionClaimable
	got, err = s.TicketStore().List(ctx, claimable, application.Page{Limit: 10})
	if err != nil {
		t.Fatalf("list claimable section: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("claimable section count = %d, want 2 current unassigned member-desk claims: %+v", len(got), got)
	}
	for _, ticket := range got {
		for _, excluded := range []string{"Assigned", "Other desk claim", "Claim assigned to other", "Moved past claim"} {
			if ticket.Title == excluded {
				t.Fatalf("claimable section must exclude %q, got %+v", excluded, got)
			}
		}
	}
	count, err := s.TicketStore().Count(ctx, claimable)
	if err != nil || count != len(got) {
		t.Fatalf("claimable count = %d, %v; want %d", count, err, len(got))
	}
	search := claimable
	search.Text = `title : "Claim"`
	searched, err := s.SearchStore().Search(ctx, search, application.Page{Limit: 10})
	if err != nil {
		t.Fatalf("search claimable section: %v", err)
	}
	searchCount, err := s.SearchStore().SearchCount(ctx, search)
	if err != nil || searchCount != len(searched) || searchCount != len(got) {
		t.Fatalf("search claimable count = %d, %v; want list/search length %d/%d", searchCount, err, len(got), len(searched))
	}
}
