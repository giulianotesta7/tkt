package application

import (
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// attainmentFrozen builds a frozen commitment anchored at base with the given
// first-response and resolution targets. The warning instants sit halfway, so
// the at-risk band is exercised as "still open".
func attainmentFrozen(base time.Time, firstTarget, resolveTarget time.Duration) *domain.TicketSLA {
	return &domain.TicketSLA{
		FirstResponseSeconds: int(firstTarget.Seconds()),
		ResolveSeconds:       int(resolveTarget.Seconds()),
		StartedAt:            base,
		WarnFirstResponseAt:  base.Add(firstTarget / 2),
		DueFirstResponseAt:   base.Add(firstTarget),
		WarnResolveAt:        base.Add(resolveTarget / 2),
		DueResolveAt:         base.Add(resolveTarget),
		PolicySnapshotAt:     base,
	}
}

func attainmentAt(base time.Time, delay time.Duration) *time.Time {
	at := base.Add(delay)
	return &at
}

// The milestone verdict is the ProjectSLA verdict: achieved at or before due
// is met, achieved late is breached, unachieved past due is breached, and
// unachieved before due is open. Open never enters the rate denominator.
func TestBuildTicketAttainmentMilestoneBoundaries(t *testing.T) {
	base := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	const firstTarget, resolveTarget = 4 * time.Hour, 24 * time.Hour
	filter := TicketMetricsFilter{Start: base, End: base.AddDate(0, 0, 7), GroupBy: TicketMetricsGroupTotal}

	tests := []struct {
		name        string
		milestones  domain.SLAMilestones
		now         time.Time
		want        TicketMetricsMilestoneAttainment
		wantResolve TicketMetricsMilestoneAttainment
	}{
		{
			name:        "achieved exactly at due is met",
			milestones:  domain.SLAMilestones{FirstResponseAt: attainmentAt(base, firstTarget)},
			now:         base.Add(firstTarget + time.Hour),
			want:        TicketMetricsMilestoneAttainment{Met: 1, Rate: 1},
			wantResolve: TicketMetricsMilestoneAttainment{Open: 1},
		},
		{
			name:        "achieved one second late is breached",
			milestones:  domain.SLAMilestones{FirstResponseAt: attainmentAt(base, firstTarget+time.Second)},
			now:         base.Add(firstTarget + time.Hour),
			want:        TicketMetricsMilestoneAttainment{Breached: 1},
			wantResolve: TicketMetricsMilestoneAttainment{Open: 1},
		},
		{
			name:        "unachieved before due is open",
			now:         base.Add(time.Hour),
			want:        TicketMetricsMilestoneAttainment{Open: 1},
			wantResolve: TicketMetricsMilestoneAttainment{Open: 1},
		},
		{
			name:        "unachieved past due is breached",
			now:         base.Add(firstTarget + time.Second),
			want:        TicketMetricsMilestoneAttainment{Breached: 1},
			wantResolve: TicketMetricsMilestoneAttainment{Open: 1},
		},
		{
			name:        "resolution achieved exactly at due is met",
			milestones:  domain.SLAMilestones{FirstResponseAt: attainmentAt(base, time.Hour), FirstResolvedAt: attainmentAt(base, resolveTarget)},
			now:         base.Add(resolveTarget + time.Hour),
			want:        TicketMetricsMilestoneAttainment{Met: 1, Rate: 1},
			wantResolve: TicketMetricsMilestoneAttainment{Met: 1, Rate: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			records := []TicketMetricsRecord{{
				TicketID:     1,
				CreatedAt:    base.Add(time.Hour),
				CurrentState: domain.StateInProgress,
				Priority:     domain.PriorityHigh,
				CategoryName: "Bugs",
				SLA:          attainmentFrozen(base, firstTarget, resolveTarget),
				Milestones:   tt.milestones,
			}}
			a := buildTicketMetrics(records, filter, tt.now).Attainment
			if len(a.Groups) != 1 {
				t.Fatalf("groups = %d, want 1", len(a.Groups))
			}
			if got := a.Groups[0].FirstResponse; got != tt.want {
				t.Fatalf("first response = %+v, want %+v", got, tt.want)
			}
			if got := a.Groups[0].Resolve; got != tt.wantResolve {
				t.Fatalf("resolve = %+v, want %+v", got, tt.wantResolve)
			}
		})
	}
}

// The rate is Met / (Met + Breached); open tickets are counted but never
// dilute the denominator.
func TestBuildTicketAttainmentRateUsesDecidedDenominator(t *testing.T) {
	base := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	filter := TicketMetricsFilter{Start: base, End: base.AddDate(0, 0, 7), GroupBy: TicketMetricsGroupTotal}
	frozen := attainmentFrozen(base, 4*time.Hour, 24*time.Hour)
	record := func(id int64, milestones domain.SLAMilestones) TicketMetricsRecord {
		return TicketMetricsRecord{
			TicketID: id, CreatedAt: base.Add(time.Hour), CurrentState: domain.StateInProgress,
			Priority: domain.PriorityMedium, CategoryName: "Bugs", SLA: frozen, Milestones: milestones,
		}
	}
	records := []TicketMetricsRecord{
		record(1, domain.SLAMilestones{FirstResponseAt: attainmentAt(base, time.Hour)}),
		record(2, domain.SLAMilestones{FirstResponseAt: attainmentAt(base, 5*time.Hour)}),
		record(3, domain.SLAMilestones{}),
	}

	a := buildTicketMetrics(records, filter, base.Add(2*time.Hour)).Attainment
	want := TicketMetricsMilestoneAttainment{Met: 1, Breached: 1, Open: 1, Rate: 0.5}
	if got := a.Groups[0].FirstResponse; got != want {
		t.Fatalf("first response = %+v, want %+v", got, want)
	}
}

// Nothing decided yet means a zero denominator, so the rate is zero even
// though open tickets are counted.
func TestBuildTicketAttainmentRateIsZeroWithoutDecisions(t *testing.T) {
	base := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	filter := TicketMetricsFilter{Start: base, End: base.AddDate(0, 0, 7), GroupBy: TicketMetricsGroupTotal}
	frozen := attainmentFrozen(base, 4*time.Hour, 24*time.Hour)
	records := []TicketMetricsRecord{
		{TicketID: 1, CreatedAt: base, SLA: frozen, CurrentState: domain.StateInProgress, CategoryName: "Bugs", Priority: domain.PriorityHigh},
		{TicketID: 2, CreatedAt: base, SLA: frozen, CurrentState: domain.StateInProgress, CategoryName: "Bugs", Priority: domain.PriorityHigh},
	}

	a := buildTicketMetrics(records, filter, base.Add(time.Hour)).Attainment
	want := TicketMetricsMilestoneAttainment{Open: 2}
	if got := a.Groups[0].FirstResponse; got != want {
		t.Fatalf("first response = %+v, want %+v", got, want)
	}
	if got := a.Groups[0].Resolve; got != want {
		t.Fatalf("resolve = %+v, want %+v", got, want)
	}
}

// The cohort is tickets CREATED in [Start, End) that carry a frozen
// commitment. A record in the read set but created outside the period is
// excluded; a commitless ticket (or a pre-0016 zero-instant row) is counted
// separately and never enters a group.
func TestBuildTicketAttainmentCohortAndNoCommitment(t *testing.T) {
	base := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	filter := TicketMetricsFilter{Start: base, End: base.AddDate(0, 0, 7), GroupBy: TicketMetricsGroupTotal}
	frozen := attainmentFrozen(base, 4*time.Hour, 24*time.Hour)
	met := domain.SLAMilestones{FirstResponseAt: attainmentAt(base, time.Hour), FirstResolvedAt: attainmentAt(base, 2*time.Hour)}
	record := func(id int64, createdAt time.Time, sla *domain.TicketSLA) TicketMetricsRecord {
		return TicketMetricsRecord{TicketID: id, CreatedAt: createdAt, CurrentState: domain.StateResolved,
			Priority: domain.PriorityHigh, CategoryName: "Bugs", SLA: sla, Milestones: met}
	}
	records := []TicketMetricsRecord{
		record(1, base.Add(time.Hour), frozen),
		record(2, base.AddDate(0, 0, -1), frozen),
		record(3, base.AddDate(0, 0, 7), frozen),
		record(4, base.Add(time.Hour), nil),
		record(5, base.Add(time.Hour), &domain.TicketSLA{StartedAt: base}),
	}

	a := buildTicketMetrics(records, filter, base.Add(time.Hour)).Attainment
	if a.NoCommitment != 2 {
		t.Fatalf("NoCommitment = %d, want 2 (nil commitment and pre-0016 zero-instants)", a.NoCommitment)
	}
	if len(a.Groups) != 1 || a.Groups[0].Tickets != 1 {
		t.Fatalf("groups = %+v, want one total group with the single in-period commitment", a.Groups)
	}
}

// total is one group for the whole cohort; priority is always the four
// canonical priorities in rank order, including empty ones; category is only
// the categories present, ordered by name; empty or unknown values fail soft
// to total.
func TestBuildTicketAttainmentGroupingsAndOrder(t *testing.T) {
	base := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	now := base.Add(time.Hour)
	frozen := attainmentFrozen(base, 4*time.Hour, 24*time.Hour)
	met := domain.SLAMilestones{FirstResponseAt: attainmentAt(base, time.Hour), FirstResolvedAt: attainmentAt(base, 2*time.Hour)}
	record := func(id int64, priority domain.Priority, category string) TicketMetricsRecord {
		return TicketMetricsRecord{TicketID: id, CreatedAt: base, CurrentState: domain.StateResolved,
			Priority: priority, CategoryName: category, SLA: frozen, Milestones: met}
	}
	records := []TicketMetricsRecord{
		record(1, domain.PriorityHigh, "Beta"),
		record(2, domain.PriorityCritical, "Alpha"),
		record(3, domain.PriorityHigh, "Beta"),
		record(4, domain.PriorityLow, "Alpha"),
	}
	filter := func(groupBy string) TicketMetricsFilter {
		return TicketMetricsFilter{Start: base, End: base.AddDate(0, 0, 7), GroupBy: groupBy}
	}

	total := buildTicketMetrics(records, filter("total"), now).Attainment
	if total.GroupBy != TicketMetricsGroupTotal || len(total.Groups) != 1 || total.Groups[0].Label != "Total" || total.Groups[0].Tickets != 4 {
		t.Fatalf("total attainment = %+v", total)
	}

	byPriority := buildTicketMetrics(records, filter("priority"), now).Attainment
	wantPriority := []struct {
		label   string
		tickets int
	}{{"Critical", 1}, {"High", 2}, {"Medium", 0}, {"Low", 1}}
	if byPriority.GroupBy != TicketMetricsGroupPriority || len(byPriority.Groups) != 4 {
		t.Fatalf("priority attainment = %+v, want four groups", byPriority)
	}
	for i, want := range wantPriority {
		got := byPriority.Groups[i]
		if got.Label != want.label || got.Tickets != want.tickets {
			t.Fatalf("priority group %d = %+v, want %q with %d tickets", i, got, want.label, want.tickets)
		}
	}
	if g := byPriority.Groups[1]; g.FirstResponse != (TicketMetricsMilestoneAttainment{Met: 2, Rate: 1}) {
		t.Fatalf("High first response = %+v, want two met", g.FirstResponse)
	}

	byCategory := buildTicketMetrics(records, filter("category"), now).Attainment
	wantCategory := []struct {
		label   string
		tickets int
	}{{"Alpha", 2}, {"Beta", 2}}
	if byCategory.GroupBy != TicketMetricsGroupCategory || len(byCategory.Groups) != 2 {
		t.Fatalf("category attainment = %+v, want two groups", byCategory)
	}
	for i, want := range wantCategory {
		if got := byCategory.Groups[i]; got.Label != want.label || got.Tickets != want.tickets {
			t.Fatalf("category group %d = %+v, want %q with %d tickets", i, got, want.label, want.tickets)
		}
	}

	for _, groupBy := range []string{"", "nonsense"} {
		a := buildTicketMetrics(records, filter(groupBy), now).Attainment
		if a.GroupBy != TicketMetricsGroupTotal || len(a.Groups) != 1 || a.Groups[0].Label != "Total" {
			t.Fatalf("fail-soft group %q = %+v, want total", groupBy, a)
		}
	}
}
