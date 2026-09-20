package domain

import "time"

// SLAState is the compliance state of one SLA milestone or of a whole
// ticket (issue #211). It is derived, never stored: the projection is
// recomputed from the FROZEN due and warning instants, the observed
// milestone instants, and a caller-supplied now snapshot.
type SLAState string

const (
	SLANone     SLAState = "none"     // no frozen SLA on this ticket
	SLAOnTrack  SLAState = "on_track" // pending, before the frozen warning instant
	SLAAtRisk   SLAState = "at_risk"  // pending, at or past the frozen warning instant
	SLABreached SLAState = "breached" // overdue and still pending, or achieved late
	SLAMet      SLAState = "met"      // achieved at or before due
)

// SLAMilestones are the observed milestone instants for one ticket. A nil
// pointer means the milestone has not happened. Both instants are FIRSTS:
// the first public staff response and the first resolution (the SLA
// measures the commitment made at creation, so a later response or a
// reopen-and-reresolve never replaces the observed first).
type SLAMilestones struct {
	FirstResponseAt *time.Time
	FirstResolvedAt *time.Time
}

// SLAMilestoneStatus is one milestone projected against its frozen
// instants.
type SLAMilestoneStatus struct {
	TargetSeconds int
	// WarnAt is the frozen at_risk point (issue #211): the warning share of
	// the target, resolved against the working calendar when the commitment
	// was frozen. The warning percent is NOT applied here — it was applied
	// once, at freeze time, so editing sla_warning_percent never rewrites
	// the verdict of an existing ticket.
	WarnAt time.Time
	// DueAt is the frozen due point.
	DueAt      time.Time
	AchievedAt *time.Time
	State      SLAState
	// Elapsed is AchievedAt - StartedAt when the milestone is achieved,
	// otherwise now - StartedAt.
	Elapsed time.Duration
	// Remaining is DueAt - now for a pending milestone — negative once
	// overdue, so a caller renders the overdue amount as -Remaining — and
	// zero for an achieved milestone (the remaining time is meaningless
	// once achieved; the elapsed time is the fact).
	Remaining time.Duration
}

// SLAProjection is the full projection for one ticket. Overall is the
// WORST of the two milestone states (slaStateRank), which is what a queue
// badge needs: a ticket whose first response was met but whose resolution
// is overdue is breached.
type SLAProjection struct {
	Frozen        *TicketSLA
	FirstResponse SLAMilestoneStatus
	Resolve       SLAMilestoneStatus
	Overall       SLAState
	// ProjectedAt is the instant the projection was taken (ProjectSLA's own
	// now parameter). It exists to ANCHOR a client-side countdown: an HTTP
	// layer stamps it onto the page so the browser can derive its clock
	// offset WITHOUT the render path reading a second clock (the render path
	// never calls time.Now(), D7). It is never used to judge a milestone —
	// the states above already did that against this same instant.
	ProjectedAt time.Time
}

// ProjectSLA derives the SLA compliance projection for one ticket from the
// commitment frozen onto it, the observed milestone instants, and one now
// snapshot. It is pure and total: no clock reads, no I/O, no error return.
//
// The due and warning points are the FROZEN instants of the commitment
// (issue #211) — the working-calendar arithmetic ran once, at creation, so
// this projection never needs the calendar, the warning percent, or any
// other setting.
//
// A nil frozen commitment is legitimate (legacy tickets, tickets created
// while SLA was disabled): the projection carries no Frozen, Overall is
// SLANone, and both milestone statuses are left zero-valued — such a
// ticket renders as "no SLA", never as retroactively breached. The same
// holds for a commitment whose due instants are the zero time: that is the
// pre-0017 row shape (migration 0017 stores ” there), and a legacy row is
// "no frozen SLA", never a date in year zero treated as breached.
func ProjectSLA(frozen *TicketSLA, m SLAMilestones, now time.Time) SLAProjection {
	if frozen == nil || (frozen.DueFirstResponseAt.IsZero() && frozen.DueResolveAt.IsZero()) {
		return SLAProjection{Overall: SLANone, ProjectedAt: now}
	}
	firstResponse := projectSLAMilestone(frozen.StartedAt, frozen.WarnFirstResponseAt,
		frozen.DueFirstResponseAt, frozen.FirstResponseSeconds, m.FirstResponseAt, now)
	resolve := projectSLAMilestone(frozen.StartedAt, frozen.WarnResolveAt,
		frozen.DueResolveAt, frozen.ResolveSeconds, m.FirstResolvedAt, now)
	return SLAProjection{
		Frozen:        frozen,
		FirstResponse: firstResponse,
		Resolve:       resolve,
		Overall:       worstSLAState(firstResponse.State, resolve.State),
		ProjectedAt:   now,
	}
}

// projectSLAMilestone projects one milestone against its frozen warning
// and due instants. An ACHIEVED milestone is met when it happened at or
// before DueAt and breached otherwise. A PENDING milestone is breached
// once now reaches DueAt, at_risk once now reaches WarnAt, and on_track
// before that. A milestone whose DueAt is the zero time is the
// pre-0017 single-milestone shape: it projects as SLANone rather than
// comparing against a date in year zero.
func projectSLAMilestone(startedAt, warnAt, dueAt time.Time, targetSeconds int, achievedAt *time.Time, now time.Time) SLAMilestoneStatus {
	st := SLAMilestoneStatus{
		TargetSeconds: targetSeconds,
		WarnAt:        warnAt,
		DueAt:         dueAt,
		AchievedAt:    achievedAt,
	}
	if dueAt.IsZero() {
		st.Elapsed = now.Sub(startedAt)
		st.State = SLANone
		return st
	}
	if achievedAt != nil {
		st.Elapsed = achievedAt.Sub(startedAt)
		if achievedAt.After(dueAt) {
			st.State = SLABreached
		} else {
			st.State = SLAMet
		}
		return st
	}
	st.Elapsed = now.Sub(startedAt)
	st.Remaining = dueAt.Sub(now)
	// Legacy row note (issue #211): a commitment frozen BEFORE the freeze
	// began validating the warning percent can carry warnAt at or past
	// dueAt. Such a row has an EMPTY warning window by construction — WarnAt
	// is never reached while the milestone is still pending, so it reads
	// on_track until DueAt and breached from DueAt on, and at_risk is
	// unreachable (the due comparison below always wins first). The freeze
	// now refuses to create new rows of that shape; this projection keeps
	// the rows already frozen honest instead of resurrecting a warning or
	// masking the breach.
	switch {
	case !now.Before(dueAt):
		st.State = SLABreached
	case !now.Before(warnAt):
		st.State = SLAAtRisk
	default:
		st.State = SLAOnTrack
	}
	return st
}

// slaStateRank orders the states from best (0) to worst (4) for the
// overall worst-of computation.
func slaStateRank(s SLAState) int {
	switch s {
	case SLABreached:
		return 4
	case SLAAtRisk:
		return 3
	case SLAOnTrack:
		return 2
	case SLAMet:
		return 1
	default:
		return 0
	}
}

// worstSLAState returns the worse of two states by slaStateRank.
func worstSLAState(a, b SLAState) SLAState {
	if slaStateRank(b) > slaStateRank(a) {
		return b
	}
	return a
}
