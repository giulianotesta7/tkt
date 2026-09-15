package application

import (
	"context"
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
