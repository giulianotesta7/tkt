package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

type ticketMetricsNoopStore struct{}

func (s *ticketMetricsNoopStore) TicketMetrics(context.Context, TicketMetricsFilter) ([]TicketMetricsRecord, error) {
	return nil, nil
}

type ticketMetricsFixedClock struct{ now time.Time }

func (c ticketMetricsFixedClock) Now() time.Time { return c.now }

type ticketMetricsServiceStoreSpy struct {
	calls   int
	filter  TicketMetricsFilter
	records []TicketMetricsRecord
	err     error
}

func (s *ticketMetricsServiceStoreSpy) TicketMetrics(_ context.Context, f TicketMetricsFilter) ([]TicketMetricsRecord, error) {
	s.calls++
	s.filter = f
	return s.records, s.err
}

type ticketMetricsAdvancingClock struct {
	now   time.Time
	step  time.Duration
	reads int
}

func (c *ticketMetricsAdvancingClock) Now() time.Time {
	c.reads++
	now := c.now
	c.now = c.now.Add(c.step)
	return now
}

type ticketMetricsServiceEntry struct {
	name string
	call func(*TicketMetricsService, context.Context, domain.User) (TicketMetrics, error)
}

var ticketMetricsServiceEntries = []ticketMetricsServiceEntry{
	{"View", func(s *TicketMetricsService, ctx context.Context, actor domain.User) (TicketMetrics, error) {
		return s.View(ctx, actor, TicketMetricsFilter{})
	}},
	{"ViewCurrentWeek", func(s *TicketMetricsService, ctx context.Context, actor domain.User) (TicketMetrics, error) {
		return s.ViewCurrentWeek(ctx, actor)
	}},
}

func TestTicketMetricsServiceEntryPointsAuthorizeBeforeClockAndStore(t *testing.T) {
	roles := []struct {
		name  string
		role  domain.Role
		allow bool
	}{
		{"root", domain.RoleRoot, true}, {"admin", domain.RoleAdmin, true},
		{"agent", domain.RoleAgent, false}, {"user", domain.RoleUser, false},
		{"unknown", domain.Role("unknown"), false}, {"empty", domain.Role(""), false},
	}
	for _, entry := range ticketMetricsServiceEntries {
		for _, tc := range roles {
			t.Run(entry.name+"/"+tc.name, func(t *testing.T) {
				store := &ticketMetricsServiceStoreSpy{}
				clock := &ticketMetricsAdvancingClock{now: time.Date(2026, 3, 29, 23, 59, 59, 0, time.UTC)}
				_, err := entry.call(NewTicketMetricsService(store, clock), context.Background(), domain.User{Role: tc.role})
				if (err == nil) != tc.allow {
					t.Fatalf("error = %v, allowed = %t", err, tc.allow)
				}
				wantCalls := 0
				if tc.allow {
					wantCalls = 1
				} else {
					var forbidden *domain.ForbiddenError
					if !errors.As(err, &forbidden) {
						t.Fatalf("denied error = %T, want *domain.ForbiddenError", err)
					}
				}
				if clock.reads != wantCalls || store.calls != wantCalls {
					t.Fatalf("clock/store calls = %d/%d, want %d/%d", clock.reads, store.calls, wantCalls, wantCalls)
				}
			})
		}
	}
}

func TestTicketMetricsServiceDeniedEntryPointsAllowNilDependencies(t *testing.T) {
	for _, entry := range ticketMetricsServiceEntries {
		t.Run(entry.name, func(t *testing.T) {
			_, err := entry.call(NewTicketMetricsService(nil, nil), context.Background(), domain.User{Role: domain.RoleUser})
			var forbidden *domain.ForbiddenError
			if !errors.As(err, &forbidden) {
				t.Fatalf("denied nil-dependency error = %T, want *domain.ForbiddenError", err)
			}
		})
	}
}

func TestTicketMetricsServiceViewUsesOneSnapshotAndPropagatesErrors(t *testing.T) {
	now := time.Date(2026, 3, 29, 23, 59, 59, 0, time.UTC)
	store := &ticketMetricsServiceStoreSpy{records: []TicketMetricsRecord{{
		CreatedAt: time.Date(2026, 3, 27, 0, 0, 0, 0, time.UTC), CurrentState: domain.StateNew,
	}}}
	clock := &ticketMetricsAdvancingClock{now: now, step: 2 * time.Second}
	metrics, err := NewTicketMetricsService(store, clock).View(context.Background(), domain.User{Role: domain.RoleAdmin}, TicketMetricsFilter{})
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	wantEnd := time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC)
	// End is the exclusive storage boundary, so the inclusive last day 2026-03-29
	// is normalized to the next UTC midnight.
	wantExclusiveEnd := wantEnd.AddDate(0, 0, 1)
	if len(metrics.Ages) < 2 || clock.reads != 1 || store.calls != 1 || !store.filter.Start.Equal(wantEnd.AddDate(0, 0, -29)) || !store.filter.End.Equal(wantExclusiveEnd) || !metrics.Filter.End.Equal(wantExclusiveEnd) || metrics.Ages[0].Count != 1 || metrics.Ages[1].Count != 0 {
		t.Fatalf("clock/store/filter/ages = %d/%d/%s..%s/%+v", clock.reads, store.calls, store.filter.Start, store.filter.End, metrics.Ages)
	}

	deskID := int64(7)
	customStore := &ticketMetricsServiceStoreSpy{records: []TicketMetricsRecord{{
		CreatedAt: now, CurrentState: domain.StateNew, DeskID: &deskID, DeskName: "Support",
	}}}
	custom, err := NewTicketMetricsService(customStore, ticketMetricsFixedClock{now: now}).View(context.Background(), domain.User{Role: domain.RoleRoot}, TicketMetricsFilter{
		Start:      time.Date(2026, 3, 28, 15, 0, 0, 0, time.FixedZone("utc+3", 3*60*60)),
		End:        time.Date(2026, 3, 30, 1, 0, 0, 0, time.FixedZone("utc+3", 3*60*60)),
		WorkloadBy: "desk",
	})
	if err != nil || customStore.calls != 1 || len(custom.Workload) != 1 || custom.Pending != 1 || custom.Workload[0].Label != "Support" || custom.Workload[0].Count != 1 || custom.Workload[0].Unassigned ||
		!customStore.filter.Start.Equal(time.Date(2026, 3, 28, 0, 0, 0, 0, time.UTC)) || !customStore.filter.End.Equal(wantExclusiveEnd) || customStore.filter.WorkloadBy != "desk" {
		t.Fatalf("custom metrics/filter = %+v/%+v, err = %v", custom, customStore.filter, err)
	}

	storeErr := errors.New("store failed")
	failedStore := &ticketMetricsServiceStoreSpy{records: []TicketMetricsRecord{{CurrentState: domain.StateNew}}, err: storeErr}
	got, err := NewTicketMetricsService(failedStore, ticketMetricsFixedClock{now: now}).View(context.Background(), domain.User{Role: domain.RoleRoot}, TicketMetricsFilter{})
	if err != storeErr || failedStore.calls != 1 || got.Pending != 0 || len(got.Ages) != 0 || len(got.Workload) != 0 {
		t.Fatalf("store error = %v, calls = %d, metrics = %+v", err, failedStore.calls, got)
	}

	invalidStore := &ticketMetricsServiceStoreSpy{}
	invalidClock := &ticketMetricsAdvancingClock{now: now, step: time.Second}
	_, err = NewTicketMetricsService(invalidStore, invalidClock).View(context.Background(), domain.User{Role: domain.RoleAdmin}, TicketMetricsFilter{WorkloadBy: "invalid"})
	var validation *domain.ValidationError
	if !errors.As(err, &validation) || invalidClock.reads != 1 || invalidStore.calls != 0 {
		t.Fatalf("validation error/calls = %T/%d/%d", err, invalidClock.reads, invalidStore.calls)
	}
}

func TestTicketMetricsServiceViewCurrentWeekUsesOneUTCSnapshot(t *testing.T) {
	localMonday := time.Date(2026, 3, 30, 1, 59, 59, 0, time.FixedZone("utc+2", 2*60*60))
	clock := &ticketMetricsAdvancingClock{now: localMonday, step: 2 * time.Second}
	store := &ticketMetricsServiceStoreSpy{records: []TicketMetricsRecord{{
		CreatedAt: time.Date(2026, 3, 27, 0, 0, 0, 0, time.UTC), CurrentState: domain.StateNew,
	}}}
	metrics, err := NewTicketMetricsService(store, clock).ViewCurrentWeek(context.Background(), domain.User{Role: domain.RoleAdmin})
	if err != nil {
		t.Fatalf("ViewCurrentWeek: %v", err)
	}
	wantStart := time.Date(2026, 3, 23, 0, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC)
	if len(metrics.Ages) == 0 || clock.reads != 1 || store.calls != 1 || !store.filter.Start.Equal(wantStart) || !store.filter.End.Equal(wantEnd.AddDate(0, 0, 1)) || store.filter.WorkloadBy != "agent" || metrics.Ages[0].Count != 1 {
		t.Fatalf("clock/store/filter/ages = %d/%d/%+v/%+v", clock.reads, store.calls, store.filter, metrics.Ages)
	}
}

// NewTicketMetricsService wires the read port and the clock; with no
// operational methods yet the constructor contract is signature and non-nil
// wiring only.
func TestNewTicketMetricsServiceWiresStoreAndClock(t *testing.T) {
	svc := NewTicketMetricsService(&ticketMetricsNoopStore{}, ticketMetricsFixedClock{
		now: time.Date(2026, 3, 29, 23, 59, 59, 0, time.UTC),
	})
	if svc == nil {
		t.Fatal("NewTicketMetricsService returned nil")
	}
}

// The DTO shapes are the PR1 contract: constructing them by named field pins
// the key names later slices and templates rely on.
func TestTicketMetricsDTOShapes(t *testing.T) {
	agent := int64(7)
	zero := time.Unix(0, 0).UTC()
	m := TicketMetrics{
		Filter:     TicketMetricsFilter{Start: zero, End: zero},
		WorkloadBy: "agent",
		Weeks:      []TicketMetricsWeek{{Start: zero, Created: 1, Resolved: 2, Partial: true}},
		Ages:       []TicketMetricsBucket{{Label: "0–2 days", Count: 3}},
		Workload:   []TicketMetricsWorkload{{Label: "Unassigned", Count: 1, Unassigned: true}},
	}
	if m.WorkloadBy != "agent" || m.Weeks[0].Created != 1 || m.Weeks[0].Resolved != 2 ||
		!m.Weeks[0].Partial || m.Ages[0].Count != 3 || !m.Workload[0].Unassigned {
		t.Fatalf("DTO fields = %+v / %+v / %+v", m.Weeks[0], m.Ages[0], m.Workload[0])
	}
	record := TicketMetricsRecord{
		TicketID: 1, CreatedAt: zero, ResolvedAt: zero, CurrentState: domain.StateNew,
		DeskID: &agent, DeskName: "Support", AgentID: &agent, AgentName: "Ada",
		ResolutionWeek: zero,
	}
	if record.TicketID != 1 || record.DeskName != "Support" || record.AgentName != "Ada" {
		t.Fatalf("record DTO = %+v", record)
	}
}

// Empty dates default to the last 30 UTC calendar days derived from the one
// supplied now: End is the exclusive next UTC midnight, Start 29 days earlier.
func TestNormalizeTicketMetricsFilterDefaultsToLast30UTCDays(t *testing.T) {
	tests := []struct {
		name      string
		now       time.Time
		wantStart time.Time
		wantEnd   time.Time
	}{
		{
			name:      "late UTC evening",
			now:       time.Date(2026, 3, 29, 23, 59, 59, 0, time.UTC),
			wantStart: time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC),
		},
		{
			name:      "non-UTC instant rolls back to the UTC calendar day",
			now:       time.Date(2026, 3, 31, 1, 30, 0, 0, time.FixedZone("utc+2", 2*60*60)),
			wantStart: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := normalizeTicketMetricsFilter(TicketMetricsFilter{WorkloadBy: "agent"}, tt.now)
			if err != nil {
				t.Fatalf("normalizeTicketMetricsFilter: %v", err)
			}
			if !f.Start.Equal(tt.wantStart) || !f.End.Equal(tt.wantEnd) {
				t.Fatalf("default period = %s .. %s, want %s .. %s", f.Start, f.End, tt.wantStart, tt.wantEnd)
			}
		})
	}
}

// Explicit dates are normalized to UTC calendar days: midnight UTC, whatever
// offset or wall time the caller supplied; End is the exclusive next midnight.
func TestNormalizeTicketMetricsFilterNormalizesExplicitDatesToUTCCalendarDays(t *testing.T) {
	start := time.Date(2026, 3, 10, 15, 0, 0, 0, time.FixedZone("utc-5", -5*60*60))
	end := time.Date(2026, 3, 20, 2, 0, 0, 0, time.FixedZone("utc+3", 3*60*60))
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)

	f, err := normalizeTicketMetricsFilter(TicketMetricsFilter{Start: start, End: end, WorkloadBy: "desk"}, now)
	if err != nil {
		t.Fatalf("normalizeTicketMetricsFilter: %v", err)
	}
	wantStart := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC)
	if !f.Start.Equal(wantStart) || !f.End.Equal(wantEnd) {
		t.Fatalf("normalized period = %s .. %s, want %s .. %s", f.Start, f.End, wantStart, wantEnd)
	}
	if f.Start.Location() != time.UTC || f.End.Location() != time.UTC {
		t.Fatalf("normalized dates must be UTC, got %s and %s", f.Start.Location(), f.End.Location())
	}
}

func wantMetricsDateError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	ve, ok := err.(*domain.ValidationError)
	if !ok {
		t.Fatalf("want *domain.ValidationError, got %T", err)
	}
	if ve.Field != "metrics_date" || ve.Message != want {
		t.Fatalf("validation error = (%q, %q), want (metrics_date, %q)", ve.Field, ve.Message, want)
	}
}

// Supplying only one of the two dates is invalid; both errors share the
// metrics_date field and the choose-both message.
func TestNormalizeTicketMetricsFilterRequiresBothDates(t *testing.T) {
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	start := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)

	_, err := normalizeTicketMetricsFilter(TicketMetricsFilter{Start: start}, now)
	wantMetricsDateError(t, err, "choose both metrics dates")
	_, err = normalizeTicketMetricsFilter(TicketMetricsFilter{End: start}, now)
	wantMetricsDateError(t, err, "choose both metrics dates")
}

// After UTC normalization the end date must not fall before the start date.
func TestNormalizeTicketMetricsFilterRejectsReversedRange(t *testing.T) {
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	filter := TicketMetricsFilter{
		Start: time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC),
	}
	_, err := normalizeTicketMetricsFilter(filter, now)
	wantMetricsDateError(t, err, "metrics end date must not be before the start date")
}

// Empty WorkloadBy defaults to agent; only agent and desk are accepted.
func TestNormalizeTicketMetricsFilterWorkloadBy(t *testing.T) {
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	period := TicketMetricsFilter{
		Start: time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC),
	}

	f, err := normalizeTicketMetricsFilter(period, now)
	if err != nil {
		t.Fatalf("normalizeTicketMetricsFilter: %v", err)
	}
	if f.WorkloadBy != "agent" {
		t.Fatalf("default WorkloadBy = %q, want agent", f.WorkloadBy)
	}
	for _, group := range []string{"agent", "desk"} {
		f, err := normalizeTicketMetricsFilter(TicketMetricsFilter{WorkloadBy: group}, now)
		if err != nil {
			t.Fatalf("WorkloadBy %q: %v", group, err)
		}
		if f.WorkloadBy != group {
			t.Fatalf("WorkloadBy = %q, want %q", f.WorkloadBy, group)
		}
	}

	_, err = normalizeTicketMetricsFilter(TicketMetricsFilter{WorkloadBy: "desks"}, now)
	if err == nil {
		t.Fatal("unknown WorkloadBy must be rejected")
	}
	ve, ok := err.(*domain.ValidationError)
	if !ok {
		t.Fatalf("want *domain.ValidationError, got %T", err)
	}
	if ve.Field != "metrics_group" || ve.Message != "metrics workload group must be agent or desk" {
		t.Fatalf("validation error = (%q, %q), want (metrics_group, metrics workload group must be agent or desk)", ve.Field, ve.Message)
	}
}

// dateUTC truncates any instant to midnight of its UTC calendar date; a
// non-UTC instant that already crossed into the next local day still maps to
// the UTC day it belongs to.
func TestDateUTC(t *testing.T) {
	tests := []struct {
		name string
		in   time.Time
		want time.Time
	}{
		{
			name: "UTC midnight stays midnight",
			in:   time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC),
			want: time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "UTC midday truncates",
			in:   time.Date(2026, 3, 30, 15, 45, 30, 123, time.UTC),
			want: time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "non-UTC instant maps to its UTC calendar day",
			in:   time.Date(2026, 3, 31, 1, 30, 0, 0, time.FixedZone("utc+2", 2*60*60)),
			want: time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dateUTC(tt.in)
			if !got.Equal(tt.want) {
				t.Fatalf("dateUTC(%s) = %s, want %s", tt.in, got, tt.want)
			}
			if got.Location() != time.UTC {
				t.Fatalf("dateUTC must return time.UTC, got %s", got.Location())
			}
		})
	}
}

// monday returns midnight UTC of the Monday of the given instant's UTC
// calendar week, including Sunday rollovers and year boundaries.
func TestMonday(t *testing.T) {
	tests := []struct {
		name string
		in   time.Time
		want time.Time
	}{
		{
			name: "monday maps to itself",
			in:   time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC),
			want: time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "sunday maps back to monday",
			in:   time.Date(2026, 4, 5, 23, 0, 0, 0, time.UTC),
			want: time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "year boundary maps into the previous year",
			in:   time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC),
			want: time.Date(2025, 12, 29, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "non-UTC instant uses its UTC calendar week",
			in:   time.Date(2026, 3, 31, 1, 30, 0, 0, time.FixedZone("utc+2", 2*60*60)),
			want: time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := monday(tt.in)
			if !got.Equal(tt.want) {
				t.Fatalf("monday(%s) = %s, want %s", tt.in, got, tt.want)
			}
			if got.Location() != time.UTC {
				t.Fatalf("monday must return time.UTC, got %s", got.Location())
			}
		})
	}
}

// Weekly volume must use Monday UTC buckets in chronological order, count
// created tickets inside the half-open [Start, End+1day) window, and mark
// edge buckets the interval does not fully cover as Partial.
func TestBuildTicketMetricsWeeklyCreatedResolvedAndPartialBuckets(t *testing.T) {
	start := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC) // Monday
	end := time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC)  // Monday
	now := end.AddDate(0, 0, 1).Add(time.Hour)
	alice := int64(4)
	records := []TicketMetricsRecord{
		{TicketID: 1, CreatedAt: start.Add(12 * time.Hour), ResolvedAt: start.AddDate(0, 0, 1), ResolutionWeek: start.AddDate(0, 0, 1), CurrentState: domain.StateResolved},
		{TicketID: 2, CreatedAt: start.AddDate(0, 0, 7), CurrentState: domain.StateNew, AgentID: &alice, AgentName: "Alice"},
		{TicketID: 3, CreatedAt: end.Add(23 * time.Hour), ResolvedAt: end.AddDate(0, 0, 1).Add(time.Hour), ResolutionWeek: end.AddDate(0, 0, 1).Add(time.Hour), CurrentState: domain.StateResolved},
		{TicketID: 4, CreatedAt: start.Add(-time.Second), CurrentState: domain.StateResolved},
		{TicketID: 5, CreatedAt: end.AddDate(0, 0, 1), CurrentState: domain.StateNew, AgentID: &alice, AgentName: "Alice"},
		{TicketID: 6, CreatedAt: end.Add(23*time.Hour + 59*time.Minute), CurrentState: domain.StateNew, AgentID: &alice, AgentName: "Alice"},
	}

	metrics := buildTicketMetrics(records, TicketMetricsFilter{Start: start, End: end, WorkloadBy: "agent"}, now)

	if metrics.Filter.Start != start || metrics.Filter.End != end || metrics.WorkloadBy != "agent" {
		t.Fatalf("filter/workload = %+v/%q, want supplied filter/agent", metrics.Filter, metrics.WorkloadBy)
	}
	if metrics.Created != 4 || metrics.Resolved != 2 {
		t.Fatalf("created/resolved = %d/%d, want 4/2 (half-open creation window)", metrics.Created, metrics.Resolved)
	}
	if metrics.Pending != 3 {
		t.Fatalf("pending = %d, want 3", metrics.Pending)
	}
	wantStarts := []time.Time{
		time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC),
	}
	wantPartial := []bool{false, false, true}
	wantCreated := []int{1, 1, 2}
	wantResolved := []int{1, 0, 1}
	if len(metrics.Weeks) != len(wantStarts) {
		t.Fatalf("weeks = %d, want %d", len(metrics.Weeks), len(wantStarts))
	}
	for i, w := range metrics.Weeks {
		if !w.Start.Equal(wantStarts[i]) || w.Partial != wantPartial[i] || w.Created != wantCreated[i] || w.Resolved != wantResolved[i] {
			t.Fatalf("week %d = %+v, want start %s partial %v created %d resolved %d", i, w, wantStarts[i], wantPartial[i], wantCreated[i], wantResolved[i])
		}
	}
	if len(metrics.Workload) != 1 || metrics.Workload[0].Label != "Alice" || metrics.Workload[0].Count != 3 {
		t.Fatalf("workload = %+v, want one Alice row with three pending tickets", metrics.Workload)
	}
	leftPartial := buildTicketMetrics(nil, TicketMetricsFilter{Start: start.AddDate(0, 0, 1), End: end, WorkloadBy: "agent"}, now)
	if len(leftPartial.Weeks) == 0 || !leftPartial.Weeks[0].Partial {
		t.Fatalf("left-edge week = %+v, want partial when period starts after Monday", leftPartial.Weeks)
	}
}

// Backlog ages use completed elapsed days with exact bucket transitions, and
// only new/in_progress tickets contribute to pending, ages, and unassigned.
func TestBuildTicketMetricsPendingAgesCountOnlyPendingStates(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	age := func(days int, extra time.Duration) time.Time {
		return now.Add(-time.Duration(days)*24*time.Hour - extra)
	}
	alice := int64(4)
	records := []TicketMetricsRecord{
		{TicketID: 1, CreatedAt: age(2, 23*time.Hour+59*time.Minute), CurrentState: domain.StateNew},
		{TicketID: 2, CreatedAt: age(3, 0), CurrentState: domain.StateInProgress, AgentID: &alice},
		{TicketID: 3, CreatedAt: age(7, 23*time.Hour+59*time.Minute), CurrentState: domain.StateNew},
		{TicketID: 4, CreatedAt: age(8, 0), CurrentState: domain.StateNew, AgentID: &alice},
		{TicketID: 5, CreatedAt: age(14, 23*time.Hour+59*time.Minute), CurrentState: domain.StateNew},
		{TicketID: 6, CreatedAt: age(15, 0), CurrentState: domain.StateNew, AgentID: &alice},
		{TicketID: 7, CreatedAt: age(1, 0), CurrentState: domain.StateResolved},
		{TicketID: 8, CreatedAt: age(1, 0), CurrentState: domain.StateClosed},
	}

	metrics := buildTicketMetrics(records, TicketMetricsFilter{Start: now, End: now, WorkloadBy: "agent"}, now)

	wantAges := []TicketMetricsBucket{
		{Label: "0–2 days", Count: 1},
		{Label: "3–7 days", Count: 2},
		{Label: "8–14 days", Count: 2},
		{Label: ">14 days", Count: 1},
	}
	for i, want := range wantAges {
		if got := metrics.Ages[i]; got.Label != want.Label || got.Count != want.Count {
			t.Fatalf("age bucket %d = %+v, want %+v", i, got, want)
		}
	}
	if metrics.Pending != 6 || metrics.Unassigned != 3 {
		t.Fatalf("pending/unassigned = %d/%d, want 6/3 (only new and in_progress count)", metrics.Pending, metrics.Unassigned)
	}
}

// Workload rows keep stable identities behind labels: same-label agents order
// deterministically by identity, a named agent called Unassigned stays
// separate from the genuinely nil-agent identity, and only the nil-agent
// identity carries the Unassigned flag. Desk grouping falls back to the
// No desk label without ever flagging a row.
func TestBuildTicketMetricsWorkloadIdentityOrderingAndDisambiguation(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	agents := []int64{3, 4, 5, 10, 20}
	agentNamedUnassigned, agentAlice, agentUnknown, agentTen, agentTwenty := agents[0], agents[1], agents[2], agents[3], agents[4]
	deskGeneral, deskNoName := int64(10), int64(20)
	records := []TicketMetricsRecord{
		{TicketID: 1, CreatedAt: now, CurrentState: domain.StateNew},
		{TicketID: 2, CreatedAt: now, CurrentState: domain.StateNew},
		{TicketID: 3, CreatedAt: now, CurrentState: domain.StateNew, AgentID: &agentNamedUnassigned, AgentName: "Unassigned"},
		{TicketID: 4, CreatedAt: now, CurrentState: domain.StateNew, AgentID: &agentAlice, AgentName: "Alice", DeskID: &deskGeneral, DeskName: "General"},
		{TicketID: 5, CreatedAt: now, CurrentState: domain.StateNew, AgentID: &agentTen, AgentName: "Same name"},
		{TicketID: 6, CreatedAt: now, CurrentState: domain.StateNew, AgentID: &agentTwenty, AgentName: "Same name"},
		{TicketID: 7, CreatedAt: now, CurrentState: domain.StateNew, AgentID: &agentTwenty, AgentName: "Same name", DeskID: &deskNoName},
		{TicketID: 8, CreatedAt: now, CurrentState: domain.StateNew, AgentID: &agentUnknown},
	}
	wantAgent := []struct {
		label      string
		count      int
		unassigned bool
	}{
		{"Alice", 1, false},
		{"Same name", 1, false},
		{"Same name", 2, false},
		{"Unassigned", 1, false},
		{"Unassigned (no agent)", 2, true},
		{"Unknown", 1, false},
	}

	agentRows := buildTicketMetrics(records, TicketMetricsFilter{Start: now, End: now, WorkloadBy: "agent"}, now).Workload
	if len(agentRows) != len(wantAgent) {
		t.Fatalf("agent workload rows = %d, want %d", len(agentRows), len(wantAgent))
	}
	for i, want := range wantAgent {
		if got := agentRows[i]; got.Label != want.label || got.Count != want.count || got.Unassigned != want.unassigned {
			t.Fatalf("agent workload row %d = %+v, want %q count %d unassigned %v", i, got, want.label, want.count, want.unassigned)
		}
	}

	desks := buildTicketMetrics(records, TicketMetricsFilter{Start: now, End: now, WorkloadBy: "desk"}, now)
	wantDesk := []struct {
		label string
		count int
	}{
		{"General", 1},
		{"No desk", 1},
		{"No desk", 6},
	}
	if len(desks.Workload) != len(wantDesk) {
		t.Fatalf("desk workload rows = %d, want %d", len(desks.Workload), len(wantDesk))
	}
	for i, want := range wantDesk {
		if got := desks.Workload[i]; got.Label != want.label || got.Count != want.count || got.Unassigned {
			t.Fatalf("desk workload row %d = %+v, want %q count %d unflagged", i, got, want.label, want.count)
		}
	}

	for run := 0; run < 50; run++ {
		again := buildTicketMetrics(records, TicketMetricsFilter{Start: now, End: now, WorkloadBy: "agent"}, now).Workload
		for i, want := range wantAgent {
			if again[i].Label != want.label || again[i].Count != want.count {
				t.Fatalf("run %d workload row %d unstable: %+v", run, i, again[i])
			}
		}
	}
}

func TestBuildTicketMetricsDurationStatistics(t *testing.T) {
	base := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	records := []TicketMetricsRecord{
		{CreatedAt: base, ResolvedAt: base.Add(66 * time.Hour), CurrentState: domain.StateClosed},
		{CreatedAt: base, ResolvedAt: base.Add(time.Hour), CurrentState: domain.StateClosed},
		{CreatedAt: base, ResolvedAt: base.Add(99 * time.Hour), CurrentState: domain.StateClosed},
		{CreatedAt: base, ResolvedAt: base.Add(30 * time.Hour), CurrentState: domain.StateClosed},
		{ResolvedAt: base, CurrentState: domain.StateClosed},
		{CreatedAt: base, ResolvedAt: base.Add(-time.Hour), CurrentState: domain.StateClosed},
	}

	metrics := buildTicketMetrics(records, TicketMetricsFilter{Start: base, End: base.AddDate(0, 0, 5), WorkloadBy: "agent"}, base)
	if metrics.Resolved != 6 || metrics.Excluded != 2 || metrics.Samples != 4 {
		t.Fatalf("resolved/excluded/samples = %d/%d/%d, want 6/2/4", metrics.Resolved, metrics.Excluded, metrics.Samples)
	}
	if metrics.MeanDuration != 49*time.Hour || metrics.Median != 48*time.Hour || metrics.P90 != 99*time.Hour {
		t.Fatalf("mean/median/p90 = %v/%v/%v, want 49h/48h/99h", metrics.MeanDuration, metrics.Median, metrics.P90)
	}
}

func TestBuildTicketMetricsOddDurationStatistics(t *testing.T) {
	base := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	days := []int{11, 1, 10, 2, 9, 3, 8, 4, 7, 5, 6}
	records := make([]TicketMetricsRecord, len(days))
	for i, d := range days {
		records[i] = TicketMetricsRecord{CreatedAt: base, ResolvedAt: base.Add(time.Duration(d) * 24 * time.Hour), CurrentState: domain.StateClosed}
	}

	metrics := buildTicketMetrics(records, TicketMetricsFilter{Start: base, End: base.AddDate(0, 0, 11), WorkloadBy: "agent"}, base)
	if metrics.MeanDuration != 6*24*time.Hour || metrics.Median != 6*24*time.Hour || metrics.P90 != 10*24*time.Hour {
		t.Fatalf("mean/median/p90 = %v/%v/%v, want 6d/6d/10d", metrics.MeanDuration, metrics.Median, metrics.P90)
	}
}

func TestBuildTicketMetricsDurationStatisticsAvoidOverflow(t *testing.T) {
	base := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	const count = 10_000
	records := make([]TicketMetricsRecord, count)
	for i := range records {
		records[i] = TicketMetricsRecord{CreatedAt: base, ResolvedAt: base.Add(11 * 24 * time.Hour), CurrentState: domain.StateClosed}
	}
	metrics := buildTicketMetrics(records, TicketMetricsFilter{Start: base, End: base.AddDate(0, 0, 11), WorkloadBy: "agent"}, base)
	if metrics.MeanDuration != 11*24*time.Hour {
		t.Fatalf("large-sample mean = %v, want 11d", metrics.MeanDuration)
	}

	max := time.Duration(1<<63 - 1)
	lower, upper := max-3*time.Hour, max-time.Hour
	extreme := buildTicketMetrics([]TicketMetricsRecord{
		{CreatedAt: base, ResolvedAt: base.Add(upper), CurrentState: domain.StateClosed},
		{CreatedAt: base, ResolvedAt: base.Add(lower), CurrentState: domain.StateClosed},
	}, TicketMetricsFilter{Start: base, End: base, WorkloadBy: "agent"}, base)
	want := max - 2*time.Hour
	if extreme.MeanDuration != want || extreme.Median != want || extreme.P90 != upper {
		t.Fatalf("extreme mean/median/p90 = %v/%v/%v, want %v/%v/%v", extreme.MeanDuration, extreme.Median, extreme.P90, want, want, upper)
	}
}

func TestResolutionHistogramUsesIndividualDurationsAndBoundaries(t *testing.T) {
	durations := []time.Duration{0, 23 * time.Hour, 5 * 24 * time.Hour, 10 * 24 * time.Hour, 30 * 24 * time.Hour}
	bins := resolutionHistogram(durations)
	if len(bins) != 7 {
		t.Fatalf("bins = %d, want 7", len(bins))
	}
	for i, want := range []int{2, 1, 1, 0, 0, 0, 1} {
		if bins[i].Count != want {
			t.Fatalf("bin %d = %+v, want count %d", i, bins[i], want)
		}
		if bins[i].UpperDays-bins[i].LowerDays != 5 || (i > 0 && bins[i].LowerDays != bins[i-1].UpperDays) {
			t.Fatalf("bin %d is not a contiguous five-day interval: %+v", i, bins[i])
		}
	}
	if bins[0].Label != "0–<5 days" || bins[6].Label != "30–<35 days" {
		t.Fatalf("edge labels = %q / %q", bins[0].Label, bins[6].Label)
	}
}

func TestResolutionHistogramSelectsWholeDayWidths(t *testing.T) {
	maximum := time.Duration(1<<63 - 1)
	tests := []struct {
		name       string
		durations  []time.Duration
		wantWidth  float64
		wantFirst  string
		wantSecond string
		wantLast   string
		wantCounts []int
	}{
		{"two days", []time.Duration{0, 2 * 24 * time.Hour, 12 * 24 * time.Hour}, 2, "0–<2 days", "2–<4 days", "12–<14 days", []int{1, 1, 0, 0, 0, 0, 1}},
		{"ten days", []time.Duration{5 * 24 * time.Hour, 10 * 24 * time.Hour, 60 * 24 * time.Hour}, 10, "0–<10 days", "10–<20 days", "60–<70 days", []int{1, 1, 0, 0, 0, 0, 1}},
		{"subday", []time.Duration{time.Hour, 23 * time.Hour}, 1, "0–<1 days", "", "0–<1 days", []int{2}},
		{"maximum", []time.Duration{0, maximum}, 20000, "0–<20000 days", "20000–<40000 days", "100000–<120000 days", []int{1, 0, 0, 0, 0, 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bins := resolutionHistogram(tt.durations)
			if len(bins) != len(tt.wantCounts) {
				t.Fatalf("bins = %d, want %d", len(bins), len(tt.wantCounts))
			}
			if bins[0].Label != tt.wantFirst || bins[len(bins)-1].Label != tt.wantLast || (tt.wantSecond != "" && bins[1].Label != tt.wantSecond) {
				t.Fatalf("labels = %+v, want first/second/last %q/%q/%q", bins, tt.wantFirst, tt.wantSecond, tt.wantLast)
			}
			for i, bin := range bins {
				if bin.Count != tt.wantCounts[i] {
					t.Fatalf("bin %d count = %d, want %d", i, bin.Count, tt.wantCounts[i])
				}
				if bin.LowerDays != float64(i)*tt.wantWidth || bin.UpperDays != float64(i+1)*tt.wantWidth {
					t.Fatalf("bin %d bounds = %v–%v, want %v–%v", i, bin.LowerDays, bin.UpperDays, float64(i)*tt.wantWidth, float64(i+1)*tt.wantWidth)
				}
			}
		})
	}
}

func TestBuildTicketMetricsDurationStatisticsWithoutAndWithOneSample(t *testing.T) {
	base := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	filter := TicketMetricsFilter{Start: base, End: base, WorkloadBy: "agent"}
	empty := buildTicketMetrics([]TicketMetricsRecord{{ResolvedAt: base, CurrentState: domain.StateClosed}}, filter, base)
	if empty.Resolved != 1 || empty.Excluded != 1 || empty.Samples != 0 || len(empty.Histogram) != 0 || empty.MeanDuration != 0 || empty.Median != 0 || empty.P90 != 0 {
		t.Fatalf("empty duration statistics = %+v", empty)
	}
	one := buildTicketMetrics([]TicketMetricsRecord{{CreatedAt: base, ResolvedAt: base.Add(25 * time.Hour), CurrentState: domain.StateClosed}}, filter, base)
	if one.Samples != 1 || one.MeanDuration != 25*time.Hour || one.Median != 25*time.Hour || one.P90 != 25*time.Hour {
		t.Fatalf("one-sample statistics = %+v", one)
	}
	if len(one.Histogram) != 2 || one.Histogram[1].Count != 1 {
		t.Fatalf("one-sample histogram = %+v", one.Histogram)
	}
}
