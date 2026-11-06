package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Historical fixtures deliberately include incomplete evidence. Reads go through
// the public metrics port and service; audits use the append-only audited path.
func TestWorkflowResponsibilityMetrics(t *testing.T) {
	for _, tc := range []struct {
		name          string
		step          domain.StepType
		strategy      domain.AssignmentStrategy
		cursor        int
		completed     bool
		missingConfig bool
		history       []int
		want          int
	}{
		{name: "latest assignment replaces previous desk", history: []int{1, 2}, want: 2},
		{name: "pending claim overrides previous desk", step: domain.StepAssignToDesk, strategy: domain.StrategyClaim, history: []int{1}, want: 2},
		{name: "first pending claim needs no audit", step: domain.StepAssignToDesk, strategy: domain.StrategyClaim, want: 2},
		{name: "manual step retains desk", step: domain.StepManualTask, history: []int{1}, want: 1},
		{name: "form step retains desk", step: domain.StepForm, history: []int{1}, want: 1},
		{name: "completed run ignores stale claim cursor and retains last desk", step: domain.StepAssignToDesk, strategy: domain.StrategyClaim, completed: true, history: []int{2, 1}, want: 1},
		{name: "legacy without history has no category or member fallback"},
		{name: "future claim is not responsibility", step: domain.StepManualTask},
		{name: "claim at active cursor is responsibility", step: domain.StepManualTask, cursor: 1, want: 2},
		{name: "unexecuted least loaded is not responsibility", step: domain.StepAssignToDesk, strategy: domain.StrategyLeastLoaded},
		{name: "out of range cursor retains evidence", step: domain.StepAssignToDesk, strategy: domain.StrategyClaim, cursor: 9, history: []int{1}, want: 1},
		{name: "malformed assignment config is not evidence", step: domain.StepAssignToDesk, missingConfig: true},
		{name: "latest null desk does not resurrect earlier desk", history: []int{1, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestDB(t)
			ctx := context.Background()
			old := seedMetricDesk(t, s, "Previous")
			next := seedMetricDesk(t, s, "Next")
			// The agent's own desk is a decoy, not responsibility evidence.
			unrelated := seedMetricDesk(t, s, "Category desk")
			category := seedMetricCategory(t, s, "Category")
			agent := seedUserRaw(t, s, "Agent", "responsibility@example.com", "agent")
			addMemberRaw(t, s, unrelated.ID, agent)
			desks := []*int64{nil, &old.ID, &next.ID}
			ticket := domain.Ticket{Number: 1, Title: "Responsibility", CategoryID: category.ID, Priority: domain.PriorityMedium, State: domain.StateInProgress, UserID: &agent, CreatedAt: testClock, UpdatedAt: testClock}
			if tc.completed {
				ticket.State = domain.StateClosed
			}
			if tc.step != "" {
				step := domain.WorkflowStep{Type: tc.step}
				switch tc.step {
				case domain.StepAssignToDesk:
					if !tc.missingConfig {
						step.AssignToDesk = &domain.AssignToDeskStep{DeskID: next.ID, Strategy: tc.strategy}
					}
				case domain.StepManualTask:
					step.ManualTask = &domain.ManualTaskStep{Instructions: "Do work"}
				case domain.StepForm:
					step.Form = &domain.FormStep{Actor: domain.FormActorAssignee, Fields: []domain.FormField{{Key: "answer", Label: "Answer", Kind: domain.FieldShortText}}}
				}
				def := domain.WorkflowDefinition{step, {Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: next.ID, Strategy: domain.StrategyClaim}}}
				vid := seedPublished(t, s, category.ID, def)
				ticket.WorkflowVersionID = &vid
			}
			ticket = seedPinnedTicket(t, s, ticket)
			if tc.step != "" {
				seedRun(t, s, ticket.ID, tc.cursor, "active", testClock)
				if tc.completed {
					tx, err := s.db.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					if err := applyCursorCAS(ctx, tx, ticket.ID, tc.cursor, "active", tc.cursor, "completed", &testClock); err != nil {
						t.Fatal(err)
					}
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
				}
				// Current publication is not the ticket's immutable pin.
				if _, err := s.db.Exec(`UPDATE category_workflows SET current_version_id=NULL WHERE category_id=?`, category.ID); err != nil {
					t.Fatal(err)
				}
			}
			for _, desk := range tc.history {
				appendMetricAssignment(t, s, ticket.ID, desks[desk], testClock.Add(-48*time.Hour))
			}
			if len(tc.history) > 0 && tc.want != 0 {
				// A later inserted, older audit must not win over occurrence time.
				appendMetricAssignment(t, s, ticket.ID, &unrelated.ID, testClock.Add(-72*time.Hour))
			}
			// A non-workflow audit with desk context must never establish ownership.
			if err := s.AuditStore().Append(ctx, domain.AuditEvent{TicketID: ticket.ID, Actor: "agent", Action: domain.ActionTransition, DeskID: &unrelated.ID, CreatedAt: testClock}); err != nil {
				t.Fatal(err)
			}
			deskRows, err := s.CatalogStore().(application.CatalogTreeStore).ListTreeDesks(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, desk := range deskRows {
				wantOpen := 0
				if !tc.completed && desks[tc.want] != nil && desk.ID == *desks[tc.want] {
					wantOpen = 1
				}
				if desk.OpenTickets != wantOpen {
					t.Errorf("desk %s open=%d want=%d", desk.Name, desk.OpenTickets, wantOpen)
				}
			}
			filter := application.TicketMetricsFilter{Start: testClock.Add(-24 * time.Hour), End: testClock.Add(24 * time.Hour)}
			rows, err := s.TicketMetricsStore().TicketMetrics(ctx, filter)
			if err != nil || len(rows) != 1 {
				t.Fatalf("rows=%+v err=%v", rows, err)
			}
			want := desks[tc.want]
			label := "No desk"
			if tc.want == 1 {
				label = old.Name
			}
			if tc.want == 2 {
				label = next.Name
			}
			if !sameIntPtr(rows[0].DeskID, want) || rows[0].AgentID == nil || *rows[0].AgentID != agent {
				t.Fatalf("attribution=%+v, want desk %s and agent %d", rows[0], label, agent)
			}
			if (want == nil && rows[0].DeskName != "") || (want != nil && rows[0].DeskName != label) {
				t.Fatalf("desk name=%q, want %q (empty for no desk)", rows[0].DeskName, label)
			}
			for _, desk := range []int64{old.ID, next.ID, unrelated.ID} {
				filter.DeskID = &desk
				got, err := s.TicketMetricsStore().TicketMetrics(ctx, filter)
				count := 0
				if want != nil && *want == desk {
					count = 1
				}
				if err != nil || len(got) != count {
					t.Fatalf("desk filter %d: rows=%+v err=%v want=%d", desk, got, err, count)
				}
			}
			filter.DeskID = nil
			filter.WorkloadBy = "desk"
			metrics, err := application.NewTicketMetricsService(s.TicketMetricsStore(), metricStoreClock{now: testClock}).View(ctx, domain.User{Role: domain.RoleAdmin}, filter)
			if err != nil {
				t.Fatal(err)
			}
			if tc.completed {
				if metrics.Pending != 0 || len(metrics.Workload) != 0 {
					t.Fatalf("completed workload: %+v", metrics)
				}
			} else if metrics.Pending != 1 || metrics.Unassigned != 0 || len(metrics.Workload) != 1 || metrics.Workload[0].Label != label || metrics.Workload[0].Count != 1 {
				t.Fatalf("workload=%+v pending=%d unassigned=%d", metrics.Workload, metrics.Pending, metrics.Unassigned)
			}
		})
	}
}

func appendMetricAssignment(t *testing.T, s *Store, ticketID int64, deskID *int64, at time.Time) {
	t.Helper()
	if err := s.AuditStore().Append(context.Background(), domain.AuditEvent{TicketID: ticketID, Actor: "workflow", Action: domain.ActionWorkflowAssignment, Field: ptr("user"), ToValue: ptr("1"), DeskID: deskID, CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowResponsibilityMetricsUsesAuditIndex(t *testing.T) {
	s := newTestDB(t)
	query, args := ticketMetricsQuery(application.TicketMetricsFilter{Start: testClock, End: testClock.Add(24 * time.Hour)})
	rows, err := s.db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	indexed := false
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(detail, "SEARCH assignment USING INDEX idx_audit_ticket") {
			indexed = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !indexed {
		t.Fatal("responsibility audit lookup must reuse idx_audit_ticket")
	}
}
