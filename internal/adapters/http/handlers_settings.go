package httpadapter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/internal/i18n"
)

// SettingsHandlers expose the instance configuration settings (issue #383,
// S1/S2/S4). The Settings surface is one section per URL under a left
// sub-navigation: GET /settings redirects to /settings/organization, and
// GET /settings/{section} renders exactly that section for the sections
// organization, language, tickets (appearance and the SLA panel), email (root
// only, as the capability already dictated), security (the instance password
// policy) and calendar (working hours). An
// unknown section is a 404.
//
// Every existing POST /settings/* endpoint keeps its path, validation and
// capability: POST /settings/appearance, POST /settings/sla and
// POST /settings/hold-auto-close persist the three forms of the tickets
// section, POST /settings/language the language,
// POST /settings/calendar the working calendar, and POST /settings/email (with
// its test) the SMTP configuration. POST /settings/organization is new and
// multipart, because the identity and its logo are one submission. A successful
// save redirects (303) back to its own section; a rejected one re-renders that
// section with the error. POST /settings/security (issue #414) saves the two
// self-service switches and the minimum new passwords must meet in one call.
//
// All routes are gated on CapManageSettings (admin/root) at the HTTP boundary;
// the application service re-enforces the same capability before mutating
// anything. The gate is CapManageSettings and NOT CapManageUsers: the two
// coincide for admin/root today, but they mean different things, and reusing
// the user-management grant would silently widen every future settings route to
// whatever managing people comes to mean. The rail link follows the same
// capability through pageData.CanManageSettings.
//
// The Email section and its two routes (issue #362) are the exception to that
// gate: they are root-only through CapManageEmail, because the SMTP credentials
// are the instance's own and an administrator manages the instance without
// holding them. An administrator's page does not render the section, its URL is
// refused with 403, and an administrator's POST is refused here before the
// service re-enforces the same capability.
//
// The Security section (issue #414) is one more CapManageSettings section: it
// holds the instance password policy (the two self-service switches and the
// minimum length new passwords must meet) as one card of a section that later
// instance-wide sign-in controls can join.
type SettingsHandlers struct {
	settings     *application.SettingsService
	sla          *application.SLAService
	slaStore     application.SLAStore
	appSet       application.SettingsStore
	email        *application.EmailSettingsService
	organization *application.OrganizationService
	signin       *application.SigninService
	passwords    *application.PasswordSettingsService
	renderer     *Renderer
}

// NewSettingsHandlers wires the settings routes against the appearance and
// SLA use cases, the SLA store port the SLA panel reads its default matrix
// through, and the settings store port it reads the SLA flags through.
func NewSettingsHandlers(settings *application.SettingsService, sla *application.SLAService, slaStore application.SLAStore, appSet application.SettingsStore, renderer *Renderer) *SettingsHandlers {
	return &SettingsHandlers{settings: settings, sla: sla, slaStore: slaStore, appSet: appSet, renderer: renderer}
}

// WithEmail wires the email settings use cases behind the root-only Email
// section (issue #362).
func (h *SettingsHandlers) WithEmail(email *application.EmailSettingsService) *SettingsHandlers {
	h.email = email
	return h
}

// WithSignin wires the sign-in page use cases behind the Sign-in page section
// (issue #383, S9/S10).
func (h *SettingsHandlers) WithSignin(signin *application.SigninService) *SettingsHandlers {
	h.signin = signin
	return h
}

// WithPasswordSettings wires the password-policy use cases behind the Security
// section (issue #414, S1/S4/S5).
func (h *SettingsHandlers) WithPasswordSettings(passwords *application.PasswordSettingsService) *SettingsHandlers {
	h.passwords = passwords
	return h
}

// WithOrganization wires the organization-identity use cases behind the
// Organization section (issue #383, S3/S4).
func (h *SettingsHandlers) WithOrganization(organization *application.OrganizationService) *SettingsHandlers {
	h.organization = organization
	return h
}

// Register mounts the settings routes.
func (h *SettingsHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /settings", h.index)
	mux.HandleFunc("GET /settings/{section}", h.section)
	mux.HandleFunc("POST /settings/appearance", h.updateAppearance)
	mux.HandleFunc("POST /settings/ticket-creation", h.updateTicketCreation)
	mux.HandleFunc("POST /settings/language", h.updateLanguage)
	mux.HandleFunc("POST /settings/sla", h.updateSLA)
	mux.HandleFunc("POST /settings/hold-auto-close", h.updateHoldAutoClose)
	mux.HandleFunc("POST /settings/calendar", h.updateCalendar)
	mux.HandleFunc("POST /settings/email", h.updateEmail)
	mux.HandleFunc("POST /settings/email/test", h.sendTestEmail)
	mux.HandleFunc("POST /settings/organization", h.updateOrganization)
	mux.HandleFunc("POST /settings/organization/logo/remove", h.removeOrganizationLogo)
	mux.HandleFunc("POST /settings/signin", h.updateSignin)
	mux.HandleFunc("POST /settings/signin/background/remove", h.removeSigninBackground)
	mux.HandleFunc("POST /settings/security", h.updateSecurity)
}

// The settings sections, named exactly as their URL segment (S1).
const (
	settingsSectionOrganization = "organization"
	settingsSectionSignin       = "signin"
	settingsSectionLanguage     = "language"
	settingsSectionTickets      = "tickets"
	settingsSectionEmail        = "email"
	settingsSectionCalendar     = "calendar"
	settingsSectionSecurity     = "security"
)

// settingsSectionHref is a section's URL. One function keeps the nav, the
// redirect after a save and the forms' targets from drifting apart.
func settingsSectionHref(section string) string { return "/settings/" + section }

// settingsNavItem is one entry of the left sub-navigation: the section it opens,
// the catalogue key of its label, and whether it is the active one.
type settingsNavItem struct {
	Section string
	Href    string
	Label   string
	Active  bool
}

// settingsNavItems lists the sections in the order the rail shows them, with
// the Entry the actor may not open omitted entirely (S2): an administrator never
// sees an Email entry they cannot use, and the active entry carries
// aria-current="page" through its own flag.
func settingsNavItems(active string, canManageEmail bool) []settingsNavItem {
	items := []settingsNavItem{
		{Section: settingsSectionOrganization, Href: settingsSectionHref(settingsSectionOrganization), Label: "settings.nav.organization"},
		{Section: settingsSectionSignin, Href: settingsSectionHref(settingsSectionSignin), Label: "settings.nav.signin"},
		{Section: settingsSectionLanguage, Href: settingsSectionHref(settingsSectionLanguage), Label: "settings.nav.language"},
		{Section: settingsSectionTickets, Href: settingsSectionHref(settingsSectionTickets), Label: "settings.nav.tickets"},
		{Section: settingsSectionEmail, Href: settingsSectionHref(settingsSectionEmail), Label: "settings.nav.email"},
		{Section: settingsSectionSecurity, Href: settingsSectionHref(settingsSectionSecurity), Label: "settings.nav.security"},
		{Section: settingsSectionCalendar, Href: settingsSectionHref(settingsSectionCalendar), Label: "settings.nav.calendar"},
	}
	out := make([]settingsNavItem, 0, len(items))
	for _, item := range items {
		if item.Section == settingsSectionEmail && !canManageEmail {
			continue
		}
		item.Active = item.Section == active
		out = append(out, item)
	}
	return out
}

// settingsSectionData is the payload every settings page shares: the shell, a
// rejected-save message, and the sub-navigation for the active section.
type settingsSectionData struct {
	pageData
	Error   string
	Section string
	Nav     []settingsNavItem
}

// sectionData builds the shared payload for one section. canManageEmail gates
// the Email nav entry the same way it gates that section's markup, so the rail
// never offers a URL the actor cannot open.
func (h *SettingsHandlers) sectionData(r *http.Request, section string) settingsSectionData {
	actor := *userFromContext(r.Context())
	canManageEmail := h.email != nil && application.NewPolicy().Capabilities(actor.Role).Require(application.CapManageEmail)
	page := pageDataFrom(r, "settings")
	page.SettingsAssets = true
	return settingsSectionData{
		pageData: page,
		Section:  section,
		Nav:      settingsNavItems(section, canManageEmail),
	}
}

// settingsOrganizationData is the Organization section payload (S4): the stored
// identity's editable fields, plus the brand the logo block previews.
type settingsOrganizationData struct {
	settingsSectionData
	Name         string
	HelpDesk     string
	SupportEmail string
	Brand        brandData
}

// settingsLanguageData is the Language section payload.
type settingsLanguageData struct {
	settingsSectionData
	CurrentLanguage string
	Languages       []interfaceLanguageOption
}

// settingsTicketsData is the Tickets section payload: the internal-comment
// background control, the ticket creation flow and the SLA panel, which are
// three forms on one page because all three are ticket configuration (S2, L2;
// issue #391, S1).
type settingsTicketsData struct {
	settingsSectionData
	Current           string
	Colors            []appearanceOption
	CreationMode      string
	CreationModes     []ticketCreationOption
	SLAEnabled        bool
	SLAWarningPercent int
	SLAGrid           slaGridData
	// HoldAutoCloseDays is the automatic hold close window (issue #353, S2) as
	// the field renders it: EMPTY when the feature is off, never a literal 0.
	HoldAutoCloseDays string
}

// settingsCalendarData is the Working calendar section payload.
type settingsCalendarData struct {
	settingsSectionData
	CalendarDays     []calendarDayOption
	CalendarStart    string
	CalendarEnd      string
	CalendarTimezone string
}

// settingsEmailData is the root-only Email section payload (issue #362).
type settingsEmailData struct {
	settingsSectionData
	Email emailPanelData
}

// emailPanelData is the Email section payload (issue #362): the stored SMTP
// configuration with the password reduced to PasswordSet, the encryption
// choices, and whether a test email just succeeded. It never carries the
// password or its sealed form, so no render path can print one (S8).
type emailPanelData struct {
	Enabled       bool
	Host          string
	Port          string
	Encryption    application.EmailEncryption
	SkipTLSVerify bool
	Username      string
	PasswordSet   bool
	FromAddress   string
	FromName      string
	BaseURL       string
	Encryptions   []emailEncryptionOption
	// TestSent marks the page rendered by a successful test email.
	TestSent bool
}

// emailEncryptionOption is one encryption choice: the value the form submits
// and the catalogue key of its label.
type emailEncryptionOption struct {
	Value    application.EmailEncryption
	Label    string
	Selected bool
}

// emailEncryptionOptions lists the offered modes in the service's order (none,
// STARTTLS, TLS — S3) with the active one marked.
func emailEncryptionOptions(selected application.EmailEncryption) []emailEncryptionOption {
	labels := map[application.EmailEncryption]string{
		application.EmailEncryptionNone:     "settings.email.encryption.none",
		application.EmailEncryptionSTARTTLS: "settings.email.encryption.starttls",
		application.EmailEncryptionTLS:      "settings.email.encryption.tls",
	}
	modes := application.AllowedEmailEncryptions()
	out := make([]emailEncryptionOption, 0, len(modes))
	for _, mode := range modes {
		out = append(out, emailEncryptionOption{Value: mode, Label: labels[mode], Selected: mode == selected})
	}
	return out
}

// emailPanelFromStored reduces a stored configuration to the panel: the sealed
// password stays behind, and only the fact that one is stored travels.
func emailPanelFromStored(stored application.EmailSettings) emailPanelData {
	return emailPanelData{
		Enabled:       stored.Enabled,
		Host:          stored.Host,
		Port:          strconv.Itoa(stored.Port),
		Encryption:    stored.Encryption,
		SkipTLSVerify: stored.SkipTLSVerify,
		Username:      stored.Username,
		PasswordSet:   stored.PasswordSet,
		FromAddress:   stored.FromAddress,
		FromName:      stored.FromName,
		BaseURL:       stored.BaseURL,
		Encryptions:   emailEncryptionOptions(stored.Encryption),
	}
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
	for isoDay := range calendarWeekDays {
		options = append(options, calendarDayOption{
			Value:   isoDay + 1,
			Label:   "label.weekday." + strconv.Itoa(isoDay+1),
			Checked: picked(isoDay + 1),
		})
	}
	return options
}

// interfaceLanguageOption pairs a stored interface-language code with the
// catalogue key of its display label.
type interfaceLanguageOption struct {
	Value string
	Label string
}

// interfaceLanguageOptions lists the shipped interface languages in the order
// the panel offers them, each labelled with its AUTONYM (English, Español): a
// language name is never translated, so someone who cannot read the current
// interface language can still find their own.
func interfaceLanguageOptions() []interfaceLanguageOption {
	labels := map[string]string{
		application.InterfaceLanguageEnglish: "settings.language.english",
		application.InterfaceLanguageSpanish: "settings.language.spanish",
	}
	codes := application.AllowedInterfaceLanguages()
	out := make([]interfaceLanguageOption, 0, len(codes))
	for _, code := range codes {
		out = append(out, interfaceLanguageOption{Value: code, Label: labels[code]})
	}
	return out
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
	// Catalogue keys, not English labels (issue #335): the template renders
	// them through t, so the language stays a presentation decision.
	labels := map[string]string{
		application.DefaultInternalCommentBg: "settings.appearance.color_blue",
		"#EFE9FB":                            "settings.appearance.color_violet",
		"#FFF6DC":                            "settings.appearance.color_yellow",
	}
	colors := application.AllowedInternalCommentBg()
	out := make([]appearanceOption, 0, len(colors))
	for _, c := range colors {
		out = append(out, appearanceOption{Value: c, Label: labels[c]})
	}
	return out
}

// ticketCreationOption pairs a stored ticket-creation mode with the catalogue
// key of its display label (issue #391, S1).
type ticketCreationOption struct {
	Value string
	Label string
}

// ticketCreationOptions lists the shipped creation flows in the order the panel
// offers them: the recommended one first, since it is also the default.
func ticketCreationOptions() []ticketCreationOption {
	labels := map[string]string{
		application.TicketCreationSingle:  "settings.ticket_creation.single",
		application.TicketCreationTwoStep: "settings.ticket_creation.two_step",
	}
	modes := application.AllowedTicketCreationModes()
	out := make([]ticketCreationOption, 0, len(modes))
	for _, mode := range modes {
		out = append(out, ticketCreationOption{Value: mode, Label: labels[mode]})
	}
	return out
}

// updateTicketCreation persists the instance ticket creation flow (issue #391,
// S1). A rejected mode re-renders the Tickets section with an inline error (422)
// and changes nothing; success redirects 303 back to the section with the same
// save feedback every other settings form uses.
func (h *SettingsHandlers) updateTicketCreation(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageSettings) {
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	err := h.settings.SetTicketCreationMode(r.Context(), *userFromContext(r.Context()), r.Form.Get("ticket_creation_mode"))
	if err != nil {
		status, msg := mapErrorFor(r, err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		h.renderTickets(w, r, status, msg, nil)
		return
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, settingsSectionHref(settingsSectionTickets))
}

// index answers GET /settings: the surface has no page of its own any more, so
// it sends the visitor to the first section (S1). It keeps the capability gate
// the old page had, so an actor without CapManageSettings is refused here
// instead of being bounced to a section that refuses them.
func (h *SettingsHandlers) index(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageSettings) {
		return
	}
	redirect(w, r, settingsSectionHref(settingsSectionOrganization))
}

// section answers GET /settings/{section} (S1). An unknown segment is a 404
// whatever the actor holds; a known one is gated on the capability that owns
// it — CapManageEmail for the root-only Email section, CapManageSettings for
// every other.
func (h *SettingsHandlers) section(w http.ResponseWriter, r *http.Request) {
	section := r.PathValue("section")
	switch section {
	case settingsSectionOrganization, settingsSectionSignin, settingsSectionLanguage, settingsSectionTickets, settingsSectionCalendar, settingsSectionEmail, settingsSectionSecurity:
	default:
		http.NotFound(w, r)
		return
	}
	if section == settingsSectionEmail {
		if !h.requireEmail(w, r) {
			return
		}
	} else if !requireCapability(w, r, application.CapManageSettings) {
		return
	}
	switch section {
	case settingsSectionOrganization:
		h.renderOrganization(w, r, http.StatusOK, "", nil)
	case settingsSectionSignin:
		h.renderSignin(w, r, http.StatusOK, "", nil)
	case settingsSectionLanguage:
		h.renderLanguage(w, r, http.StatusOK, "")
	case settingsSectionTickets:
		h.renderTickets(w, r, http.StatusOK, "", nil)
	case settingsSectionCalendar:
		h.renderCalendar(w, r, http.StatusOK, "", nil)
	case settingsSectionEmail:
		h.renderEmail(w, r, http.StatusOK, "", nil)
	case settingsSectionSecurity:
		h.renderSecurity(w, r, http.StatusOK, "", nil)
	}
}

// ---------------------------------------------------------------------------
// Organization section (issue #383, S3/S4)
// ---------------------------------------------------------------------------

// renderOrganization renders the Organization section. echo, when non-nil,
// replaces the editable fields with a rejected submission's values, so one bad
// field does not cost the administrator the other two; the logo block always
// previews what is actually stored, because a rejected file was not saved.
func (h *SettingsHandlers) renderOrganization(w http.ResponseWriter, r *http.Request, status int, message string, echo *settingsOrganizationData) {
	if h.organization == nil {
		// A handler set built without the identity use cases fails closed
		// instead of dereferencing a nil service.
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	stored, err := h.organization.GetOrganization(r.Context())
	if err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	data := settingsOrganizationData{
		settingsSectionData: h.sectionData(r, settingsSectionOrganization),
		Name:                stored.Name,
		HelpDesk:            stored.HelpDesk,
		SupportEmail:        stored.SupportEmail,
		Brand:               brandOf(stored),
	}
	data.Error = message
	if echo != nil {
		data.Name, data.HelpDesk, data.SupportEmail = echo.Name, echo.HelpDesk, echo.SupportEmail
	}
	h.renderer.Render(w, r, "settings_organization", "", data, status)
}

// organizationLogoField is the multipart field the logo travels under.
const organizationLogoField = "logo"

// organizationLogoMemory bounds how much of a multipart body is held in memory
// while the parts are parsed: the text fields are tiny, so the logo — capped at
// 256 KiB — is streamed from the temporary file the parser writes.
const organizationLogoMemory = 64 << 10

// updateOrganization persists the identity (S4). The form is multipart because
// the name, the help desk name, the support email and an optional logo are ONE
// submission: the service validates every field before the store writes
// anything, and the store applies the text and the bytes in one transaction, so
// a rejected logo stores neither. An unsubmitted file keeps the stored logo.
func (h *SettingsHandlers) updateOrganization(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageSettings) {
		return
	}
	if h.organization == nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	// ParseMultipartForm also parses a plain urlencoded body, so the endpoint
	// accepts a text-only submission from a client that sends no file.
	if err := r.ParseMultipartForm(organizationLogoMemory); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			h.renderOrganizationRejection(w, r, http.StatusRequestEntityTooLarge, "error.org_logo_too_large")
			return
		}
		httpError(w, r, "error.bad_request", http.StatusBadRequest)
		return
	}
	submitted := &settingsOrganizationData{
		Name:         strings.TrimSpace(r.FormValue("name")),
		HelpDesk:     strings.TrimSpace(r.FormValue("help_desk")),
		SupportEmail: strings.TrimSpace(r.FormValue("support_email")),
	}
	logo, err := readOrganizationLogo(r)
	if err != nil {
		h.renderOrganizationRejection(w, r, http.StatusBadRequest, "error.org_logo_invalid")
		return
	}
	if len(logo) > domain.MaxOrganizationLogoBytes {
		h.renderOrganizationRejection(w, r, http.StatusRequestEntityTooLarge, "error.org_logo_too_large")
		return
	}
	actor := *userFromContext(r.Context())
	err = h.organization.UpdateOrganization(r.Context(), actor, application.OrganizationInput{
		Name:         submitted.Name,
		HelpDesk:     submitted.HelpDesk,
		SupportEmail: submitted.SupportEmail,
		Logo:         logo,
	})
	if err != nil {
		status, msg := mapErrorFor(r, err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		h.renderOrganization(w, r, organizationRejectionStatus(err, status), msg, submitted)
		return
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, settingsSectionHref(settingsSectionOrganization))
}

// renderOrganizationRejection re-renders the section with one catalogue message
// for a refusal the handler itself made (an unparseable or oversized file),
// keeping the submitted text fields the administrator typed.
func (h *SettingsHandlers) renderOrganizationRejection(w http.ResponseWriter, r *http.Request, status int, key string) {
	h.renderOrganization(w, r, status, i18n.T(requestLocale(r), key), &settingsOrganizationData{
		Name:         strings.TrimSpace(r.FormValue("name")),
		HelpDesk:     strings.TrimSpace(r.FormValue("help_desk")),
		SupportEmail: strings.TrimSpace(r.FormValue("support_email")),
	})
}

// organizationRejectionStatus answers the status a refused identity save
// renders: an oversized logo is a request-entity problem (413), not a field
// value (422), exactly as S4 specifies.
func organizationRejectionStatus(err error, fallback int) int {
	var validation *domain.ValidationError
	if errors.As(err, &validation) && validation.Key == "error.org_logo_too_large" {
		return http.StatusRequestEntityTooLarge
	}
	return fallback
}

// readOrganizationLogo reads the submitted logo file, or answers nil when the
// form carried none. The read stops one byte past the cap so an oversized file
// is reported by the handler's own check instead of being buffered whole.
func readOrganizationLogo(r *http.Request) ([]byte, error) {
	return readUpload(r, organizationLogoField, domain.MaxOrganizationLogoBytes)
}

// isBodyTooLarge reports the MaxBytesError the body-limit middleware surfaces
// through a multipart parse: the request exceeded the route's cap.
func isBodyTooLarge(err error) bool {
	var maxErr *http.MaxBytesError
	return errors.As(err, &maxErr)
}

// removeOrganizationLogo drops the stored logo (S4) and returns to the section.
// Removing a logo the instance does not have is not an error.
func (h *SettingsHandlers) removeOrganizationLogo(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageSettings) {
		return
	}
	if h.organization == nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	if err := h.organization.RemoveLogo(r.Context(), *userFromContext(r.Context())); err != nil {
		status, msg := mapErrorFor(r, err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		h.renderOrganization(w, r, status, msg, nil)
		return
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, settingsSectionHref(settingsSectionOrganization))
}

// ---------------------------------------------------------------------------
// Language section (issue #335, S7-S9)
// ---------------------------------------------------------------------------

// renderLanguage renders the Language section from the stored code: a rejected
// save keeps the ACTIVE language marked, because the refusal is about the code
// that was submitted, not about the one the instance renders with.
func (h *SettingsHandlers) renderLanguage(w http.ResponseWriter, r *http.Request, status int, message string) {
	current, err := h.settings.GetInterfaceLanguage(r.Context())
	if err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	data := settingsLanguageData{
		settingsSectionData: h.sectionData(r, settingsSectionLanguage),
		CurrentLanguage:     current,
		Languages:           interfaceLanguageOptions(),
	}
	data.Error = message
	h.renderer.Render(w, r, "settings_language", "", data, status)
}

// updateLanguage persists the instance-wide interface language (issue #335,
// S7-S9). A rejected code re-renders the section (422) with an inline error in
// the language the instance is CURRENTLY rendering (the middleware stamped it
// for this request) and changes nothing. Success redirects 303 back to the
// section: because the redirect is a new request, the middleware reads the saved
// language and the page the administrator lands on renders in it.
func (h *SettingsHandlers) updateLanguage(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageSettings) {
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	submitted := r.Form.Get("interface_language")
	err := h.settings.SetInterfaceLanguage(r.Context(), *userFromContext(r.Context()), submitted)
	if err != nil {
		status, msg := mapErrorFor(r, err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		h.renderLanguage(w, r, status, msg)
		return
	}
	// The save toast is resolved into the flash cookie on THIS request, so it
	// must resolve in the language just saved: the page that renders it is that
	// language, and S8 allows no leftover string in the previous one.
	r = r.WithContext(context.WithValue(r.Context(), ctxKeyInterfaceLanguage{}, submitted))
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, settingsSectionHref(settingsSectionLanguage))
}

// ---------------------------------------------------------------------------
// Tickets section: appearance, the SLA panel and the automatic hold close
// (issue #211, #335, #353)
// ---------------------------------------------------------------------------

// renderTickets renders the Tickets section: the internal-comment background
// control, the SLA panel and the automatic hold close window, from storage
// unless adjust supplies a rejected submission's values. adjust, when non-nil,
// changes the payload the stored configuration produced — the submitted values
// of whichever form of the section was refused — so a rejection never costs the
// administrator the panels that form does not own.
func (h *SettingsHandlers) renderTickets(w http.ResponseWriter, r *http.Request, status int, message string, adjust func(*settingsTicketsData)) {
	current, err := h.settings.GetAppearance(r.Context())
	if err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	data := settingsTicketsData{
		settingsSectionData: h.sectionData(r, settingsSectionTickets),
		Current:             current,
		Colors:              appearanceOptions(),
		CreationModes:       ticketCreationOptions(),
	}
	mode, err := h.settings.GetTicketCreationMode(r.Context())
	if err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	data.CreationMode = mode
	data.Error = message
	if err := h.loadSLA(r, &data); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	if adjust != nil {
		adjust(&data)
	}
	// The shell CSS must carry the same color the panel shows; stamp the
	// authoritative read into the page data.
	data.InternalCommentBg = current
	h.renderer.Render(w, r, "settings_tickets", "", data, status)
}

// loadSLA fills the SLA panel payload from the authoritative reads: the
// enabled flag and warning percent come through the settings store port, the
// automatic hold close window through the SLA service, and the default target
// matrix through the SLA store port (the application service exposes no read
// use case for them).
func (h *SettingsHandlers) loadSLA(r *http.Request, data *settingsTicketsData) error {
	enabled, err := h.appSet.GetSLAEnabled(r.Context())
	if err != nil {
		return err
	}
	percent, err := h.appSet.GetSLAWarningPercent(r.Context())
	if err != nil {
		return err
	}
	holdAutoClose, err := h.sla.HoldAutoCloseDays(r.Context())
	if err != nil {
		return err
	}
	defaults, err := h.slaStore.ListDefaults(r.Context())
	if err != nil {
		return err
	}
	data.SLAEnabled = enabled
	data.SLAWarningPercent = percent
	data.HoldAutoCloseDays = holdAutoCloseFieldValue(holdAutoClose)
	data.SLAGrid = slaGridData{Rows: slaPolicyRows(defaults)}
	return nil
}

// holdAutoCloseFieldValue renders the stored hold auto-close window for the
// field: the feature is off at 0, and the panel says so by rendering the field
// EMPTY, because "empty = off" is the contract the label states (issue #353,
// S2). A negative value cannot be stored; it renders empty like 0.
func holdAutoCloseFieldValue(days int) string {
	if days <= 0 {
		return ""
	}
	return strconv.Itoa(days)
}

// parseHoldAutoCloseDays reads the submitted window: blank is 0 (off), and a
// value that is not a whole number is refused instead of silently turning the
// feature off. The 0..60 bound stays the SLA service's rule, so exactly one
// place decides what a storable window is.
func parseHoldAutoCloseDays(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, true
	}
	days, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return days, true
}

// updateHoldAutoClose persists the automatic hold close window (issue #353, S2)
// from the Tickets section's own form (the section the SLA panel owns, issue
// #383). ONE application call applies it, so a rejected window writes nothing
// and the SLA panel that shares the section is never half-saved; success
// redirects 303 back to the section, exactly as the SLA save does.
func (h *SettingsHandlers) updateHoldAutoClose(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageSettings) {
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	raw := strings.TrimSpace(r.Form.Get("hold_auto_close_working_days"))
	days, ok := parseHoldAutoCloseDays(raw)
	var err error
	if ok {
		err = h.sla.SetHoldAutoCloseDays(r.Context(), *userFromContext(r.Context()), days)
	} else {
		// A non-numeric submission never reaches the service, so the refusal is
		// built here with the SAME catalogue key and English bytes the service
		// uses: every refusal of this one field reads the same sentence.
		err = &domain.ValidationError{
			Field:   "hold_auto_close_working_days",
			Message: "the hold auto-close window must be between 0 and 60 working days",
			Key:     "error.hold_auto_close_days_invalid",
		}
	}
	if err != nil {
		status, msg := mapErrorFor(r, err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		// The re-render echoes the SUBMITTED window, so the administrator reads
		// what was refused instead of the value that is still stored; the
		// appearance and SLA panels come from storage, because this POST owns
		// neither.
		h.renderTickets(w, r, status, msg, func(data *settingsTicketsData) {
			data.HoldAutoCloseDays = raw
		})
		return
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, settingsSectionHref(settingsSectionTickets))
}

// updateAppearance persists the chosen color. A rejected color re-renders the
// Tickets section with an inline error (422) and changes nothing; success
// redirects 303 back to the section.
func (h *SettingsHandlers) updateAppearance(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageSettings) {
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	err := h.settings.SetInternalCommentBg(r.Context(), *userFromContext(r.Context()), r.Form.Get("internal_comment_bg"))
	if err != nil {
		status, msg := mapErrorFor(r, err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		h.renderTickets(w, r, status, msg, nil)
		return
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, settingsSectionHref(settingsSectionTickets))
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
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	actor := *userFromContext(r.Context())
	enabled := r.Form.Get("sla_enabled") == "1"
	percent := slaFormInt(r, "sla_warning_percent")
	policies := parseSLATargets(r)
	// Written durations (issue #395, S10) replace the h/m/s fields when they
	// are submitted. One that does not parse rejects the whole panel before
	// the application call, so nothing is written.
	var durations map[string]string
	if hasSLADurations(r) {
		durations = slaDurationValues(r)
		var valid bool
		policies, valid = parseSLADurations(r)
		if !valid {
			h.rejectSLA(w, r, http.StatusUnprocessableEntity, i18n.T(requestLocale(r), "sla.grid.duration_invalid"), enabled, percent, policies, durations)
			return
		}
	}

	// ONE application call applies the whole panel: validation runs before
	// any store write and the store commits it in one transaction, so there
	// is nothing to compensate for here.
	err := h.sla.SetGlobalConfiguration(r.Context(), actor, enabled, percent, policies)
	if err != nil {
		status, msg := mapErrorFor(r, err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		h.rejectSLA(w, r, status, msg, enabled, percent, policies, durations)
		return
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, settingsSectionHref(settingsSectionTickets))
}

// rejectSLA re-renders the Tickets section with the SUBMITTED panel: one bad
// value must not cost the administrator the rest of what they typed.
func (h *SettingsHandlers) rejectSLA(w http.ResponseWriter, r *http.Request, status int, message string, enabled bool, percent int, policies []domain.SLAPolicy, durations map[string]string) {
	h.renderTickets(w, r, status, message, func(data *settingsTicketsData) {
		data.SLAEnabled = enabled
		data.SLAWarningPercent = percent
		data.SLAGrid = slaGridData{Rows: slaPolicyRows(policies), DurationValues: durations}
	})
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

// ---------------------------------------------------------------------------
// Working calendar section (issue #211)
// ---------------------------------------------------------------------------

// renderCalendar renders the Working calendar section. echo, when non-nil,
// echoes a rejected submission's values instead of the stored calendar.
func (h *SettingsHandlers) renderCalendar(w http.ResponseWriter, r *http.Request, status int, message string, echo *settingsCalendarData) {
	data := settingsCalendarData{settingsSectionData: h.sectionData(r, settingsSectionCalendar)}
	data.Error = message
	if err := h.loadCalendar(r, &data); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	if echo != nil {
		data.CalendarDays = echo.CalendarDays
		data.CalendarStart = echo.CalendarStart
		data.CalendarEnd = echo.CalendarEnd
		data.CalendarTimezone = echo.CalendarTimezone
	}
	h.renderer.Render(w, r, "settings_calendar", "", data, status)
}

// loadCalendar fills the working-calendar panel payload from the
// authoritative read through the SLA service's Calendar use case: the
// working days decomposed into the seven ISO checkboxes, the window as
// HH:MM clock strings, and the zone's IANA name.
func (h *SettingsHandlers) loadCalendar(r *http.Request, data *settingsCalendarData) error {
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
// section (422) with the SUBMITTED values and changes nothing; success
// redirects 303 back to the section.
func (h *SettingsHandlers) updateCalendar(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageSettings) {
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	actor := *userFromContext(r.Context())
	calendar, err := parseCalendarForm(r)
	if err == nil {
		err = h.sla.SetSLACalendar(r.Context(), actor, calendar)
	}
	if err != nil {
		status, msg := mapErrorFor(r, err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		h.renderCalendar(w, r, status, msg, &settingsCalendarData{
			CalendarDays:     calendarDayOptionsFromValues(r.Form["sla_calendar_days"]),
			CalendarStart:    r.Form.Get("sla_calendar_start"),
			CalendarEnd:      r.Form.Get("sla_calendar_end"),
			CalendarTimezone: r.Form.Get("sla_calendar_timezone"),
		})
		return
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, settingsSectionHref(settingsSectionCalendar))
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
			Key:     "error.calendar_time_form",
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
			Key:     "error.unknown_timezone",
			Args:    []any{rawZone},
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

// ---------------------------------------------------------------------------
// Email section (issue #362; specs S1-S4, S8, S10, S14, S15, S16)
// ---------------------------------------------------------------------------

// renderEmail renders the root-only Email section. adjust, when non-nil,
// changes the panel the stored configuration produced — the submitted values of
// a rejected save, or the success of a test email.
func (h *SettingsHandlers) renderEmail(w http.ResponseWriter, r *http.Request, status int, message string, adjust func(*emailPanelData)) {
	if h.email == nil {
		// A handler set built without WithEmail fails closed: the section is
		// never rendered for anyone and no read runs.
		httpError(w, r, "error.forbidden", http.StatusForbidden)
		return
	}
	actor := *userFromContext(r.Context())
	stored, err := h.email.Get(r.Context(), actor)
	if err != nil {
		status, msg := mapErrorFor(r, err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		http.Error(w, msg, status)
		return
	}
	data := settingsEmailData{
		settingsSectionData: h.sectionData(r, settingsSectionEmail),
		Email:               emailPanelFromStored(stored),
	}
	data.Error = message
	if adjust != nil {
		adjust(&data.Email)
	}
	h.renderer.Render(w, r, "settings_email", "", data, status)
}

// requireEmail answers the root-only email gate: the capability AND the wired
// use case, so a handler set built without WithEmail refuses instead of
// dereferencing a nil service.
func (h *SettingsHandlers) requireEmail(w http.ResponseWriter, r *http.Request) bool {
	if h.email == nil {
		httpError(w, r, "error.forbidden", http.StatusForbidden)
		return false
	}
	return requireCapability(w, r, application.CapManageEmail)
}

// emailInputFromForm reconstructs one submission from the panel's fields. The
// port is parsed rather than validated here: an unparseable value — and an
// explicit 0, which the service would read as "unset" — becomes -1 so the
// application service produces the 422 (the boundary never invents a default
// that would silently persist the wrong port), and an empty one stays 0 so the
// service applies its own default of 25 (S1).
func emailInputFromForm(r *http.Request) application.EmailSettingsInput {
	port := 0
	if raw := strings.TrimSpace(r.Form.Get("email_port")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed == 0 {
			parsed = -1
		}
		port = parsed
	}
	return application.EmailSettingsInput{
		Enabled:       r.Form.Get("email_enabled") == "1",
		Host:          r.Form.Get("email_host"),
		Port:          port,
		Encryption:    application.EmailEncryption(strings.TrimSpace(r.Form.Get("email_encryption"))),
		SkipTLSVerify: r.Form.Get("email_skip_tls_verify") == "1",
		Username:      r.Form.Get("email_username"),
		Password:      r.Form.Get("email_password"),
		FromAddress:   r.Form.Get("email_from_address"),
		FromName:      r.Form.Get("email_from_name"),
		BaseURL:       r.Form.Get("email_base_url"),
	}
}

// echoEmailForm overwrites the panel's editable fields with one submission, so
// a rejected save costs root nothing but the password — which no response
// carries (S8), only the "one is saved" flag the stored configuration reports.
func echoEmailForm(panel emailPanelData, r *http.Request) emailPanelData {
	in := emailInputFromForm(r)
	panel.Enabled = in.Enabled
	panel.Host = in.Host
	panel.Port = strings.TrimSpace(r.Form.Get("email_port"))
	panel.Encryption = in.Encryption
	panel.SkipTLSVerify = in.SkipTLSVerify
	panel.Username = in.Username
	panel.FromAddress = in.FromAddress
	panel.FromName = in.FromName
	panel.BaseURL = in.BaseURL
	panel.Encryptions = emailEncryptionOptions(in.Encryption)
	return panel
}

// updateEmail persists the whole SMTP configuration (S1-S4, S8, S10, S15, S16).
// Root-only: refused here and re-refused by the service. ONE application call
// validates everything before the store writes anything, so a rejected
// submission changes nothing; it re-renders the section (422) with the submitted
// values except the password. Success redirects 303 back to the section, so the
// saved configuration applies without a restart (S4).
func (h *SettingsHandlers) updateEmail(w http.ResponseWriter, r *http.Request) {
	if !h.requireEmail(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	actor := *userFromContext(r.Context())
	err := h.email.Save(r.Context(), actor, emailInputFromForm(r))
	if err != nil {
		status, msg := mapErrorFor(r, err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		h.renderEmail(w, r, status, msg, func(panel *emailPanelData) {
			*panel = echoEmailForm(*panel, r)
		})
		return
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, settingsSectionHref(settingsSectionEmail))
}

// sendTestEmail sends one message to the ACTING ROOT's own address with the
// stored configuration (S14), enabled or not, so root can try a server before
// turning delivery on. The action cannot address anyone else, so it is never a
// relay. Success re-renders the section (200) with the confirmation; a rejected
// configuration re-renders it (422) with the localized reason; a refused send
// re-renders it (502) with the server's own reply, which never contains the
// password (S8).
func (h *SettingsHandlers) sendTestEmail(w http.ResponseWriter, r *http.Request) {
	if !h.requireEmail(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	if err := h.email.SendTest(r.Context(), *userFromContext(r.Context())); err != nil {
		status, msg := mapErrorFor(r, err)
		var delivery *application.EmailDeliveryError
		if errors.As(err, &delivery) {
			// The submitted form is valid; the SMTP server is what failed, so
			// this is an upstream failure rather than a rejected field.
			status = http.StatusBadGateway
			msg = i18n.New(requestLocale(r)).Tf("error.email_delivery_failed", delivery.Detail)
		}
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		h.renderEmail(w, r, status, msg, nil)
		return
	}
	h.renderEmail(w, r, http.StatusOK, "", func(panel *emailPanelData) {
		panel.TestSent = true
	})
}

// ---------------------------------------------------------------------------
// Security section (issue #414; specs S1, S4, S5, S10, S11)
// ---------------------------------------------------------------------------

// settingsSecurityData is the Security section payload: the stored password
// policy, the band the minimum may sit in, and whether recovery has a delivery
// path. The policy is one card inside the section rather than the whole page, so
// later instance-wide sign-in controls (an external identity provider, a
// directory) can join it as their own cards.
type settingsSecurityData struct {
	settingsSectionData
	ChangeEnabled   bool
	RecoveryEnabled bool
	// MinLength is the minimum as the field renders it: a value the service
	// refused comes back exactly as it was submitted, so a rejection never shows
	// the value that is still stored.
	MinLength string
	// EmailConfigured is whether the instance can actually send email. S5 makes
	// the recovery switch meaningless without it, so the section says so.
	EmailConfigured bool
	// CanManageEmail is whether the acting administrator may open the root-only
	// Email section, which is the only place the missing configuration can be
	// fixed.
	CanManageEmail   bool
	MinLengthFloor   int
	MinLengthCeiling int
}

// canManageEmail answers whether the actor may open the root-only Email
// section. It is the same capability sectionData applies to that rail entry,
// asked separately by the Security section, which links to it.
func (h *SettingsHandlers) canManageEmail(r *http.Request) bool {
	actor := *userFromContext(r.Context())
	return h.email != nil && application.NewPolicy().Capabilities(actor.Role).Require(application.CapManageEmail)
}

// emailDeliveryConfigured answers whether the instance can send email, read
// through the delivery path the sender itself uses, so "configured" here means
// exactly what it means to a notification. A handler set built without the email
// use cases answers false: recovery needs a delivery path.
func (h *SettingsHandlers) emailDeliveryConfigured(r *http.Request) (bool, error) {
	if h.email == nil {
		return false, nil
	}
	_, ok, err := h.email.DeliveryConfig(r.Context())
	return ok, err
}

// renderSecurity renders the Security section. echo, when non-nil, replaces the
// policy with a rejected submission's values, so a refused minimum does not cost
// the administrator the switches they set.
func (h *SettingsHandlers) renderSecurity(w http.ResponseWriter, r *http.Request, status int, message string, echo *settingsSecurityData) {
	if h.passwords == nil {
		// A handler set built without the password use cases fails closed
		// instead of dereferencing a nil service.
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	stored, err := h.passwords.Get(r.Context())
	if err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	configured, err := h.emailDeliveryConfigured(r)
	if err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	data := settingsSecurityData{
		settingsSectionData: h.sectionData(r, settingsSectionSecurity),
		ChangeEnabled:       stored.ChangeEnabled,
		RecoveryEnabled:     stored.RecoveryEnabled,
		MinLength:           strconv.Itoa(stored.MinLength),
		EmailConfigured:     configured,
		CanManageEmail:      h.canManageEmail(r),
		MinLengthFloor:      application.MinPasswordMinLength,
		MinLengthCeiling:    application.MaxPasswordMinLength,
	}
	data.Error = message
	if echo != nil {
		data.ChangeEnabled = echo.ChangeEnabled
		data.RecoveryEnabled = echo.RecoveryEnabled
		data.MinLength = echo.MinLength
	}
	h.renderer.Render(w, r, "settings_security", "", data, status)
}

// updateSecurity persists the instance password policy (S1, S4): the two
// switches and the minimum in ONE application call, so a rejected value writes
// nothing. A minimum that is not a whole number never reaches the service — the
// boundary refuses it with the SAME sentence the service would, because the
// administrator must not be able to tell the two refusals apart. A rejected save
// re-renders the section (422) with the submitted values; success redirects 303
// back to it with the shared save feedback.
func (h *SettingsHandlers) updateSecurity(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageSettings) {
		return
	}
	if h.passwords == nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	raw := strings.TrimSpace(r.Form.Get("password_min_length"))
	in := application.PasswordSettingsInput{
		ChangeEnabled:   r.Form.Get("password_change_enabled") == "1",
		RecoveryEnabled: r.Form.Get("password_recovery_enabled") == "1",
	}
	var err error
	if minimum, parseErr := strconv.Atoi(raw); parseErr != nil {
		err = application.PasswordMinLengthRangeError()
	} else {
		in.MinLength = minimum
		err = h.passwords.Update(r.Context(), *userFromContext(r.Context()), in)
	}
	if err != nil {
		status, msg := mapErrorFor(r, err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		h.renderSecurity(w, r, status, msg, &settingsSecurityData{
			ChangeEnabled:   in.ChangeEnabled,
			RecoveryEnabled: in.RecoveryEnabled,
			MinLength:       raw,
		})
		return
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, settingsSectionHref(settingsSectionSecurity))
}
