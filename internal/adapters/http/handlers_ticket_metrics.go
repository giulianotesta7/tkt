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
	Error          string
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
	f := application.TicketMetricsFilter{Start: dates[0], End: dates[1], WorkloadBy: r.URL.Query().Get("metrics_group")}
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
	return ticketMetricsData{Metrics: metrics, Desks: desks, Users: users, ReturnHref: metricsReturnHref(r.URL.RequestURI())}, nil
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
	if metrics.Samples > 0 {
		mean = metricsDuration(metrics.MeanDuration)
	}
	return ticketMetricsData{
		Metrics:        metrics,
		ReturnHref:     metricsReturnHref(r.URL.RequestURI()),
		MeanResolution: mean,
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
