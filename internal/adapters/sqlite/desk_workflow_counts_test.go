package sqlite

import (
	"context"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestDeskWorkflowCountsCurrentPublication(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	a := seedMetricDesk(t, s, "Alpha")
	b := seedMetricDesk(t, s, "Beta")
	c := seedMetricCategory(t, s, "Routing")
	claim := func(id int64) domain.WorkflowStep {
		return domain.WorkflowStep{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: id, Strategy: domain.StrategyClaim}}
	}
	check := func(wantA, wantB int) {
		t.Helper()
		rows, err := s.CatalogStore().(application.CatalogTreeStore).ListTreeDesks(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			want := 0
			if row.ID == a.ID {
				want = wantA
			}
			if row.ID == b.ID {
				want = wantB
			}
			if row.CategoryCount != want || row.OfferedCategoryCount != want {
				t.Errorf("desk %s categories=%d offered=%d want=%d", row.Name, row.CategoryCount, row.OfferedCategoryCount, want)
			}
		}
	}
	if err := s.WorkflowStore().UpsertDraft(ctx, c.ID, mustCanon(t, domain.WorkflowDefinition{claim(a.ID)})); err != nil {
		t.Fatal(err)
	}
	check(0, 0)
	old := seedPublished(t, s, c.ID, domain.WorkflowDefinition{claim(a.ID), claim(a.ID), claim(b.ID)})
	check(1, 1)
	seedPinnedTicket(t, s, domain.Ticket{Number: 1, Title: "Old pin", CategoryID: c.ID, WorkflowVersionID: &old, State: domain.StateNew, Priority: domain.PriorityMedium, CreatedAt: testClock, UpdatedAt: testClock})
	agent := seedUserRaw(t, s, "Agent", "counts@example.com", "agent")
	addMemberRaw(t, s, b.ID, agent)
	if _, issues, err := s.WorkflowStore().Publish(ctx, c.ID, mustCanon(t, domain.WorkflowDefinition{claim(b.ID)}), nil); err != nil || len(issues) != 0 {
		t.Fatalf("republish: %v issues=%v", err, issues)
	}
	if err := s.WorkflowStore().UpsertDraft(ctx, c.ID, mustCanon(t, domain.WorkflowDefinition{claim(a.ID)})); err != nil {
		t.Fatal(err)
	}
	check(0, 1)
	other := seedMetricCategory(t, s, "Second")
	seedPublished(t, s, other.ID, domain.WorkflowDefinition{claim(b.ID)})
	check(0, 2)
}

// Historical versions can contain schema-valid JSON that is not a workflow
// definition. Arrange that corruption directly; assert through the public port.
func TestDeskWorkflowCountsPrimitiveSteps(t *testing.T) {
	for _, steps := range []string{`["broken"]`, `[null]`, `[true,false]`, `[1,1.5]`, `[[]]`} {
		t.Run(steps, func(t *testing.T) {
			s := newTestDB(t)
			ctx := context.Background()
			desk := seedMetricDesk(t, s, "Target")
			category := seedMetricCategory(t, s, "Malformed")
			seedPublished(t, s, category.ID, domain.WorkflowDefinition{{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: desk.ID, Strategy: domain.StrategyClaim}}})
			result, err := s.db.ExecContext(ctx, `INSERT INTO workflow_versions (category_id, version_no, steps_json, published_at) VALUES (?, 2, ?, ?)`, category.ID, steps, formatTime(testClock))
			if err != nil {
				t.Fatal(err)
			}
			version, err := result.LastInsertId()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.ExecContext(ctx, `UPDATE category_workflows SET current_version_id=? WHERE category_id=?`, version, category.ID); err != nil {
				t.Fatal(err)
			}
			rows, err := s.CatalogStore().(application.CatalogTreeStore).ListTreeDesks(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 2 {
				t.Fatalf("desks=%d, want 2 (General and Target)", len(rows))
			}
			for _, row := range rows {
				if row.CategoryCount != 0 || row.OfferedCategoryCount != 0 || row.OpenTickets != 0 || row.AgentCount != 0 {
					t.Errorf("primitive steps must not contribute counts: %+v", row)
				}
			}
		})
	}
}

func TestDeskWorkflowCountsOpenPopulation(t *testing.T) {
	for _, state := range []domain.State{domain.StateNew, domain.StateInProgress, domain.StateResolved, domain.StateClosed, domain.StateCancelled} {
		t.Run(string(state), func(t *testing.T) {
			s := newTestDB(t)
			desk := seedMetricDesk(t, s, "Responsible")
			category := seedMetricCategory(t, s, "Legacy")
			ticket := seedPinnedTicket(t, s, domain.Ticket{Number: 1, Title: "Work", CategoryID: category.ID, State: state, Priority: domain.PriorityMedium, CreatedAt: testClock, UpdatedAt: testClock})
			// A completed run retains its assignment for any ticket state.
			seedRun(t, s, ticket.ID, 0, "active", testClock)
			tx, err := s.db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if err := applyCursorCAS(context.Background(), tx, ticket.ID, 0, "active", 0, "completed", &testClock); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			appendMetricAssignment(t, s, ticket.ID, &desk.ID, testClock)
			rows, err := s.CatalogStore().(application.CatalogTreeStore).ListTreeDesks(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				want := 0
				if row.ID == desk.ID && state != domain.StateClosed && state != domain.StateCancelled {
					want = 1
				}
				if row.OpenTickets != want {
					t.Errorf("desk %s open=%d want=%d", row.Name, row.OpenTickets, want)
				}
			}
		})
	}
}
