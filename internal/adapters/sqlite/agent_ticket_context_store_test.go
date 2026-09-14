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

// TestAgentQueueContextAssignedWithoutDeskAuditEmpty proves a manually
// assigned ticket (person assignment without any desk-bearing audit) has an
// empty desk name — the read model never infers a desk — while its current
// task text still resolves from the active run.
func TestAgentQueueContextAssignedWithoutDeskAuditEmpty(t *testing.T) {
	s := newTestDB(t)
	agent := seedUser(t, s, "Ava", "ava@example.com", true)
	desk := seedDeskWithMemberNamed(t, s, agent, "Desk A")
	ticket := seedAgentQueueClaimTicket(t, s, 1, "Manually assigned", desk, domain.StrategyClaim, 1, 0, "active", &agent)

	got, err := newTicketStore(s.db).AgentQueueContext(context.Background(), []int64{ticket})
	if err != nil {
		t.Fatalf("AgentQueueContext: %v", err)
	}
	row := got[ticket]
	if row.DeskName != "" {
		t.Fatalf("manually assigned desk = %q, want empty (no desk audit to infer from)", row.DeskName)
	}
	if row.CurrentTask != "step 0" {
		t.Fatalf("current task = %q, want the pinned manual instruction", row.CurrentTask)
	}
	if row.Position != nil {
		t.Fatalf("assigned row must carry no claim position, got %d", *row.Position)
	}
}

// TestAgentQueueContextInactiveRunsHaveNoTask proves CurrentTask is empty for
// a completed run, a missing run, and an out-of-bounds cursor (invalid run
// position) — never fabricated from history or the definition's first step.
func TestAgentQueueContextInactiveRunsHaveNoTask(t *testing.T) {
	s := newTestDB(t)
	agent := seedUser(t, s, "Ava", "ava@example.com", true)
	desk := seedDeskWithMemberNamed(t, s, agent, "Desk A")

	completed := seedAgentQueueClaimTicket(t, s, 1, "Completed run", desk, domain.StrategyClaim, 1, 0, "active", nil)
	// insertRunTx always writes completed_at NULL, so a completed run is seeded
	// directly with its completion timestamp (CHECK-enforced pair).
	if _, err := s.db.ExecContext(context.Background(), `UPDATE ticket_workflow_runs SET status = 'completed', completed_at = ? WHERE ticket_id = ?`, formatTime(testClock), completed); err != nil {
		t.Fatalf("complete run: %v", err)
	}
	noRun := seedAgentQueueClaimTicket(t, s, 2, "No run", desk, domain.StrategyClaim, 1, 0, "active", nil)
	s.db.ExecContext(context.Background(), `DELETE FROM ticket_workflow_runs WHERE ticket_id = ?`, noRun)
	outOfBounds := seedAgentQueueClaimTicket(t, s, 3, "Cursor past end", desk, domain.StrategyClaim, 0, 5, "active", nil)

	got, err := newTicketStore(s.db).AgentQueueContext(context.Background(), []int64{completed, noRun, outOfBounds})
	if err != nil {
		t.Fatalf("AgentQueueContext: %v", err)
	}
	for _, tc := range []struct {
		name   string
		ticket int64
	}{
		{"completed run", completed},
		{"missing run", noRun},
		{"out-of-bounds cursor", outOfBounds},
	} {
		if got[tc.ticket].CurrentTask != "" {
			t.Fatalf("%s: current task = %q, want empty", tc.name, got[tc.ticket].CurrentTask)
		}
		if got[tc.ticket].Position != nil {
			t.Fatalf("%s: position = %d, want none", tc.name, *got[tc.ticket].Position)
		}
	}
}

// TestAgentQueueContextClaimPositionOnlyForCurrentUnassignedClaimStep proves
// the 1-based claim position appears ONLY for the exact active current step
// being an unassigned assign_to_desk[claim]: a moved cursor, a least_loaded
// current step, and an assigned ticket at a claim step all stay positionless.
// An unassigned non-claim ticket never inherits its category desk or desk
// audit history.
func TestAgentQueueContextClaimPositionOnlyForCurrentUnassignedClaimStep(t *testing.T) {
	s := newTestDB(t)
	agent := seedUser(t, s, "Ava", "ava@example.com", true)
	desk := seedDeskWithMemberNamed(t, s, agent, "Desk A")

	cat := seedCategory(t, s, "Moved queue")
	// A cursor that already moved PAST the claim step: claim at step 1,
	// manual at step 2, run sitting on the manual step.
	moved := seedAgentQueueRunTicket(t, s, 1, "Moved past claim", cat, domain.WorkflowDefinition{
		{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: desk, Strategy: domain.StrategyClaim}},
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "already moved"}},
	}, 1, "active", nil)
	leastLoaded := seedAgentQueueClaimTicket(t, s, 2, "Least loaded", desk, domain.StrategyLeastLoaded, 0, 0, "active", nil)
	assigned := seedAgentQueueClaimTicket(t, s, 3, "Assigned at claim", desk, domain.StrategyClaim, 0, 0, "active", &agent)
	historical := seedAgentQueueClaimTicket(t, s, 4, "Historical desk audit", desk, domain.StrategyClaim, 1, 0, "active", nil)
	seedAgentQueueDeskAudit(t, s, historical, desk)

	got, err := newTicketStore(s.db).AgentQueueContext(context.Background(), []int64{moved, leastLoaded, assigned, historical})
	if err != nil {
		t.Fatalf("AgentQueueContext: %v", err)
	}
	for _, tc := range []struct {
		name   string
		ticket int64
	}{
		{"moved cursor", moved},
		{"least_loaded step", leastLoaded},
		{"assigned at claim step", assigned},
	} {
		if got[tc.ticket].Position != nil {
			t.Fatalf("%s: position = %d, want none", tc.name, *got[tc.ticket].Position)
		}
	}
	if row := got[historical]; row.DeskName != "" || row.Position != nil {
		t.Fatalf("unassigned non-claim row = %+v, want no desk/position (no category or history inference)", row)
	}
	if row := got[moved]; row.CurrentTask != "already moved" {
		t.Fatalf("moved cursor current task = %q, want the current step's instruction", row.CurrentTask)
	}
}

// TestAgentQueueContextDeletedDesksDegradeToEmpty proves deleted desks yield
// an empty desk name both for the pinned claim step and for the desk-bearing
// audit (the ON DELETE SET NULL FK leaves no desk_id to join).
func TestAgentQueueContextDeletedDesksDegradeToEmpty(t *testing.T) {
	s := newTestDB(t)
	agent := seedUser(t, s, "Ava", "ava@example.com", true)
	desk := seedDeskWithMemberNamed(t, s, agent, "Doomed desk")

	claim := seedAgentQueueClaimTicket(t, s, 1, "Claim on doomed desk", desk, domain.StrategyClaim, 0, 0, "active", nil)
	assigned := seedAgentQueueClaimTicket(t, s, 2, "Assigned doomed desk", desk, domain.StrategyClaim, 1, 0, "active", &agent)
	seedAgentQueueDeskAudit(t, s, assigned, desk)
	if _, err := s.db.ExecContext(context.Background(), `DELETE FROM desks WHERE id = ?`, desk); err != nil {
		t.Fatalf("delete desk: %v", err)
	}

	got, err := newTicketStore(s.db).AgentQueueContext(context.Background(), []int64{claim, assigned})
	if err != nil {
		t.Fatalf("AgentQueueContext: %v", err)
	}
	if row := got[claim]; row.DeskName != "" {
		t.Fatalf("claim desk after deletion = %q, want empty", row.DeskName)
	}
	if row := got[assigned]; row.DeskName != "" {
		t.Fatalf("assigned desk after deletion = %q, want empty (audit desk_id was set NULL)", row.DeskName)
	}
}

// TestAgentQueueContextEmptyInputReturnsEmptyWithoutQuery proves empty input
// returns an empty map without error. Query absence is by construction (the
// guard returns before any QueryContext) — this database layer has no
// query-count instrumentation to observe it at runtime.
func TestAgentQueueContextEmptyInputReturnsEmptyWithoutQuery(t *testing.T) {
	s := newTestDB(t)
	got, err := newTicketStore(s.db).AgentQueueContext(context.Background(), nil)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("nil ids must return an empty map without error, got %v, %v", got, err)
	}
	got, err = newTicketStore(s.db).AgentQueueContext(context.Background(), []int64{})
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty ids must return an empty map without error, got %v, %v", got, err)
	}
}

// TestAgentQueueContextRejectsOversizedBatch proves the batch bound fails
// closed: more than the bounded 20 ids is a caller bug, never a silently
// truncated answer.
func TestAgentQueueContextRejectsOversizedBatch(t *testing.T) {
	s := newTestDB(t)
	ids := make([]int64, 21)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	got, err := newTicketStore(s.db).AgentQueueContext(context.Background(), ids)
	if err == nil || got != nil {
		t.Fatalf("oversized batch must fail closed, got rows=%v err=%v", got, err)
	}
}
