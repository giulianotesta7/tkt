package domain

import (
	"testing"
	"time"
)

// Automatic hold expiry (issue #353, specs S2/S3/S5): the deadline of a hold
// and the reminder that precedes it are pure working-day arithmetic over the
// instance calendar, and the automatic close has its own attribution values.
// Every case is driven by explicit instants — no clock reads, no I/O.

// TestHoldAutoCloseDeadlineIsWorkingDaysAfterThePause pins S2: closing a hold
// happens N WORKING days after it started, on the same wall-clock time, using
// the instance calendar — not N calendar days.
func TestHoldAutoCloseDeadlineIsWorkingDaysAfterThePause(t *testing.T) {
	cal := DefaultSLACalendar()
	tests := []struct {
		name  string
		since time.Time
		days  int
		want  time.Time
	}{
		{
			name:  "three working days from a Monday",
			since: utcAt(2026, 8, 10, 11, 0, 0), // Monday
			days:  3,
			want:  utcAt(2026, 8, 13, 11, 0, 0), // Thursday
		},
		{
			name:  "the weekend is not counted",
			since: utcAt(2026, 8, 14, 16, 0, 0), // Friday
			days:  1,
			want:  utcAt(2026, 8, 17, 16, 0, 0), // Monday
		},
		{
			name:  "a Saturday pause counts from Monday's window opening",
			since: utcAt(2026, 8, 8, 12, 0, 0),
			days:  2,
			want:  utcAt(2026, 8, 12, 9, 0, 0),
		},
		{
			name:  "zero days is off, and answers the pause itself",
			since: utcAt(2026, 8, 10, 11, 0, 0),
			days:  0,
			want:  utcAt(2026, 8, 10, 11, 0, 0),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HoldAutoCloseAt(tt.since, tt.days, cal); !got.Equal(tt.want) {
				t.Errorf("HoldAutoCloseAt = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestHoldReminderIsOneWorkingDayBeforeTheDeadline pins S3: the reminder fires
// N-1 working days after the pause, which is exactly one working day before
// the deadline — never one calendar day, and never the pause itself for the
// N >= 2 holds it is meaningful for.
func TestHoldReminderIsOneWorkingDayBeforeTheDeadline(t *testing.T) {
	cal := DefaultSLACalendar()
	tests := []struct {
		name  string
		since time.Time
		days  int
		want  time.Time
	}{
		{
			name:  "two working days: the reminder is the next working day",
			since: utcAt(2026, 8, 12, 12, 0, 0), // Wednesday
			days:  2,
			want:  utcAt(2026, 8, 13, 12, 0, 0), // Thursday
		},
		{
			name:  "three working days across a weekend: the reminder is the Friday",
			since: utcAt(2026, 8, 13, 15, 0, 0), // Thursday
			days:  3,
			want:  utcAt(2026, 8, 17, 15, 0, 0), // Monday
		},
		{
			name:  "a Saturday pause: the reminder is the day after Monday's opening",
			since: utcAt(2026, 8, 8, 10, 0, 0),
			days:  2,
			want:  utcAt(2026, 8, 11, 9, 0, 0), // one working day before the deadline
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HoldReminderAt(tt.since, tt.days, cal)
			if !got.Equal(tt.want) {
				t.Errorf("HoldReminderAt = %v, want %v", got, tt.want)
			}
			deadline := HoldAutoCloseAt(tt.since, tt.days, cal)
			if !got.Before(deadline) {
				t.Errorf("HoldReminderAt = %v, want it strictly before the deadline %v", got, deadline)
			}
		})
	}
}

// TestHoldExpiryAttributionVocabulary pins the two stored values the automatic
// close writes (S1/S5): the closure attribution of the timeline event and the
// actor name of an action nobody performed. They are stored values, so they
// never change with display copy.
func TestHoldExpiryAttributionVocabulary(t *testing.T) {
	if ClosureViaHoldExpired != "hold_expired" {
		t.Errorf("ClosureViaHoldExpired = %q, want %q", ClosureViaHoldExpired, "hold_expired")
	}
	if AutomaticActorSystem != "system" {
		t.Errorf("AutomaticActorSystem = %q, want %q", AutomaticActorSystem, "system")
	}
	if AutomaticActorSystem == "workflow" {
		t.Error("the automatic hold close must not borrow the workflow actor: a reader cannot tell the two apart")
	}
}
