package httpadapter

import (
	"errors"
	"html/template"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/internal/i18n"
)

// The sign-in page section and the panel it renders (issue #383, S9/S10). The
// two-column sign-in page carries a dark presentation panel whose brand, title,
// subtitle, middle content, notice and footer come from here; the defaults are
// catalogue copy, so an unconfigured instance renders the full panel in the
// instance language and it is never empty.

// signinReadLog deduplicates the sign-in panel read failure the same way
// languageReadLog does (R4-002): the first failure logs, the next success
// re-arms the counter, and a long outage logs once every
// interfaceLanguageEveryNthFailure attempts. It is a separate type because its
// fallback is the default panel, not a locale, a shell state or an identity.
type signinReadLog struct {
	mu      sync.Mutex
	failing bool
	count   int
}

func (l *signinReadLog) failed(err error) {
	l.mu.Lock()
	l.count++
	report := !l.failing || l.count%interfaceLanguageEveryNthFailure == 0
	l.failing = true
	l.mu.Unlock()
	if report {
		log.Printf("httpadapter: read sign-in panel: %v (rendering the default panel)", err)
	}
}

func (l *signinReadLog) recovered() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failing = false
	l.count = 0
}

// settingsSigninData is the Sign-in page section payload: the editable form
// values (the stored ones, or a rejected submission's) and the preview the form
// mirrors.
type settingsSigninData struct {
	settingsSectionData
	Values signinFormValues
	// Panel is the stored panel the preview renders; settings.js mirrors the
	// form's inputs into it while the administrator types.
	Panel    signinPanelData
	HasImage bool
	// ImageVersion busts the day-long cache of the background image URL.
	ImageVersion string
	// The closed sets, in the order the form offers them.
	Contents    []signinChoice
	Backgrounds []signinChoice
	NoticeKinds []signinChoice
	Icons       []signinChoice
}

// signinChoice is one radio or option choice: the value the form submits and
// whether it is the current one. The label is not carried here: the template
// resolves it through its own catalogue namespace (settings.signin.content.*,
// settings.signin.background.*, settings.signin.notice_kind.* and signin.icon.*),
// so the closed sets stay one source in the domain and no Go call site owns a
// fragment of copy.
type signinChoice struct {
	Value    string
	Selected bool
}

// signinFormValues is the Sign-in form as a submission or as the stored state.
// Highlights always carries the three rows the form renders, so an administator
// can fill any of them and an untouched row is simply empty.
type signinFormValues struct {
	Title         string
	Subtitle      string
	Content       string
	Highlights    []signinHighlightValues
	NoticeEnabled bool
	NoticeKind    string
	NoticeTitle   string
	NoticeText    string
	Background    string
	BrandColor    string
	SupportPhone  string
	SupportHours  string
}

// signinHighlightValues is one editable highlight row.
type signinHighlightValues struct {
	Icon  string
	Title string
	Text  string
}

// signinPanelData is the presentation payload of the panel itself, shared by the
// sign-in page and the section's live preview.
type signinPanelData struct {
	Title    string
	Subtitle string
	// Content is the EFFECTIVE middle zone: an image background that no longer
	// has bytes falls back to the dark surface, and a stored "highlights" with
	// no rows falls back to the steps, so the panel is never empty.
	Content       string
	Steps         []signinStepData
	Highlights    []signinHighlightData
	NoticeEnabled bool
	NoticeKind    string
	NoticeTitle   string
	NoticeText    string
	// Background is the EFFECTIVE background, and BackgroundStyle is the custom
	// property the stylesheet paints it with (a brand colour or the photo URL),
	// built here from validated values.
	Background      string
	BackgroundStyle template.CSS
	SupportEmail    string
	SupportPhone    string
	SupportHours    string
}

// signinStepData is one default step of the middle zone; the second one is
// highlighted, the way the panel always drew the journey.
type signinStepData struct {
	Number int
	Title  string
	Body   string
	Active bool
}

// signinHighlightData is one rendered highlight row.
type signinHighlightData struct {
	Icon  string
	Title string
	Text  string
}

// signinFormRows is how many highlight rows the form always renders: the
// documented maximum, so "add" only reveals a row that already exists.
const signinFormRows = domain.MaxSigninHighlights

// signinFormValuesFromConfiguration turns the stored panel into the form's
// values, padding the highlight rows to the documented maximum.
func signinFormValuesFromConfiguration(panel domain.SigninPanel) signinFormValues {
	values := signinFormValues{
		Title:         panel.Title,
		Subtitle:      panel.Subtitle,
		Content:       panel.Content,
		NoticeEnabled: panel.NoticeEnabled,
		NoticeKind:    panel.NoticeKind,
		NoticeTitle:   panel.NoticeTitle,
		NoticeText:    panel.NoticeText,
		Background:    panel.Background,
		BrandColor:    panel.BrandColor,
		SupportPhone:  panel.SupportPhone,
		SupportHours:  panel.SupportHours,
	}
	if values.Content == "" {
		values.Content = domain.SigninContentSteps
	}
	if values.Background == "" {
		values.Background = domain.SigninBackgroundDark
	}
	if values.NoticeKind == "" {
		values.NoticeKind = domain.SigninNoticeInfo
	}
	values.Highlights = make([]signinHighlightValues, signinFormRows)
	for i := range values.Highlights {
		if i < len(panel.Highlights) {
			values.Highlights[i] = signinHighlightValues{
				Icon:  panel.Highlights[i].Icon,
				Title: panel.Highlights[i].Title,
				Text:  panel.Highlights[i].Text,
			}
			continue
		}
		// An empty row still offers the first icon as its select's default, so a
		// row the administrator fills in one field submits a complete row.
		values.Highlights[i] = signinHighlightValues{Icon: domain.SigninIconOptions()[0]}
	}
	return values
}

// signinPanelDataFor builds the panel the sign-in page renders. Defaults come
// from the catalogue so they follow the instance language: the title names the
// help desk, the subtitle is the shipped sentence, and the middle zone is the
// three-step journey.
func signinPanelDataFor(configuration application.SigninConfiguration, brand brandData, locale string) signinPanelData {
	tr := i18n.New(locale)
	panel := configuration.Panel
	data := signinPanelData{
		Title:         strings.TrimSpace(panel.Title),
		Subtitle:      strings.TrimSpace(panel.Subtitle),
		Content:       panel.Content,
		NoticeEnabled: panel.NoticeEnabled,
		NoticeKind:    panel.NoticeKind,
		NoticeTitle:   panel.NoticeTitle,
		NoticeText:    panel.NoticeText,
		Background:    panel.Background,
		SupportEmail:  brand.SupportEmail,
		SupportPhone:  panel.SupportPhone,
		SupportHours:  panel.SupportHours,
	}
	if data.Title == "" {
		data.Title = tr.Tf("auth.signin.title_default", brandHelpDesk(brand))
	}
	if data.Subtitle == "" {
		data.Subtitle = tr.T("auth.signin.subtitle_default")
	}
	if data.NoticeEnabled && data.NoticeKind != domain.SigninNoticeWarning {
		data.NoticeKind = domain.SigninNoticeInfo
	}

	data.Steps = []signinStepData{
		{Number: 1, Title: tr.T("auth.signin.steps.received.title"), Body: tr.T("auth.signin.steps.received.body")},
		{Number: 2, Title: tr.T("auth.signin.steps.assigned.title"), Body: tr.T("auth.signin.steps.assigned.body"), Active: true},
		{Number: 3, Title: tr.T("auth.signin.steps.resolved.title"), Body: tr.T("auth.signin.steps.resolved.body")},
	}
	for _, row := range panel.Highlights {
		data.Highlights = append(data.Highlights, signinHighlightData{Icon: row.Icon, Title: row.Title, Text: row.Text})
	}
	// The middle zone never renders empty: a highlights zone with no rows, or an
	// unknown value, falls back to the steps.
	switch {
	case data.Content == domain.SigninContentNone:
	case data.Content == domain.SigninContentHighlights && len(data.Highlights) > 0:
	default:
		data.Content = domain.SigninContentSteps
	}

	switch {
	case data.Background == domain.SigninBackgroundBrand && domain.ValidSigninBrandColor(panel.BrandColor):
		data.BackgroundStyle = template.CSS("--signin-brand:" + domain.DarkenForWhiteText(panel.BrandColor))
	case data.Background == domain.SigninBackgroundImage && configuration.HasBackground():
		data.BackgroundStyle = template.CSS("--signin-photo:url('/brand/signin-background?v=" + logoVersion(configuration.Background) + "')")
	default:
		data.Background = domain.SigninBackgroundDark
	}
	return data
}

// signinChoices marks the option matching selected.
func signinChoices(values []string, selected string) []signinChoice {
	out := make([]signinChoice, 0, len(values))
	for _, value := range values {
		out = append(out, signinChoice{Value: value, Selected: value == selected})
	}
	return out
}

// panelChoices fills the four closed-set pickers for the given values.
func panelChoices(values signinFormValues) ([]signinChoice, []signinChoice, []signinChoice, []signinChoice) {
	return signinChoices(domain.SigninContentOptions(), values.Content),
		signinChoices(domain.SigninBackgroundOptions(), values.Background),
		signinChoices(domain.SigninNoticeKinds(), values.NoticeKind),
		signinChoices(domain.SigninIconOptions(), "")
}

// renderSignin renders the Sign-in page section. echo, when non-nil, replaces the
// editable values with a rejected submission's, so one bad field does not cost
// the administrator the rest; the preview always mirrors what is stored.
func (h *SettingsHandlers) renderSignin(w http.ResponseWriter, r *http.Request, status int, message string, echo *signinFormValues) {
	if h.signin == nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	configuration, err := h.signin.GetSigninConfiguration(r.Context())
	if err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	values := signinFormValuesFromConfiguration(configuration.Panel)
	if echo != nil {
		values = *echo
	}
	data := settingsSigninData{
		settingsSectionData: h.sectionData(r, settingsSectionSignin),
		Values:              values,
		Panel:               signinPanelDataFor(configuration, brandFromContext(r.Context()), requestLocale(r)),
		HasImage:            configuration.HasBackground(),
		ImageVersion:        logoVersion(configuration.Background),
	}
	data.Error = message
	data.Contents, data.Backgrounds, data.NoticeKinds, data.Icons = panelChoices(values)
	h.renderer.Render(w, r, "settings_signin", "", data, status)
}

// signinBackgroundField is the multipart field the background image travels
// under, and signinBackgroundMemory bounds how much of the body is held in
// memory while the parts are parsed.
const (
	signinBackgroundField  = "background"
	signinBackgroundMemory = 64 << 10
)

// updateSignin persists the sign-in page (S10). The form is multipart because
// the panel's text and an optional background image are ONE submission: the
// service validates every field — and the image's magic bytes — before the store
// writes anything, and the store applies the text and the bytes in one
// transaction, so a rejected image stores neither. An unsubmitted file keeps the
// stored image.
func (h *SettingsHandlers) updateSignin(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageSettings) {
		return
	}
	if h.signin == nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	if err := r.ParseMultipartForm(signinBackgroundMemory); err != nil {
		if isBodyTooLarge(err) {
			h.renderSigninRejection(w, r, http.StatusRequestEntityTooLarge, i18n.T(requestLocale(r), "error.signin_image_too_large"))
			return
		}
		httpError(w, r, "error.bad_request", http.StatusBadRequest)
		return
	}
	submitted := signinValuesFromForm(r)
	image, err := readUpload(r, signinBackgroundField, domain.MaxSigninBackgroundBytes)
	if err != nil {
		httpError(w, r, "error.bad_request", http.StatusBadRequest)
		return
	}
	if len(image) > domain.MaxSigninBackgroundBytes {
		h.renderSigninRejection(w, r, http.StatusRequestEntityTooLarge, i18n.T(requestLocale(r), "error.signin_image_too_large"))
		return
	}
	actor := *userFromContext(r.Context())
	err = h.signin.UpdateSignin(r.Context(), actor, application.SigninInput{
		Panel:      submitted.panel(),
		Background: image,
	})
	if err != nil {
		status, msg := mapErrorFor(r, err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		h.renderSignin(w, r, signinRejectionStatus(err, status), msg, &submitted)
		return
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, settingsSectionHref(settingsSectionSignin))
}

// renderSigninRejection re-renders the section with one catalogue message for a
// refusal the handler itself made (an unparseable or oversized image), keeping
// the text the administrator typed.
func (h *SettingsHandlers) renderSigninRejection(w http.ResponseWriter, r *http.Request, status int, message string) {
	submitted := signinValuesFromForm(r)
	h.renderSignin(w, r, status, message, &submitted)
}

// signinRejectionStatus answers the status a refused panel save renders: an
// oversized image is a request-entity problem (413), not a field value (422).
func signinRejectionStatus(err error, fallback int) int {
	var validation *domain.ValidationError
	if errors.As(err, &validation) && validation.Key == "error.signin_image_too_large" {
		return http.StatusRequestEntityTooLarge
	}
	return fallback
}

// removeSigninBackground drops the stored background image and returns to the
// section.
func (h *SettingsHandlers) removeSigninBackground(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageSettings) {
		return
	}
	if h.signin == nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	if err := h.signin.RemoveBackground(r.Context(), *userFromContext(r.Context())); err != nil {
		status, msg := mapErrorFor(r, err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		h.renderSignin(w, r, status, msg, nil)
		return
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, settingsSectionHref(settingsSectionSignin))
}

// signinValuesFromForm reads one submission. Every value stays a string: the
// service validates it, and a rejected save echoes exactly what was typed.
func signinValuesFromForm(r *http.Request) signinFormValues {
	values := signinFormValues{
		Title:         r.FormValue("title"),
		Subtitle:      r.FormValue("subtitle"),
		Content:       r.FormValue("content"),
		NoticeEnabled: r.FormValue("notice_enabled") == "1",
		NoticeKind:    r.FormValue("notice_kind"),
		NoticeTitle:   r.FormValue("notice_title"),
		NoticeText:    r.FormValue("notice_text"),
		Background:    r.FormValue("background"),
		BrandColor:    r.FormValue("brand_color"),
		SupportPhone:  r.FormValue("support_phone"),
		SupportHours:  r.FormValue("support_hours"),
	}
	values.Highlights = make([]signinHighlightValues, signinFormRows)
	for i := range values.Highlights {
		index := strconv.Itoa(i)
		values.Highlights[i] = signinHighlightValues{
			Icon:  r.FormValue("highlight_" + index + "_icon"),
			Title: r.FormValue("highlight_" + index + "_title"),
			Text:  r.FormValue("highlight_" + index + "_text"),
		}
	}
	return values
}

// panel turns the form values into the domain panel the service validates.
func (v signinFormValues) panel() domain.SigninPanel {
	panel := domain.SigninPanel{
		Title:         v.Title,
		Subtitle:      v.Subtitle,
		Content:       v.Content,
		NoticeEnabled: v.NoticeEnabled,
		NoticeKind:    v.NoticeKind,
		NoticeTitle:   v.NoticeTitle,
		NoticeText:    v.NoticeText,
		Background:    v.Background,
		BrandColor:    v.BrandColor,
		SupportPhone:  v.SupportPhone,
		SupportHours:  v.SupportHours,
	}
	for _, row := range v.Highlights {
		panel.Highlights = append(panel.Highlights, domain.SigninHighlight{Icon: row.Icon, Title: row.Title, Text: row.Text})
	}
	return panel
}

// readUpload reads one uploaded file part, bounded at limit+1 bytes so an
// oversized file is reported by the caller's own size check instead of being
// buffered whole. A part with no bytes and no name is what a browser sends for
// an untouched file input, and means "no file".
func readUpload(r *http.Request, field string, limit int64) ([]byte, error) {
	if r.MultipartForm == nil {
		return nil, nil
	}
	files := r.MultipartForm.File[field]
	if len(files) == 0 {
		return nil, nil
	}
	header := files[0]
	if header.Filename == "" && header.Size == 0 {
		return nil, nil
	}
	file, err := header.Open()
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	return data, nil
}
