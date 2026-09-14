package application

import (
	"context"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// TicketMetricsFilter is independent from the ticket-list filters. Dates are UTC
// calendar dates; End is inclusive at the HTTP boundary and exclusive in storage.
type TicketMetricsFilter struct {
	Start, End time.Time
	DeskID     *int64
	AgentID    *int64
	WorkloadBy string // agent (default) or desk
}

// TicketMetricsRecord is the single-row projection returned by the metrics read
// port. Attribution is current assignment and category desk, never historical.
type TicketMetricsRecord struct {
	TicketID       int64
	CreatedAt      time.Time
	ResolvedAt     time.Time
	CurrentState   domain.State
	DeskID         *int64
	DeskName       string
	AgentID        *int64
	AgentName      string
	ResolutionWeek time.Time
}

// TicketMetricsStore performs one consistent, non-paginated metrics read.
type TicketMetricsStore interface {
	TicketMetrics(context.Context, TicketMetricsFilter) ([]TicketMetricsRecord, error)
}

type TicketMetricsService struct {
	store TicketMetricsStore
	clock domain.Clock
}

func NewTicketMetricsService(store TicketMetricsStore, clock domain.Clock) *TicketMetricsService {
	return &TicketMetricsService{store: store, clock: clock}
}

type TicketMetrics struct {
	Filter       TicketMetricsFilter
	Pending      int
	Unassigned   int
	Created      int
	Resolved     int
	MeanDuration time.Duration
	Samples      int
	Excluded     int
	Weeks        []TicketMetricsWeek
	Ages         []TicketMetricsBucket
	Workload     []TicketMetricsWorkload
	WorkloadBy   string
	Histogram    []TicketMetricsBucket
	Median       time.Duration
	P90          time.Duration
}

type TicketMetricsWeek struct {
	Start             time.Time
	Created, Resolved int
	// Partial marks a Monday bucket the selected interval does not fully
	// cover: the presentation can style edge weeks instead of implying a
	// complete Monday-Sunday count.
	Partial bool
}
type TicketMetricsBucket struct {
	Label string
	Count int
	// LowerDays and UpperDays are the numeric half-open bounds in days for
	// the equal-width resolution histogram. Age buckets are nonuniform and
	// keep them at zero rather than reparsing Label.
	LowerDays float64
	UpperDays float64
}
type TicketMetricsWorkload struct {
	Label string
	Count int
	// Unassigned marks the genuinely unassigned identity (nil current
	// agent during agent grouping) so presentation can style it neutrally
	// without inferring from the label string.
	Unassigned bool
}

// normalizeTicketMetricsFilter turns a filter into the canonical UTC period the
// read port receives: empty dates default to the last 30 UTC calendar days
// derived from the caller's single now snapshot, explicit dates collapse to
// UTC midnight of their calendar day, and the workload grouping key is
// validated before any read happens.
func normalizeTicketMetricsFilter(f TicketMetricsFilter, now time.Time) (TicketMetricsFilter, error) {
	today := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	if f.Start.IsZero() && f.End.IsZero() {
		f.End = today
		f.Start = today.AddDate(0, 0, -29)
	} else if f.Start.IsZero() || f.End.IsZero() {
		return TicketMetricsFilter{}, &domain.ValidationError{Field: "metrics_date", Message: "choose both metrics dates"}
	}
	f.Start = dateUTC(f.Start)
	f.End = dateUTC(f.End)
	if f.End.Before(f.Start) {
		return TicketMetricsFilter{}, &domain.ValidationError{Field: "metrics_date", Message: "metrics end date must not be before the start date"}
	}
	if f.WorkloadBy == "" {
		f.WorkloadBy = "agent"
	}
	if f.WorkloadBy != "agent" && f.WorkloadBy != "desk" {
		return TicketMetricsFilter{}, &domain.ValidationError{Field: "metrics_group", Message: "metrics workload group must be agent or desk"}
	}
	return f, nil
}

func dateUTC(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}
func monday(t time.Time) time.Time {
	t = dateUTC(t)
	d := (int(t.Weekday()) + 6) % 7
	return t.AddDate(0, 0, -d)
}
