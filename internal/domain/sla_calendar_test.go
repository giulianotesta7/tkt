package domain

import (
	"testing"
	"time"

	// Embedded IANA tz database: the non-UTC calendar cases must not
	// depend on the host's zoneinfo being installed.
	_ "time/tzdata"
)

// SLACalendar (issue #211): the instance working calendar and its
// AddWorkingSeconds arithmetic. Every case is driven by explicit instants
// — no clock reads, no I/O — and every answer is asserted in UTC.

// workingWeekUTC returns the default calendar: Monday-Friday, 09:00-18:00,
// UTC (a 9h window = 32400 working seconds per day).
func workingWeekUTC() SLACalendar { return DefaultSLACalendar() }

// utcAt builds a UTC instant from explicit local wall-clock fields.
func utcAt(year int, month time.Month, day, hour, minute, second int) time.Time {
	return time.Date(year, month, day, hour, minute, second, 0, time.UTC)
}

// TestSLACalendarDefaultIsWeekdayNineToEighteenUTC pins the documented
// default: the same calendar the settings store falls back to.
func TestSLACalendarDefaultIsWeekdayNineToEighteenUTC(t *testing.T) {
	c := DefaultSLACalendar()
	if !c.Valid() {
		t.Fatal("DefaultSLACalendar is not Valid")
	}
	wantDays := []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday}
	if len(c.WorkingDays) != len(wantDays) {
		t.Fatalf("WorkingDays = %v, want %v", c.WorkingDays, wantDays)
	}
	for i, d := range wantDays {
		if c.WorkingDays[i] != d {
			t.Errorf("WorkingDays[%d] = %v, want %v", i, c.WorkingDays[i], d)
		}
	}
	if c.StartMinute != 540 {
		t.Errorf("StartMinute = %d, want 540", c.StartMinute)
	}
	if c.EndMinute != 1080 {
		t.Errorf("EndMinute = %d, want 1080", c.EndMinute)
	}
	if c.Location != time.UTC {
		t.Errorf("Location = %v, want UTC", c.Location)
	}
}

// TestSLACalendarValid pins the validity contract: no working day at all,
// a non-positive window, StartMinute not strictly before EndMinute, a
// window crossing midnight, minutes outside the calendar day, or a
// missing Location each make the calendar unusable.
func TestSLACalendarValid(t *testing.T) {
	tests := []struct {
		name string
		cal  SLACalendar
		want bool
	}{
		{
			name: "default calendar is valid",
			cal:  workingWeekUTC(),
			want: true,
		},
		{
			name: "no working day at all",
			cal:  SLACalendar{WorkingDays: nil, StartMinute: 540, EndMinute: 1080, Location: time.UTC},
			want: false,
		},
		{
			name: "single working day is valid",
			cal: SLACalendar{
				WorkingDays: []time.Weekday{time.Wednesday},
				StartMinute: 540, EndMinute: 1080, Location: time.UTC,
			},
			want: true,
		},
		{
			name: "zero-length window",
			cal: SLACalendar{
				WorkingDays: workingWeekUTC().WorkingDays,
				StartMinute: 540, EndMinute: 540, Location: time.UTC,
			},
			want: false,
		},
		{
			name: "negative window",
			cal: SLACalendar{
				WorkingDays: workingWeekUTC().WorkingDays,
				StartMinute: 1080, EndMinute: 540, Location: time.UTC,
			},
			want: false,
		},
		{
			name: "window crossing midnight is rejected",
			cal: SLACalendar{
				WorkingDays: workingWeekUTC().WorkingDays,
				StartMinute: 1080, EndMinute: 1620, Location: time.UTC,
			},
			want: false,
		},
		{
			name: "end minute past the calendar day is rejected",
			cal: SLACalendar{
				WorkingDays: workingWeekUTC().WorkingDays,
				StartMinute: 540, EndMinute: 1441, Location: time.UTC,
			},
			want: false,
		},
		{
			name: "negative start minute is rejected",
			cal: SLACalendar{
				WorkingDays: workingWeekUTC().WorkingDays,
				StartMinute: -30, EndMinute: 1080, Location: time.UTC,
			},
			want: false,
		},
		{
			name: "no location is rejected",
			cal: SLACalendar{
				WorkingDays: workingWeekUTC().WorkingDays,
				StartMinute: 540, EndMinute: 1080, Location: nil,
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cal.Valid(); got != tt.want {
				t.Errorf("Valid() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSLACalendarLocationIsPortable proves the read-back contract the store
// depends on: a zone is storable only when its NAME resolves back to the same
// zone, so persisting it cannot shift the working window on the next read.
func TestSLACalendarLocationIsPortable(t *testing.T) {
	buenosAires, err := time.LoadLocation("America/Argentina/Buenos_Aires")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	etcGMTMinusThree, err := time.LoadLocation("Etc/GMT-3")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	tests := []struct {
		name string
		loc  *time.Location
		want bool
	}{
		{name: "nil has no name at all", loc: nil, want: false},
		{name: "UTC is an IANA name", loc: time.UTC, want: true},
		{name: "a named zone", loc: buenosAires, want: true},
		{name: "the fixed-offset IANA family carries its offset in its name", loc: etcGMTMinusThree, want: true},
		{name: "machine-local means different things per host", loc: time.Local, want: false},
		{name: "an ad-hoc fixed zone has no reloadable name", loc: time.FixedZone("UTC+3", 3*60*60), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LocationIsPortable(tt.loc); got != tt.want {
				t.Errorf("LocationIsPortable(%v) = %v, want %v", tt.loc, got, tt.want)
			}
		})
	}
}

// TestSLACalendarAddWorkingSeconds is the arithmetic table: inside and
// outside the window, weekend skips, multi-day and multi-week spans,
// exact boundaries, and the degenerate inputs.
func TestSLACalendarAddWorkingSeconds(t *testing.T) {
	tests := []struct {
		name     string
		cal      SLACalendar
		from     time.Time
		seconds  int
		want     time.Time
		wantSame bool // want from returned unchanged
	}{
		{
			name:    "inside the window on a working day",
			cal:     workingWeekUTC(),
			from:    utcAt(2026, 8, 10, 10, 0, 0), // Monday
			seconds: 3600,
			want:    utcAt(2026, 8, 10, 11, 0, 0),
		},
		{
			name:    "before the window opens: clock starts at the opening",
			cal:     workingWeekUTC(),
			from:    utcAt(2026, 8, 10, 7, 0, 0),
			seconds: 3600,
			want:    utcAt(2026, 8, 10, 10, 0, 0),
		},
		{
			name:    "after the window closes: next working day's opening",
			cal:     workingWeekUTC(),
			from:    utcAt(2026, 8, 10, 19, 0, 0), // Monday evening
			seconds: 3600,
			want:    utcAt(2026, 8, 11, 10, 0, 0), // Tuesday
		},
		{
			name:    "on a Saturday: skipped to Monday's opening",
			cal:     workingWeekUTC(),
			from:    utcAt(2026, 8, 8, 10, 0, 0),
			seconds: 3600,
			want:    utcAt(2026, 8, 10, 10, 0, 0),
		},
		{
			name:    "on a Sunday: skipped to Monday's opening",
			cal:     workingWeekUTC(),
			from:    utcAt(2026, 8, 9, 10, 0, 0),
			seconds: 3600,
			want:    utcAt(2026, 8, 10, 10, 0, 0),
		},
		{
			name:    "span consuming several working days",
			cal:     workingWeekUTC(),
			from:    utcAt(2026, 8, 10, 17, 0, 0), // Monday: 1h left
			seconds: 3 * 3600,
			want:    utcAt(2026, 8, 11, 11, 0, 0), // Tuesday 09:00 + 2h
		},
		{
			name:    "span that exactly exhausts a day's window",
			cal:     workingWeekUTC(),
			from:    utcAt(2026, 8, 10, 9, 0, 0),
			seconds: 32400,
			want:    utcAt(2026, 8, 10, 18, 0, 0),
		},
		{
			name:    "span that lands exactly on the window close",
			cal:     workingWeekUTC(),
			from:    utcAt(2026, 8, 10, 10, 0, 0),
			seconds: 8 * 3600,
			want:    utcAt(2026, 8, 10, 18, 0, 0),
		},
		{
			name:    "wrap across the weekend",
			cal:     workingWeekUTC(),
			from:    utcAt(2026, 8, 14, 17, 30, 0), // Friday: 30m left
			seconds: 3600,
			want:    utcAt(2026, 8, 17, 9, 30, 0), // Monday 09:00 + 30m
		},
		{
			name:    "multi-week span",
			cal:     workingWeekUTC(),
			from:    utcAt(2026, 8, 10, 9, 0, 0),  // Monday
			seconds: 10 * 32400,                   // two full working weeks
			want:    utcAt(2026, 8, 21, 18, 0, 0), // Friday of the second week's close
		},
		{
			name:    "single-day working week skips everything else",
			cal:     SLACalendar{WorkingDays: []time.Weekday{time.Wednesday}, StartMinute: 540, EndMinute: 1080, Location: time.UTC},
			from:    utcAt(2026, 8, 10, 10, 0, 0), // Monday
			seconds: 3600,
			want:    utcAt(2026, 8, 12, 10, 0, 0), // Wednesday
		},
		{
			name:     "zero seconds returns from unchanged",
			cal:      workingWeekUTC(),
			from:     utcAt(2026, 8, 8, 12, 0, 0),
			seconds:  0,
			wantSame: true,
		},
		{
			name:     "negative seconds returns from unchanged",
			cal:      workingWeekUTC(),
			from:     utcAt(2026, 8, 8, 12, 0, 0),
			seconds:  -90,
			wantSame: true,
		},
		{
			name:     "invalid calendar returns from unchanged",
			cal:      SLACalendar{WorkingDays: nil, StartMinute: 540, EndMinute: 1080, Location: time.UTC},
			from:     utcAt(2026, 8, 10, 10, 0, 0),
			seconds:  3600,
			wantSame: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cal.AddWorkingSeconds(tt.from, tt.seconds)
			if tt.wantSame {
				if !got.Equal(tt.from) {
					t.Errorf("AddWorkingSeconds = %v, want from unchanged (%v)", got, tt.from)
				}
				return
			}
			if !got.Equal(tt.want) {
				t.Errorf("AddWorkingSeconds = %v, want %v", got, tt.want)
			}
			if got.Location() != time.UTC {
				t.Errorf("AddWorkingSeconds location = %v, want UTC", got.Location())
			}
		})
	}
}

// TestSLACalendarNonUTCLocationProducesUTCAnswer pins the zone contract:
// the window is interpreted in the calendar's location (09:00 local), the
// walk runs local, and the answer converts back to UTC.
func TestSLACalendarNonUTCLocationProducesUTCAnswer(t *testing.T) {
	ba, err := time.LoadLocation("America/Argentina/Buenos_Aires")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	cal := SLACalendar{
		WorkingDays: []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday},
		StartMinute: 540, EndMinute: 1080, Location: ba,
	}

	// Monday 12:00 ART = 15:00 UTC; +1 working hour = 13:00 ART = 16:00 UTC.
	from := time.Date(2026, 8, 10, 12, 0, 0, 0, ba)
	got := cal.AddWorkingSeconds(from, 3600)
	want := utcAt(2026, 8, 10, 16, 0, 0)
	if !got.Equal(want) {
		t.Errorf("AddWorkingSeconds = %v, want %v", got, want)
	}

	// Created after the local close (21:00 ART): the clock starts at
	// Tuesday 09:00 ART = 12:00 UTC, so +1 working hour = 10:00 ART = 13:00 UTC.
	from = time.Date(2026, 8, 10, 21, 0, 0, 0, ba)
	got = cal.AddWorkingSeconds(from, 3600)
	want = utcAt(2026, 8, 11, 13, 0, 0)
	if !got.Equal(want) {
		t.Errorf("AddWorkingSeconds after local close = %v, want %v", got, want)
	}
}

// TestSLACalendarDSTShiftsWindowWithWallClock pins the daylight-saving
// behavior: the daily window stays 09:00-18:00 LOCAL across a transition,
// so the UTC answer reflects the offset change instead of drifting.
func TestSLACalendarDSTShiftsWindowWithWallClock(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	cal := SLACalendar{
		WorkingDays: []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday},
		StartMinute: 540, EndMinute: 1080, Location: ny,
	}

	// US DST ends Sunday 2026-11-01. Friday 2026-10-30 17:00 EDT has one
	// working hour left; +2 working hours lands Monday 2026-11-02 10:00
	// EST (the wall clock moved, the UTC offset flipped EDT→EST).
	from := time.Date(2026, 10, 30, 17, 0, 0, 0, ny)
	got := cal.AddWorkingSeconds(from, 2*3600)
	want := utcAt(2026, 11, 2, 15, 0, 0) // 10:00 EST = 15:00 UTC
	if !got.Equal(want) {
		t.Errorf("AddWorkingSeconds across DST = %v, want %v", got, want)
	}
}
