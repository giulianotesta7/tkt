// Package httpadapter implements the presentation layer (design "Package
// Layout"): stdlib net/http handlers over http.ServeMux (D9), HX-aware
// rendering (D6), session middleware (D14, D16, D17), and D5 error mapping.
// It imports the application ports only; persistence stays behind the sqlite
// adapter.
package httpadapter

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"math"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/web/templates"
)

const displayTimeLayout = "15:04 · 02-01-2006"

// cardTimeLayout is the compact card-only form (issue #122): exact UTC
// "21:42 · 13 Sep". The global displayTimeLayout stays the accessible full
// date (year included) exposed through the card timestamp's tooltip.
const cardTimeLayout = "15:04 · 02 Jan"

func formatDisplayTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(displayTimeLayout)
}

func formatCardTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(cardTimeLayout)
}

func formatDatetime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func humanizeLabel(value any) string {
	words := strings.Fields(strings.ReplaceAll(fmt.Sprint(value), "_", " "))
	for i, word := range words {
		words[i] = strings.ToUpper(word[:1]) + strings.ToLower(word[1:])
	}
	return strings.Join(words, " ")
}

// niceMetricsScale rounds the data maximum up to a friendly y-axis maximum
// whose tick count stays readable (at most 4 gridlines above the zero
// baseline). Returns the tick step and the axis maximum.
func niceMetricsScale(max int) (step int, axisMax int) {
	candidates := []int{1, 2, 5, 10, 20, 25, 50, 100, 200, 250, 500, 1000, 2000, 2500, 5000, 10000, 20000, 25000, 50000, 100000, 1000000}
	for _, c := range candidates {
		if max <= 4*c {
			return c, ((max + c - 1) / c) * c
		}
	}
	return 1000000, ((max + 999999) / 1000000) * 1000000
}

// metricsLineChart renders the weekly created/resolved lines: created is a
// continuous solid stroke with circle markers, resolved a dashed stroke (via
// its semantic CSS class) with square markers. Value labels are collision
// aware: when the two series meet near a point, each label moves
// deterministically to the opposite side of its own point; labels are always
// clamped inside the viewBox away from the axes and week labels.
func metricsLineChart(weeks []application.TicketMetricsWeek) template.HTML {
	const width, height, left, top, bottom = 620, 204, 46, 22, 40
	max := 1
	for _, week := range weeks {
		if week.Created > max {
			max = week.Created
		}
		if week.Resolved > max {
			max = week.Resolved
		}
	}
	step, axisMax := niceMetricsScale(max)
	x := func(i int) float64 {
		if len(weeks) < 2 {
			return left
		}
		return float64(left) + float64(width-left-12)*float64(i)/float64(len(weeks)-1)
	}
	y := func(value int) float64 {
		return float64(top) + float64(height-top-bottom)*(1-float64(value)/float64(axisMax))
	}
	points := func(resolved bool) string {
		var b strings.Builder
		for i, week := range weeks {
			value := week.Created
			if resolved {
				value = week.Resolved
			}
			fmt.Fprintf(&b, "%.1f,%.1f ", x(i), y(value))
		}
		return b.String()
	}
	// labelY picks the value-label baseline for one series at one point. When
	// the series sit closer than one label height apart, the labels move to
	// opposite sides: the lower point keeps its label above, the higher point
	// takes its label below, and an exact tie keeps created above and resolved
	// below. The clamp keeps every label inside the plot area clear of the axis
	// and week labels.
	clampY := func(v float64) float64 {
		return math.Min(math.Max(v, 12), float64(height-24))
	}
	labelY := func(ownY, otherY float64, belowOnTie bool) float64 {
		v := ownY - 7
		if math.Abs(ownY-otherY) < 24 && (ownY > otherY || (belowOnTie && ownY == otherY)) {
			v = ownY + 16
		}
		return clampY(v)
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="ticket-metrics-chart" viewBox="0 0 %d %d" role="img" aria-label="Weekly created tickets as a continuous line with circle markers and resolved tickets as a dashed line with square markers. Y axis from 0 to %d. Y axis title Tickets.">`, width, height, axisMax)
	midY := float64(top) + float64(height-top-bottom)/2
	fmt.Fprintf(&b, `<text x="10" y="%.1f" transform="rotate(-90 10 %.1f)" text-anchor="middle" class="metrics-axis-title">Tickets</text>`, midY, midY)
	fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" class="metrics-axis"/>`, left, height-bottom, width-12, height-bottom)
	for tick := step; tick <= axisMax; tick += step {
		fmt.Fprintf(&b, `<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" class="metrics-gridline"/>`, left, y(tick), width-12, y(tick))
	}
	for tick := 0; tick <= axisMax; tick += step {
		fmt.Fprintf(&b, `<text x="%d" y="%.1f" text-anchor="end" class="metrics-axis-label">%d</text>`, left-6, y(tick)+3.5, tick)
	}
	fmt.Fprintf(&b, `<polyline class="metrics-line metrics-created" points="%s"/><polyline class="metrics-line metrics-resolved" points="%s"/>`, points(false), points(true))
	for i, week := range weeks {
		fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="3.5" class="metrics-marker metrics-marker-created"/>`, x(i), y(week.Created))
		fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="7" height="7" class="metrics-marker metrics-marker-resolved"/>`, x(i)-3.5, y(week.Resolved)-3.5)
		label := week.Start.Format("Jan 2")
		if week.Partial {
			label += "*"
		}
		fmt.Fprintf(&b, `<text x="%.1f" y="%d" class="metrics-chart-label">%s</text>`, x(i), height-8, template.HTMLEscapeString(label))
		if week.Created > 0 {
			fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" text-anchor="middle" class="metrics-chart-value metrics-chart-value-created">%d</text>`, x(i), labelY(y(week.Created), y(week.Resolved), false), week.Created)
		}
		if week.Resolved > 0 {
			fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" text-anchor="middle" class="metrics-chart-value metrics-chart-value-resolved">%d</text>`, x(i), labelY(y(week.Resolved), y(week.Created), true), week.Resolved)
		}
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// metricsAgeChart renders the pending-ticket age bars: the four fixed ranges
// in their established order, the same accent fill for every bar, an x axis
// that starts at zero with nice integer ticks and gridlines, left labels, and
// the count at each bar end.
func metricsAgeChart(buckets []application.TicketMetricsBucket) template.HTML {
	const width, left, top, rowH = 620, 120, 14, 30
	const bottom = 54
	max := 1
	for _, bucket := range buckets {
		if bucket.Count > max {
			max = bucket.Count
		}
	}
	step, axisMax := niceMetricsScale(max)
	plotW := float64(width - left - 16)
	plotH := len(buckets) * rowH
	baseline := float64(top + plotH)
	barW := func(count int) float64 {
		return plotW * float64(count) / float64(axisMax)
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="ticket-metrics-chart ticket-metrics-bars" viewBox="0 0 %d %d" role="img" aria-label="Pending tickets by completed elapsed age. X axis from 0 to %d. X axis title Pending tickets.">`, width, top+plotH+bottom, axisMax)
	for tick := step; tick <= axisMax; tick += step {
		fmt.Fprintf(&b, `<line x1="%.1f" y1="%d" x2="%.1f" y2="%.1f" class="metrics-gridline"/>`, float64(left)+barW(tick), top, float64(left)+barW(tick), baseline)
	}
	fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%.1f" class="metrics-axis"/>`, left, top, left, baseline)
	fmt.Fprintf(&b, `<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" class="metrics-axis"/>`, left, baseline, width-16, baseline)
	for tick := 0; tick <= axisMax; tick += step {
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" text-anchor="middle" class="metrics-axis-label">%d</text>`, float64(left)+barW(tick), baseline+16, tick)
	}
	fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" text-anchor="middle" class="metrics-axis-title">Pending tickets</text>`, float64(left)+plotW/2, baseline+34)
	for i, bucket := range buckets {
		y := top + i*rowH + 6
		fmt.Fprintf(&b, `<text x="2" y="%d" class="metrics-chart-label">%s</text><rect x="%d" y="%d" width="%.1f" height="18" class="metrics-bar"/><text x="%.1f" y="%d" class="metrics-chart-value">%d</text>`,
			y+14, template.HTMLEscapeString(bucket.Label), left, y, barW(bucket.Count), float64(left)+barW(bucket.Count)+6, y+14, bucket.Count)
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// metricsHistogramChart renders one adjacent rectangular bar per equal-width
// resolution bin: identical pixel width, zero gap, zero-height bins included.
// X ticks sit on the bin boundaries (each LowerDays plus the final UpperDays);
// the y axis starts at zero with nice integer counts.
func metricsHistogramChart(buckets []application.TicketMetricsBucket) template.HTML {
	if len(buckets) == 0 {
		return ""
	}
	const width, height, left, top, bottom = 620, 200, 46, 14, 54
	max := 1
	for _, bucket := range buckets {
		if bucket.Count > max {
			max = bucket.Count
		}
	}
	step, axisMax := niceMetricsScale(max)
	plotW := float64(width - left - 14)
	plotH := float64(height - top - bottom)
	baseline := top + plotH
	binW := plotW / float64(len(buckets))
	x := func(i int) float64 { return float64(left) + binW*float64(i) }
	y := func(count int) float64 {
		return float64(top) + float64(plotH)*(1-float64(count)/float64(axisMax))
	}
	boundary := func(i int) float64 {
		if i < len(buckets) {
			return buckets[i].LowerDays
		}
		return buckets[len(buckets)-1].UpperDays
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="ticket-metrics-chart ticket-metrics-histogram" viewBox="0 0 %d %d" role="img" aria-label="Resolved tickets by resolution duration in equal-width day bins. Y axis from 0 to %d. Y axis title Tickets. X axis title Resolution time (days).">`, width, height, axisMax)
	midY := float64(top) + float64(plotH)/2
	fmt.Fprintf(&b, `<text x="10" y="%.1f" transform="rotate(-90 10 %.1f)" text-anchor="middle" class="metrics-axis-title">Tickets</text>`, midY, midY)
	fmt.Fprintf(&b, `<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" class="metrics-axis"/>`, left, baseline, width-14, baseline)
	for tick := step; tick <= axisMax; tick += step {
		fmt.Fprintf(&b, `<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" class="metrics-gridline"/>`, left, y(tick), width-14, y(tick))
	}
	for tick := 0; tick <= axisMax; tick += step {
		fmt.Fprintf(&b, `<text x="%d" y="%.1f" text-anchor="end" class="metrics-axis-label">%d</text>`, left-6, y(tick)+3.5, tick)
	}
	for i, bucket := range buckets {
		barH := baseline - y(bucket.Count)
		fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" class="metrics-hist-bar"/>`, x(i), y(bucket.Count), binW, barH)
	}
	for i := 0; i <= len(buckets); i++ {
		anchor := "middle"
		px := x(i)
		if i == 0 {
			anchor = "start"
		} else if i == len(buckets) {
			anchor = "end"
			px = x(len(buckets))
		}
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" text-anchor="%s" class="metrics-axis-label">%s</text>`, px, baseline+16, anchor, strconv.FormatFloat(boundary(i), 'f', -1, 64))
	}
	fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" text-anchor="middle" class="metrics-axis-title">Resolution time (days)</text>`, float64(left)+plotW/2, baseline+34)
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// metricsBarChartClasses renders one horizontal bar per bucket over the same
// axis contract as the age chart: an x axis that starts at zero with nice
// integer ticks and gridlines, a visible Pending tickets axis title, left
// labels, and the count at each bar end. classes[i] (when non-empty) is
// appended to the rect class so callers can style single rows (e.g. the
// neutral unassigned workload bar) without parsing labels.
func metricsBarChartClasses(buckets []application.TicketMetricsBucket, label string, classes []string) template.HTML {
	const width, left, top, row, bottom = 620, 176, 6, 28, 54
	max := 1
	for _, bucket := range buckets {
		if bucket.Count > max {
			max = bucket.Count
		}
	}
	step, axisMax := niceMetricsScale(max)
	plotW := float64(width - left - 16)
	plotH := len(buckets) * row
	baseline := float64(top + plotH)
	barW := func(count int) float64 {
		return plotW * float64(count) / float64(axisMax)
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="ticket-metrics-chart ticket-metrics-bars" viewBox="0 0 %d %d" role="img" aria-label="%s. X axis from 0 to %d. X axis title Pending tickets.">`, width, top+plotH+bottom, template.HTMLEscapeString(label), axisMax)
	for tick := step; tick <= axisMax; tick += step {
		fmt.Fprintf(&b, `<line x1="%.1f" y1="%d" x2="%.1f" y2="%.1f" class="metrics-gridline"/>`, float64(left)+barW(tick), top, float64(left)+barW(tick), baseline)
	}
	fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%.1f" class="metrics-axis"/>`, left, top, left, baseline)
	fmt.Fprintf(&b, `<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" class="metrics-axis"/>`, left, baseline, width-16, baseline)
	for tick := 0; tick <= axisMax; tick += step {
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" text-anchor="middle" class="metrics-axis-label">%d</text>`, float64(left)+barW(tick), baseline+16, tick)
	}
	fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" text-anchor="middle" class="metrics-axis-title">Pending tickets</text>`, float64(left)+plotW/2, baseline+34)
	for i, bucket := range buckets {
		y := top + i*row + 6
		class := "metrics-bar"
		if i < len(classes) && classes[i] != "" {
			class += " " + classes[i]
		}
		fmt.Fprintf(&b, `<text x="2" y="%d" class="metrics-chart-label">%s</text><rect x="%d" y="%d" width="%.1f" height="18" class="%s"/><text x="%.1f" y="%d" class="metrics-chart-value">%d</text>`,
			y+14, template.HTMLEscapeString(bucket.Label), left, y, barW(bucket.Count), class, float64(left)+barW(bucket.Count)+6, y+14, bucket.Count)
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// metricsWorkloadChart renders the pending workload bars. Only the genuine
// unassigned identity (nil current agent) receives the neutral class; a named
// agent keeps the accent bar regardless of its display label.
func metricsWorkloadChart(rows []application.TicketMetricsWorkload, label string) template.HTML {
	buckets := make([]application.TicketMetricsBucket, len(rows))
	classes := make([]string, len(rows))
	for i, row := range rows {
		buckets[i] = application.TicketMetricsBucket{Label: row.Label, Count: row.Count}
		if row.Unassigned {
			classes[i] = "metrics-bar-unassigned"
		}
	}
	return metricsBarChartClasses(buckets, label, classes)
}

// templateFuncs are the presentation helpers shared by every template set.
// The render path never calls time.Now() (D7): formatTime formats the
// already-stamped instants the handlers pass in.
var templateFuncs = template.FuncMap{
	"formatTime":     formatDisplayTime,
	"formatCardTime": formatCardTime,
	"formatDatetime": formatDatetime,
	"humanize":       humanizeLabel,
	"ticketNumber": func(n int) string {
		return "TKT-" + strconv.Itoa(n)
	},
	"initials": func(name string) string {
		parts := strings.Fields(name)
		if len(parts) == 0 {
			return "?"
		}
		out := strings.ToUpper(parts[0][:1])
		if len(parts) > 1 {
			out += strings.ToUpper(parts[len(parts)-1][:1])
		}
		return out
	},
	"categoryDepartmentValue": func(id int64) string {
		if id == -1 {
			return "unassigned"
		}
		return strconv.FormatInt(id, 10)
	},
	"hasDesk": func(desks []domain.Desk, id int64) bool {
		for _, d := range desks {
			if d.ID == id {
				return true
			}
		}
		return false
	},
	"metricSelected":        func(id int64, selected *int64) bool { return selected != nil && id == *selected },
	"metricsLineChart":      metricsLineChart,
	"metricsAgeChart":       metricsAgeChart,
	"metricsWorkloadChart":  metricsWorkloadChart,
	"metricsHistogramChart": metricsHistogramChart,
	"metricsDuration":       metricsDuration,
	"workflowTypeLabel": func(t domain.StepType) string {
		switch t {
		case domain.StepAssignToDesk:
			return "Assign to desk"
		case domain.StepForm:
			return "Form"
		case domain.StepManualTask:
			return "Manual task"
		case domain.StepResolve:
			return "Resolve ticket"
		case domain.StepClose:
			return "Close ticket"
		default:
			return "Workflow step"
		}
	},
}

// shellFor maps a page to its shell root. Application pages use the rail
// shell (base.html); the auth pages (login, setup) use the split shell
// (auth.html). A page with no entry defaults to base.html.
func shellFor(page string) string {
	switch page {
	case "login", "setup":
		return "auth.html"
	default:
		return "base.html"
	}
}

// pageSet is a fully parsed page: the shell root to execute and the set
// holding the shell + all partials + the page's own "content" definition.
type pageSet struct {
	shell string
	tmpl  *template.Template
}

// Renderer owns the parsed template sets (D6): one set per page (shell +
// partials + page content) and one shared fragment set for HX swaps.
type Renderer struct {
	pages     map[string]pageSet
	fragments *template.Template
}

// NewRenderer parses the embedded template tree (web/templates). Parse
// errors are fatal: templates are static assets, a broken template is a
// build error.
func NewRenderer() *Renderer {
	r, err := parseRenderer(templates.FS)
	if err != nil {
		panic("httpadapter: parse templates: " + err.Error())
	}
	return r
}

// NewRendererWith parses an arbitrary template tree (test harness: fixture
// sets, golden regeneration).
func NewRendererWith(fsys fs.FS) (*Renderer, error) {
	return parseRenderer(fsys)
}

// parseRenderer builds the per-page sets and the shared fragment set from
// fsys. Layout: shell roots (base.html, auth.html) at the root; pages under
// pages/ (each defines "content"); swap fragments under partials/.
func parseRenderer(fsys fs.FS) (*Renderer, error) {
	fragments, err := template.New("").Funcs(templateFuncs).ParseFS(fsys, "partials/*.html")
	if err != nil {
		return nil, err
	}

	base, err := template.New("base.html").Funcs(templateFuncs).ParseFS(fsys, "base.html")
	if err != nil {
		return nil, err
	}
	shells := map[string]*template.Template{"base.html": base}
	if _, err := fs.Stat(fsys, "auth.html"); err == nil {
		auth, err := template.New("auth.html").Funcs(templateFuncs).ParseFS(fsys, "auth.html")
		if err != nil {
			return nil, err
		}
		shells["auth.html"] = auth
	}

	pageFiles, err := fs.Glob(fsys, "pages/*.html")
	if err != nil {
		return nil, err
	}
	pages := make(map[string]pageSet, len(pageFiles))
	for _, file := range pageFiles {
		name := strings.TrimSuffix(path.Base(file), ".html")
		shell := shells[shellFor(name)]
		if shell == nil {
			return nil, fs.ErrNotExist // shell file missing from fsys
		}
		page, err := shell.Clone()
		if err != nil {
			return nil, err
		}
		if _, err := page.ParseFS(fsys, "partials/*.html", file); err != nil {
			return nil, err
		}
		pages[name] = pageSet{shell: shellFor(name), tmpl: page}
	}
	return &Renderer{pages: pages, fragments: fragments}, nil
}

// Render executes the page (full request) or the fragment (HX-Request) with
// the given status (D6). The response is buffered so template errors cannot
// corrupt a partial write; a render failure degrades to a generic 500.
func (r *Renderer) Render(w http.ResponseWriter, rq *http.Request, page, fragment string, data any, status int) {
	var buf bytes.Buffer
	var err error

	if rq.Header.Get("HX-Request") != "" {
		if fragment != "" {
			err = r.fragments.ExecuteTemplate(&buf, fragment, data)
		} else {
			// Natural fragment: the page's own content block (login/setup forms).
			ps, ok := r.pages[page]
			if !ok {
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}
			err = ps.tmpl.ExecuteTemplate(&buf, "content", data)
		}
	} else {
		ps, ok := r.pages[page]
		if !ok {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		err = ps.tmpl.ExecuteTemplate(&buf, ps.shell, data)
	}
	if err != nil {
		log.Printf("httpadapter: render %s/%s: %v", page, fragment, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	clearSaveFeedbackCookie(w, rq)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}
