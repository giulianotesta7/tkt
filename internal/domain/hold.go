package domain

import "time"

// Automatic hold expiry (issue #353). A ticket put on hold waits for the
// requester (#352); when the requester never replies the instance can close it
// on its own after a configured number of WORKING days, after a reminder one
// working day earlier (specs S2/S3). The rules below are pure working-day
// arithmetic over the instance calendar — no clock reads, no I/O, no
// persistence — so the deadline and the reminder are testable against explicit
// instants and identical for every caller (the pass, the settings preview, the
// tests).

// AutomaticActorSystem is the actor name of a ticket change nobody performed:
// the automatic hold close and its reminder are system actions, so their audit
// events carry this name and a NULL ActorUserID — the same automatic-actor
// convention as the workflow runner's "workflow" actor, under a name of its
// own so a reader can tell an automatic hold close from a workflow step.
const AutomaticActorSystem = "system"

// HoldAutoCloseAt returns the instant the hold that began at since expires and
// the ticket is closed automatically (issue #353, S2): N WORKING days after the
// pause, on the same wall-clock time, over the instance calendar. A days value
// of 0 means the feature is off and answers since itself; the caller checks the
// setting before it acts on the answer.
func HoldAutoCloseAt(since time.Time, days int, cal SLACalendar) time.Time {
	return cal.AddWorkingDays(since, days)
}

// HoldReminderAt returns the instant the requester is warned that the held
// ticket is about to be closed (issue #353, S3): one WORKING day before the
// deadline, i.e. day days-1 after the pause. It is only meaningful for days
// >= 2 — a one-working-day window sends no reminder, and days-1 would then be
// the pause instant itself — so the caller guards on that before reading it.
func HoldReminderAt(since time.Time, days int, cal SLACalendar) time.Time {
	return cal.AddWorkingDays(since, days-1)
}
