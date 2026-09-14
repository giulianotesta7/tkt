package sqlite

import (
	"context"
	"fmt"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// seedAgentQueueDeskAudit appends one desk-bearing audit event (arrange).
func seedAgentQueueDeskAudit(t *testing.T, s *Store, ticketID, deskID int64) {
	t.Helper()
	if err := s.AuditStore().Append(context.Background(), domain.AuditEvent{
		TicketID: ticketID, Actor: "system", Action: "workflow_assignment",
		DeskID: &deskID, CreatedAt: testClock,
	}); err != nil {
		t.Fatalf("append desk audit: %v", err)
	}
}

// seedAgentQueueClaimTicket seeds a dedicated category whose published
// workflow ends in the requested claim/least_loaded step (after stepsBefore
// manual steps), creates the ticket pinned to it, and starts its run at the
// requested cursor/status (arrange).
func seedAgentQueueClaimTicket(t *testing.T, s *Store, number int, title string, deskID int64, strategy domain.AssignmentStrategy, stepsBefore int, cursor int, status string, assignee *int64) int64 {
	t.Helper()
	catID := seedCategory(t, s, fmt.Sprintf("Queue %d", number))
	def := domain.WorkflowDefinition{}
	for i := 0; i < stepsBefore; i++ {
		def = append(def, domain.WorkflowStep{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: fmt.Sprintf("step %d", i)}})
	}
	def = append(def, domain.WorkflowStep{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: deskID, Strategy: strategy}})
	return seedAgentQueueRunTicket(t, s, number, title, catID, def, cursor, status, assignee)
}

// seedAgentQueueRunTicket pins def onto a fresh category's published version,
// creates the ticket, and starts its run at the requested cursor/status.
func seedAgentQueueRunTicket(t *testing.T, s *Store, number int, title string, catID int64, def domain.WorkflowDefinition, cursor int, status string, assignee *int64) int64 {
	t.Helper()
	versionID := seedPublished(t, s, catID, def)
	ticket := seedPinnedTicket(t, s, domain.Ticket{
		Number: number, Title: title, CategoryID: catID, WorkflowVersionID: &versionID,
		UserID: assignee, Priority: domain.PriorityMedium, State: domain.StateNew,
		CreatedAt: testClock, UpdatedAt: testClock,
	})
	seedRun(t, s, ticket.ID, cursor, status, testClock)
	return ticket.ID
}

// TestAgentQueueContextBatchesPageIDsAndResolvesFacts proves the whole page
// resolves in ONE call: the assigned row takes the LATEST surviving
// desk-bearing audit (older audits never win) and the unassigned current
// claim row takes the pinned claim desk plus its 1-based position.
func TestAgentQueueContextBatchesPageIDsAndResolvesFacts(t *testing.T) {
	s := newTestDB(t)
	agent := seedUser(t, s, "Ava", "ava@example.com", true)
	deskA := seedDeskWithMemberNamed(t, s, agent, "Desk A")
	deskB := seedDeskWithMemberNamed(t, s, agent, "Desk B")

	// Assigned at a manual step: two desk audits — the LATEST (Desk B) wins.
	assigned := seedAgentQueueClaimTicket(t, s, 1, "Assigned manual", deskA, domain.StrategyClaim, 1, 0, "active", &agent)
	seedAgentQueueDeskAudit(t, s, assigned, deskA)
	seedAgentQueueDeskAudit(t, s, assigned, deskB)

	// Unassigned at the current claim step: desk from the pinned claim step.
	claim := seedAgentQueueClaimTicket(t, s, 2, "Claimable", deskA, domain.StrategyClaim, 0, 0, "active", nil)

	got, err := newTicketStore(s.db).AgentQueueContext(context.Background(), []int64{assigned, claim})
	if err != nil {
		t.Fatalf("AgentQueueContext: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("rows = %+v, want contexts for both page ids", got)
	}
	a := got[assigned]
	if a.DeskName != "Desk B" {
		t.Fatalf("assigned desk = %q, want latest surviving desk audit %q", a.DeskName, "Desk B")
	}
	if a.CurrentTask != "step 0" {
		t.Fatalf("assigned current task = %q, want pinned manual instruction", a.CurrentTask)
	}
	if a.Position != nil {
		t.Fatalf("assigned row must carry no claim position, got %d", *a.Position)
	}
	c := got[claim]
	if c.DeskName != "Desk A" {
		t.Fatalf("claim desk = %q, want current pinned claim desk %q", c.DeskName, "Desk A")
	}
	if c.Position == nil || *c.Position != 1 {
		t.Fatalf("claim position = %v, want 1 (1-based current step)", c.Position)
	}
	if c.CurrentTask != "Waiting for claim" {
		t.Fatalf("claim current task = %q, want %q", c.CurrentTask, "Waiting for claim")
	}
}
