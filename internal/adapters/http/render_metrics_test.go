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
