package application

import (
	"context"
	"sort"
	"strconv"
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
// UTC midnight of their calendar day (End is the exclusive next midnight), and
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
	f.End = dateUTC(f.End).AddDate(0, 0, 1)
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

func workloadIdentity(r TicketMetricsRecord, group string) (string, string) {
	if group == "desk" {
		if r.DeskID == nil {
			return "desk:unassigned", "No desk"
		}
		label := r.DeskName
		if label == "" {
			label = "No desk"
		}
		return "desk:" + strconv.FormatInt(*r.DeskID, 10), label
	}
	if r.AgentID == nil {
		return "agent:unassigned", "Unassigned"
	}
	label := r.AgentName
	if label == "" {
		label = "Unknown"
	}
	return "agent:" + strconv.FormatInt(*r.AgentID, 10), label
}

func dateUTC(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}
func monday(t time.Time) time.Time {
	// Normalize to midnight UTC so every instant in a week shares one bucket key.
	t = dateUTC(t)
	d := (int(t.Weekday()) + 6) % 7
	return t.AddDate(0, 0, -d)
}

// buildTicketMetrics aggregates one consistent record read into the weekly
// volume, current backlog, and workload projection. It is a pure function:
// the period comes from the already-normalized filter and every elapsed-day
// calculation uses the single now snapshot the caller captured.
func buildTicketMetrics(records []TicketMetricsRecord, f TicketMetricsFilter, now time.Time) TicketMetrics {
	m := TicketMetrics{Filter: f, WorkloadBy: f.WorkloadBy}
	weeks := map[time.Time]*TicketMetricsWeek{}
	endExclusive := f.End.AddDate(0, 0, 1)
	for w := monday(f.Start); !w.After(f.End); w = w.AddDate(0, 0, 7) {
		weeks[w] = &TicketMetricsWeek{
			Start:   w,
			Partial: f.Start.After(w) || w.AddDate(0, 0, 7).After(endExclusive),
		}
	}
	ages := []TicketMetricsBucket{
		{Label: "0–2 days"}, {Label: "3–7 days"}, {Label: "8–14 days"}, {Label: ">14 days"},
	}
	work := map[string]TicketMetricsWorkload{}
	var durations []time.Duration
	for _, r := range records {
		if r.CurrentState == domain.StateNew || r.CurrentState == domain.StateInProgress {
			m.Pending++
			if r.AgentID == nil {
				m.Unassigned++
			}
			key, label := workloadIdentity(r, f.WorkloadBy)
			row := work[key]
			if row.Label == "" {
				row.Label = label
				row.Unassigned = key == "agent:unassigned"
			}
			row.Count++
			work[key] = row
			age := int(now.UTC().Sub(r.CreatedAt.UTC()) / (24 * time.Hour))
			switch {
			case age <= 2:
				ages[0].Count++
			case age <= 7:
				ages[1].Count++
			case age <= 14:
				ages[2].Count++
			default:
				ages[3].Count++
			}
		}
		if r.ResolvedAt.IsZero() {
			continue
		}
		m.Resolved++
		if w := weeks[monday(r.ResolutionWeek)]; w != nil {
			w.Resolved++
		}
		if r.CreatedAt.IsZero() || r.ResolvedAt.Before(r.CreatedAt) {
			m.Excluded++
			continue
		}
		durations = append(durations, r.ResolvedAt.Sub(r.CreatedAt))
	}
	for _, r := range records {
		if r.CreatedAt.IsZero() || r.CreatedAt.Before(f.Start) || !r.CreatedAt.Before(endExclusive) {
			continue
		}
		m.Created++
		if w := weeks[monday(r.CreatedAt)]; w != nil {
			w.Created++
		}
	}
	for _, w := range weeks {
		m.Weeks = append(m.Weeks, *w)
	}
	sort.Slice(m.Weeks, func(i, j int) bool { return m.Weeks[i].Start.Before(m.Weeks[j].Start) })
	if unassigned, ok := work["agent:unassigned"]; ok {
		for key, row := range work {
			if key != "agent:unassigned" && row.Label == "Unassigned" {
				unassigned.Label = "Unassigned (no agent)"
				work["agent:unassigned"] = unassigned
				break
			}
		}
	}
	workloadKeys := make([]string, 0, len(work))
	for key := range work {
		workloadKeys = append(workloadKeys, key)
	}
	sort.Slice(workloadKeys, func(i, j int) bool {
		left, right := work[workloadKeys[i]], work[workloadKeys[j]]
		if left.Label == right.Label {
			return workloadKeys[i] < workloadKeys[j]
		}
		return left.Label < right.Label
	})
	for _, key := range workloadKeys {
		m.Workload = append(m.Workload, work[key])
	}
	m.Ages = ages
	m.Histogram = resolutionHistogram(durations)
	m.Samples = len(durations)
	if len(durations) == 0 {
		return m
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	m.MeanDuration = averageDuration(durations)
	middle := len(durations) / 2
	m.Median = durations[middle]
	if len(durations)%2 == 0 {
		m.Median = durations[middle-1] + (durations[middle]-durations[middle-1])/2
	}
	m.P90 = durations[len(durations)-len(durations)/10-1]
	return m
}

const maxHistogramBins = 7

func averageDuration(durations []time.Duration) time.Duration {
	n := time.Duration(len(durations))
	var quotient, remainder time.Duration
	for _, d := range durations {
		quotient += d / n
		mod := d % n
		if remainder >= n-mod {
			quotient++
			remainder -= n - mod
			continue
		}
		remainder += mod
	}
	return quotient
}

func resolutionHistogram(durations []time.Duration) []TicketMetricsBucket {
	if len(durations) == 0 {
		return nil
	}
	max := durations[0]
	for _, d := range durations {
		if d > max {
			max = d
		}
	}
	widthDays, width := resolutionHistogramWidth(max)
	bins := make([]TicketMetricsBucket, int(max/width)+1)
	for i := range bins {
		lower := float64(int64(i) * widthDays)
		bins[i] = TicketMetricsBucket{
			Label:     strconv.FormatFloat(lower, 'f', -1, 64) + "–<" + strconv.FormatFloat(lower+float64(widthDays), 'f', -1, 64) + " days",
			LowerDays: lower,
			UpperDays: lower + float64(widthDays),
		}
	}
	for _, d := range durations {
		bins[int(d/width)].Count++
	}
	return bins
}

func resolutionHistogramWidth(max time.Duration) (int64, time.Duration) {
	day := 24 * time.Hour
	for scale := int64(1); ; scale *= 10 {
		for _, mult := range []int64{1, 2, 5} {
			days := mult * scale
			width := time.Duration(days) * day
			if int(max/width)+1 <= maxHistogramBins {
				return days, width
			}
		}
	}
}
