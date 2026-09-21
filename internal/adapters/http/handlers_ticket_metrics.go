package httpadapter

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// WithMetrics wires the optional ticket-metrics read service into the ticket handlers (issue #123); a nil service keeps every metrics surface hidden.
func (h *TicketHandlers) WithMetrics(metrics *application.TicketMetricsService) *TicketHandlers {
	h.metrics = metrics
	return h
}

// ticketMetricsData holds the compact summary and its safe detail-page origin.
type ticketMetricsData struct {
	Metrics        application.TicketMetrics
	Desks          []domain.Desk
	Users          []domain.User
	ReturnHref     string
	MeanResolution string
	// Attainment is the pre-formatted SLA attainment report (issue #211, PR 5).
	// The render path has no percent formatter, so the 0..1 rate is formatted
	// here for one consistent presentation, the same way the detail SLA panel
	// pre-formats its durations.
	Attainment ticketMetricsAttainmentView
	// MeanResolutionIsDuration is false when there is no computable mean, so the value
	// slot carries prose. The copy and its rendering rung come from this one condition
	// and cannot drift apart.
	MeanResolutionIsDuration bool
	Error                    string
}

// ticketMetricsDetailData is the dedicated /tickets/metrics page payload (issue #123).
type ticketMetricsDetailData struct {
	pageData
	ticketMetricsData
	BackHref string
}

// parseTicketMetricsFilter reads the independent metric filter set (issue #123); list query parameters are never read.
func parseTicketMetricsFilter(r *http.Request) (application.TicketMetricsFilter, error) {
	parseDate := func(v string) (time.Time, error) {
		if v == "" {
			return time.Time{}, nil
		}
		t, err := time.Parse("2006-01-02", v)
		if err != nil {
			return time.Time{}, &domain.ValidationError{Field: "metrics_date", Message: "metrics dates must use YYYY-MM-DD"}
		}
		return t, nil
	}
	var dates [2]time.Time
	for i, key := range [2]string{"metrics_start", "metrics_end"} {
		t, err := parseDate(r.URL.Query().Get(key))
		if err != nil {
			return application.TicketMetricsFilter{}, err
		}
		dates[i] = t
	}
	f := application.TicketMetricsFilter{
		Start:      dates[0],
		End:        dates[1],
		WorkloadBy: r.URL.Query().Get("metrics_group"),
		// The attainment grouping is its own parameter so the two selectors
		// never collide; like the list's sort it fails soft (an empty or
		// unknown value selects total) because the service normalizes it.
		GroupBy: r.URL.Query().Get("metrics_attainment_group"),
	}
	if id := parseID(r.URL.Query().Get("metrics_desk_id")); id != 0 {
		f.DeskID = &id
	}
	if id := parseID(r.URL.Query().Get("metrics_agent_id")); id != 0 {
		f.AgentID = &id
	}
	return f, nil
}

// metricsReturnHref builds the safe relative /tickets return URL for the
// summary's "View metrics" link (issue #123): only a /tickets origin
// survives, unknown or malformed query values are ignored, and the
// recognized list parameters are re-encoded by url.Values; else "/tickets".
func metricsReturnHref(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Path != "/tickets" || u.Host != "" || u.User != nil {
		return "/tickets"
	}
	if strings.ContainsRune(raw, 0) {
		return "/tickets"
	}
	q := u.Query()
	v := url.Values{}
	if s := domain.State(q.Get("state")); validState(s) {
		v.Set("state", string(s))
	}
	if p := domain.Priority(q.Get("priority")); domain.IsValidPriority(p) {
		v.Set("priority", string(p))
	}
	if id := parseID(q.Get("category_id")); id != 0 {
		v.Set("category_id", strconv.FormatInt(id, 10))
	}
	if id := parseID(q.Get("user_id")); id != 0 {
		v.Set("user_id", strconv.FormatInt(id, 10))
	}
	if text := q.Get("q"); text != "" {
		v.Set("q", text)
	}
	if page := parseID(q.Get("page")); page > 1 {
		v.Set("page", strconv.FormatInt(page, 10))
	}
	if len(v) == 0 {
		return "/tickets"
	}
	return "/tickets?" + v.Encode()
}

// metricsData loads the dedicated detail view plus the metric-filter option lists;
// empty dates default to the last 30 UTC days via the service's shared normalization.
func (h *TicketHandlers) metricsData(r *http.Request) (ticketMetricsData, error) {
	if h.metrics == nil {
		return ticketMetricsData{}, domain.NewForbiddenError("ticket metrics unavailable")
	}
	actor := *userFromContext(r.Context())
	filter, err := parseTicketMetricsFilter(r)
	if err != nil {
		return ticketMetricsData{}, err
	}
	metrics, err := h.metrics.View(r.Context(), actor, filter)
	if err != nil {
		return ticketMetricsData{}, err
	}
	desks, err := h.desks.List(r.Context())
	if err != nil {
		return ticketMetricsData{}, err
	}
	users, err := h.users.ListAssignable(r.Context(), actor)
	if err != nil {
		return ticketMetricsData{}, err
	}
	return ticketMetricsData{Metrics: metrics, Attainment: attainmentViewFor(metrics.Attainment), Desks: desks, Users: users, ReturnHref: metricsReturnHref(r.URL.RequestURI())}, nil
}

// ticketMetricsAttainmentView is the pre-formatted SLA attainment panel
// (issue #211, PR 5). Cohort is the number of tickets CREATED in the period
// that carry a frozen commitment, NoCommitment counts the period's created
// tickets excluded from every rate, and the milestone views carry the whole
// cohort's Met/Breached/Open totals. The template only prints fields.
type ticketMetricsAttainmentView struct {
	GroupBy       string
	Cohort        int
	NoCommitment  int
	FirstResponse ticketMetricsAttainmentMilestoneView
	Resolve       ticketMetricsAttainmentMilestoneView
	Groups        []ticketMetricsAttainmentGroupView
}

// ticketMetricsAttainmentGroupView is one row of the attainment table.
type ticketMetricsAttainmentGroupView struct {
	Label         string
	Tickets       int
	FirstResponse ticketMetricsAttainmentMilestoneView
	Resolve       ticketMetricsAttainmentMilestoneView
}

// ticketMetricsAttainmentMilestoneView carries one milestone's counts.
// Decided is the Met+Breached denominator, rendered next to the rate so a 0%
// with nothing decided cannot be misread as total failure; Open never enters
// the denominator.
type ticketMetricsAttainmentMilestoneView struct {
	Met      int
	Breached int
	Open     int
	Decided  int
	Rate     string
}

// attainmentViewFor projects the frozen attainment report into its
// presentation form, summing the per-group milestone counts into the cohort
// totals and formatting each rate as a whole percent.
func attainmentViewFor(a application.TicketMetricsAttainment) ticketMetricsAttainmentView {
	v := ticketMetricsAttainmentView{GroupBy: a.GroupBy, NoCommitment: a.NoCommitment}
	for _, g := range a.Groups {
		v.Cohort += g.Tickets
		v.FirstResponse = accumulateAttainmentMilestone(v.FirstResponse, g.FirstResponse)
		v.Resolve = accumulateAttainmentMilestone(v.Resolve, g.Resolve)
		v.Groups = append(v.Groups, ticketMetricsAttainmentGroupView{
			Label:         g.Label,
			Tickets:       g.Tickets,
			FirstResponse: attainmentMilestoneViewFor(g.FirstResponse),
			Resolve:       attainmentMilestoneViewFor(g.Resolve),
		})
	}
	v.FirstResponse = finalizeAttainmentMilestone(v.FirstResponse)
	v.Resolve = finalizeAttainmentMilestone(v.Resolve)
	return v
}

// attainmentMilestoneViewFor converts one group milestone to its presentation
// form; finalizeAttainmentMilestone derives the rate string.
func attainmentMilestoneViewFor(m application.TicketMetricsMilestoneAttainment) ticketMetricsAttainmentMilestoneView {
	return finalizeAttainmentMilestone(ticketMetricsAttainmentMilestoneView{Met: m.Met, Breached: m.Breached, Open: m.Open})
}

func accumulateAttainmentMilestone(dst ticketMetricsAttainmentMilestoneView, m application.TicketMetricsMilestoneAttainment) ticketMetricsAttainmentMilestoneView {
	dst.Met += m.Met
	dst.Breached += m.Breached
	dst.Open += m.Open
	return dst
}

// finalizeAttainmentMilestone derives Decided and the whole-percent Rate from
// the counts, so the label and the denominator can never drift apart.
func finalizeAttainmentMilestone(m ticketMetricsAttainmentMilestoneView) ticketMetricsAttainmentMilestoneView {
	m.Decided = m.Met + m.Breached
	rate := 0.0
	if m.Decided > 0 {
		rate = float64(m.Met) / float64(m.Decided)
	}
	m.Rate = strconv.FormatFloat(rate*100, 'f', 0, 64) + "%"
	return m
}

// metricsSummaryData loads the fixed current-UTC-week list summary.
func (h *TicketHandlers) metricsSummaryData(r *http.Request) (ticketMetricsData, error) {
	if h.metrics == nil {
		return ticketMetricsData{}, domain.NewForbiddenError("ticket metrics unavailable")
	}
	metrics, err := h.metrics.ViewCurrentWeek(r.Context(), *userFromContext(r.Context()))
	if err != nil {
		return ticketMetricsData{}, err
	}
	mean := "No verifiable durations"
	meanIsDuration := false
	if metrics.Samples > 0 {
		mean = metricsDuration(metrics.MeanDuration)
		meanIsDuration = true
	}
	return ticketMetricsData{
		Metrics:                  metrics,
		ReturnHref:               metricsReturnHref(r.URL.RequestURI()),
		MeanResolution:           mean,
		MeanResolutionIsDuration: meanIsDuration,
	}, nil
}

// renderMetricsDetail answers the dedicated /tickets/metrics page (normal request)
// or its #ticket-metrics-detail-content fragment (HX-Request); the Back href is
// always the sanitized return value. Option lists reload best-effort only for
// validation renders (HX-visible 200 or full 422): 403/other failures never
// pay for option lists.
func (h *TicketHandlers) renderMetricsDetail(w http.ResponseWriter, r *http.Request, data ticketMetricsData, status int) {
	if data.Error != "" && (status == http.StatusOK || status == http.StatusUnprocessableEntity) {
		if data.Desks == nil {
			if desks, err := h.desks.List(r.Context()); err == nil {
				data.Desks = desks
			}
		}
		if data.Users == nil {
			if users, err := h.users.ListAssignable(r.Context(), *userFromContext(r.Context())); err == nil {
				data.Users = users
			}
		}
	}
	page := pageDataFrom(r, "tickets")
	page.PageFoundationAssets = true
	page.MetricsAssets = true
	detail := ticketMetricsDetailData{pageData: page, ticketMetricsData: data, BackHref: metricsReturnHref(r.URL.Query().Get("return"))}
	h.renderer.Render(w, r, "ticket_metrics_detail", "ticket_metrics_detail_content", detail, status)
}

// metricsView answers GET /tickets/metrics (issue #123): a normal request renders
// the dedicated full page, an HX request the detail fragment; filter validation
// alone degrades to a 200 fragment so HTMX shows the visible alert.
func (h *TicketHandlers) metricsView(w http.ResponseWriter, r *http.Request) {
	hx := r.Header.Get("HX-Request") != ""
	data, err := h.metricsData(r)
	if err != nil {
		status, message := mapError(err)
		if hx && status == http.StatusUnprocessableEntity {
			status = http.StatusOK
		}
		h.renderMetricsDetail(w, r, ticketMetricsData{Error: message}, status)
		return
	}
	h.renderMetricsDetail(w, r, data, http.StatusOK)
}

// metricsDuration formats a resolution duration in compact UTC days.
func metricsDuration(d time.Duration) string {
	return strconv.FormatFloat(d.Hours()/24, 'f', 1, 64) + " days"
}
