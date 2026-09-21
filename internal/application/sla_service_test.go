package application_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// SLAService.ForTicket (issue #211): composition of the FROZEN warning
// and due instants, the observed milestones, and ONE clock snapshot into
// the pure domain projection. The warning percent is not consulted at
// projection time — it was applied once, at freeze time.

// slaServiceFrozen returns the frozen commitment used by the service
// tests: 1800s to first response, 14400s to resolution, anchored at the
// fake clock's instant, with the freeze-time instants an 80 percent
// warning threshold resolves to.
func slaServiceFrozen(clock *fakeClock) *domain.TicketSLA {
	return &domain.TicketSLA{
		FirstResponseSeconds: 1800,
		ResolveSeconds:       14400,
		WarnFirstResponseAt:  clock.now.Add(1440 * time.Second),
		DueFirstResponseAt:   clock.now.Add(1800 * time.Second),
		WarnResolveAt:        clock.now.Add(11520 * time.Second),
		DueResolveAt:         clock.now.Add(14400 * time.Second),
		StartedAt:            clock.now,
		PolicySnapshotAt:     clock.now,
	}
}

// countingClock wraps fakeClock and counts Now() calls, so a test can
// prove ForTicket reads the clock exactly once.
type countingClock struct {
	*fakeClock
	nowCalls int
}

func (c *countingClock) Now() time.Time {
	c.nowCalls++
	return c.fakeClock.Now()
}

func TestSLAServiceNoFrozenSLA(t *testing.T) {
	sla := &fakeSLAStore{}
	svc := application.NewSLAService(sla, &fakeSLASettingsStore{}, fixedClock())

	got, err := svc.ForTicket(context.Background(), 42)
	if err != nil {
		t.Fatalf("ForTicket: %v", err)
	}
	if got.Frozen != nil {
		t.Errorf("Frozen = %+v, want nil", got.Frozen)
	}
	if got.Overall != domain.SLANone {
		t.Errorf("Overall = %q, want %q", got.Overall, domain.SLANone)
	}
}

func TestSLAServiceProjectsFrozenRowAndMilestones(t *testing.T) {
	clock := fixedClock()
	ticketID := int64(42)
	sla := &fakeSLAStore{
		frozen: map[int64]*domain.TicketSLA{ticketID: slaServiceFrozen(clock)},
		milestones: map[int64]domain.SLAMilestones{ticketID: {
			FirstResponseAt: ptr(clock.now.Add(10 * time.Minute)), // met (600s < 1800s)
			// no resolution yet; 14500s past start is past the 14400s due
		}},
	}
	clock.Advance(14500 * time.Second)

	svc := application.NewSLAService(sla, &fakeSLASettingsStore{}, clock)
	got, err := svc.ForTicket(context.Background(), ticketID)
	if err != nil {
		t.Fatalf("ForTicket: %v", err)
	}
	if got.Frozen == nil || got.Frozen.FirstResponseSeconds != 1800 || got.Frozen.ResolveSeconds != 14400 {
		t.Fatalf("Frozen = %+v, want the frozen row (1800, 14400)", got.Frozen)
	}
	if got.FirstResponse.State != domain.SLAMet {
		t.Errorf("FirstResponse.State = %q, want %q", got.FirstResponse.State, domain.SLAMet)
	}
	if got.Resolve.State != domain.SLABreached {
		t.Errorf("Resolve.State = %q, want %q", got.Resolve.State, domain.SLABreached)
	}
	if got.Overall != domain.SLABreached {
		t.Errorf("Overall = %q, want %q (worst-of)", got.Overall, domain.SLABreached)
	}
	// Remaining is negative once overdue so the caller renders -Remaining.
	if got.Resolve.Remaining >= 0 {
		t.Errorf("Resolve.Remaining = %v, want negative once overdue", got.Resolve.Remaining)
	}
}

// TestSLAServiceFrozenWarnInstantDrivesAtRisk proves at_risk comes from
// the FROZEN warning instant, not the current warning percent: at 1440s
// past start the milestone is at_risk exactly because the commitment was
// frozen with WarnAt there — with a percent of 50 in settings, an 80
// percent freeze would now be on_track, and the frozen verdict must not
// move (issue #211).
func TestSLAServiceFrozenWarnInstantDrivesAtRisk(t *testing.T) {
	clock := fixedClock()
	ticketID := int64(7)
	sla := &fakeSLAStore{
		frozen:     map[int64]*domain.TicketSLA{ticketID: slaServiceFrozen(clock)},
		milestones: map[int64]domain.SLAMilestones{},
	}
	clock.Advance(900 * time.Second)

	// 900s in: before the frozen 1440s warning instant — on_track.
	onTrack := application.NewSLAService(sla, &fakeSLASettingsStore{slaWarningPercent: 80}, clock)
	got, err := onTrack.ForTicket(context.Background(), ticketID)
	if err != nil {
		t.Fatalf("ForTicket: %v", err)
	}
	if got.FirstResponse.State != domain.SLAOnTrack {
		t.Errorf("FirstResponse.State = %q before the frozen WarnAt, want %q", got.FirstResponse.State, domain.SLAOnTrack)
	}

	// The settings percent changed to 50 after the freeze: crossing the
	// FROZEN 1440s instant still turns the milestone at_risk.
	clock.Advance(540 * time.Second)
	afterFreeze := application.NewSLAService(sla, &fakeSLASettingsStore{slaWarningPercent: 50}, clock)
	got, err = afterFreeze.ForTicket(context.Background(), ticketID)
	if err != nil {
		t.Fatalf("ForTicket: %v", err)
	}
	if got.FirstResponse.State != domain.SLAAtRisk {
		t.Errorf("FirstResponse.State = %q at the frozen WarnAt, want %q", got.FirstResponse.State, domain.SLAAtRisk)
	}
}

// TestSLAServiceSingleClockSnapshot proves ForTicket reads the clock
// exactly ONCE and threads that snapshot into the projection: both
// milestones are measured against the same instant.
func TestSLAServiceSingleClockSnapshot(t *testing.T) {
	clock := &countingClock{fakeClock: fixedClock()}
	ticketID := int64(42)
	sla := &fakeSLAStore{
		frozen:     map[int64]*domain.TicketSLA{ticketID: slaServiceFrozen(clock.fakeClock)},
		milestones: map[int64]domain.SLAMilestones{},
	}

	svc := application.NewSLAService(sla, &fakeSLASettingsStore{}, clock)
	if _, err := svc.ForTicket(context.Background(), ticketID); err != nil {
		t.Fatalf("ForTicket: %v", err)
	}
	if clock.nowCalls != 1 {
		t.Errorf("clock.Now() calls = %d, want 1 (one snapshot for the whole projection)", clock.nowCalls)
	}
}

// --- ResolveForCreate (issue #211): the commitment frozen onto a ticket
// about to be created. now is a PARAMETER — the caller passes the same
// instant it stamps on the ticket's CreatedAt.

// slaServicePolicies returns a category matrix with one row per priority,
// keyed by category id, for the ResolveForCreate tests.
func slaServicePolicies(catID int64) map[int64][]domain.SLAPolicy {
	return map[int64][]domain.SLAPolicy{catID: {
		{Priority: domain.PriorityCritical, FirstResponseSeconds: 300, ResolveSeconds: 3600},
		{Priority: domain.PriorityHigh, FirstResponseSeconds: 1800, ResolveSeconds: 14400},
		{Priority: domain.PriorityMedium, FirstResponseSeconds: 7200, ResolveSeconds: 86400},
		{Priority: domain.PriorityLow, FirstResponseSeconds: 86400, ResolveSeconds: 604800},
	}}
}

// TestSLAServiceResolveDisabledReturnsNil proves sla_enabled false answers
// (nil, nil): SLA off means new tickets carry no commitment, even when the
// category has a materialized policy row.
func TestSLAServiceResolveDisabledReturnsNil(t *testing.T) {
	catID := int64(3)
	sla := &fakeSLAStore{policies: slaServicePolicies(catID)}
	svc := application.NewSLAService(sla, &fakeSLASettingsStore{slaEnabled: false}, fixedClock())

	got, err := svc.ResolveForCreate(context.Background(), catID, domain.PriorityHigh, fixedClock().now)
	if err != nil {
		t.Fatalf("ResolveForCreate: %v", err)
	}
	if got != nil {
		t.Errorf("ResolveForCreate = %+v, want nil (SLA disabled)", got)
	}
}

// TestSLAServiceResolveMissingPolicyReturnsNil proves a category without a
// policy row for the priority answers (nil, nil): absence is a state, not
// a failure.
func TestSLAServiceResolveMissingPolicyReturnsNil(t *testing.T) {
	clock := fixedClock()
	catID := int64(3)
	sla := &fakeSLAStore{policies: slaServicePolicies(catID)}
	svc := application.NewSLAService(sla, &fakeSLASettingsStore{slaEnabled: true}, clock)

	// No row for low... actually there is: ask for a category with NO matrix.
	got, err := svc.ResolveForCreate(context.Background(), 999, domain.PriorityHigh, clock.now)
	if err != nil {
		t.Fatalf("ResolveForCreate (no matrix): %v", err)
	}
	if got != nil {
		t.Errorf("ResolveForCreate (no matrix) = %+v, want nil", got)
	}

	// A matrix without the requested priority is the same absence state:
	// remove the high row and ask for high.
	sla.policies[catID] = []domain.SLAPolicy{
		{Priority: domain.PriorityCritical, FirstResponseSeconds: 300, ResolveSeconds: 3600},
		{Priority: domain.PriorityLow, FirstResponseSeconds: 86400, ResolveSeconds: 604800},
	}
	got, err = svc.ResolveForCreate(context.Background(), catID, domain.PriorityHigh, clock.now)
	if err != nil {
		t.Fatalf("ResolveForCreate (missing priority): %v", err)
	}
	if got != nil {
		t.Errorf("ResolveForCreate (missing priority) = %+v, want nil", got)
	}
}

// TestSLAServiceResolveFreezesCallerNow proves a present policy row yields
// the commitment built from its two targets with BOTH StartedAt and
// PolicySnapshotAt equal to the now PARAMETER — never a clock read. The
// clock is advanced past the parameter so any clock read would produce a
// visibly different instant.
func TestSLAServiceResolveFreezesCallerNow(t *testing.T) {
	clock := fixedClock()
	catID := int64(3)
	sla := &fakeSLAStore{policies: slaServicePolicies(catID)}
	svc := application.NewSLAService(sla, &fakeSLASettingsStore{slaEnabled: true}, clock)

	now := clock.now.Add(5 * time.Minute)
	clock.Advance(3 * time.Hour) // a clock read would answer clock.now, NOT now

	got, err := svc.ResolveForCreate(context.Background(), catID, domain.PriorityMedium, now)
	if err != nil {
		t.Fatalf("ResolveForCreate: %v", err)
	}
	if got == nil {
		t.Fatal("ResolveForCreate = nil, want the commitment built from the policy row")
	}
	if got.FirstResponseSeconds != 7200 || got.ResolveSeconds != 86400 {
		t.Errorf("targets = (%d, %d), want the policy row (7200, 86400)", got.FirstResponseSeconds, got.ResolveSeconds)
	}
	if !got.StartedAt.Equal(now) {
		t.Errorf("StartedAt = %v, want the caller's now %v (never a clock read)", got.StartedAt, now)
	}
	if !got.PolicySnapshotAt.Equal(now) {
		t.Errorf("PolicySnapshotAt = %v, want the caller's now %v", got.PolicySnapshotAt, now)
	}
	// The four instants are real (non-zero): the commitment is fully
	// populated even when the freeze-time arithmetic needs no skipping.
	if got.DueFirstResponseAt.IsZero() || got.WarnFirstResponseAt.IsZero() || got.DueResolveAt.IsZero() || got.WarnResolveAt.IsZero() {
		t.Errorf("frozen instants not all populated: %+v", got)
	}
}

// TestSLAServiceResolveInvalidCalendarFailsClosed proves the freeze path
// re-validates the calendar and FAILS CLOSED. An invalid calendar makes
// AddWorkingSeconds answer `now` unchanged, so freezing would store four
// instants equal to started_at and every new ticket would read as breached
// at creation. The commitment must be refused with an error instead: the
// store is not the only possible settings port, so the guarantee cannot
// live only there.
func TestSLAServiceResolveInvalidCalendarFailsClosed(t *testing.T) {
	clock := fixedClock()
	catID := int64(3)
	sla := &fakeSLAStore{policies: slaServicePolicies(catID)}
	settings := &fakeSLASettingsStore{
		slaEnabled: true,
		calendar: domain.SLACalendar{
			WorkingDays: []time.Weekday{time.Monday},
			StartMinute: 1200, EndMinute: 1080,
			Location: time.UTC,
		},
	}
	svc := application.NewSLAService(sla, settings, clock)

	got, err := svc.ResolveForCreate(context.Background(), catID, domain.PriorityHigh, clock.now)
	if err == nil {
		t.Fatalf("ResolveForCreate with an invalid calendar = %+v, want an error", got)
	}
	if got != nil {
		t.Errorf("ResolveForCreate = %+v, want nil alongside the error", got)
	}
}

// TestSLAServiceResolveComputesFrozenInstants proves the four instants
// come from the CURRENT calendar and the freeze-time warning percent:
// with the default Mon-Fri 09:00-18:00 UTC calendar, a Thursday 10:00
// creation of a high ticket (1800s/14400s targets, 80 percent warning)
// freezes warn 10:24, due 10:30, warn 13:12, due 14:00 — all inside the
// same working day.
func TestSLAServiceResolveComputesFrozenInstants(t *testing.T) {
	clock := fixedClock() // Thursday 2026-08-06 10:00 UTC
	catID := int64(3)
	sla := &fakeSLAStore{policies: slaServicePolicies(catID)}
	svc := application.NewSLAService(sla, &fakeSLASettingsStore{slaEnabled: true, slaWarningPercent: 80}, clock)

	got, err := svc.ResolveForCreate(context.Background(), catID, domain.PriorityHigh, clock.now)
	if err != nil {
		t.Fatalf("ResolveForCreate: %v", err)
	}
	if got == nil {
		t.Fatal("ResolveForCreate = nil, want the commitment")
	}
	want := domain.TicketSLA{
		WarnFirstResponseAt: clock.now.Add(1440 * time.Second),  // 10:24
		DueFirstResponseAt:  clock.now.Add(1800 * time.Second),  // 10:30
		WarnResolveAt:       clock.now.Add(11520 * time.Second), // 13:12
		DueResolveAt:        clock.now.Add(14400 * time.Second), // 14:00
	}
	if !got.WarnFirstResponseAt.Equal(want.WarnFirstResponseAt) {
		t.Errorf("WarnFirstResponseAt = %v, want %v", got.WarnFirstResponseAt, want.WarnFirstResponseAt)
	}
	if !got.DueFirstResponseAt.Equal(want.DueFirstResponseAt) {
		t.Errorf("DueFirstResponseAt = %v, want %v", got.DueFirstResponseAt, want.DueFirstResponseAt)
	}
	if !got.WarnResolveAt.Equal(want.WarnResolveAt) {
		t.Errorf("WarnResolveAt = %v, want %v", got.WarnResolveAt, want.WarnResolveAt)
	}
	if !got.DueResolveAt.Equal(want.DueResolveAt) {
		t.Errorf("DueResolveAt = %v, want %v", got.DueResolveAt, want.DueResolveAt)
	}
}

// TestSLAServiceResolveSkipsNonWorkingSpan proves a ticket created OUTSIDE
// working hours gets due instants that skip the non-working span: a
// Thursday 19:00 creation (after the 18:00 close) consumes nothing until
// Friday 09:00, so an 1800s first-response target is due Friday 09:30 and
// a 14400s resolution target Friday 13:00.
func TestSLAServiceResolveSkipsNonWorkingSpan(t *testing.T) {
	clock := fixedClock()
	catID := int64(3)
	sla := &fakeSLAStore{policies: slaServicePolicies(catID)}
	svc := application.NewSLAService(sla, &fakeSLASettingsStore{slaEnabled: true, slaWarningPercent: 80}, clock)

	now := clock.now.Add(9 * time.Hour) // Thursday 19:00 UTC, past the close
	got, err := svc.ResolveForCreate(context.Background(), catID, domain.PriorityHigh, now)
	if err != nil {
		t.Fatalf("ResolveForCreate: %v", err)
	}
	fridayNine := now.Add(14 * time.Hour) // Friday 09:00 UTC
	if !got.DueFirstResponseAt.Equal(fridayNine.Add(30 * time.Minute)) {
		t.Errorf("DueFirstResponseAt = %v, want Friday 09:30 UTC", got.DueFirstResponseAt)
	}
	if !got.DueResolveAt.Equal(fridayNine.Add(4 * time.Hour)) {
		t.Errorf("DueResolveAt = %v, want Friday 13:00 UTC", got.DueResolveAt)
	}
	// The creation instant itself stays the truthful started_at.
	if !got.StartedAt.Equal(now) {
		t.Errorf("StartedAt = %v, want the creation instant %v", got.StartedAt, now)
	}
}

// TestSLAServiceResolveWarnInstantUsesFreezePercent proves the warning
// percent at freeze time shapes the warning instants (they come from the
// calendar AND the percent as of creation) while the due instants are
// percent-independent: a 50 percent freeze warns earlier than an 80
// percent freeze of the same targets, and both dues are identical.
func TestSLAServiceResolveWarnInstantUsesFreezePercent(t *testing.T) {
	clock := fixedClock()
	catID := int64(3)
	sla := &fakeSLAStore{policies: slaServicePolicies(catID)}

	fifty := application.NewSLAService(sla, &fakeSLASettingsStore{slaEnabled: true, slaWarningPercent: 50}, clock)
	a, err := fifty.ResolveForCreate(context.Background(), catID, domain.PriorityHigh, clock.now)
	if err != nil {
		t.Fatalf("ResolveForCreate (50%%): %v", err)
	}
	eighty := application.NewSLAService(sla, &fakeSLASettingsStore{slaEnabled: true, slaWarningPercent: 80}, clock)
	b, err := eighty.ResolveForCreate(context.Background(), catID, domain.PriorityHigh, clock.now)
	if err != nil {
		t.Fatalf("ResolveForCreate (80%%): %v", err)
	}

	if !a.WarnFirstResponseAt.Equal(clock.now.Add(900 * time.Second)) {
		t.Errorf("50%% WarnFirstResponseAt = %v, want +900s", a.WarnFirstResponseAt)
	}
	if !b.WarnFirstResponseAt.Equal(clock.now.Add(1440 * time.Second)) {
		t.Errorf("80%% WarnFirstResponseAt = %v, want +1440s", b.WarnFirstResponseAt)
	}
	if !a.DueFirstResponseAt.Equal(b.DueFirstResponseAt) {
		t.Errorf("DueFirstResponseAt differs across percents: %v vs %v (dues are percent-independent)",
			a.DueFirstResponseAt, b.DueFirstResponseAt)
	}
	if !a.DueResolveAt.Equal(b.DueResolveAt) {
		t.Errorf("DueResolveAt differs across percents: %v vs %v", a.DueResolveAt, b.DueResolveAt)
	}
}

// TestSLAServiceResolveCalendarErrorPropagates proves a calendar read
// failure is returned rather than swallowed: freezing instants from a
// defaulted calendar would be silent data corruption.
func TestSLAServiceResolveCalendarErrorPropagates(t *testing.T) {
	readErr := errors.New("calendar read failure")
	catID := int64(3)
	sla := &fakeSLAStore{policies: slaServicePolicies(catID)}
	settings := &fakeSLASettingsStore{slaEnabled: true, calendarErr: readErr}
	svc := application.NewSLAService(sla, settings, fixedClock())
	if _, err := svc.ResolveForCreate(context.Background(), catID, domain.PriorityHigh, fixedClock().now); !errors.Is(err, readErr) {
		t.Errorf("calendar-read error = %v, want %v propagated", err, readErr)
	}
}

// TestSLAServiceResolveStoreErrorPropagates proves a store error is
// returned rather than swallowed: inability to read is not absence, and a
// create proceeding without its commitment would be silent data loss.
func TestSLAServiceResolveStoreErrorPropagates(t *testing.T) {
	catID := int64(3)
	readErr := errors.New("sla read failure")

	sla := &fakeSLAStore{policies: slaServicePolicies(catID), listByCategoryErr: readErr}
	svc := application.NewSLAService(sla, &fakeSLASettingsStore{slaEnabled: true}, fixedClock())
	if _, err := svc.ResolveForCreate(context.Background(), catID, domain.PriorityHigh, fixedClock().now); !errors.Is(err, readErr) {
		t.Errorf("policy-read error = %v, want %v propagated", err, readErr)
	}

	settings := &fakeSLASettingsStore{slaEnabled: true, slaEnabledErr: readErr}
	svc = application.NewSLAService(&fakeSLAStore{}, settings, fixedClock())
	if _, err := svc.ResolveForCreate(context.Background(), catID, domain.PriorityHigh, fixedClock().now); !errors.Is(err, readErr) {
		t.Errorf("settings-read error = %v, want %v propagated", err, readErr)
	}
}

// --- Configuration write use cases (issue #211): the admin-gated writes
// for the SLA settings and the default/category target matrices. Every
// use case checks the capability BEFORE any validation and BEFORE any
// store call, so a denied actor changes nothing.

// slaAdminActor returns an admin: it holds BOTH CapManageSettings and
// CapManageCategories (the external test package defines its own actors;
// settings_service_test.go's helpers live in the internal test package).
func slaAdminActor() domain.User { return domain.User{ID: 1, Name: "Admin", Role: domain.RoleAdmin} }

// slaAgentActor returns an agent: it holds neither SLA configuration
// capability.
func slaAgentActor() domain.User { return domain.User{ID: 2, Name: "Agent", Role: domain.RoleAgent} }

// slaValidMatrix returns a complete, valid four-priority matrix: exactly
// low, medium, high, critical — each once, every target at least 60s.
func slaValidMatrix() []domain.SLAPolicy {
	return []domain.SLAPolicy{
		{Priority: domain.PriorityLow, FirstResponseSeconds: 28800, ResolveSeconds: 259200},
		{Priority: domain.PriorityMedium, FirstResponseSeconds: 14400, ResolveSeconds: 86400},
		{Priority: domain.PriorityHigh, FirstResponseSeconds: 3600, ResolveSeconds: 28800},
		{Priority: domain.PriorityCritical, FirstResponseSeconds: 1800, ResolveSeconds: 14400},
	}
}

// TestSLAServiceConfigWritesDeniedTouchesNoStore proves each of the four
// write use cases rejects an actor without its capability and leaves the
// store untouched: SetEnabled/SetWarningPercent/SetDefaultTargets require
// CapManageSettings (admin/root), SetCategoryTargets requires
// CapManageCategories (admin+).
func TestSLAServiceConfigWritesDeniedTouchesNoStore(t *testing.T) {
	denied := slaAgentActor() // agent holds neither capability

	settings := &fakeSLASettingsStore{}
	sla := &fakeSLAStore{policies: slaServicePolicies(3)}
	svc := application.NewSLAService(sla, settings, fixedClock())

	if err := svc.SetEnabled(context.Background(), denied, true); err == nil {
		t.Fatal("SetEnabled(agent) succeeded, want ForbiddenError")
	}
	if settings.setSLAEnabledStateCalls != 0 {
		t.Errorf("denied SetEnabled touched the store (%d calls)", settings.setSLAEnabledStateCalls)
	}

	if err := svc.SetWarningPercent(context.Background(), denied, 50); err == nil {
		t.Fatal("SetWarningPercent(agent) succeeded, want ForbiddenError")
	}
	if settings.setSLAWarningPercentCalls != 0 {
		t.Errorf("denied SetWarningPercent touched the store (%d calls)", settings.setSLAWarningPercentCalls)
	}

	if err := svc.SetDefaultTargets(context.Background(), denied, slaValidMatrix()); err == nil {
		t.Fatal("SetDefaultTargets(agent) succeeded, want ForbiddenError")
	}
	if sla.upsertDefaultsCalls != 0 {
		t.Errorf("denied SetDefaultTargets touched the store (%d calls)", sla.upsertDefaultsCalls)
	}

	if err := svc.SetCategoryTargets(context.Background(), denied, 3, slaValidMatrix()); err == nil {
		t.Fatal("SetCategoryTargets(agent) succeeded, want ForbiddenError")
	}
	if sla.upsertCategoryTargetsCalls != 0 {
		t.Errorf("denied SetCategoryTargets touched the store (%d calls)", sla.upsertCategoryTargetsCalls)
	}

	// Nothing changed anywhere.
	if settings.slaEnabled || settings.slaWarningPercent != 0 || !settings.enabledAt.IsZero() {
		t.Errorf("settings mutated by denied actor: %+v", settings)
	}
	if len(sla.defaults) != 0 {
		t.Errorf("defaults mutated by denied actor: %+v", sla.defaults)
	}
}

// TestSLAServiceSetWarningPercentBoundaries proves the threshold accepts
// exactly 1..99: the projection treats a value outside (0,100) exclusive
// as an empty warning window, so 0 and 100 would silently disable the
// warning rather than configure it.
func TestSLAServiceSetWarningPercentBoundaries(t *testing.T) {
	for _, tc := range []struct {
		percent int
		wantErr bool
	}{
		{1, false},
		{99, false},
		{50, false},
		{0, true},
		{100, true},
		{-1, true},
		{101, true},
	} {
		settings := &fakeSLASettingsStore{}
		svc := application.NewSLAService(&fakeSLAStore{}, settings, fixedClock())

		err := svc.SetWarningPercent(context.Background(), slaAdminActor(), tc.percent)
		if tc.wantErr {
			var validation *domain.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("percent %d: err = %v, want ValidationError", tc.percent, err)
			}
			if validation.Field != "sla_warning_percent" {
				t.Errorf("percent %d: field = %q, want %q", tc.percent, validation.Field, "sla_warning_percent")
			}
			if settings.setSLAWarningPercentCalls != 0 {
				t.Errorf("percent %d: store touched on rejected value", tc.percent)
			}
			continue
		}
		if err != nil {
			t.Fatalf("percent %d: %v", tc.percent, err)
		}
		if settings.slaWarningPercent != tc.percent {
			t.Errorf("percent %d: store holds %d", tc.percent, settings.slaWarningPercent)
		}
	}
}

// TestSLAServiceMatrixValidationRejectsBrokenMatrices proves both matrix
// use cases reject a missing priority, a duplicated priority, an unknown
// priority, and a target below 60 seconds — each naming the offending
// priority in the ValidationError field — and touch no store.
func TestSLAServiceMatrixValidationRejectsBrokenMatrices(t *testing.T) {
	cases := []struct {
		name      string
		policies  []domain.SLAPolicy
		wantField string
	}{
		{
			name:      "missing priority",
			policies:  []domain.SLAPolicy{slaValidMatrix()[0], slaValidMatrix()[1], slaValidMatrix()[2]}, // no critical
			wantField: string(domain.PriorityCritical),
		},
		{
			name: "duplicated priority",
			policies: []domain.SLAPolicy{
				slaValidMatrix()[0], slaValidMatrix()[1], slaValidMatrix()[2],
				{Priority: domain.PriorityCritical, FirstResponseSeconds: 900, ResolveSeconds: 7200},
				{Priority: domain.PriorityCritical, FirstResponseSeconds: 1800, ResolveSeconds: 14400},
			},
			wantField: string(domain.PriorityCritical),
		},
		{
			name: "unknown priority",
			policies: append(slaValidMatrix(),
				domain.SLAPolicy{Priority: domain.Priority("urgent"), FirstResponseSeconds: 900, ResolveSeconds: 7200}),
			wantField: "urgent",
		},
		{
			name: "first response below 60",
			policies: []domain.SLAPolicy{
				slaValidMatrix()[0], slaValidMatrix()[1], slaValidMatrix()[2],
				{Priority: domain.PriorityCritical, FirstResponseSeconds: 59, ResolveSeconds: 14400},
			},
			wantField: string(domain.PriorityCritical),
		},
		{
			name: "resolve below 60",
			policies: []domain.SLAPolicy{
				slaValidMatrix()[0], slaValidMatrix()[1], slaValidMatrix()[2],
				{Priority: domain.PriorityCritical, FirstResponseSeconds: 1800, ResolveSeconds: 30},
			},
			wantField: string(domain.PriorityCritical),
		},
	}

	for _, tc := range cases {
		for _, useCase := range []string{"defaults", "category"} {
			sla := &fakeSLAStore{}
			svc := application.NewSLAService(sla, &fakeSLASettingsStore{}, fixedClock())

			var err error
			if useCase == "defaults" {
				err = svc.SetDefaultTargets(context.Background(), slaAdminActor(), tc.policies)
			} else {
				err = svc.SetCategoryTargets(context.Background(), slaAdminActor(), 3, tc.policies)
			}
			var validation *domain.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("%s/%s: err = %v, want ValidationError", useCase, tc.name, err)
			}
			if validation.Field != tc.wantField {
				t.Errorf("%s/%s: field = %q, want offending priority %q", useCase, tc.name, validation.Field, tc.wantField)
			}
			if sla.upsertDefaultsCalls != 0 || sla.upsertCategoryTargetsCalls != 0 {
				t.Errorf("%s/%s: store touched on rejected matrix", useCase, tc.name)
			}
		}
	}
}

// TestSLAServiceSetEnabledStampsActivationInstant proves SetEnabled(true)
// stamps the activation instant from ONE clock snapshot, while
// SetEnabled(false) leaves the instant alone: it records when SLA was
// FIRST switched on, and disabling does not erase that history.
func TestSLAServiceSetEnabledStampsActivationInstant(t *testing.T) {
	clock := fixedClock()
	settings := &fakeSLASettingsStore{}
	svc := application.NewSLAService(&fakeSLAStore{}, settings, clock)

	if err := svc.SetEnabled(context.Background(), slaAdminActor(), true); err != nil {
		t.Fatalf("SetEnabled(true): %v", err)
	}
	if !settings.slaEnabled {
		t.Error("slaEnabled = false, want true persisted")
	}
	if !settings.enabledAt.Equal(clock.now) {
		t.Errorf("enabledAt = %v, want the clock snapshot %v", settings.enabledAt, clock.now)
	}

	// Time moves on, SLA is switched off: the instant must NOT change.
	clock.Advance(48 * time.Hour)
	if err := svc.SetEnabled(context.Background(), slaAdminActor(), false); err != nil {
		t.Fatalf("SetEnabled(false): %v", err)
	}
	if settings.slaEnabled {
		t.Error("slaEnabled = true, want false persisted")
	}
	if !settings.enabledAt.Equal(fixedClock().now) {
		t.Errorf("enabledAt = %v, want the original activation instant %v (disabling never erases it)",
			settings.enabledAt, fixedClock().now)
	}
}

// TestSLAServiceConfigWritesPropagateStoreErrors proves a store failure
// propagates out of each write untouched: a silently swallowed write
// failure would report success for a configuration that never landed.
func TestSLAServiceConfigWritesPropagateStoreErrors(t *testing.T) {
	writeErr := errors.New("sla write failure")

	settings := &fakeSLASettingsStore{setSLAEnabledStateErr: writeErr}
	svc := application.NewSLAService(&fakeSLAStore{}, settings, fixedClock())
	if err := svc.SetEnabled(context.Background(), slaAdminActor(), true); !errors.Is(err, writeErr) {
		t.Errorf("SetEnabled error = %v, want %v", err, writeErr)
	}

	settings = &fakeSLASettingsStore{setSLAWarningPercentErr: writeErr}
	svc = application.NewSLAService(&fakeSLAStore{}, settings, fixedClock())
	if err := svc.SetWarningPercent(context.Background(), slaAdminActor(), 50); !errors.Is(err, writeErr) {
		t.Errorf("SetWarningPercent error = %v, want %v", err, writeErr)
	}

	sla := &fakeSLAStore{upsertDefaultsErr: writeErr}
	svc = application.NewSLAService(sla, &fakeSLASettingsStore{}, fixedClock())
	if err := svc.SetDefaultTargets(context.Background(), slaAdminActor(), slaValidMatrix()); !errors.Is(err, writeErr) {
		t.Errorf("SetDefaultTargets error = %v, want %v", err, writeErr)
	}

	sla = &fakeSLAStore{upsertCategoryTargetsErr: writeErr}
	svc = application.NewSLAService(sla, &fakeSLASettingsStore{}, fixedClock())
	if err := svc.SetCategoryTargets(context.Background(), slaAdminActor(), 3, slaValidMatrix()); !errors.Is(err, writeErr) {
		t.Errorf("SetCategoryTargets error = %v, want %v", err, writeErr)
	}
}

// TestSLAServiceConfigWritesHappyPath proves the accepted writes persist
// through the ports: enabled, percent, and both full matrices land.
func TestSLAServiceConfigWritesHappyPath(t *testing.T) {
	settings := &fakeSLASettingsStore{}
	sla := &fakeSLAStore{}
	svc := application.NewSLAService(sla, settings, fixedClock())

	if err := svc.SetEnabled(context.Background(), slaAdminActor(), true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if err := svc.SetWarningPercent(context.Background(), slaAdminActor(), 75); err != nil {
		t.Fatalf("SetWarningPercent: %v", err)
	}
	if err := svc.SetDefaultTargets(context.Background(), slaAdminActor(), slaValidMatrix()); err != nil {
		t.Fatalf("SetDefaultTargets: %v", err)
	}
	if err := svc.SetCategoryTargets(context.Background(), slaAdminActor(), 3, slaValidMatrix()); err != nil {
		t.Fatalf("SetCategoryTargets: %v", err)
	}

	if !settings.slaEnabled || settings.slaWarningPercent != 75 {
		t.Errorf("settings = (enabled=%v, percent=%d), want (true, 75)", settings.slaEnabled, settings.slaWarningPercent)
	}
	if len(sla.defaults) != 4 {
		t.Errorf("defaults rows = %d, want 4", len(sla.defaults))
	}
	if got := sla.policies[3]; len(got) != 4 {
		t.Errorf("category 3 matrix rows = %d, want 4", len(got))
	}
}

// TestSLAServiceSetGlobalConfiguration proves the whole panel is applied as
// ONE unit. A missing capability, a warning percent outside 1..99, and a
// partial matrix are each rejected BEFORE any store call; a valid panel
// reaches the store exactly once, because three sequential writes with
// caller-side compensation could not be atomic.
func TestSLAServiceSetGlobalConfiguration(t *testing.T) {
	matrix := slaValidMatrix()

	cases := []struct {
		name    string
		actor   domain.User
		enabled bool
		percent int
		matrix  []domain.SLAPolicy
		wantErr bool
	}{
		{name: "admin applies the panel", actor: slaAdminActor(), enabled: true, percent: 80, matrix: matrix},
		{name: "agent denied", actor: slaAgentActor(), enabled: true, percent: 80, matrix: matrix, wantErr: true},
		{name: "percent below range", actor: slaAdminActor(), percent: 0, matrix: matrix, wantErr: true},
		{name: "percent above range", actor: slaAdminActor(), percent: 100, matrix: matrix, wantErr: true},
		{name: "missing priority", actor: slaAdminActor(), percent: 80, matrix: matrix[:3], wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			settings := &fakeSLASettingsStore{}
			svc := application.NewSLAService(&fakeSLAStore{}, settings, fixedClock())

			err := svc.SetGlobalConfiguration(context.Background(), tc.actor, tc.enabled, tc.percent, tc.matrix)
			if tc.wantErr {
				if err == nil {
					t.Fatal("want a rejection")
				}
				if settings.setSLAConfigurationCalls != 0 {
					t.Errorf("rejected panel touched the store (%d calls)", settings.setSLAConfigurationCalls)
				}
				return
			}
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			if settings.setSLAConfigurationCalls != 1 {
				t.Errorf("store calls = %d, want 1", settings.setSLAConfigurationCalls)
			}
			if !settings.slaEnabled || settings.slaWarningPercent != 80 {
				t.Errorf("panel = (enabled %v, percent %d), want (true, 80)", settings.slaEnabled, settings.slaWarningPercent)
			}
			if len(settings.defaults) != len(matrix) {
				t.Errorf("defaults rows = %d, want %d", len(settings.defaults), len(matrix))
			}
		})
	}
}

// slaValidCalendar returns a valid non-default calendar: Monday-Friday
// plus Saturday morning, 08:00-12:00, UTC.
func slaValidCalendar() domain.SLACalendar {
	return domain.SLACalendar{
		WorkingDays: []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday},
		StartMinute: 8 * 60,
		EndMinute:   12 * 60,
		Location:    time.UTC,
	}
}

// TestSLAServiceSetSLACalendarValidatesBeforeStore proves the calendar
// write is validated BEFORE any store call: no working day at all, a
// non-positive window, a window crossing midnight, minutes outside
// 0..1439, and a missing timezone are each a ValidationError with the
// sla_calendar field and touch no store (issue #211).
func TestSLAServiceSetSLACalendarValidatesBeforeStore(t *testing.T) {
	cases := []struct {
		name     string
		calendar domain.SLACalendar
	}{
		{
			name:     "no working day at all",
			calendar: domain.SLACalendar{StartMinute: 540, EndMinute: 1080, Location: time.UTC},
		},
		{
			name: "zero-length window",
			calendar: domain.SLACalendar{
				WorkingDays: []time.Weekday{time.Monday},
				StartMinute: 540, EndMinute: 540, Location: time.UTC,
			},
		},
		{
			name: "window crossing midnight",
			calendar: domain.SLACalendar{
				WorkingDays: []time.Weekday{time.Monday},
				StartMinute: 1080, EndMinute: 540, Location: time.UTC,
			},
		},
		{
			name: "start minute below 0",
			calendar: domain.SLACalendar{
				WorkingDays: []time.Weekday{time.Monday},
				StartMinute: -1, EndMinute: 1080, Location: time.UTC,
			},
		},
		{
			name: "end minute above 1439",
			calendar: domain.SLACalendar{
				WorkingDays: []time.Weekday{time.Monday},
				StartMinute: 540, EndMinute: 1440, Location: time.UTC,
			},
		},
		{
			name: "no timezone",
			calendar: domain.SLACalendar{
				WorkingDays: []time.Weekday{time.Monday},
				StartMinute: 540, EndMinute: 1080, Location: nil,
			},
		},
		{
			name: "machine-local timezone is not portable",
			calendar: domain.SLACalendar{
				WorkingDays: []time.Weekday{time.Monday},
				StartMinute: 540, EndMinute: 1080, Location: time.Local,
			},
		},
		{
			name: "fixed-offset timezone cannot be read back by name",
			calendar: domain.SLACalendar{
				WorkingDays: []time.Weekday{time.Monday},
				StartMinute: 540, EndMinute: 1080, Location: time.FixedZone("UTC+3", 3*60*60),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			settings := &fakeSLASettingsStore{}
			svc := application.NewSLAService(&fakeSLAStore{}, settings, fixedClock())

			err := svc.SetSLACalendar(context.Background(), slaAdminActor(), tc.calendar)
			var validation *domain.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("err = %v, want ValidationError", err)
			}
			if validation.Field != "sla_calendar" {
				t.Errorf("validation field = %q, want %q", validation.Field, "sla_calendar")
			}
			if settings.setSLACalendarCalls != 0 {
				t.Errorf("rejected calendar touched the store (%d calls)", settings.setSLACalendarCalls)
			}
		})
	}
}

// TestSLAServiceSetSLACalendarAuthorizationAndHappyPath proves an agent is
// denied before any store call, an admin's valid calendar reaches the
// store, and a store failure propagates untouched.
func TestSLAServiceSetSLACalendarAuthorizationAndHappyPath(t *testing.T) {
	settings := &fakeSLASettingsStore{}
	denied := application.NewSLAService(&fakeSLAStore{}, settings, fixedClock())
	if err := denied.SetSLACalendar(context.Background(), slaAgentActor(), slaValidCalendar()); err == nil {
		t.Fatal("SetSLACalendar(agent) succeeded, want ForbiddenError")
	}
	if settings.setSLACalendarCalls != 0 {
		t.Errorf("denied SetSLACalendar touched the store (%d calls)", settings.setSLACalendarCalls)
	}

	if err := settings.SetSLACalendar(context.Background(), slaValidCalendar()); err != nil {
		t.Fatalf("seed calendar: %v", err)
	}
	svc := application.NewSLAService(&fakeSLAStore{}, settings, fixedClock())
	want := domain.SLACalendar{
		WorkingDays: []time.Weekday{time.Sunday},
		StartMinute: 600, EndMinute: 900, Location: time.UTC,
	}
	if err := svc.SetSLACalendar(context.Background(), slaAdminActor(), want); err != nil {
		t.Fatalf("SetSLACalendar: %v", err)
	}
	if len(settings.calendar.WorkingDays) != 1 || settings.calendar.WorkingDays[0] != time.Sunday {
		t.Errorf("stored calendar days = %v, want [Sunday]", settings.calendar.WorkingDays)
	}
	if settings.calendar.StartMinute != 600 || settings.calendar.EndMinute != 900 {
		t.Errorf("stored calendar window = (%d, %d), want (600, 900)", settings.calendar.StartMinute, settings.calendar.EndMinute)
	}

	writeErr := errors.New("calendar write failure")
	svc = application.NewSLAService(&fakeSLAStore{}, &fakeSLASettingsStore{setSLACalendarErr: writeErr}, fixedClock())
	if err := svc.SetSLACalendar(context.Background(), slaAdminActor(), slaValidCalendar()); !errors.Is(err, writeErr) {
		t.Errorf("SetSLACalendar error = %v, want %v", err, writeErr)
	}
}

// TestSLAServiceCalendarRead proves the read use case answers the port's
// calendar and propagates its error: the Settings screen reads the current
// calendar through the same port the freeze consumes.
func TestSLAServiceCalendarRead(t *testing.T) {
	want := slaValidCalendar()
	svc := application.NewSLAService(&fakeSLAStore{}, &fakeSLASettingsStore{calendar: want}, fixedClock())
	got, err := svc.Calendar(context.Background())
	if err != nil {
		t.Fatalf("Calendar: %v", err)
	}
	if len(got.WorkingDays) != len(want.WorkingDays) || got.StartMinute != want.StartMinute ||
		got.EndMinute != want.EndMinute || got.Location != want.Location {
		t.Errorf("Calendar = %+v, want %+v", got, want)
	}

	readErr := errors.New("calendar read failure")
	svc = application.NewSLAService(&fakeSLAStore{}, &fakeSLASettingsStore{calendarErr: readErr}, fixedClock())
	if _, err = svc.Calendar(context.Background()); !errors.Is(err, readErr) {
		t.Errorf("Calendar error = %v, want %v", err, readErr)
	}
}

// --- ForTickets (issue #211): the batch projection behind list rendering.

// TestSLAServiceForTicketsParityAndBatchReads proves the two ForTickets
// guarantees: the whole batch is judged against ONE clock snapshot, and
// the batch costs exactly ONE TicketSLAs and ONE MilestonesFor call for
// any number of tickets — never the ForTicket N+1. Every returned
// projection must equal the projection ForTicket produces for the same
// ticket at that same instant (parity), including the two tickets with no
// frozen commitment.
func TestSLAServiceForTicketsParityAndBatchReads(t *testing.T) {
	clock := &countingClock{fakeClock: fixedClock()}
	ids := []int64{1, 2, 3, 4}
	sla := &fakeSLAStore{
		frozen: map[int64]*domain.TicketSLA{
			1: slaServiceFrozen(clock.fakeClock),
			3: slaServiceFrozen(clock.fakeClock),
			// 2 and 4 have no frozen commitment at all.
		},
		milestones: map[int64]domain.SLAMilestones{
			1: {FirstResponseAt: ptr(clock.now.Add(10 * time.Minute))}, // met
			3: {FirstResolvedAt: ptr(clock.now.Add(2 * time.Hour))},    // met (7200s < 14400s)
		},
	}
	clock.Advance(14500 * time.Second) // past the resolve due: ticket 3's response state visible

	svc := application.NewSLAService(sla, &fakeSLASettingsStore{}, clock)
	got, err := svc.ForTickets(context.Background(), ids)
	if err != nil {
		t.Fatalf("ForTickets: %v", err)
	}

	// One snapshot and one batch read per method, total — asserted BEFORE
	// the parity loop, whose ForTicket calls read the clock themselves.
	if clock.nowCalls != 1 {
		t.Errorf("clock.Now() calls = %d, want 1 (one snapshot for the whole batch)", clock.nowCalls)
	}
	if sla.ticketSLAsCalls != 1 {
		t.Errorf("TicketSLAs calls = %d, want 1 (one read for the whole batch)", sla.ticketSLAsCalls)
	}
	if sla.milestonesForCalls != 1 {
		t.Errorf("MilestonesFor calls = %d, want 1 (one read for the whole batch)", sla.milestonesForCalls)
	}

	if len(got) != len(ids) {
		t.Fatalf("entries = %d, want %d (an entry for EVERY requested id)", len(got), len(ids))
	}
	for _, id := range ids {
		want, err := svc.ForTicket(context.Background(), id)
		if err != nil {
			t.Fatalf("ForTicket(%d): %v", id, err)
		}
		if !reflect.DeepEqual(got[id], want) {
			t.Errorf("ticket %d: ForTickets = %+v, want the ForTicket projection %+v at the same instant",
				id, got[id], want)
		}
	}
}

// TestSLAServiceForTicketsEmptyIDs proves empty ids answer an empty map
// with NO store call and NO clock read — an empty list page must not pay
// for anything.
func TestSLAServiceForTicketsEmptyIDs(t *testing.T) {
	clock := &countingClock{fakeClock: fixedClock()}
	sla := &fakeSLAStore{}
	svc := application.NewSLAService(sla, &fakeSLASettingsStore{}, clock)

	got, err := svc.ForTickets(context.Background(), nil)
	if err != nil {
		t.Fatalf("ForTickets: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("entries = %d, want 0 for empty ids", len(got))
	}
	if sla.ticketSLAsCalls != 0 || sla.milestonesForCalls != 0 {
		t.Errorf("store touched on empty ids (TicketSLAs=%d, MilestonesFor=%d), want zero calls",
			sla.ticketSLAsCalls, sla.milestonesForCalls)
	}
	if clock.nowCalls != 0 {
		t.Errorf("clock.Now() calls = %d, want 0 (no clock read on empty ids)", clock.nowCalls)
	}
}

// TestSLAServiceForTicketsNoFrozenEntry proves a ticket without a frozen
// commitment is PRESENT in the map with Overall SLANone — the "no SLA"
// state, never a zero-value projection or a missing entry.
func TestSLAServiceForTicketsNoFrozenEntry(t *testing.T) {
	sla := &fakeSLAStore{}
	svc := application.NewSLAService(sla, &fakeSLASettingsStore{}, fixedClock())

	got, err := svc.ForTickets(context.Background(), []int64{42})
	if err != nil {
		t.Fatalf("ForTickets: %v", err)
	}
	entry, ok := got[42]
	if !ok {
		t.Fatal("ticket 42 absent from the map, want an entry")
	}
	if entry.Frozen != nil {
		t.Errorf("Frozen = %+v, want nil", entry.Frozen)
	}
	if entry.Overall != domain.SLANone {
		t.Errorf("Overall = %q, want %q", entry.Overall, domain.SLANone)
	}
}
