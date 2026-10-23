package domain

import (
	"testing"
	"time"
)

// Issue #408 verification (S1, S2, S6): the alert kinds come only from the
// FROZEN instants through ProjectSLA, a breach replaces the risk of the same
// milestone, achieved milestones and legacy tickets never alert, and the
// inactivity window is two working days on the instance calendar.

func verifySLA() *TicketSLA {
	start := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	return &TicketSLA{
		FirstResponseSeconds: 3600, ResolveSeconds: 28800,
		WarnFirstResponseAt: start.Add(2 * time.Hour), DueFirstResponseAt: start.Add(4 * time.Hour),
		WarnResolveAt: start.Add(6 * time.Hour), DueResolveAt: start.Add(8 * time.Hour),
		StartedAt: start, PolicySnapshotAt: start,
	}
}

func verifyKinds(f *TicketSLA, m SLAMilestones, now time.Time) []NotificationKind {
	return SLAAlertKinds(ProjectSLA(f, m, now))
}

func sameKinds(got []NotificationKind, want ...NotificationKind) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// S1/S2: each milestone reports at_risk from its frozen warn instant and the
// breach alone from its frozen due instant; nothing before the warn instant.
func TestSLAAlertKindsFollowTheFrozenInstants(t *testing.T) {
	f := verifySLA()
	s := f.StartedAt
	cases := []struct {
		name string
		now  time.Time
		want []NotificationKind
	}{
		{"before warn", s.Add(2*time.Hour - time.Second), nil},
		{"at first-response warn", s.Add(2 * time.Hour), []NotificationKind{NotificationSLAFirstResponseAtRisk}},
		{"before first-response due", s.Add(4*time.Hour - time.Second), []NotificationKind{NotificationSLAFirstResponseAtRisk}},
		{"at first-response due", s.Add(4 * time.Hour), []NotificationKind{NotificationSLAFirstResponseBreached}},
		{"at resolution warn", s.Add(6 * time.Hour), []NotificationKind{NotificationSLAFirstResponseBreached, NotificationSLAResolutionAtRisk}},
		{"at resolution due", s.Add(8 * time.Hour), []NotificationKind{NotificationSLAFirstResponseBreached, NotificationSLAResolutionBreached}},
	}
	for _, c := range cases {
		if got := verifyKinds(f, SLAMilestones{}, c.now); !sameKinds(got, c.want...) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// S1: a pass that first runs after the due instant announces the breach ALONE,
// never at_risk and breached for the same milestone.
func TestSLAAlertKindsLatePassReportsOnlyBreach(t *testing.T) {
	f := verifySLA()
	got := verifyKinds(f, SLAMilestones{}, f.StartedAt.Add(100*time.Hour))
	if !sameKinds(got, NotificationSLAFirstResponseBreached, NotificationSLAResolutionBreached) {
		t.Fatalf("late pass: got %v, want only the two breaches", got)
	}
}

// S2: an achieved milestone never alerts however late it was, a nil or
// zero-instant commitment (legacy ticket) never alerts, and the frozen warn
// instant is the only threshold read.
func TestSLAAlertKindsSkipAchievedAndLegacy(t *testing.T) {
	f := verifySLA()
	late := f.StartedAt.Add(200 * time.Hour)
	onTime := f.StartedAt.Add(30 * time.Minute)
	later := f.StartedAt.Add(400 * time.Hour)
	if got := verifyKinds(f, SLAMilestones{FirstResponseAt: &late}, later); !sameKinds(got, NotificationSLAResolutionBreached) {
		t.Errorf("late achieved first response alerted: %v", got)
	}
	if got := verifyKinds(f, SLAMilestones{FirstResponseAt: &onTime, FirstResolvedAt: &late}, later); len(got) != 0 {
		t.Errorf("achieved milestones alerted: %v", got)
	}
	if got := verifyKinds(nil, SLAMilestones{FirstResponseAt: &onTime}, later); len(got) != 0 {
		t.Errorf("legacy ticket with no commitment alerted: %v", got)
	}
	if got := verifyKinds(&TicketSLA{StartedAt: f.StartedAt}, SLAMilestones{}, later); len(got) != 0 {
		t.Errorf("zero-instant legacy row alerted: %v", got)
	}
	legacyWarn := verifySLA()
	legacyWarn.WarnResolveAt = legacyWarn.StartedAt
	if got := verifyKinds(legacyWarn, SLAMilestones{}, legacyWarn.StartedAt.Add(time.Hour)); !sameKinds(got, NotificationSLAResolutionAtRisk) {
		t.Errorf("frozen warn instant ignored: %v", got)
	}
}

// S6: two WORKING days on the instance calendar, weekends skipped; activity on
// a non-working day starts counting at the next window opening.
func TestNoMovementAtIsTwoWorkingDays(t *testing.T) {
	if InactivityWorkingDays != 2 {
		t.Fatalf("InactivityWorkingDays = %d, want 2", InactivityWorkingDays)
	}
	cal := DefaultSLACalendar()
	for _, c := range []struct{ from, want time.Time }{
		{time.Date(2026, 10, 23, 15, 0, 0, 0, time.UTC), time.Date(2026, 10, 27, 15, 0, 0, 0, time.UTC)}, // Fri -> Tue
		{time.Date(2026, 10, 22, 9, 30, 0, 0, time.UTC), time.Date(2026, 10, 26, 9, 30, 0, 0, time.UTC)}, // Thu -> Mon
		{time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC), time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)},  // Sat -> Wed 09:00
		{time.Date(2026, 10, 20, 11, 0, 0, 0, time.UTC), time.Date(2026, 10, 22, 11, 0, 0, 0, time.UTC)}, // Tue -> Thu
	} {
		if got := NoMovementAt(c.from, cal); !got.Equal(c.want) {
			t.Errorf("NoMovementAt(%s) = %s, want %s", c.from, got, c.want)
		}
	}
}
