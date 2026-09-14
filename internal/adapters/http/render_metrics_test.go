package httpadapter

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
)

// svgTags returns every opening tag of the given kind inside an SVG fragment.
func svgTags(t *testing.T, svg, tag string) []string {
	t.Helper()
	re := regexp.MustCompile("<" + tag + `\b[^>]*>`)
	return re.FindAllString(svg, -1)
}

// svgTagAttr extracts one attribute value from a single opening tag.
func svgTagAttr(t *testing.T, tag, name string) string {
	t.Helper()
	re := regexp.MustCompile(name + `="([^"]*)"`)
	m := re.FindStringSubmatch(tag)
	if m == nil {
		t.Fatalf("tag %q has no %s attribute", tag, name)
	}
	return m[1]
}

func lineChartWeeks() []application.TicketMetricsWeek {
	return []application.TicketMetricsWeek{
		{Start: time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC), Created: 3, Resolved: 2},
		{Start: time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC), Created: 2, Resolved: 2, Partial: true},
	}
}

func TestNiceMetricsScale(t *testing.T) {
	for _, tc := range []struct{ max, step, axis int }{
		{1, 1, 1},
		{4, 1, 4},
		{5, 2, 6},
		{99, 25, 100},
		{4_000_001, 1_000_000, 5_000_000},
	} {
		step, axis := niceMetricsScale(tc.max)
		if step != tc.step || axis != tc.axis {
			t.Fatalf("niceMetricsScale(%d) = %d/%d, want %d/%d", tc.max, step, axis, tc.step, tc.axis)
		}
	}
}

// Created is a continuous solid polyline, resolved a dashed polyline, and the
// two series use different marker shapes (circle vs square) with semantic
// classes.
func TestMetricsLineChartSeriesAndMarkers(t *testing.T) {
	svg := string(metricsLineChart(lineChartWeeks()))
	if !strings.Contains(svg, `<polyline class="metrics-line metrics-created"`) {
		t.Fatalf("created series must be a continuous polyline: %s", svg)
	}
	if !strings.Contains(svg, `<polyline class="metrics-line metrics-resolved"`) {
		t.Fatalf("resolved series must be a polyline: %s", svg)
	}
	for _, tag := range svgTags(t, svg, "polyline") {
		if strings.Contains(tag, "metrics-resolved") && strings.Contains(tag, "stroke-dasharray") {
			t.Fatalf("dashing must come from the semantic CSS class, not an inline attribute: %s", tag)
		}
	}
	circles := 0
	for _, tag := range svgTags(t, svg, "circle") {
		if strings.Contains(tag, "metrics-marker-created") {
			circles++
		}
	}
	if circles != 2 {
		t.Fatalf("created series must draw one circle marker per week, got %d: %s", circles, svg)
	}
	rects := 0
	for _, tag := range svgTags(t, svg, "rect") {
		if strings.Contains(tag, "metrics-marker-resolved") {
			rects++
		}
	}
	if rects != 2 {
		t.Fatalf("resolved series must draw one square marker per week, got %d: %s", rects, svg)
	}
	if !strings.Contains(svg, `role="img"`) || !strings.Contains(svg, "aria-label") || !strings.Contains(svg, "Tickets") || !strings.Contains(svg, `class="metrics-axis"`) || !strings.Contains(svg, `class="metrics-gridline"`) {
		t.Fatalf("chart must expose an accessible image with labelled axes: %s", svg)
	}
}

// Partial weeks carry a trailing asterisk on the week label; complete weeks do not.
func TestMetricsLineChartPartialWeekMarker(t *testing.T) {
	svg := string(metricsLineChart(lineChartWeeks()))
	if !strings.Contains(svg, `class="metrics-chart-label">Mar 2</text>`) {
		t.Fatalf("complete week label must not be starred: %s", svg)
	}
	if !strings.Contains(svg, `class="metrics-chart-label">Mar 9*</text>`) {
		t.Fatalf("partial week label must append an asterisk: %s", svg)
	}
}

// Value labels never collide: when the series meet at a point the labels move
// to deterministic opposite sides of their own points, and they stay clamped
// inside the viewBox in every case.
func TestMetricsLineChartValueLabelCollisionOffsets(t *testing.T) {
	for _, tc := range []struct {
		name              string
		created, resolved int
	}{
		{name: "equal values", created: 5, resolved: 5},
		{name: "created above", created: 6, resolved: 5},
		{name: "resolved above", created: 5, resolved: 6},
		{name: "far apart", created: 8, resolved: 1},
		{name: "max on axis", created: 4, resolved: 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			weeks := []application.TicketMetricsWeek{
				{Start: time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC), Created: tc.created, Resolved: tc.resolved},
			}
			svg := string(metricsLineChart(weeks))
			m := regexp.MustCompile(`viewBox="0 0 \d+ (\d+)"`).FindStringSubmatch(svg)
			if m == nil {
				t.Fatalf("no viewBox: %s", svg)
			}
			viewHeight, _ := strconv.ParseFloat(m[1], 64)
			createdY := chartValueLabelY(t, svg, "metrics-chart-value-created")
			resolvedY := chartValueLabelY(t, svg, "metrics-chart-value-resolved")
			createdPointY := seriesPointY(t, svg, "metrics-created")
			resolvedPointY := seriesPointY(t, svg, "metrics-resolved")
			if createdY < 12 || createdY > viewHeight-24 {
				t.Fatalf("created label y %.1f outside clamped range in viewBox height %.0f: %s", createdY, viewHeight, svg)
			}
			if resolvedY < 12 || resolvedY > viewHeight-24 {
				t.Fatalf("resolved label y %.1f outside clamped range in viewBox height %.0f: %s", resolvedY, viewHeight, svg)
			}
			// The two value labels must not sit on top of each other.
			if abs(createdY-resolvedY) < 12 {
				t.Fatalf("value labels overlap: created %.1f resolved %.1f: %s", createdY, resolvedY, svg)
			}
			// When the points are closer than one label height the labels take
			// deterministic opposite sides; otherwise each label stays above
			// its own point.
			if abs(createdPointY-resolvedPointY) < 24 {
				if createdPointY <= resolvedPointY {
					if createdY >= createdPointY {
						t.Fatalf("created label must sit above its point: label %.1f point %.1f: %s", createdY, createdPointY, svg)
					}
					if resolvedY <= resolvedPointY {
						t.Fatalf("resolved label must sit below its point: label %.1f point %.1f: %s", resolvedY, resolvedPointY, svg)
					}
				} else {
					if createdY <= createdPointY {
						t.Fatalf("created label must sit below its point: label %.1f point %.1f: %s", createdY, createdPointY, svg)
					}
					if resolvedY >= resolvedPointY {
						t.Fatalf("resolved label must sit above its point: label %.1f point %.1f: %s", resolvedY, resolvedPointY, svg)
					}
				}
			} else {
				if createdY >= createdPointY {
					t.Fatalf("separated points keep the created label above: label %.1f point %.1f: %s", createdY, createdPointY, svg)
				}
				if resolvedY >= resolvedPointY {
					t.Fatalf("separated points keep the resolved label above: label %.1f point %.1f: %s", resolvedY, resolvedPointY, svg)
				}
			}
		})
	}
}

// Zero points may omit their value label; the totals line and View data table
// stay visible in the template.
func TestMetricsLineChartOmitsZeroValueLabels(t *testing.T) {
	svg := string(metricsLineChart([]application.TicketMetricsWeek{
		{Start: time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)},
	}))
	if strings.Contains(svg, "metrics-chart-value") {
		t.Fatalf("zero values must omit value labels: %s", svg)
	}
	if !strings.Contains(svg, "metrics-marker-created") || !strings.Contains(svg, "metrics-marker-resolved") {
		t.Fatalf("zero points still render markers on the zero baseline: %s", svg)
	}
}

func chartValueLabelY(t *testing.T, svg, class string) float64 {
	t.Helper()
	re := regexp.MustCompile(`<text [^>]*class="metrics-chart-value ` + class + `"[^>]*>`)
	tag := re.FindString(svg)
	if tag == "" {
		t.Fatalf("no %s value label: %s", class, svg)
	}
	y, err := strconv.ParseFloat(svgTagAttr(t, tag, "y"), 64)
	if err != nil {
		t.Fatalf("bad y in %s: %s", tag, err)
	}
	return y
}

func seriesPointY(t *testing.T, svg, class string) float64 {
	t.Helper()
	re := regexp.MustCompile(`<polyline class="metrics-line ` + class + `" points="([^"]*)"`)
	m := re.FindStringSubmatch(svg)
	if m == nil {
		t.Fatalf("no %s polyline: %s", class, svg)
	}
	parts := strings.Fields(strings.TrimSpace(m[1]))
	if len(parts) == 0 {
		t.Fatalf("empty points: %s", svg)
	}
	xy := strings.Split(parts[0], ",")
	y, err := strconv.ParseFloat(xy[1], 64)
	if err != nil {
		t.Fatalf("bad point %q: %s", parts[0], err)
	}
	return y
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// Age chart: horizontal accent bars in the fixed ranges, x axis starting at
// zero with integer ticks and gridlines, a visible Pending tickets axis title,
// left labels and end-of-bar counts.
func TestMetricsAgeChartLayout(t *testing.T) {
	svg := string(metricsAgeChart([]application.TicketMetricsBucket{
		{Label: "0–2 days", Count: 0},
		{Label: "3–7 days", Count: 2},
		{Label: "8–14 days", Count: 5},
		{Label: ">14 days", Count: 1},
	}))
	if !strings.Contains(svg, "Pending tickets") {
		t.Fatalf("age chart must show the Pending tickets axis title: %s", svg)
	}
	if !strings.Contains(svg, "aria-label") {
		t.Fatalf("age chart must stay accessible: %s", svg)
	}
	bars := 0
	for _, tag := range svgTags(t, svg, "rect") {
		if strings.Contains(tag, "metrics-bar") && !strings.Contains(tag, "metrics-bar-unassigned") {
			bars++
		}
	}
	if bars != 4 {
		t.Fatalf("one accent bar per fixed age range, got %d: %s", bars, svg)
	}
	// X axis begins at zero with at least the 0 and max integer ticks.
	zeroTick := false
	ticks := 0
	for _, tag := range svgTags(t, svg, "text") {
		if strings.Contains(tag, "metrics-axis-label") {
			ticks++
			if svgTextContent(t, svg, tag) == "0" {
				zeroTick = true
			}
		}
	}
	if ticks < 2 || !zeroTick {
		t.Fatalf("x axis must start at zero with integer ticks: %s", svg)
	}
	for _, want := range []string{"0–2 days", "3–7 days", "8–14 days", "&gt;14 days", ">2<", ">5<", ">1<"} {
		if !strings.Contains(svg, want) {
			t.Fatalf("age chart must render label/count %q: %s", want, svg)
		}
	}
}

// svgTextContent returns the inner text of the <text> element opened by tag.
func svgTextContent(t *testing.T, svg, tag string) string {
	t.Helper()
	start := strings.Index(svg, tag)
	if start < 0 {
		t.Fatalf("tag not in svg: %s", tag)
	}
	rest := svg[start+len(tag):]
	end := strings.Index(rest, "</text>")
	if end < 0 {
		t.Fatalf("unclosed text tag: %s", tag)
	}
	return rest[:end]
}

// Workload bars share the age chart's axis contract: an x axis that starts
// at zero with integer ticks and gridlines, a visible Pending tickets axis
// title, left labels, end-of-bar counts, and the neutral class on the
// genuinely unassigned row.
func TestMetricsWorkloadChartAxisLayout(t *testing.T) {
	svg := string(metricsWorkloadChart([]application.TicketMetricsWorkload{
		{Label: "Alice Admin", Count: 2},
		{Label: "Unassigned", Count: 3, Unassigned: true},
	}, "Pending workload"))
	if !strings.Contains(svg, "Pending tickets") {
		t.Fatalf("workload chart must show the Pending tickets axis title: %s", svg)
	}
	if !strings.Contains(svg, "X axis from 0 to") {
		t.Fatalf("workload chart must describe a zero-based x axis: %s", svg)
	}
	zeroTick := false
	ticks := 0
	for _, tag := range svgTags(t, svg, "text") {
		if strings.Contains(tag, "metrics-axis-label") {
			ticks++
			if svgTextContent(t, svg, tag) == "0" {
				zeroTick = true
			}
		}
	}
	if ticks < 2 || !zeroTick {
		t.Fatalf("workload x axis must start at zero with integer ticks: %s", svg)
	}
	bars := 0
	unassigned := 0
	for _, tag := range svgTags(t, svg, "rect") {
		if !strings.Contains(tag, "metrics-bar") {
			continue
		}
		bars++
		if strings.Contains(tag, "metrics-bar-unassigned") {
			unassigned++
		}
	}
	if bars != 2 || unassigned != 1 {
		t.Fatalf("one accent bar per row with exactly one neutral unassigned bar, got %d bars / %d neutral: %s", bars, unassigned, svg)
	}
}

// Histogram: one adjacent rectangular bar per equal-width bin with identical
// pixel width and zero gap, including zero-height bins; boundary ticks come
// from LowerDays plus the final UpperDays.
func TestMetricsHistogramChartBarsAndBoundaries(t *testing.T) {
	svg := string(metricsHistogramChart([]application.TicketMetricsBucket{
		{Label: "0–<5 days", Count: 3, LowerDays: 0, UpperDays: 5},
		{Label: "5–<10 days", Count: 0, LowerDays: 5, UpperDays: 10},
		{Label: "10–<15 days", Count: 1, LowerDays: 10, UpperDays: 15},
	}))
	if !strings.Contains(svg, "Resolution time (days)") {
		t.Fatalf("histogram must show the Resolution time (days) axis title: %s", svg)
	}
	if !strings.Contains(svg, ">Tickets</text>") {
		t.Fatalf("histogram must show the Tickets y axis title: %s", svg)
	}
	var widths []string
	for _, tag := range svgTags(t, svg, "rect") {
		if strings.Contains(tag, "metrics-hist-bar") {
			widths = append(widths, svgTagAttr(t, tag, "width"))
		}
	}
	if len(widths) != 3 {
		t.Fatalf("one bar per bin including zero-height bins, got %d: %s", len(widths), svg)
	}
	for _, w := range widths {
		if w != widths[0] {
			t.Fatalf("histogram bars must share one pixel width, got %v: %s", widths, svg)
		}
	}
	for _, boundary := range []string{"0", "5", "10", "15"} {
		found := false
		for _, tag := range svgTags(t, svg, "text") {
			if strings.Contains(tag, "metrics-axis-label") && svgTextContent(t, svg, tag) == boundary {
				found = true
			}
		}
		if !found {
			t.Fatalf("boundary tick %q missing: %s", boundary, svg)
		}
	}
}

// No valid samples means no histogram at all; the template shows the empty
// state instead of an empty axis frame.
func TestMetricsHistogramChartWithoutBinsIsEmpty(t *testing.T) {
	if got := string(metricsHistogramChart(nil)); got != "" {
		t.Fatalf("nil bins must render no histogram svg, got %s", got)
	}
}

// Workload labels are user-controlled agent/category names, so any markup in
// them must be HTML-escaped before the fragment is wrapped in template.HTML.
func TestMetricsWorkloadChartEscapesUserControlledLabels(t *testing.T) {
	svg := string(metricsWorkloadChart([]application.TicketMetricsWorkload{
		{Label: "<script>alert(1)</script>", Count: 1},
	}, "Pending workload"))
	if strings.Contains(svg, "<script>") {
		t.Fatalf("workload label must be HTML-escaped: %s", svg)
	}
	if !strings.Contains(svg, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatalf("workload label must survive as escaped text: %s", svg)
	}
}
