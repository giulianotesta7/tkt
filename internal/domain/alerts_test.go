package domain

import (
	"testing"
	"time"
)

// Operational alerts (issue #408). The rules are pure: they read a projection
// and a calendar, never a clock, so every case below pins an explicit instant.

func alertCalendar(t *testing.T) SLACalendar {
	t.Helper()
	cal := DefaultSLACalendar()
	if !cal.Valid() {
		t.Fatalf("test calendar is not valid")
	}
	return cal
}

func frozenAt(started time.Time) *TicketSLA {
	return &TicketSLA{
		StartedAt:            started,
		FirstResponseSeconds: 3600,
		ResolveSeconds:       7200,
		WarnFirstResponseAt:  started.Add(1 * time.Hour),
		DueFirstResponseAt:   started.Add(2 * time.Hour),
		WarnResolveAt:        started.Add(3 * time.Hour),
		DueResolveAt:         started.Add(4 * time.Hour),
	}
}

func TestSLAAlertKindsReportsEachMilestoneWhenItBecomesDue(t *testing.T) {
	started := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	frozen := frozenAt(started)

	for _, tc := range []struct {
		name string
		now  time.Time
		want []NotificationKind
	}{
		{"on track alerts nothing", started.Add(30 * time.Minute), nil},
		{"first response at risk", started.Add(1 * time.Hour), []NotificationKind{NotificationSLAFirstResponseAtRisk}},
		{
			"first response breached and resolution at risk",
			started.Add(3 * time.Hour),
			[]NotificationKind{NotificationSLAFirstResponseBreached, NotificationSLAResolutionAtRisk},
		},
		{
			"both breached",
			started.Add(4 * time.Hour),
			[]NotificationKind{NotificationSLAFirstResponseBreached, NotificationSLAResolutionBreached},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := SLAAlertKinds(ProjectSLA(frozen, SLAMilestones{}, tc.now))
			assertKinds(t, got, tc.want)
		})
	}
}

// A breached milestone reports ONLY the breach: a pass that ran late must not
// also announce a risk the ticket already left behind.
func TestSLAAlertKindsNeverReportsRiskAndBreachForOneMilestone(t *testing.T) {
	started := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	got := SLAAlertKinds(ProjectSLA(frozenAt(started), SLAMilestones{}, started.Add(10*time.Hour)))
	for _, k := range got {
		if k == NotificationSLAFirstResponseAtRisk || k == NotificationSLAResolutionAtRisk {
			t.Fatalf("a breached milestone reported the at-risk kind %q: %v", k, got)
		}
	}
}

// An achieved milestone is finished: late or not, it is never alerted about,
// because there is no longer an action the alert could ask for.
func TestSLAAlertKindsIgnoresAchievedMilestones(t *testing.T) {
	started := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	responded := started.Add(90 * time.Minute)
	resolved := started.Add(10 * time.Hour)
	got := SLAAlertKinds(ProjectSLA(frozenAt(started), SLAMilestones{
		FirstResponseAt: &responded, FirstResolvedAt: &resolved,
	}, started.Add(12*time.Hour)))
	assertKinds(t, got, nil)
}

// A ticket with no frozen commitment projects as SLANone and alerts nothing:
// legacy tickets are never retroactively in trouble.
func TestSLAAlertKindsIgnoresTicketsWithoutACommitment(t *testing.T) {
	now := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	assertKinds(t, SLAAlertKinds(ProjectSLA(nil, SLAMilestones{}, now)), nil)
}

func TestNoMovementAtCountsTwoWorkingDays(t *testing.T) {
	cal := alertCalendar(t)
	// Thursday 10:00 plus two working days lands on Monday 10:00: the weekend
	// does not count against an agent.
	lastActivity := time.Date(2026, 3, 5, 10, 0, 0, 0, time.UTC)
	want := time.Date(2026, 3, 9, 10, 0, 0, 0, time.UTC)
	if got := NoMovementAt(lastActivity, cal); !got.Equal(want) {
		t.Fatalf("NoMovementAt = %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

func TestNoMovementAtMatchesTheConfiguredWindow(t *testing.T) {
	cal := alertCalendar(t)
	lastActivity := time.Date(2026, 3, 2, 11, 30, 0, 0, time.UTC)
	want := cal.AddWorkingDays(lastActivity, InactivityWorkingDays)
	if got := NoMovementAt(lastActivity, cal); !got.Equal(want) {
		t.Fatalf("NoMovementAt = %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

// Every alert kind must be a member of the persisted vocabulary, or the row the
// pass builds could never be stored.
func TestAlertKindsAreValidNotificationKinds(t *testing.T) {
	for _, k := range []NotificationKind{
		NotificationSLAFirstResponseAtRisk,
		NotificationSLAFirstResponseBreached,
		NotificationSLAResolutionAtRisk,
		NotificationSLAResolutionBreached,
		NotificationNoMovement,
	} {
		if !k.Valid() {
			t.Fatalf("alert kind %q is not part of the persisted vocabulary", k)
		}
		if k.IsComment() {
			t.Fatalf("alert kind %q must not be treated as a comment kind", k)
		}
	}
}

func assertKinds(t *testing.T, got, want []NotificationKind) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", got, want)
		}
	}
}
