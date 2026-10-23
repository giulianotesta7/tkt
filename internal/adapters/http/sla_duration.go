package httpadapter

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Written SLA durations (issue #395): the category screen and the Settings
// panel take one explicit h/m/s duration per target, such as 1h 30m or
// 45m 20s, instead of three numeric boxes. The h/m/s field parser
// (parseSLATargets) stays as it was, so an older form that posts those fields
// keeps working; the written fields take over only when they are submitted.

var slaDurationPart = regexp.MustCompile(`([0-9]+)\s*([hms])`)

// slaDurationMilestones names the two written fields of each priority row.
var slaDurationMilestones = []string{"first_response", "resolve"}

// hasSLADurations reports whether the post carries any written duration. Once
// one is present all eight must validate: a partial written form never falls
// back to h/m/s fields submitted alongside it.
func hasSLADurations(r *http.Request) bool {
	for _, p := range slaGridPriorities {
		for _, milestone := range slaDurationMilestones {
			if _, ok := r.PostForm[milestone+"_duration_"+string(p)]; ok {
				return true
			}
		}
	}
	return false
}

// parseSLADuration reads one written duration into seconds. Each of h, m and
// s may appear once, in any order, with whole numbers only; anything else,
// including an empty value or a total beyond the int range, is refused.
func parseSLADuration(raw string) (int, bool) {
	text := strings.ToLower(strings.TrimSpace(raw))
	matches := slaDurationPart.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 || len(matches) > 3 {
		return 0, false
	}
	total, last := 0, 0
	used := map[string]bool{}
	for _, match := range matches {
		if strings.TrimSpace(text[last:match[0]]) != "" {
			return 0, false
		}
		unit := text[match[4]:match[5]]
		if used[unit] {
			return 0, false
		}
		used[unit] = true
		number, err := strconv.Atoi(text[match[2]:match[3]])
		factor := map[string]int{"h": 3600, "m": 60, "s": 1}[unit]
		if err != nil || number > (int(^uint(0)>>1)-total)/factor {
			return 0, false
		}
		total += number * factor
		last = match[1]
	}
	return total, strings.TrimSpace(text[last:]) == ""
}

// parseSLADurations builds the four-priority matrix from the written fields.
// It reports false when any field does not parse; the matrix it returns then
// carries 0 for that target and must not be saved. The 60-second floor stays
// the application service's rule.
func parseSLADurations(r *http.Request) ([]domain.SLAPolicy, bool) {
	policies := make([]domain.SLAPolicy, 0, len(slaGridPriorities))
	valid := true
	for _, p := range slaGridPriorities {
		response, ok := parseSLADuration(r.PostFormValue("first_response_duration_" + string(p)))
		resolve, otherOK := parseSLADuration(r.PostFormValue("resolve_duration_" + string(p)))
		valid = valid && ok && otherOK
		policies = append(policies, domain.SLAPolicy{Priority: p, FirstResponseSeconds: response, ResolveSeconds: resolve})
	}
	return policies, valid
}

// slaDurationValues keeps the eight written fields exactly as submitted, so a
// rejected save re-renders what the administrator typed rather than the
// partially parsed matrix.
func slaDurationValues(r *http.Request) map[string]string {
	values := make(map[string]string, 2*len(slaGridPriorities))
	for _, p := range slaGridPriorities {
		for _, milestone := range slaDurationMilestones {
			name := milestone + "_duration_" + string(p)
			values[name] = r.PostFormValue(name)
		}
	}
	return values
}

// slaDurationText renders a target as its canonical written form: 1h 30m,
// never 1h 30m 0s, and 0s for an empty target.
func slaDurationText(units slaTargetUnits) string {
	parts := make([]string, 0, 3)
	for _, part := range []struct {
		number int
		unit   string
	}{{units.Hours, "h"}, {units.Minutes, "m"}, {units.Seconds, "s"}} {
		if part.number != 0 {
			parts = append(parts, strconv.Itoa(part.number)+part.unit)
		}
	}
	if len(parts) == 0 {
		return "0s"
	}
	return strings.Join(parts, " ")
}

// slaDurationField is one written field as the grid renders it: its text, the
// normalized reading below it, and the catalogue key of its error, if any.
type slaDurationField struct{ Text, Reading, Error string }

// DurationField keeps the exact submitted text on rejected saves, while GET
// and golden fixtures derive canonical text from their existing policy units.
func (grid slaGridData) DurationField(name string, units slaTargetUnits) slaDurationField {
	text := slaDurationText(units)
	if raw, ok := grid.DurationValues[name]; ok {
		text = raw
	}
	field := slaDurationField{Text: text}
	seconds, ok := parseSLADuration(text)
	if !ok {
		field.Error = "sla.grid.duration_invalid"
		return field
	}
	field.Reading = strings.NewReplacer("h", " h", "m", " min", "s", " s").Replace(slaDurationText(slaUnits(seconds)))
	if seconds < application.MinSLATargetSeconds {
		field.Error = "sla.grid.duration_minimum"
	}
	return field
}

// DurationMaximum is the largest total the browser accepts, matching the
// server's int range so client and server refuse the same overflow.
func (grid slaGridData) DurationMaximum() string { return strconv.Itoa(int(^uint(0) >> 1)) }
