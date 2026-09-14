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
	ReturnHref     string
	MeanResolution string
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

// metricsDuration formats a resolution duration in compact UTC days.
func metricsDuration(d time.Duration) string {
	return strconv.FormatFloat(d.Hours()/24, 'f', 1, 64) + " days"
}
