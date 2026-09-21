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
	// GroupBy selects the SLA attainment dimension. Like the ticket list's
	// sort parameter it fails soft: empty or any unknown value means total.
	GroupBy string // total (default), priority, or category
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
	// Priority and CategoryName are the attainment grouping identities.
	Priority     domain.Priority
	CategoryName string
	// SLA is the commitment FROZEN onto the ticket at creation, nil when the
	// ticket has no frozen row (legacy tickets, or tickets created while SLA
	// was disabled). Milestones are the observed FIRST public staff response
	// and FIRST resolution, each nil when it has not happened.
	SLA        *domain.TicketSLA
	Milestones domain.SLAMilestones
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

// View authorizes before observing the clock or read port. The filter is
// normalized and aggregated from one UTC clock snapshot.
func (s *TicketMetricsService) View(ctx context.Context, actor domain.User, filter TicketMetricsFilter) (TicketMetrics, error) {
	if !NewPolicy().Capabilities(actor.Role).Require(CapViewTicketMetrics) {
		return TicketMetrics{}, domain.NewForbiddenError("ticket metrics require admin access")
	}
	return s.view(ctx, filter, s.clock.Now().UTC())
}

// ViewCurrentWeek returns the current UTC Monday-Sunday period using one clock
// snapshot. Workload defaults to agents through the shared normalization path.
func (s *TicketMetricsService) ViewCurrentWeek(ctx context.Context, actor domain.User) (TicketMetrics, error) {
	if !NewPolicy().Capabilities(actor.Role).Require(CapViewTicketMetrics) {
		return TicketMetrics{}, domain.NewForbiddenError("ticket metrics require admin access")
	}
	now := s.clock.Now().UTC()
	weekStart := monday(now)
	return s.view(ctx, TicketMetricsFilter{Start: weekStart, End: weekStart.AddDate(0, 0, 6)}, now)
}

func (s *TicketMetricsService) view(ctx context.Context, filter TicketMetricsFilter, now time.Time) (TicketMetrics, error) {
	filter, err := normalizeTicketMetricsFilter(filter, now)
	if err != nil {
		return TicketMetrics{}, err
	}
	records, err := s.store.TicketMetrics(ctx, filter)
	if err != nil {
		return TicketMetrics{}, err
	}
	return buildTicketMetrics(records, filter, now), nil
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
	Attainment   TicketMetricsAttainment
}

// Ticket metrics attainment grouping values.
const (
	TicketMetricsGroupTotal    = "total"
	TicketMetricsGroupPriority = "priority"
	TicketMetricsGroupCategory = "category"
)

// TicketMetricsAttainment is the SLA attainment report for the PERIOD COHORT:
// tickets CREATED in [Filter.Start, Filter.End) that carry a frozen
// commitment. Tickets created in the period without a commitment are excluded
// from every rate and counted once in NoCommitment. GroupBy names the
// dimension actually used (after fail-soft normalization); Groups is in the
// deterministic order documented on buildTicketAttainment.
type TicketMetricsAttainment struct {
	GroupBy      string
	NoCommitment int
	Groups       []TicketMetricsAttainmentGroup
}

// TicketMetricsAttainmentGroup is one attainment group. Label is printable as
// is. Tickets is the cohort size in the group (only tickets with a commitment),
// so a priority group with zero tickets is still present with a stable shape.
type TicketMetricsAttainmentGroup struct {
	Label         string
	Tickets       int
	FirstResponse TicketMetricsMilestoneAttainment
	Resolve       TicketMetricsMilestoneAttainment
}

// TicketMetricsMilestoneAttainment is one milestone's counts for one group.
// Met means achieved at or before the frozen due instant; Breached means
// achieved late or still unachieved at or past due; Open means still
// unachieved before due. The same states the ticket badge projects, so the
// report and the badge can never disagree. Rate is Met/(Met+Breached) and is 0
// when nothing is decided: Open never enters the denominator.
type TicketMetricsMilestoneAttainment struct {
	Met      int
	Breached int
	Open     int
	Rate     float64
}

// attainmentPriorities is the canonical priority order the priority grouping
// always emits, even for empty groups, so the report table shape is stable.
var attainmentPriorities = []domain.Priority{
	domain.PriorityCritical,
	domain.PriorityHigh,
	domain.PriorityMedium,
	domain.PriorityLow,
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
	// The attainment grouping is deliberately fail-soft (unlike WorkloadBy):
	// empty or unknown means total, never a validation error.
	f.GroupBy = normalizeTicketMetricsGroupBy(f.GroupBy)
	return f, nil
}

// normalizeTicketMetricsGroupBy resolves the attainment grouping dimension,
// selecting total for the empty string or any unknown value.
func normalizeTicketMetricsGroupBy(value string) string {
	switch value {
	case TicketMetricsGroupPriority, TicketMetricsGroupCategory:
		return value
	default:
		return TicketMetricsGroupTotal
	}
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
	m.Attainment = buildTicketAttainment(records, f, now)
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

// buildTicketAttainment aggregates the SLA attainment of the period cohort:
// tickets CREATED in [f.Start, f.End) with a frozen commitment. It reuses
// domain.ProjectSLA for the per-milestone verdict, so the report and the ticket
// badge share one definition. Milestones are NOT period-limited: a first
// response or resolution outside the window still counts for a cohort ticket.
//
// Group order is deterministic: total emits exactly one group; priority emits
// the four canonical priorities in rank order, empty ones included, so the
// table shape is stable; category emits only the categories present, ordered by
// name. Rate counts Met/(Met+Breached), and Open never enters the denominator.
func buildTicketAttainment(records []TicketMetricsRecord, f TicketMetricsFilter, now time.Time) TicketMetricsAttainment {
	groupBy := normalizeTicketMetricsGroupBy(f.GroupBy)
	a := TicketMetricsAttainment{GroupBy: groupBy}
	groups := map[string]*TicketMetricsAttainmentGroup{}
	var order []string
	for _, r := range records {
		if r.CreatedAt.IsZero() || r.CreatedAt.Before(f.Start) || !r.CreatedAt.Before(f.End) {
			continue
		}
		projection := domain.ProjectSLA(r.SLA, r.Milestones, now)
		if projection.Overall == domain.SLANone {
			a.NoCommitment++
			continue
		}
		key, label := attainmentGroupIdentity(r, groupBy)
		g := groups[key]
		if g == nil {
			g = &TicketMetricsAttainmentGroup{Label: label}
			groups[key] = g
			order = append(order, key)
		}
		g.Tickets++
		accumulateMilestone(&g.FirstResponse, projection.FirstResponse.State)
		accumulateMilestone(&g.Resolve, projection.Resolve.State)
	}

	switch groupBy {
	case TicketMetricsGroupPriority:
		a.Groups = make([]TicketMetricsAttainmentGroup, 0, len(attainmentPriorities))
		for _, p := range attainmentPriorities {
			if g := groups["priority:"+string(p)]; g != nil {
				a.Groups = append(a.Groups, *g)
			} else {
				a.Groups = append(a.Groups, TicketMetricsAttainmentGroup{Label: attainmentPriorityLabel(p)})
			}
		}
	case TicketMetricsGroupCategory:
		sort.Slice(order, func(i, j int) bool { return groups[order[i]].Label < groups[order[j]].Label })
		for _, key := range order {
			a.Groups = append(a.Groups, *groups[key])
		}
	default:
		if g := groups["total"]; g != nil {
			a.Groups = []TicketMetricsAttainmentGroup{*g}
		} else {
			a.Groups = []TicketMetricsAttainmentGroup{{Label: "Total"}}
		}
	}
	for i := range a.Groups {
		finalizeMilestone(&a.Groups[i].FirstResponse)
		finalizeMilestone(&a.Groups[i].Resolve)
	}
	return a
}

// attainmentGroupIdentity maps one cohort record to its stable group key and
// printable label.
func attainmentGroupIdentity(r TicketMetricsRecord, groupBy string) (string, string) {
	switch groupBy {
	case TicketMetricsGroupPriority:
		return "priority:" + string(r.Priority), attainmentPriorityLabel(r.Priority)
	case TicketMetricsGroupCategory:
		return "category:" + r.CategoryName, r.CategoryName
	default:
		return "total", "Total"
	}
}

// attainmentPriorityLabel renders a priority as a printable label matching the
// UI's humanized form.
func attainmentPriorityLabel(p domain.Priority) string {
	switch p {
	case domain.PriorityCritical:
		return "Critical"
	case domain.PriorityHigh:
		return "High"
	case domain.PriorityMedium:
		return "Medium"
	case domain.PriorityLow:
		return "Low"
	}
	return string(p)
}

// accumulateMilestone folds one ProjectSLA milestone state into its counter:
// met and breached are decisions, at_risk and on_track are still open, and
// none (a zero-instant milestone on an otherwise frozen row) is not counted.
func accumulateMilestone(m *TicketMetricsMilestoneAttainment, state domain.SLAState) {
	switch state {
	case domain.SLAMet:
		m.Met++
	case domain.SLABreached:
		m.Breached++
	case domain.SLAAtRisk, domain.SLAOnTrack:
		m.Open++
	}
}

// finalizeMilestone derives the rate from the decided counts only; a zero
// denominator yields a zero rate.
func finalizeMilestone(m *TicketMetricsMilestoneAttainment) {
	if decided := m.Met + m.Breached; decided > 0 {
		m.Rate = float64(m.Met) / float64(decided)
	}
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
