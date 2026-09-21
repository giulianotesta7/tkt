package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestTicketMetricsStoreUsesLastInPeriodResolutionAndCurrentAttribution(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	desk := &domain.Desk{Name: "Metrics desk", CreatedAt: testClock}
	if err := s.DeskStore().Create(ctx, desk); err != nil {
		t.Fatal(err)
	}
	category := &domain.Category{Name: "Metrics category", DeskID: desk.ID, CreatedAt: testClock}
	if err := s.CategoryStore().Create(ctx, category); err != nil {
		t.Fatal(err)
	}
	agentID := seedUserRaw(t, s, "Metrics agent", "metrics-agent@example.com", "agent")
	created := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO tickets(number,title,description,requester_name,requester_email,category_id,priority,state,user_id,created_at,updated_at) VALUES(1,'metric','', 'r','r@example.com',?,'medium','in_progress',?,?,?)`, category.ID, agentID, formatTime(created), formatTime(created)); err != nil {
		t.Fatal(err)
	}
	first := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	last := time.Date(2026, 3, 5, 10, 0, 0, 0, time.UTC)
	for _, at := range []time.Time{first, last} {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO audit_events(ticket_id,actor,action,field,to_value,created_at) VALUES(1,'agent','transition','state','resolved',?)`, formatTime(at)); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.TicketMetricsStore().TicketMetrics(ctx, application.TicketMetricsFilter{Start: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !rows[0].ResolvedAt.Equal(last) || rows[0].AgentID == nil || *rows[0].AgentID != agentID || rows[0].DeskID == nil || *rows[0].DeskID != desk.ID {
		t.Fatalf("metrics rows = %+v", rows)
	}
}

func TestTicketMetricsStoreRejectsMalformedResolutionTime(t *testing.T) {
	s := newTestDB(t)
	category := seedMetricCategory(t, s, "Malformed resolution")
	ticketID := insertMetricTicket(t, s, 1, category.ID, nil, domain.StateClosed, formatTime(testClock))
	if _, err := s.db.ExecContext(context.Background(), `INSERT INTO audit_events(ticket_id,actor,action,field,to_value,created_at) VALUES(?, 'agent', 'transition', 'state', 'resolved', '2026-08-06T10:00:00Zbad')`, ticketID); err != nil {
		t.Fatal(err)
	}
	_, err := s.TicketMetricsStore().TicketMetrics(context.Background(), application.TicketMetricsFilter{Start: testClock, End: testClock.AddDate(0, 0, 1)})
	if err == nil || !strings.Contains(err.Error(), "parse metric resolution time") {
		t.Fatalf("TicketMetrics error = %v, want resolution-time parse context", err)
	}
}

func TestTicketMetricsStoreRegressionCases(t *testing.T) {
	marchStart := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	// marchEnd is the inclusive last day of the window as the HTTP boundary
	// expresses it; marchEndExclusive is the exclusive storage boundary the read
	// port receives, matching the metrics filter contract.
	marchEnd := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)
	marchEndExclusive := marchEnd.AddDate(0, 0, 1)
	type testCase struct {
		name    string
		filter  application.TicketMetricsFilter
		arrange func(t *testing.T, s *Store)
		assert  func(t *testing.T, rows []application.TicketMetricsRecord)
	}
	cases := []testCase{
		{
			name:   "month end is exclusive and later resolution does not suppress in-period resolution",
			filter: application.TicketMetricsFilter{Start: marchStart, End: marchEndExclusive},
			arrange: func(t *testing.T, s *Store) {
				category := seedMetricCategory(t, s, "Bounds")
				ticketID := insertMetricTicket(t, s, 1, category.ID, nil, domain.StateClosed, formatTime(marchStart.AddDate(0, 0, -1)))
				valid := marchEnd.Add(24*time.Hour - time.Second)
				insertMetricResolution(t, s, ticketID, valid)
				onlyAfter := insertMetricTicket(t, s, 2, category.ID, nil, domain.StateClosed, formatTime(marchStart.AddDate(0, 0, -1)))
				for _, event := range [][3]string{{"updated", "state", "resolved"}, {"transition", "priority", "resolved"}, {"transition", "state", "closed"}} {
					insertMetricAudit(t, s, onlyAfter, event[0], event[1], event[2], valid)
				}
				insertMetricResolution(t, s, onlyAfter, marchEnd.AddDate(0, 0, 1))
			},
			assert: func(t *testing.T, rows []application.TicketMetricsRecord) {
				t.Helper()
				if len(rows) != 1 {
					t.Fatalf("rows = %d, want 1", len(rows))
				}
				want := marchEnd.Add(24*time.Hour - time.Second)
				if rows[0].TicketID != 1 || !rows[0].ResolvedAt.Equal(want) {
					t.Fatalf("selected resolution = %+v, want ticket 1 at %s", rows[0], want)
				}
			},
		},
		{
			name:   "reopened and closed tickets retain resolved events",
			filter: application.TicketMetricsFilter{Start: marchStart, End: marchEndExclusive},
			arrange: func(t *testing.T, s *Store) {
				category := seedMetricCategory(t, s, "Retained")
				reopened := insertMetricTicket(t, s, 1, category.ID, nil, domain.StateInProgress, formatTime(marchStart))
				closed := insertMetricTicket(t, s, 2, category.ID, nil, domain.StateClosed, formatTime(marchStart))
				insertMetricResolution(t, s, reopened, marchStart.AddDate(0, 0, 4))
				insertMetricResolution(t, s, closed, marchStart.AddDate(0, 0, 5))
			},
			assert: func(t *testing.T, rows []application.TicketMetricsRecord) {
				t.Helper()
				if len(rows) != 2 || rows[0].CurrentState != domain.StateInProgress || rows[1].CurrentState != domain.StateClosed || rows[0].ResolvedAt.IsZero() || rows[1].ResolvedAt.IsZero() {
					t.Fatalf("rows = %+v, want reopened and closed tickets with resolutions", rows)
				}
			},
		},
		{
			name:   "identical timestamps use audit ID and do not multiply pending ticket",
			filter: application.TicketMetricsFilter{Start: marchStart, End: marchEndExclusive},
			arrange: func(t *testing.T, s *Store) {
				category := seedMetricCategory(t, s, "Tie")
				ticketID := insertMetricTicket(t, s, 1, category.ID, nil, domain.StateNew, formatTime(marchStart))
				at := marchStart.AddDate(0, 0, 7)
				insertMetricResolution(t, s, ticketID, at)
				insertMetricResolution(t, s, ticketID, at)
			},
			assert: func(t *testing.T, rows []application.TicketMetricsRecord) {
				t.Helper()
				if len(rows) != 1 || rows[0].TicketID != 1 || rows[0].CurrentState != domain.StateNew || !rows[0].ResolvedAt.Equal(marchStart.AddDate(0, 0, 7)) {
					t.Fatalf("rows = %+v, want one pending ticket with one selected resolution", rows)
				}
			},
		},
		{
			name:   "malformed creation time keeps resolved ticket and omits duration input",
			filter: application.TicketMetricsFilter{Start: marchStart, End: marchEndExclusive},
			arrange: func(t *testing.T, s *Store) {
				category := seedMetricCategory(t, s, "Malformed")
				ticketID := insertMetricTicket(t, s, 1, category.ID, nil, domain.StateClosed, "not-a-timestamp")
				insertMetricResolution(t, s, ticketID, marchStart.AddDate(0, 0, 7))
			},
			assert: func(t *testing.T, rows []application.TicketMetricsRecord) {
				t.Helper()
				if len(rows) != 1 || !rows[0].CreatedAt.IsZero() || rows[0].ResolvedAt.IsZero() {
					t.Fatalf("rows = %+v, want resolved ticket with no usable creation time", rows)
				}
			},
		},
		{
			name:   "current reassignment and category desk constrain filters",
			filter: application.TicketMetricsFilter{Start: marchStart, End: marchEndExclusive},
			arrange: func(t *testing.T, s *Store) {
				oldDesk := seedMetricDesk(t, s, "Old desk")
				newDesk := seedMetricDesk(t, s, "New desk")
				oldCategory := seedMetricCategoryAtDesk(t, s, "Old category", oldDesk.ID)
				newCategory := seedMetricCategoryAtDesk(t, s, "New category", newDesk.ID)
				oldAgent := seedUserRaw(t, s, "Old agent", "old-agent@example.com", "agent")
				newAgent := seedUserRaw(t, s, "New agent", "new-agent@example.com", "agent")
				ticketID := insertMetricTicket(t, s, 1, oldCategory.ID, &oldAgent, domain.StateInProgress, formatTime(marchStart))
				if _, err := s.db.ExecContext(context.Background(), `UPDATE tickets SET category_id = ?, user_id = ? WHERE id = ?`, newCategory.ID, newAgent, ticketID); err != nil {
					t.Fatal(err)
				}
				insertMetricResolution(t, s, ticketID, marchStart.AddDate(0, 0, 5))
			},
			assert: func(t *testing.T, rows []application.TicketMetricsRecord) {
				t.Helper()
				if len(rows) != 1 || rows[0].DeskName != "New desk" || rows[0].AgentName != "New agent" {
					t.Fatalf("current attribution = %+v, want new desk and agent", rows)
				}
			},
		},
		{
			name:   "pending backlog is independent of selected period",
			filter: application.TicketMetricsFilter{Start: marchStart, End: marchEndExclusive},
			arrange: func(t *testing.T, s *Store) {
				category := seedMetricCategory(t, s, "Pending")
				insertMetricTicket(t, s, 1, category.ID, nil, domain.StateInProgress, formatTime(marchStart.AddDate(0, 0, -30)))
				insertMetricTicket(t, s, 2, category.ID, nil, domain.StateClosed, formatTime(marchStart))
			},
			assert: func(t *testing.T, rows []application.TicketMetricsRecord) {
				t.Helper()
				if len(rows) != 2 || rows[0].TicketID != 1 || rows[0].CurrentState != domain.StateInProgress || rows[0].AgentID != nil || rows[1].TicketID != 2 {
					t.Fatalf("rows = %+v, want unassigned pending and created-only ticket", rows)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestDB(t)
			tc.arrange(t, s)
			rows, err := s.TicketMetricsStore().TicketMetrics(context.Background(), tc.filter)
			if err != nil {
				t.Fatalf("TicketMetrics: %v", err)
			}
			tc.assert(t, rows)
			if tc.name == "current reassignment and category desk constrain filters" {
				if rows[0].DeskID == nil || rows[0].AgentID == nil {
					t.Fatalf("current attribution IDs = %+v, want assigned desk and agent", rows[0])
				}
				newDeskID, newAgentID := *rows[0].DeskID, *rows[0].AgentID
				var oldDeskID, oldAgentID int64
				if err := s.db.QueryRowContext(context.Background(), `SELECT id FROM desks WHERE name = 'Old desk'`).Scan(&oldDeskID); err != nil {
					t.Fatal(err)
				}
				if err := s.db.QueryRowContext(context.Background(), `SELECT id FROM users WHERE email = 'old-agent@example.com'`).Scan(&oldAgentID); err != nil {
					t.Fatal(err)
				}
				for _, query := range []struct {
					desk, agent *int64
					want        int
				}{{&newDeskID, nil, 1}, {nil, &newAgentID, 1}, {&newDeskID, &newAgentID, 1}, {&oldDeskID, nil, 0}, {nil, &oldAgentID, 0}} {
					filtered, err := s.TicketMetricsStore().TicketMetrics(context.Background(), application.TicketMetricsFilter{Start: marchStart, End: marchEndExclusive, DeskID: query.desk, AgentID: query.agent})
					if err != nil || len(filtered) != query.want {
						t.Fatalf("desk=%v agent=%v rows=%+v err=%v, want %d", query.desk, query.agent, filtered, err, query.want)
					}
				}
				metrics, err := application.NewTicketMetricsService(s.TicketMetricsStore(), metricStoreClock{now: marchEnd}).View(context.Background(), domain.User{Role: domain.RoleAdmin}, application.TicketMetricsFilter{Start: marchStart, End: marchEnd, DeskID: &newDeskID, AgentID: &newAgentID, WorkloadBy: "desk"})
				if err != nil || metrics.Pending != 1 || len(metrics.Workload) != 1 || metrics.Workload[0] != (application.TicketMetricsWorkload{Label: "New desk", Count: 1}) {
					t.Fatalf("current desk workload = %+v, err=%v, want one New desk ticket", metrics.Workload, err)
				}
			}
			if tc.name == "pending backlog is independent of selected period" {
				aprilStart := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
				aprilEnd := time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)
				aprilRows, err := s.TicketMetricsStore().TicketMetrics(context.Background(), application.TicketMetricsFilter{Start: aprilStart, End: aprilEnd})
				if err != nil || len(aprilRows) != 1 || aprilRows[0].TicketID != rows[0].TicketID || aprilRows[0].TicketID == 2 {
					t.Fatalf("changed-period pending rows = %+v, err=%v, want only the pending ticket", aprilRows, err)
				}
			}
		})
	}
}

func seedMetricDesk(t *testing.T, s *Store, name string) *domain.Desk {
	t.Helper()
	desk := &domain.Desk{Name: name, CreatedAt: testClock}
	if err := s.DeskStore().Create(context.Background(), desk); err != nil {
		t.Fatal(err)
	}
	return desk
}

func seedMetricCategory(t *testing.T, s *Store, name string) *domain.Category {
	t.Helper()
	return seedMetricCategoryAtDesk(t, s, name, seedMetricDesk(t, s, name+" desk").ID)
}

func seedMetricCategoryAtDesk(t *testing.T, s *Store, name string, deskID int64) *domain.Category {
	t.Helper()
	category := &domain.Category{Name: name, DeskID: deskID, CreatedAt: testClock}
	if err := s.CategoryStore().Create(context.Background(), category); err != nil {
		t.Fatal(err)
	}
	return category
}

func insertMetricTicket(t *testing.T, s *Store, number, categoryID int64, agentID *int64, state domain.State, createdAt string) int64 {
	t.Helper()
	var userID any
	if agentID != nil {
		userID = *agentID
	}
	res, err := s.db.ExecContext(context.Background(), `INSERT INTO tickets(number,title,description,requester_name,requester_email,category_id,priority,state,user_id,created_at,updated_at) VALUES(?, 'metric', '', 'r', 'r@example.com', ?, 'medium', ?, ?, ?, ?)`, number, categoryID, state, userID, createdAt, createdAt)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func insertMetricResolution(t *testing.T, s *Store, ticketID int64, at time.Time) {
	insertMetricAudit(t, s, ticketID, "transition", "state", "resolved", at)
}

func insertMetricAudit(t *testing.T, s *Store, ticketID int64, action, field, value string, at time.Time) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(), `INSERT INTO audit_events(ticket_id,actor,action,field,to_value,created_at) VALUES(?, 'former-agent', ?, ?, ?, ?)`, ticketID, action, field, value, formatTime(at)); err != nil {
		t.Fatal(err)
	}
}

type metricStoreClock struct{ now time.Time }

func (c metricStoreClock) Now() time.Time { return c.now }

// The metrics read carries everything the attainment aggregation needs in the
// same single read: priority and category identity, the frozen commitment, and
// the two measured milestones. Milestones OUTSIDE the selected period still
// arrive (issue #211 decision 4), and a ticket with neither a frozen row nor
// a milestone must scan through the NULL path without error.
func TestTicketMetricsStoreCarriesSLACommitmentMilestonesAndIdentity(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	periodStart := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	created := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)

	category := seedCategory(t, s, "Attainment")
	committed := seedTicket(t, s, domain.Ticket{Number: 1, Title: "committed", CategoryID: category,
		Priority: domain.PriorityHigh, State: domain.StateResolved, CreatedAt: created, UpdatedAt: created})
	legacy := seedTicket(t, s, domain.Ticket{Number: 2, Title: "legacy", CategoryID: category,
		Priority: domain.PriorityLow, State: domain.StateNew, CreatedAt: created, UpdatedAt: created})

	frozen := domain.TicketSLA{
		FirstResponseSeconds: 14400,
		ResolveSeconds:       86400,
		StartedAt:            created,
		PolicySnapshotAt:     created,
		WarnFirstResponseAt:  created.Add(2 * time.Hour),
		DueFirstResponseAt:   created.Add(4 * time.Hour),
		WarnResolveAt:        created.Add(12 * time.Hour),
		DueResolveAt:         created.Add(24 * time.Hour),
	}
	if err := s.SLAStore().InsertTicketSLA(ctx, committed.ID, frozen); err != nil {
		t.Fatal(err)
	}
	firstResponse := time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC)
	firstResolved := time.Date(2026, 4, 6, 10, 0, 0, 0, time.UTC)
	seedStaffComment(t, s, committed.ID, "public", "agent", "reply", firstResponse)
	seedStaffComment(t, s, committed.ID, "internal", "agent", "hidden earlier", firstResponse.Add(-time.Hour))
	seedResolvedTransition(t, s, committed.ID, firstResolved)

	rows, err := s.TicketMetricsStore().TicketMetrics(ctx, application.TicketMetricsFilter{Start: periodStart, End: periodEnd})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int64]application.TicketMetricsRecord{}
	for _, r := range rows {
		byID[r.TicketID] = r
	}

	got := byID[committed.ID]
	if got.Priority != domain.PriorityHigh || got.CategoryName != "Attainment" {
		t.Fatalf("identity = %q/%q, want high/Attainment", got.Priority, got.CategoryName)
	}
	if got.SLA == nil || !got.SLA.DueFirstResponseAt.Equal(frozen.DueFirstResponseAt) || !got.SLA.DueResolveAt.Equal(frozen.DueResolveAt) {
		t.Fatalf("frozen SLA = %+v, want the frozen due instants", got.SLA)
	}
	if got.Milestones.FirstResponseAt == nil || !got.Milestones.FirstResponseAt.Equal(firstResponse) {
		t.Fatalf("FirstResponseAt = %v, want the out-of-period public staff comment at %s", got.Milestones.FirstResponseAt, firstResponse)
	}
	if got.Milestones.FirstResolvedAt == nil || !got.Milestones.FirstResolvedAt.Equal(firstResolved) {
		t.Fatalf("FirstResolvedAt = %v, want the out-of-period resolution at %s", got.Milestones.FirstResolvedAt, firstResolved)
	}

	without := byID[legacy.ID]
	if without.SLA != nil || without.Milestones.FirstResponseAt != nil || without.Milestones.FirstResolvedAt != nil {
		t.Fatalf("no-commitment NULL path = %+v, want nil SLA and nil milestones", without)
	}
	if without.Priority != domain.PriorityLow || without.CategoryName != "Attainment" {
		t.Fatalf("legacy identity = %q/%q, want low/Attainment", without.Priority, without.CategoryName)
	}
}
