package domain

import "time"

// Operational alerts (issue #408). An agent used to learn a ticket was in
// trouble only by opening it. These rules decide WHEN a ticket deserves an
// alert; who is told, how often, and through which channel belongs to the
// application layer.
//
// Like the hold arithmetic in hold.go, everything here is pure: the caller
// supplies the projection and the calendar, so a deadline is testable against
// explicit instants and identical for the pass, the tests, and any future
// preview.

// InactivityWorkingDays is how long a ticket may go without any movement
// before its assignee is told. It is a constant rather than an instance
// setting: the maintainer chose "2 días hábiles" (issue #408), and an
// administrable window is a separate decision with its own settings surface.
const InactivityWorkingDays = 2

// SLAAlertKinds returns the alert kinds DUE for one projected ticket, in a
// stable order: first response before resolution.
//
// A milestone that was already achieved is never alerted about — late or not,
// there is nothing left to do about it — and a milestone with no frozen
// commitment projects as SLANone and reports nothing, so a legacy ticket is
// never retroactively in trouble.
//
// Each milestone reports at most ONE kind: a breach replaces the risk rather
// than adding to it. A pass that runs late therefore announces the breach
// alone, instead of also warning about a threshold the ticket already crossed.
func SLAAlertKinds(p SLAProjection) []NotificationKind {
	var out []NotificationKind
	if kind, ok := slaMilestoneAlert(p.FirstResponse,
		NotificationSLAFirstResponseAtRisk, NotificationSLAFirstResponseBreached); ok {
		out = append(out, kind)
	}
	if kind, ok := slaMilestoneAlert(p.Resolve,
		NotificationSLAResolutionAtRisk, NotificationSLAResolutionBreached); ok {
		out = append(out, kind)
	}
	return out
}

// slaMilestoneAlert maps ONE milestone status to the kind that describes it,
// or reports that it deserves no alert. It reads the state ProjectSLA already
// derived from the frozen instants; it never compares against a threshold of
// its own, so an alert can never disagree with the SLA the ticket displays.
func slaMilestoneAlert(m SLAMilestoneStatus, atRisk, breached NotificationKind) (NotificationKind, bool) {
	if m.AchievedAt != nil {
		return "", false
	}
	switch m.State {
	case SLABreached:
		return breached, true
	case SLAAtRisk:
		return atRisk, true
	default:
		return "", false
	}
}

// NoMovementAt returns the instant a ticket whose last activity was at
// lastActivity has gone unattended for too long: InactivityWorkingDays working
// days later on the instance calendar, so a weekend or a holiday never counts
// against an agent.
//
// What counts as activity is the caller's question (a comment or a ticket
// change, issue #408 S5); this rule only turns that instant into a deadline.
func NoMovementAt(lastActivity time.Time, cal SLACalendar) time.Time {
	return cal.AddWorkingDays(lastActivity, InactivityWorkingDays)
}
