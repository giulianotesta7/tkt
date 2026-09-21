package httpadapter

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// SettingsHandlers expose the instance configuration settings (the Settings
// screen): GET /settings renders the page, POST /settings/appearance persists
// the internal-comment background color, POST /settings/sla persists the
// SLA panel (enabled flag, warning percent, default target matrix), and
// POST /settings/calendar persists the working calendar. All routes are gated
// on CapManageSettings (admin/root) at the HTTP boundary; the application
// service re-enforces the same capability before mutating anything.
//
// The gate is CapManageSettings and NOT CapManageUsers: the two coincide for
// admin/root today, but they mean different things, and reusing the
// user-management grant would silently widen every future settings route to
// whatever managing people comes to mean. The rail link follows the same
// capability through pageData.CanManageSettings.
type SettingsHandlers struct {
	settings *application.SettingsService
	sla      *application.SLAService
	slaStore application.SLAStore
	appSet   application.SettingsStore
	renderer *Renderer
}

// NewSettingsHandlers wires the settings routes against the appearance and
// SLA use cases, the SLA store port the SLA panel reads its default matrix
// through, and the settings store port it reads the SLA flags through.
func NewSettingsHandlers(settings *application.SettingsService, sla *application.SLAService, slaStore application.SLAStore, appSet application.SettingsStore, renderer *Renderer) *SettingsHandlers {
	return &SettingsHandlers{settings: settings, sla: sla, slaStore: slaStore, appSet: appSet, renderer: renderer}
}

// Register mounts the settings routes.
func (h *SettingsHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /settings", h.index)
	mux.HandleFunc("POST /settings/appearance", h.updateAppearance)
	mux.HandleFunc("POST /settings/sla", h.updateSLA)
	mux.HandleFunc("POST /settings/calendar", h.updateCalendar)
}

// settingsIndexData is the appearance, SLA, and working-calendar panel
// payload; Error carries a rejected-update message (422 re-render).
type settingsIndexData struct {
	pageData
	Error   string
	Current string
	Colors  []appearanceOption

	SLAEnabled        bool
	SLAWarningPercent int
	SLAGrid           slaGridData

	CalendarDays     []calendarDayOption
	CalendarStart    string
	CalendarEnd      string
	CalendarTimezone string
}

// calendarDayOption is one selectable working day, in storage order
// (ISO 1=Monday … 7=Sunday), which is also the order the panel offers.
type calendarDayOption struct {
	Value   int
	Label   string
	Checked bool
}

// calendarWeekDays is the ISO week in storage order (1=Monday … 7=Sunday):
// the order the sla_calendar_days settings key stores and the order the
// panel offers the weekdays in.
var calendarWeekDays = []time.Weekday{
	time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday,
	time.Saturday, time.Sunday,
}

// calendarDayOptions builds the panel's seven checkboxes from a calendar's
// working days: every ISO day is always offered, with membership marked.
func calendarDayOptions(days []time.Weekday) []calendarDayOption {
	selected := make(map[time.Weekday]bool, len(days))
	for _, d := range days {
		selected[d] = true
	}
	return calendarDayOptionsMarked(func(isoDay int) bool { return selected[calendarWeekDays[isoDay-1]] })
}

// calendarDayOptionsFromValues marks the seven checkboxes from the raw
// submitted sla_calendar_days values ("1".."7"; unknown values simply do
// not match), so a rejected save echoes what the administrator checked.
func calendarDayOptionsFromValues(values []string) []calendarDayOption {
	submitted := make(map[string]bool, len(values))
	for _, v := range values {
		submitted[v] = true
	}
	return calendarDayOptionsMarked(func(isoDay int) bool { return submitted[strconv.Itoa(isoDay)] })
}

// calendarDayOptionsMarked renders the seven ISO options in storage order,
// delegating the checked mark to the caller's membership test.
func calendarDayOptionsMarked(picked func(isoDay int) bool) []calendarDayOption {
	options := make([]calendarDayOption, 0, len(calendarWeekDays))
	for isoDay, d := range calendarWeekDays {
		options = append(options, calendarDayOption{
			Value:   isoDay + 1,
			Label:   d.String(),
			Checked: picked(isoDay + 1),
		})
	}
	return options
}

// appearanceOption pairs a selectable color with its display label.
type appearanceOption struct {
	Value string
	Label string
}

// appearanceOptions lists the selectable internal-comment background
// colors in the canonical order (green is reserved for a future comment
// type and is not offered).
func appearanceOptions() []appearanceOption {
	labels := map[string]string{
		application.DefaultInternalCommentBg: "Blue",
		"#EFE9FB":                            "Violet",
		"#FFF6DC":                            "Yellow",
	}
	colors := application.AllowedInternalCommentBg()
	out := make([]appearanceOption, 0, len(colors))
	for _, c := range colors {
		out = append(out, appearanceOption{Value: c, Label: labels[c]})
	}
	return out
}

func (h *SettingsHandlers) index(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageSettings) {
		return
	}
	current, err := h.settings.GetAppearance(r.Context())
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	data := settingsIndexData{
		pageData: pageDataFrom(r, "settings"),
		Current:  current,
		Colors:   appearanceOptions(),
	}
	if err := h.loadSLA(r, &data); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if err := h.loadCalendar(r, &data); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	// The shell CSS must carry the same color the panel shows; stamp the
	// authoritative read into the page data.
	data.InternalCommentBg = current
	h.renderer.Render(w, r, "settings_index", "", data, http.StatusOK)
}

// loadSLA fills the SLA panel payload from the authoritative reads: the
// enabled flag and warning percent come through the settings store port,
// the default target matrix through the SLA store port (the application
// service exposes no read use case for them).
func (h *SettingsHandlers) loadSLA(r *http.Request, data *settingsIndexData) error {
	enabled, err := h.appSet.GetSLAEnabled(r.Context())
	if err != nil {
		return err
	}
	percent, err := h.appSet.GetSLAWarningPercent(r.Context())
	if err != nil {
		return err
	}
	defaults, err := h.slaStore.ListDefaults(r.Context())
	if err != nil {
		return err
	}
	data.SLAEnabled = enabled
	data.SLAWarningPercent = percent
	data.SLAGrid = slaGridData{Rows: slaPolicyRows(defaults)}
	return nil
}

// updateAppearance persists the chosen color. A rejected color re-renders
// the page with an inline error (422) and changes nothing; success
// redirects 303 back to /settings (HTMX follows it natively).
func (h *SettingsHandlers) updateAppearance(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageSettings) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	err := h.settings.SetInternalCommentBg(r.Context(), *userFromContext(r.Context()), r.Form.Get("internal_comment_bg"))
	if err != nil {
		status, msg := mapError(err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		current, getErr := h.settings.GetAppearance(r.Context())
		if getErr != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		data := settingsIndexData{
			pageData: pageDataFrom(r, "settings"),
			Error:    msg,
			Current:  current,
			Colors:   appearanceOptions(),
		}
		// The SLA and calendar panels are untouched by this POST, so they
		// re-render from the stored values.
		if loadErr := h.loadSLA(r, &data); loadErr != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		if loadErr := h.loadCalendar(r, &data); loadErr != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		data.InternalCommentBg = current
		h.renderer.Render(w, r, "settings_index", "", data, status)
		return
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, "/settings")
}

// updateSLA persists the SLA panel: the enabled flag, the warning percent,
// and the default target matrix. ONE application call applies the whole
// panel — validation runs before any store write and the store commits it in
// one transaction — so a rejected post leaves the configuration unchanged and
// there is nothing to compensate for here. Validation errors are the
// service's, never the handler's: a blank or non-numeric unit parses as 0 so
// the service produces the 422.
func (h *SettingsHandlers) updateSLA(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageSettings) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	actor := *userFromContext(r.Context())
	enabled := r.Form.Get("sla_enabled") == "1"
	percent := slaFormInt(r, "sla_warning_percent")
	policies := parseSLATargets(r)

	// ONE application call applies the whole panel: validation runs before
	// any store write and the store commits it in one transaction, so there
	// is nothing to compensate for here.
	err := h.sla.SetGlobalConfiguration(r.Context(), actor, enabled, percent, policies)
	if err != nil {
		status, msg := mapError(err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		// The appearance panel is untouched by this POST, so it re-renders
		// from the stored value.
		current, getErr := h.settings.GetAppearance(r.Context())
		if getErr != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		data := settingsIndexData{
			pageData: pageDataFrom(r, "settings"),
			Error:    msg,
			// Echo the SUBMITTED values: one bad number must not cost the
			// administrator the other twenty-three fields.
			Current:           current,
			Colors:            appearanceOptions(),
			SLAEnabled:        enabled,
			SLAWarningPercent: percent,
			SLAGrid:           slaGridData{Rows: slaPolicyRows(policies)},
		}
		// The calendar panel is untouched by this POST, so it re-renders
		// from the stored value.
		if loadErr := h.loadCalendar(r, &data); loadErr != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		data.InternalCommentBg = current
		h.renderer.Render(w, r, "settings_index", "", data, status)
		return
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, "/settings")
}

// loadCalendar fills the working-calendar panel payload from the
// authoritative read through the SLA service's Calendar use case: the
// working days decomposed into the seven ISO checkboxes, the window as
// HH:MM clock strings, and the zone's IANA name.
func (h *SettingsHandlers) loadCalendar(r *http.Request, data *settingsIndexData) error {
	cal, err := h.sla.Calendar(r.Context())
	if err != nil {
		return err
	}
	data.CalendarDays = calendarDayOptions(cal.WorkingDays)
	data.CalendarStart = formatClockMinute(cal.StartMinute)
	data.CalendarEnd = formatClockMinute(cal.EndMinute)
	data.CalendarTimezone = cal.Location.String()
	return nil
}

// updateCalendar persists the working calendar (issue #211). The boundary
// owns IANA resolution: the service's port carries an already-resolved
// *time.Location, so an unknown zone name is refused here (the store read
// silently degrades an unknown name to UTC, and a silent degradation must
// never masquerade as a saved setting). An unparseable clock is likewise
// never coerced to 00:00 — midnight is a legal window start, so coercion
// would silently persist a wrong calendar. A rejected save re-renders the
// page (422) with every panel populated — appearance and SLA from storage,
// the calendar echoing the SUBMITTED values — and changes nothing; success
// redirects 303 back to /settings (HTMX follows it natively).
func (h *SettingsHandlers) updateCalendar(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageSettings) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	actor := *userFromContext(r.Context())
	calendar, err := parseCalendarForm(r)
	if err == nil {
		err = h.sla.SetSLACalendar(r.Context(), actor, calendar)
	}
	if err != nil {
		status, msg := mapError(err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		// The appearance and SLA panels are untouched by this POST, so they
		// re-render from the stored values; the calendar panel echoes the
		// SUBMITTED values so one bad field does not cost the rest.
		current, getErr := h.settings.GetAppearance(r.Context())
		if getErr != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		data := settingsIndexData{
			pageData: pageDataFrom(r, "settings"),
			Error:    msg,
			Current:  current,
			Colors:   appearanceOptions(),

			CalendarDays:     calendarDayOptionsFromValues(r.Form["sla_calendar_days"]),
			CalendarStart:    r.Form.Get("sla_calendar_start"),
			CalendarEnd:      r.Form.Get("sla_calendar_end"),
			CalendarTimezone: r.Form.Get("sla_calendar_timezone"),
		}
		if loadErr := h.loadSLA(r, &data); loadErr != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		data.InternalCommentBg = current
		h.renderer.Render(w, r, "settings_index", "", data, status)
		return
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, "/settings")
}

// parseCalendarForm reconstructs the working calendar from the panel's
// fields: the checked ISO day numbers (unknown values ignored, result in
// canonical ISO order), the HH:MM window, and the IANA zone name (empty
// leaves Location nil so the service produces the 422).
func parseCalendarForm(r *http.Request) (domain.SLACalendar, error) {
	submitted := make(map[string]bool, len(r.Form["sla_calendar_days"]))
	for _, v := range r.Form["sla_calendar_days"] {
		submitted[v] = true
	}
	days := make([]time.Weekday, 0, len(calendarWeekDays))
	for isoDay, d := range calendarWeekDays {
		if submitted[strconv.Itoa(isoDay+1)] {
			days = append(days, d)
		}
	}
	start, startOK := parseClockMinute(r.Form.Get("sla_calendar_start"))
	end, endOK := parseClockMinute(r.Form.Get("sla_calendar_end"))
	if !startOK || !endOK {
		return domain.SLACalendar{}, &domain.ValidationError{
			Field:   "sla_calendar",
			Message: "the working calendar needs a start and end time in HH:MM form",
		}
	}
	rawZone := strings.TrimSpace(r.Form.Get("sla_calendar_timezone"))
	if rawZone == "" {
		return domain.SLACalendar{WorkingDays: days, StartMinute: start, EndMinute: end}, nil
	}
	loc, err := time.LoadLocation(rawZone)
	if err != nil {
		return domain.SLACalendar{}, &domain.ValidationError{
			Field:   "sla_calendar",
			Message: fmt.Sprintf("unknown timezone name %q", rawZone),
		}
	}
	return domain.SLACalendar{WorkingDays: days, StartMinute: start, EndMinute: end, Location: loc}, nil
}

// formatClockMinute renders minutes past midnight as an HH:MM clock value.
func formatClockMinute(minute int) string {
	return fmt.Sprintf("%02d:%02d", minute/60, minute%60)
}

// parseClockMinute parses an HH:MM clock value into minutes past midnight.
// A trailing HH:MM:SS seconds part is accepted and IGNORED, because a
// native time input may submit it. Anything else fails: the caller must
// never coerce an unparseable clock to 0, since midnight is a legal window
// start and coercion would silently persist a wrong calendar.
func parseClockMinute(value string) (int, bool) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil || hour < 0 || hour > 23 {
		return 0, false
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil || minute < 0 || minute > 59 {
		return 0, false
	}
	return hour*60 + minute, true
}

// slaFormInt parses one submitted integer, counting a blank or non-numeric
// value as 0 so the application service's own validation produces the
// rejection rather than the parser.
func slaFormInt(r *http.Request, key string) int {
	n, err := strconv.Atoi(strings.TrimSpace(r.Form.Get(key)))
	if err != nil {
		return 0
	}
	return n
}
