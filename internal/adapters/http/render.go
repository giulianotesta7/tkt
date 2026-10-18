// Package httpadapter implements the presentation layer (design "Package
// Layout"): stdlib net/http handlers over http.ServeMux (D9), HX-aware
// rendering (D6), session middleware (D14, D16, D17), and D5 error mapping.
// It imports the application ports and the interface message catalogues
// (internal/i18n); persistence stays behind the sqlite adapter.
package httpadapter

import (
	"bytes"
	"context"
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
	"github.com/giulianotesta7/tkt/internal/i18n"
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

// ticketStateLabel is the single presentation source for a ticket state's
// visible label, one label per audience (issue #334). The requester audience
// owns its vocabulary — "Received" for a newly created ticket and the
// sentence-case "In progress" its card list already used — while staff keep
// the shared label ("New", "In Progress"). Every state surface renders through
// here so a list and a detail never name one state twice for the same audience.
// Both vocabularies live in the message catalogue; an unrecognized state still
// degrades to the humanized identifier.
func ticketStateLabel(tr i18n.Translator, value any, requester bool) string {
	state := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
	if requester {
		if message, ok := tr.Lookup("label.state.requester." + state); ok {
			return message
		}
	}
	if message, ok := tr.Lookup("label.state." + state); ok {
		return message
	}
	return humanizeLabel(value)
}

// labeledValue resolves one domain identifier through its catalogue namespace
// (label.<kind>.<value>), falling back to the humanized identifier. The
// fallback is what keeps a genuinely free identifier — a value no catalogue
// names — readable instead of blank.
func labeledValue(tr i18n.Translator, kind string, value any) string {
	id := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
	if id != "" {
		if message, ok := tr.Lookup("label." + kind + "." + id); ok {
			return message
		}
	}
	return humanizeLabel(value)
}

// resolveMsg renders one application message reference through the active
// catalogue: its key names the sentence and each argument is either a resolved
// value (never translated) or a nested catalogue key (a translated
// placeholder), so a composed sentence carries no English of its own.
func resolveMsg(tr i18n.Translator, m application.Msg) string {
	if m.Key == "" {
		return ""
	}
	args := make([]any, len(m.Args))
	for i, arg := range m.Args {
		if arg.Key != "" {
			args[i] = tr.T(arg.Key)
			continue
		}
		args[i] = arg.Value
	}
	return tr.Tf(m.Key, args...)
}

// stateBadge is the state_badge partial's payload: the badge class (the raw
// state) and the audience-resolved visible label.
type stateBadge struct {
	State string
	Label string
}

// newStateBadge resolves one state for one audience through the single label
// source.
func newStateBadge(tr i18n.Translator, value any, requester bool) stateBadge {
	state := fmt.Sprint(value)
	return stateBadge{State: state, Label: ticketStateLabel(tr, state, requester)}
}

// resolveStateBadge is the partial's entry point. The staff list call sites
// pass a raw state and get the staff label; an already-resolved payload (the
// requester list and detail) passes through unchanged, so every badge keeps
// one rendering path.
func resolveStateBadge(tr i18n.Translator, value any) stateBadge {
	if badge, ok := value.(stateBadge); ok {
		return badge
	}
	return newStateBadge(tr, value, false)
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

// workflowTypeLabel names a workflow step kind the way a non-technical admin
// reads it. It is the single source for the builder's kind picker, the step
// card title, and the editor heading. It names a choice; checklistStepName in
// handlers_tickets.go names the outcome the same step produces.
func workflowTypeLabel(t domain.StepType) string {
	switch t {
	case domain.StepAssignToDesk:
		return "Send to a desk"
	case domain.StepForm:
		return "Ask for information"
	case domain.StepManualTask:
		return "Give the agent a task"
	case domain.StepResolve:
		return "Mark the ticket resolved"
	case domain.StepClose:
		return "Close the ticket"
	default:
		return "Workflow step"
	}
}

// workflowTypeHelp is the one-line plain-language description under the
// selected kind. It covers every kind the picker offers.
func workflowTypeHelp(t domain.StepType) string {
	switch t {
	case domain.StepAssignToDesk:
		return "Send the ticket to a desk."
	case domain.StepForm:
		return "Ask the requester or the agent for information."
	case domain.StepManualTask:
		return "The agent follows your instructions to complete the work."
	case domain.StepResolve:
		return "Mark the ticket resolved automatically."
	case domain.StepClose:
		return "Close the ticket automatically."
	default:
		return "Configure this workflow step."
	}
}

// workflowFieldRequiredLabel is the builder's copy for a form field's required
// control. Every kind except a checkbox says "Required" ("you must answer"); a
// checkbox says "Must be ticked" because the box has exactly one answer — yes —
// and "Required" would read as "you must answer" (#317).
func workflowFieldRequiredLabel(kind domain.FieldKind) string {
	if kind == domain.FieldCheckbox {
		return "Must be ticked"
	}
	return "Required"
}

// asStepType accepts a domain.StepType (a step's own field) or a string
// literal, so a template can name a kind directly in the picker without a
// second copy of its label.
func asStepType(v any) domain.StepType {
	switch t := v.(type) {
	case domain.StepType:
		return t
	case string:
		return domain.StepType(t)
	default:
		return ""
	}
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
	"workflowTypeLabel":     func(v any) string { return workflowTypeLabel(asStepType(v)) },
	"workflowTypeHelp":      func(v any) string { return workflowTypeHelp(asStepType(v)) },
	"workflowFieldRequiredLabel": func(v any) string {
		return workflowFieldRequiredLabel(domain.FieldKind(fmt.Sprint(v)))
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

// templateTree is one locale's parsed template tree: the per-page sets and the
// shared fragment set, both with the locale-bound message helpers installed.
// Templates are parsed once per shipped locale because a template func cannot
// read per-request state: {{t}} must be bound to a locale at parse time, so the
// renderer selects the whole tree per request instead of mutating funcs.
type templateTree struct {
	pages     map[string]pageSet
	fragments *template.Template
}

// InterfaceLanguageSource answers the instance's active interface language
// (issue #335, S15). The renderer asks it once per render and selects the
// matching parsed tree, so a single instance-wide setting — not a cookie, not
// a per-user preference — decides both <html lang> and every catalogue string.
// application.SettingsStore satisfies it.
type InterfaceLanguageSource interface {
	GetInterfaceLanguage(ctx context.Context) (string, error)
}

// Renderer owns the parsed template sets (D6): one set per page (shell +
// partials + page content) and one shared fragment set for HX swaps, parsed
// once per shipped locale.
type Renderer struct {
	trees map[string]*templateTree
	// fallback is the default locale's tree: it answers a renderer built
	// without a language source (the test harness and the fixture sets) and
	// any locale a tree was not parsed for.
	fallback *templateTree
	language InterfaceLanguageSource
	// languageFailures deduplicates the renderer's own language read failure
	// (R4-002) when no request middleware stamped one.
	languageFailures languageReadLog
	// fragments is the DEFAULT locale's shared fragment set, for a caller
	// that renders one fragment outside a request (the metrics
	// not-enough-history fixture). Render itself always resolves the active
	// locale and never reads this field, so it cannot bypass the instance
	// language.
	fragments *template.Template
}

// NewRenderer parses the embedded template tree (web/templates) for every
// shipped locale, without a language source: rendering answers the default
// locale. Parse errors are fatal: templates are static assets, a broken
// template is a build error.
func NewRenderer() *Renderer {
	r, err := newRendererWithLanguage(templates.FS, nil)
	if err != nil {
		panic("httpadapter: parse templates: " + err.Error())
	}
	return r
}

// NewRendererWithLanguage parses the embedded template tree for every shipped
// locale and resolves the active one per request through src — the composition
// root's path, so the instance setting decides the language of every page.
func NewRendererWithLanguage(src InterfaceLanguageSource) *Renderer {
	r, err := newRendererWithLanguage(templates.FS, src)
	if err != nil {
		panic("httpadapter: parse templates: " + err.Error())
	}
	return r
}

// NewRendererWith parses an arbitrary template tree (test harness: fixture
// sets, golden regeneration) for the default locale only. A renderer built this
// way answers the default locale for every request.
func NewRendererWith(fsys fs.FS) (*Renderer, error) {
	tree, err := parseTemplateTree(fsys, i18n.Default)
	if err != nil {
		return nil, err
	}
	return &Renderer{trees: map[string]*templateTree{i18n.Default: tree}, fallback: tree, fragments: tree.fragments}, nil
}

// newRendererWithLanguage parses fsys once per shipped locale and wires src as
// the per-request language resolver.
func newRendererWithLanguage(fsys fs.FS, src InterfaceLanguageSource) (*Renderer, error) {
	trees := make(map[string]*templateTree, len(i18n.Supported()))
	for _, locale := range i18n.Supported() {
		tree, err := parseTemplateTree(fsys, locale)
		if err != nil {
			return nil, fmt.Errorf("locale %s: %w", locale, err)
		}
		trees[locale] = tree
	}
	fallback, ok := trees[i18n.Default]
	if !ok {
		return nil, fmt.Errorf("the default locale %s has no parsed template tree", i18n.Default)
	}
	return &Renderer{trees: trees, fallback: fallback, language: src, fragments: fallback.fragments}, nil
}

// templateFuncsFor is the shared helper set with the locale-bound message
// helpers installed: t answers a catalogue key for that locale, and locale
// answers the code the shell declares in its lang attribute.
func templateFuncsFor(locale string) template.FuncMap {
	translator := i18n.New(locale)
	funcs := make(template.FuncMap, len(templateFuncs)+16)
	for name, fn := range templateFuncs {
		funcs[name] = fn
	}
	funcs["t"] = translator.T
	funcs["locale"] = func() string { return translator.Locale() }
	// tf interpolates the catalogue sentence with the values the surface passes
	// in, so no Go or template call site owns a fragment of copy.
	funcs["tf"] = translator.Tf
	// msg renders one application message reference (key + args) in the active
	// catalogue: the timeline narrative is decided in Go and translated here.
	funcs["msg"] = func(m application.Msg) string { return resolveMsg(translator, m) }
	// The label helpers resolve a domain identifier through the catalogue and
	// keep humanize only for an identifier no catalogue names.
	funcs["stateLabel"] = func(value any) string { return labeledValue(translator, "state", value) }
	funcs["priorityLabel"] = func(value any) string { return labeledValue(translator, "priority", value) }
	funcs["roleLabel"] = func(value any) string { return labeledValue(translator, "role", value) }
	funcs["slaLabel"] = func(value any) string { return labeledValue(translator, "sla", value) }
	funcs["stateBadge"] = func(value any) stateBadge { return resolveStateBadge(translator, value) }
	funcs["audienceStateBadge"] = func(value any, requester bool) stateBadge {
		return newStateBadge(translator, value, requester)
	}
	funcs["requesterStateBadge"] = func(value any) stateBadge {
		return newStateBadge(translator, value, true)
	}
	return funcs
}

// parseTemplateTree builds one locale's per-page sets and shared fragment set
// from fsys. Layout: shell roots (base.html, auth.html) at the root; pages under
// pages/ (each defines "content"); swap fragments under partials/.
func parseTemplateTree(fsys fs.FS, locale string) (*templateTree, error) {
	funcs := templateFuncsFor(locale)

	fragments, err := template.New("").Funcs(funcs).ParseFS(fsys, "partials/*.html")
	if err != nil {
		return nil, err
	}

	base, err := template.New("base.html").Funcs(funcs).ParseFS(fsys, "base.html")
	if err != nil {
		return nil, err
	}
	shells := map[string]*template.Template{"base.html": base}
	if _, err := fs.Stat(fsys, "auth.html"); err == nil {
		auth, err := template.New("auth.html").Funcs(funcs).ParseFS(fsys, "auth.html")
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
	return &templateTree{pages: pages, fragments: fragments}, nil
}

// activeLocale resolves the instance's interface language for one request. The
// request middleware stamps the language once per request (issue #335), so this
// is usually a context read; a renderer used without that middleware falls back
// to its own bounded settings read. Either way a failure must not cost the
// page: the default locale answers it.
func (r *Renderer) activeLocale(ctx context.Context) string {
	return requestInterfaceLanguage(ctx, r.language, &r.languageFailures)
}

// treeFor returns the parsed tree for locale, falling back to the default tree
// when no set was parsed for it.
func (r *Renderer) treeFor(locale string) *templateTree {
	if tree, ok := r.trees[locale]; ok {
		return tree
	}
	return r.fallback
}

// Render executes the page (full request) or the fragment (HX-Request) with
// the given status (D6), in the instance's active language. The response is
// buffered so template errors cannot corrupt a partial write; a render failure
// degrades to a generic 500.
func (r *Renderer) Render(w http.ResponseWriter, rq *http.Request, page, fragment string, data any, status int) {
	tree := r.treeFor(r.activeLocale(rq.Context()))
	var buf bytes.Buffer
	var err error

	if rq.Header.Get("HX-Request") != "" {
		if fragment != "" {
			err = tree.fragments.ExecuteTemplate(&buf, fragment, data)
		} else {
			// Natural fragment: the page's own content block (login/setup forms).
			ps, ok := tree.pages[page]
			if !ok {
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}
			err = ps.tmpl.ExecuteTemplate(&buf, "content", data)
		}
	} else {
		ps, ok := tree.pages[page]
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
