package domain

import (
	"testing"
	"time"
)

// SLA pause while a ticket is on hold (issue #352). Only the RESOLUTION
// milestone pauses; the first-response clock keeps running. All cases use
// the default calendar (Monday-Friday, 09:00-18:00, UTC). 2026-08-06 is a
// Thursday.

func TestSLACalendarWorkingSecondsBetween(t *testing.T) {
	cal := workingWeekUTC()
	tests := []struct {
		name     string
		from, to time.Time
		want     int
	}{
		{"inside one window", utcAt(2026, 8, 6, 10, 0, 0), utcAt(2026, 8, 6, 12, 0, 0), 7200},
		{"across the night", utcAt(2026, 8, 6, 17, 0, 0), utcAt(2026, 8, 7, 10, 0, 0), 7200},
		{"across the weekend", utcAt(2026, 8, 7, 17, 0, 0), utcAt(2026, 8, 10, 10, 0, 0), 7200},
		{"outside every window", utcAt(2026, 8, 7, 19, 0, 0), utcAt(2026, 8, 10, 8, 0, 0), 0},
		{"from before the window opens", utcAt(2026, 8, 6, 7, 0, 0), utcAt(2026, 8, 6, 9, 30, 0), 1800},
		{"to after the window closes", utcAt(2026, 8, 6, 17, 30, 0), utcAt(2026, 8, 6, 20, 0, 0), 1800},
		{"whole working day", utcAt(2026, 8, 6, 0, 0, 0), utcAt(2026, 8, 7, 0, 0, 0), 32400},
		{"equal instants", utcAt(2026, 8, 6, 10, 0, 0), utcAt(2026, 8, 6, 10, 0, 0), 0},
		{"reversed instants", utcAt(2026, 8, 6, 12, 0, 0), utcAt(2026, 8, 6, 10, 0, 0), 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := cal.WorkingSecondsBetween(tc.from, tc.to); got != tc.want {
				t.Errorf("WorkingSecondsBetween(%v, %v) = %d, want %d", tc.from, tc.to, got, tc.want)
			}
		})
	}
}

func TestSLACalendarWorkingSecondsBetweenInvalidCalendarIsZero(t *testing.T) {
	if got := (SLACalendar{}).WorkingSecondsBetween(utcAt(2026, 8, 6, 10, 0, 0), utcAt(2026, 8, 6, 12, 0, 0)); got != 0 {
		t.Errorf("invalid calendar = %d, want 0", got)
	}
}

// TestSLACalendarWorkingSecondsBetweenInvertsAddWorkingSeconds pins the
// round trip the resume shift relies on: the working time between from and
// AddWorkingSeconds(from, n) is n.
func TestSLACalendarWorkingSecondsBetweenInvertsAddWorkingSeconds(t *testing.T) {
	cal := workingWeekUTC()
	from := utcAt(2026, 8, 6, 10, 0, 0)
	for _, n := range []int{1, 3600, 28800, 32400, 50000, 200000} {
		if got := cal.WorkingSecondsBetween(from, cal.AddWorkingSeconds(from, n)); got != n {
			t.Errorf("round trip of %d = %d", n, got)
		}
	}
}

// holdFrozen is a commitment created Thursday 10:00 whose resolution warns
// at 13:12 and is due at 14:00 (all inside one window).
func holdFrozen() TicketSLA {
	return TicketSLA{
		FirstResponseSeconds: 1800,
		ResolveSeconds:       14400,
		WarnFirstResponseAt:  utcAt(2026, 8, 6, 10, 24, 0),
		DueFirstResponseAt:   utcAt(2026, 8, 6, 10, 30, 0),
		WarnResolveAt:        utcAt(2026, 8, 6, 13, 12, 0),
		DueResolveAt:         utcAt(2026, 8, 6, 14, 0, 0),
		StartedAt:            utcAt(2026, 8, 6, 10, 0, 0),
		PolicySnapshotAt:     utcAt(2026, 8, 6, 10, 0, 0),
	}
}

func TestTicketSLAShiftResolveForHold(t *testing.T) {
	cal := workingWeekUTC()
	tests := []struct {
		name              string
		pausedAt, resumed time.Time
		wantWarn, wantDue time.Time
	}{
		{
			name:     "pause before warning moves both by the working time on hold",
			pausedAt: utcAt(2026, 8, 6, 11, 0, 0), resumed: utcAt(2026, 8, 7, 11, 0, 0),
			wantWarn: utcAt(2026, 8, 7, 13, 12, 0), wantDue: utcAt(2026, 8, 7, 14, 0, 0),
		},
		{
			name:     "pause over the weekend counts working time only",
			pausedAt: utcAt(2026, 8, 6, 11, 0, 0), resumed: utcAt(2026, 8, 10, 9, 0, 0),
			wantWarn: utcAt(2026, 8, 10, 11, 12, 0), wantDue: utcAt(2026, 8, 10, 12, 0, 0),
		},
		{
			name:     "resume outside the window waits for the next opening",
			pausedAt: utcAt(2026, 8, 6, 11, 0, 0), resumed: utcAt(2026, 8, 6, 20, 0, 0),
			wantWarn: utcAt(2026, 8, 7, 11, 12, 0), wantDue: utcAt(2026, 8, 7, 12, 0, 0),
		},
		{
			name:     "warning already reached at pause stays, due moves",
			pausedAt: utcAt(2026, 8, 6, 13, 30, 0), resumed: utcAt(2026, 8, 7, 10, 0, 0),
			wantWarn: utcAt(2026, 8, 6, 13, 12, 0), wantDue: utcAt(2026, 8, 7, 10, 30, 0),
		},
		{
			name:     "already overdue at pause keeps both",
			pausedAt: utcAt(2026, 8, 6, 15, 0, 0), resumed: utcAt(2026, 8, 7, 10, 0, 0),
			wantWarn: utcAt(2026, 8, 6, 13, 12, 0), wantDue: utcAt(2026, 8, 6, 14, 0, 0),
		},
		{
			name:     "a hold with no working time moves nothing",
			pausedAt: utcAt(2026, 8, 6, 11, 0, 0), resumed: utcAt(2026, 8, 6, 11, 0, 0),
			wantWarn: utcAt(2026, 8, 6, 13, 12, 0), wantDue: utcAt(2026, 8, 6, 14, 0, 0),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := holdFrozen()
			got := before.ShiftResolveForHold(tc.pausedAt, tc.resumed, cal)
			if !got.WarnResolveAt.Equal(tc.wantWarn) || !got.DueResolveAt.Equal(tc.wantDue) {
				t.Errorf("warn/due = %v / %v, want %v / %v", got.WarnResolveAt, got.DueResolveAt, tc.wantWarn, tc.wantDue)
			}
			// The first-response milestone never pauses, and the durations,
			// start and snapshot are the frozen commitment itself.
			want := before
			want.WarnResolveAt, want.DueResolveAt = got.WarnResolveAt, got.DueResolveAt
			if got != want {
				t.Errorf("shift changed more than the resolution instants:\n got %+v\nwant %+v", got, want)
			}
			if before != holdFrozen() {
				t.Error("ShiftResolveForHold mutated its receiver")
			}
		})
	}
}

// TestTicketSLAShiftResolveForHoldNeverMovesEarlier pins the guard against a
// calendar edited between the freeze and the resume: a hold never brings a
// deadline forward.
func TestTicketSLAShiftResolveForHoldNeverMovesEarlier(t *testing.T) {
	moved := workingWeekUTC()
	moved.StartMinute, moved.EndMinute = 6*60, 15*60 // the window moved after the freeze
	frozen := holdFrozen()
	frozen.DueResolveAt = utcAt(2026, 8, 6, 17, 30, 0) // frozen under the 09:00-18:00 window
	// Under the new window no working time lies between 16:00 and 17:30, so a
	// naive shift would land on the resume instant, 16:01 — before the due.
	got := frozen.ShiftResolveForHold(utcAt(2026, 8, 6, 16, 0, 0), utcAt(2026, 8, 6, 16, 1, 0), moved)
	if !got.DueResolveAt.Equal(frozen.DueResolveAt) {
		t.Errorf("due = %v, want the frozen %v (never earlier)", got.DueResolveAt, frozen.DueResolveAt)
	}
}

func TestTicketSLAShiftResolveForHoldKeepsLegacyZeroInstants(t *testing.T) {
	legacy := TicketSLA{FirstResponseSeconds: 1800, ResolveSeconds: 14400, StartedAt: utcAt(2026, 8, 6, 10, 0, 0)}
	got := legacy.ShiftResolveForHold(utcAt(2026, 8, 6, 11, 0, 0), utcAt(2026, 8, 7, 11, 0, 0), workingWeekUTC())
	if got != legacy {
		t.Errorf("legacy row changed: %+v", got)
	}
}

func TestProjectSLAPausedResolution(t *testing.T) {
	paused := slaProjectionAt(3600)
	p := ProjectSLA(slaProjectionFrozen(), SLAMilestones{ResolvePausedAt: &paused}, slaProjectionAt(20000))
	if !p.Resolve.Paused {
		t.Error("Resolve.Paused = false, want true")
	}
	if p.Resolve.State != SLAOnTrack {
		t.Errorf("Resolve.State = %q, want on_track (judged at the pause instant)", p.Resolve.State)
	}
	if p.Resolve.Remaining != 10800*time.Second {
		t.Errorf("Resolve.Remaining = %v, want 3h frozen at the pause", p.Resolve.Remaining)
	}
	if p.Resolve.Elapsed != 3600*time.Second {
		t.Errorf("Resolve.Elapsed = %v, want 1h", p.Resolve.Elapsed)
	}
	// The first-response clock keeps running while on hold.
	if p.FirstResponse.Paused || p.FirstResponse.State != SLABreached {
		t.Errorf("FirstResponse = %+v, want running and breached", p.FirstResponse)
	}
	if p.Overall != SLABreached {
		t.Errorf("Overall = %q, want breached", p.Overall)
	}
}

func TestProjectSLAPausedAfterWarningStaysAtRisk(t *testing.T) {
	paused := slaProjectionAt(12000)
	responded := slaProjectionAt(600)
	p := ProjectSLA(slaProjectionFrozen(), SLAMilestones{FirstResponseAt: &responded, ResolvePausedAt: &paused}, slaProjectionAt(90000))
	if !p.Resolve.Paused || p.Resolve.State != SLAAtRisk {
		t.Errorf("Resolve = %+v, want paused at_risk", p.Resolve)
	}
	if p.Overall != SLAAtRisk {
		t.Errorf("Overall = %q, want at_risk", p.Overall)
	}
}

func TestProjectSLAAchievedResolutionIgnoresPause(t *testing.T) {
	paused := slaProjectionAt(3600)
	resolved := slaProjectionAt(5000)
	p := ProjectSLA(slaProjectionFrozen(), SLAMilestones{FirstResolvedAt: &resolved, ResolvePausedAt: &paused}, slaProjectionAt(20000))
	if p.Resolve.Paused || p.Resolve.State != SLAMet {
		t.Errorf("Resolve = %+v, want met and not paused", p.Resolve)
	}
}

// TestSLACalendarWorkingSecondsBetweenAcrossDST pins the inverse across the
// America/New_York spring-forward: the 09:00-18:00 window keeps its wall-clock
// hours, and a whole-day window counts the 23-hour day as 23 hours.
func TestSLACalendarWorkingSecondsBetweenAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	cal := DefaultSLACalendar()
	cal.Location = loc
	from := time.Date(2026, 3, 5, 9, 0, 0, 0, loc)
	to := time.Date(2026, 3, 9, 10, 0, 0, 0, loc)
	if got, want := cal.WorkingSecondsBetween(from, to), 19*3600; got != want {
		t.Errorf("Thu 09:00 .. Mon 10:00 = %d, want %d", got, want)
	}
	if got := cal.AddWorkingSeconds(from, cal.WorkingSecondsBetween(from, to)); !got.Equal(to) {
		t.Errorf("round trip = %v, want %v", got, to)
	}
	allDay := SLACalendar{WorkingDays: []time.Weekday{0, 1, 2, 3, 4, 5, 6}, StartMinute: 0, EndMinute: minutesPerDay, Location: loc}
	a, b := time.Date(2026, 3, 8, 0, 0, 0, 0, loc), time.Date(2026, 3, 9, 0, 0, 0, 0, loc)
	if got := allDay.WorkingSecondsBetween(a, b); got != 23*3600 {
		t.Errorf("spring-forward day = %d, want %d", got, 23*3600)
	}
}
