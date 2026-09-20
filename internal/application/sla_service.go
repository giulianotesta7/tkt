package application

import (
	"context"
	"fmt"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// SLAService implements the SLA projection use case (issue #211): derive
// one ticket's compliance state from the commitment frozen onto it, the
// observed milestone instants, and one clock snapshot. The projection
// itself is domain.ProjectSLA — pure and total — so this service is only
// composition: freeze lookup, milestone lookup, one clock read. The due
// and warning points were resolved against the working calendar at
// creation and travel inside the frozen commitment, so no setting is
// consulted at projection time.
type SLAService struct {
	sla      SLAStore
	settings SettingsStore
	clock    domain.Clock
}

// NewSLAService wires the SLA projection use case against its ports.
func NewSLAService(sla SLAStore, settings SettingsStore, clock domain.Clock) *SLAService {
	return &SLAService{sla: sla, settings: settings, clock: clock}
}

// ForTicket returns the projection for one ticket. A ticket with no
// frozen SLA returns a projection with Frozen nil and Overall SLANone,
// and a nil error — a legitimate state for legacy tickets and for tickets
// created while SLA was disabled; it renders as "no SLA", never as
// retroactively breached.
//
// ForTicket deliberately does NOT consult sla_enabled: disabling the
// feature gates NEW freezes only, and a commitment already frozen onto a
// ticket stays visible — exactly as a policy edit never rewrites history.
//
// It takes ONE clock.Now() snapshot and threads it into ProjectSLA, so
// both milestones are measured against the same instant and the domain
// projection stays clock-free.
func (s *SLAService) ForTicket(ctx context.Context, ticketID int64) (domain.SLAProjection, error) {
	frozen, err := s.sla.TicketSLA(ctx, ticketID)
	if err != nil {
		return domain.SLAProjection{}, err
	}
	if frozen == nil {
		return domain.SLAProjection{Overall: domain.SLANone}, nil
	}
	milestones, err := s.sla.Milestones(ctx, ticketID)
	if err != nil {
		return domain.SLAProjection{}, err
	}
	return domain.ProjectSLA(frozen, milestones, s.clock.Now()), nil
}

// ForTickets returns the projection for MANY tickets at once (issue
// #211): the list-rendering read path. Two guarantees:
//
//   - ONE clock snapshot for the whole batch: a page is never judged at
//     two instants, so two rows can never disagree because time moved
//     between their projections.
//   - An entry for EVERY requested id: a ticket without a frozen
//     commitment answers Overall SLANone, exactly as ForTicket does.
//
// It performs exactly two store reads for the whole batch — one
// TicketSLAs and one MilestonesFor — so a staff list renders one SLA
// badge per row without the ForTicket N+1 (two per-ticket queries plus a
// per-ticket clock read). Empty ids answer an empty map with NO store
// call and NO clock read.
func (s *SLAService) ForTickets(ctx context.Context, ticketIDs []int64) (map[int64]domain.SLAProjection, error) {
	out := make(map[int64]domain.SLAProjection, len(ticketIDs))
	if len(ticketIDs) == 0 {
		return out, nil
	}
	frozen, err := s.sla.TicketSLAs(ctx, ticketIDs)
	if err != nil {
		return nil, err
	}
	milestones, err := s.sla.MilestonesFor(ctx, ticketIDs)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	for _, id := range ticketIDs {
		f := frozen[id]
		if f == nil {
			out[id] = domain.SLAProjection{Overall: domain.SLANone}
			continue
		}
		out[id] = domain.ProjectSLA(f, milestones[id], now)
	}
	return out, nil
}

// ResolveForCreate returns the commitment to freeze onto a ticket about to
// be created, or nil when SLA is disabled or the category has no policy
// row. A store error is returned rather than swallowed.
//
// Contract (issue #211):
//
//   - sla_enabled false -> (nil, nil). SLA off means new tickets carry no
//     commitment.
//   - No policy row for (categoryID, priority) -> (nil, nil). Absence is a
//     state, not a failure.
//   - A policy row -> the commitment built from its two targets.
//   - A store error -> returned. A ticket silently lacking its commitment
//     is a data loss the SLA reports would then hide, so the create must
//     fail rather than proceed without it. Absence is a state; inability
//     to read is not.
//
// now is a PARAMETER and is deliberately NOT read from s.clock: the caller
// passes the same instant it stamps on the ticket's CreatedAt, so
// TicketSLA.StartedAt is identical to the ticket's own creation instant —
// two independent clock reads could land on different instants and would
// silently put the SLA clock out of step with the ticket clock. Both
// StartedAt and PolicySnapshotAt are set to that now.
//
// The four frozen instants are computed from the CURRENT working calendar
// and the warning percent AS OF creation (issue #211): the due point is
// `target` seconds of working time after now, the warning point the
// warning share of the target. A later calendar or percent edit therefore
// applies to tickets created from then on — the same freeze rule the
// targets already follow. The calendar the settings store answers is
// always valid (absent or unparseable keys fall back to the documented
// defaults at the store boundary), so the instants are real deadlines,
// never the unchanged-input degenerate result.
func (s *SLAService) ResolveForCreate(ctx context.Context, categoryID int64, priority domain.Priority, now time.Time) (*domain.TicketSLA, error) {
	enabled, err := s.settings.GetSLAEnabled(ctx)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, nil
	}
	policies, err := s.sla.ListByCategory(ctx, categoryID)
	if err != nil {
		return nil, err
	}
	for _, p := range policies {
		if p.Priority != priority {
			continue
		}
		return s.freezeTicketSLA(ctx, p, now)
	}
	return nil, nil
}

// freezeTicketSLA builds the fully populated commitment for one policy
// row: the two target durations plus the four instants the working
// calendar resolves them to at freeze time (issue #211). started_at stays
// the creation instant — the truthful record; the effective start of the
// SLA clock is captured by the instants themselves.
//
// The calendar is re-validated here and an invalid one is an error rather
// than a degenerate freeze: AddWorkingSeconds answers `now` unchanged for
// an invalid calendar, so freezing would store due instants equal to the
// creation instant and mark every new ticket breached at creation.
func (s *SLAService) freezeTicketSLA(ctx context.Context, p domain.SLAPolicy, now time.Time) (*domain.TicketSLA, error) {
	calendar, err := s.settings.GetSLACalendar(ctx)
	if err != nil {
		return nil, err
	}
	percent, err := s.settings.GetSLAWarningPercent(ctx)
	if err != nil {
		return nil, err
	}
	if !calendar.Valid() {
		return nil, fmt.Errorf("sla: refusing to freeze targets against an invalid working calendar")
	}
	return &domain.TicketSLA{
		FirstResponseSeconds: p.FirstResponseSeconds,
		ResolveSeconds:       p.ResolveSeconds,
		WarnFirstResponseAt:  calendar.AddWorkingSeconds(now, p.FirstResponseSeconds*percent/100),
		DueFirstResponseAt:   calendar.AddWorkingSeconds(now, p.FirstResponseSeconds),
		WarnResolveAt:        calendar.AddWorkingSeconds(now, p.ResolveSeconds*percent/100),
		DueResolveAt:         calendar.AddWorkingSeconds(now, p.ResolveSeconds),
		StartedAt:            now,
		PolicySnapshotAt:     now,
	}, nil
}

// MinSLATargetSeconds is the floor for both milestone targets, mirroring
// the CHECK(... >= 60) constraint on sla_defaults and sla_policies
// (migration 0013): a sub-minute commitment is not a configuration.
const MinSLATargetSeconds = 60

// canonicalSLAPriorities is the exact priority set a default or category
// matrix must cover, in reporting order — the validation below reports the
// FIRST offending priority in this order so failures are deterministic.
var canonicalSLAPriorities = []domain.Priority{
	domain.PriorityLow,
	domain.PriorityMedium,
	domain.PriorityHigh,
	domain.PriorityCritical,
}

// validateSLAMatrix enforces the exact-matrix contract shared by
// SetDefaultTargets and SetCategoryTargets: the slice must contain
// EXACTLY the four priorities — low, medium, high, critical — each once,
// with no duplicate, no unknown value and none missing. A partial matrix
// is not a smaller configuration, it is a broken one. Every target must
// be at least MinSLATargetSeconds. Each violation is a ValidationError
// whose Field names the offending priority.
func validateSLAMatrix(policies []domain.SLAPolicy) error {
	counts := make(map[domain.Priority]int, len(policies))
	for _, p := range policies {
		counts[p.Priority]++
	}
	for _, want := range canonicalSLAPriorities {
		switch counts[want] {
		case 0:
			return &domain.ValidationError{
				Field:   string(want),
				Message: fmt.Sprintf("missing SLA policy for priority %q", want),
			}
		case 1:
			delete(counts, want)
		default:
			return &domain.ValidationError{
				Field:   string(want),
				Message: fmt.Sprintf("duplicate SLA policy for priority %q", want),
			}
		}
	}
	for p := range counts {
		return &domain.ValidationError{
			Field:   string(p),
			Message: fmt.Sprintf("unknown SLA priority %q", p),
		}
	}
	for _, p := range policies {
		if p.FirstResponseSeconds < MinSLATargetSeconds {
			return &domain.ValidationError{
				Field:   string(p.Priority),
				Message: fmt.Sprintf("first response target for priority %q is below %d seconds", p.Priority, MinSLATargetSeconds),
			}
		}
		if p.ResolveSeconds < MinSLATargetSeconds {
			return &domain.ValidationError{
				Field:   string(p.Priority),
				Message: fmt.Sprintf("resolve target for priority %q is below %d seconds", p.Priority, MinSLATargetSeconds),
			}
		}
	}
	return nil
}

// SetEnabled turns the per-category SLA on or off. The actor must hold
// CapManageSettings (admin/root) — this is instance-wide configuration.
//
// The flag and the activation instant are written in ONE transaction by
// the store, so a failure cannot leave the feature enabled with no
// instant recorded. The clock is read exactly once and handed down; the
// store stamps the instant only when it is not already set, so it records
// when the feature was FIRST switched on and disabling never erases it.
func (s *SLAService) SetEnabled(ctx context.Context, actor domain.User, enabled bool) error {
	if !NewPolicy().Capabilities(actor.Role).Require(CapManageSettings) {
		return domain.NewForbiddenError("sla settings are not permitted")
	}
	return s.settings.SetSLAEnabledState(ctx, enabled, s.clock.Now())
}

// SetWarningPercent updates the SLA warning threshold. The actor must
// hold CapManageSettings (admin/root). The percent must be 1..99
// inclusive — the projection treats a value outside (0,100) exclusive as
// an empty warning window, so accepting 0 or 100 here would silently
// disable the warning rather than configure it. Rejected values touch no
// store.
func (s *SLAService) SetWarningPercent(ctx context.Context, actor domain.User, percent int) error {
	if !NewPolicy().Capabilities(actor.Role).Require(CapManageSettings) {
		return domain.NewForbiddenError("sla settings are not permitted")
	}
	if percent < 1 || percent > 99 {
		return &domain.ValidationError{
			Field:   "sla_warning_percent",
			Message: "the SLA warning percent must be between 1 and 99",
		}
	}
	return s.settings.SetSLAWarningPercent(ctx, percent)
}

// SetDefaultTargets replaces the global default matrix. The actor must
// hold CapManageSettings (admin/root) — instance-wide configuration. The
// matrix must be the EXACT four-priority set (validateSLAMatrix); the
// store persists all four rows all or nothing (UpsertDefaults).
func (s *SLAService) SetDefaultTargets(ctx context.Context, actor domain.User, policies []domain.SLAPolicy) error {
	if !NewPolicy().Capabilities(actor.Role).Require(CapManageSettings) {
		return domain.NewForbiddenError("sla settings are not permitted")
	}
	if err := validateSLAMatrix(policies); err != nil {
		return err
	}
	return s.sla.UpsertDefaults(ctx, policies)
}

// SetGlobalConfiguration applies the WHOLE instance SLA panel — the enable
// flag, the warning percent, and the default target matrix — as one unit.
// The actor must hold CapManageSettings (admin/root).
//
// Everything is validated BEFORE any store call, and the store then applies
// the panel in ONE transaction. Doing it in three calls and compensating for
// the completed ones on failure would leave the inconsistent state behind
// whenever the compensation itself fails, and it would put write ordering and
// undo semantics in the HTTP adapter.
func (s *SLAService) SetGlobalConfiguration(ctx context.Context, actor domain.User, enabled bool, warningPercent int, defaults []domain.SLAPolicy) error {
	if !NewPolicy().Capabilities(actor.Role).Require(CapManageSettings) {
		return domain.NewForbiddenError("sla settings are not permitted")
	}
	if warningPercent < 1 || warningPercent > 99 {
		return &domain.ValidationError{
			Field:   "sla_warning_percent",
			Message: "the SLA warning percent must be between 1 and 99",
		}
	}
	if err := validateSLAMatrix(defaults); err != nil {
		return err
	}
	return s.settings.SetSLAConfiguration(ctx, enabled, s.clock.Now(), warningPercent, defaults)
}

// SetCategoryTargets replaces one category's materialized matrix. The
// actor must hold CapManageCategories (admin+) — the same capability that
// gates category management, because this edits one category's
// configuration. The matrix must be the EXACT four-priority set
// (validateSLAMatrix); the store persists all four rows all or nothing
// (UpsertCategoryTargets).
func (s *SLAService) SetCategoryTargets(ctx context.Context, actor domain.User, categoryID int64, policies []domain.SLAPolicy) error {
	if !NewPolicy().Capabilities(actor.Role).Require(CapManageCategories) {
		return domain.NewForbiddenError("sla category targets are not permitted")
	}
	if err := validateSLAMatrix(policies); err != nil {
		return err
	}
	return s.sla.UpsertCategoryTargets(ctx, categoryID, policies)
}

// Calendar returns the instance working calendar (issue #211): the read
// use case for the Settings screen. The store assembles it from the four
// sla_calendar_* keys and falls back to the documented defaults per key,
// so a successful read always answers a usable calendar; a store error is
// a real storage failure and is returned.
func (s *SLAService) Calendar(ctx context.Context) (domain.SLACalendar, error) {
	return s.settings.GetSLACalendar(ctx)
}

// SetSLACalendar replaces the instance working calendar. The actor must
// hold CapManageSettings (admin/root) — instance-wide configuration.
//
// The calendar is validated BEFORE any store call: at least one working
// day, StartMinute strictly before EndMinute (which also rejects a window
// crossing midnight), minutes within 0..1439, and a resolvable timezone
// that is also PORTABLE — the port carries the already-resolved
// *time.Location, so resolving the IANA name is the boundary's job, and a
// name that does not read back as the same zone (time.Local, a fixed-offset
// zone) is refused because the store's read would silently substitute UTC
// and shift every future due instant. The store then writes the whole
// calendar in ONE transaction: a partially applied calendar would change
// when every future ticket is due.
//
// The edit never rewrites the frozen instants of tickets already created
// — the same freeze principle as a policy edit (issue #211).
func (s *SLAService) SetSLACalendar(ctx context.Context, actor domain.User, calendar domain.SLACalendar) error {
	if !NewPolicy().Capabilities(actor.Role).Require(CapManageSettings) {
		return domain.NewForbiddenError("sla settings are not permitted")
	}
	if err := validateSLACalendar(calendar); err != nil {
		return err
	}
	return s.settings.SetSLACalendar(ctx, calendar)
}

// validateSLACalendar enforces the working-calendar contract shared by
// every calendar write: at least one working day, StartMinute strictly
// before EndMinute (Valid also rejects a window crossing midnight), both
// minutes within 0..1439, and a resolvable timezone that is also PORTABLE
// (a non-nil location whose name reads back as itself — see
// SetSLACalendar). Each violation is a ValidationError on the sla_calendar
// field, so the Settings screen can re-render with one message.
func validateSLACalendar(calendar domain.SLACalendar) error {
	if !calendar.Valid() {
		return &domain.ValidationError{
			Field:   "sla_calendar",
			Message: "the working calendar needs at least one working day, a start minute strictly before its end minute, and a timezone",
		}
	}
	if calendar.StartMinute < 0 || calendar.StartMinute > 1439 || calendar.EndMinute < 0 || calendar.EndMinute > 1439 {
		return &domain.ValidationError{
			Field:   "sla_calendar",
			Message: "the working calendar minutes must be within 0 and 1439",
		}
	}
	// The store persists the zone by NAME, so a name that does not read back
	// as the same zone would shift every future due instant silently. Refuse
	// the write instead: a stored setting must mean what it says.
	if !domain.LocationIsPortable(calendar.Location) {
		return &domain.ValidationError{
			Field:   "sla_calendar",
			Message: "the working calendar timezone must be a portable IANA zone name",
		}
	}
	return nil
}
