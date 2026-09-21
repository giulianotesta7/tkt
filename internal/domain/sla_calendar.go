package domain

import "time"

// minutesPerDay is the number of minutes in a calendar day; the working
// window is expressed in these units.
const minutesPerDay = 24 * 60

// SLACalendar is the instance working calendar (issue #211): which
// weekdays the clock runs, the daily window it runs in, and the zone that
// window is interpreted in. It is a pure value — no clock reads, no I/O —
// and all of its arithmetic is testable because the reference instant is
// always a parameter.
type SLACalendar struct {
	// WorkingDays are the weekdays the clock runs.
	WorkingDays []time.Weekday
	// StartMinute and EndMinute are minutes past local midnight.
	StartMinute int
	EndMinute   int
	// Location interprets the window. A 09:00-18:00 window is meaningless
	// without one.
	Location *time.Location
}

// DefaultSLACalendar is Monday-Friday, 09:00-18:00, UTC. It is also the
// documented fallback for the sla_calendar_* settings keys when a row is
// absent or unparseable.
func DefaultSLACalendar() SLACalendar {
	return SLACalendar{
		WorkingDays: []time.Weekday{
			time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday,
		},
		StartMinute: 9 * 60,
		EndMinute:   18 * 60,
		Location:    time.UTC,
	}
}

// Valid reports whether the calendar can be used. It is invalid with no
// working day at all, with a non-positive window, with StartMinute not
// strictly before EndMinute, with minutes outside the calendar day, or
// with no Location — a window is meaningless without a zone, and the
// arithmetic below must never invent one.
//
// A window whose EndMinute exceeds minutesPerDay would cross midnight; it
// is rejected here so AddWorkingSeconds never has to handle it.
func (c SLACalendar) Valid() bool {
	if len(c.WorkingDays) == 0 {
		return false
	}
	if c.Location == nil {
		return false
	}
	if c.StartMinute < 0 || c.EndMinute > minutesPerDay {
		return false
	}
	return c.StartMinute < c.EndMinute
}

// LocationIsPortable reports whether a resolved location can be persisted by
// NAME and read back as itself. The store persists Location.String() and the
// read resolves that name with a UTC fallback, so a location whose name does
// not round-trip (a fixed-offset zone such as "UTC+3") or one that means
// different things on different hosts (time.Local) would silently shift the
// whole working window while both the write and the read reported success.
//
// A real IANA name round-trips and is portable, including the fixed-offset
// Etc/GMT±N family, whose name carries its own offset.
func LocationIsPortable(loc *time.Location) bool {
	if loc == nil || loc == time.Local {
		return false
	}
	reloaded, err := time.LoadLocation(loc.String())
	return err == nil && reloaded.String() == loc.String()
}

// AddWorkingSeconds returns the instant that is `seconds` of WORKING time
// after `from`. Work runs in c.Location: from is converted in, the clock
// walks the daily windows, and the answer is converted back to UTC.
//
// If from falls outside a working day or outside the window, the clock has
// not started: nothing is consumed until the next window opening (a ticket
// created at 19:00 consumes nothing until 09:00 the next working day). If
// from is inside the window, the remainder of that day's window is
// consumed first, then subsequent working days follow. A non-positive
// seconds and an invalid calendar return from unchanged rather than
// inventing a deadline — the caller validates before calling.
//
// Day boundaries are handled in the calendar's location with wall-clock
// construction (time.Date), so daylight-saving transitions shift the
// window with the wall clock instead of drifting. A window crossing
// midnight is rejected by Valid, so this function never faces one. The
// function is pure: no clock reads, no I/O.
func (c SLACalendar) AddWorkingSeconds(from time.Time, seconds int) time.Time {
	if seconds <= 0 || !c.Valid() {
		return from
	}
	loc := c.Location
	cur := from.In(loc)
	remaining := seconds
	for {
		dayStart := calendarMidnight(cur, loc)
		if c.isWorkingDay(dayStart.Weekday()) {
			windowStart := wallClock(dayStart, c.StartMinute)
			windowEnd := wallClock(dayStart, c.EndMinute)
			if cur.Before(windowStart) {
				cur = windowStart
			}
			if cur.Before(windowEnd) {
				avail := int(windowEnd.Sub(cur) / time.Second)
				if remaining <= avail {
					return cur.Add(time.Duration(remaining) * time.Second).UTC()
				}
				remaining -= avail
			}
		}
		// Advance to the next calendar day's midnight, in wall-clock terms,
		// so a day the clock cannot run is skipped without consuming.
		cur = dayStart.AddDate(0, 0, 1)
	}
}

// isWorkingDay reports whether the weekday is one of the calendar's
// working days.
func (c SLACalendar) isWorkingDay(d time.Weekday) bool {
	for _, day := range c.WorkingDays {
		if day == d {
			return true
		}
	}
	return false
}

// calendarMidnight returns midnight of t's calendar date in loc.
func calendarMidnight(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

// wallClock returns the instant minute minutes past local midnight of the
// given day. Wall-clock construction (not duration addition) is what keeps
// the window pinned to 09:00-18:00 LOCAL across daylight-saving
// transitions.
func wallClock(dayStart time.Time, minute int) time.Time {
	return time.Date(dayStart.Year(), dayStart.Month(), dayStart.Day(),
		minute/60, minute%60, 0, 0, dayStart.Location())
}
