package httpadapter

import (
	"errors"
	"net/http"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// PreferencesHandlers expose the per-user preferences page (issue #210):
// GET /preferences renders the page for the session user, POST /preferences
// persists the default queue order, POST /preferences/email persists the
// notification-email toggle (issue #362, S5), POST /preferences/sidebar
// persists the collapsed sidebar (issue #377, S2), POST /preferences/theme
// persists the interface theme (issue #378, S1), and
// POST /preferences/timeline-order persists the ticket activity order (issue
// #438, S1).
//
// There is deliberately NO capability gate. A preference is personal, not
// instance configuration: every authenticated user owns the row, and the
// session middleware is what keeps anonymous callers out (an unauthenticated
// request is redirected to /login before this handler runs).
//
// One form per preference, each posting to its own route. The preferences
// share the user_preferences table, and a single form would have to guess the
// unsent half: an unchecked checkbox is simply absent from a form body, so a
// queue-order submission carrying no email field would be indistinguishable
// from "turn email off". Separate actions remove the ambiguity at the wire
// level — a route only ever writes the one key it names — instead of relying
// on a hidden companion field nobody may forget. The sidebar route is the
// narrowest case of that rule: its whole body is a single two-valued field. The
// theme route is the same shape as the queue order — a closed set of three
// values — so it also answers a native redirect with the save feedback. The
// activity-order route (issue #438, S1) is a closed set of two values and the
// same shape again, which is what lets the Preferences control and the Activity
// header toggle write ONE preference through one use case.
//
// POST /preferences/password (issue #414, S2) is the one preference that is not
// a stored setting: it changes the account's own password, so the page needs the
// user use cases and the instance password policy, and the route reads the
// session making the request from the cookie. Both are optional wiring: a set
// built without them hides the section (fail closed) instead of offering a path
// it cannot complete.
type PreferencesHandlers struct {
	preferences *application.PreferencesService
	renderer    *Renderer
	users       *application.UserService
	passwords   *application.PasswordSettingsService
}

// NewPreferencesHandlers wires the preference routes against the
// preferences use cases and the renderer.
func NewPreferencesHandlers(preferences *application.PreferencesService, renderer *Renderer) *PreferencesHandlers {
	return &PreferencesHandlers{preferences: preferences, renderer: renderer}
}

// Register mounts the preferences routes.
func (h *PreferencesHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /preferences", h.index)
	mux.HandleFunc("POST /preferences", h.update)
	mux.HandleFunc("POST /preferences/email", h.updateEmail)
	mux.HandleFunc("POST /preferences/sidebar", h.updateSidebar)
	mux.HandleFunc("POST /preferences/theme", h.updateTheme)
	mux.HandleFunc("POST /preferences/timeline-order", h.updateTimelineOrder)
	mux.HandleFunc("POST /preferences/password", h.updatePassword)
}

// WithPasswordChange wires the own-password section (issue #414, S2): the user
// use case that changes the signed-in account's password and signs out its other
// sessions, and the instance policy that decides whether the section exists and
// how long a new password must be.
func (h *PreferencesHandlers) WithPasswordChange(users *application.UserService, passwords *application.PasswordSettingsService) *PreferencesHandlers {
	h.users = users
	h.passwords = passwords
	return h
}

// preferencesData is the preferences page payload. Current is already
// normalized to the closed set, so the control always has a selected option
// even when the stored row is unknown (fail closed). Theme is the actor's
// normalized theme for the same reason, and Themes is the closed set the theme
// control offers. TimelineOrder is the actor's normalized activity order and
// TimelineOrders the closed set that control offers (issue #438, S1).
// EmailNotifications carries the stored opt-out; absent means on, so the box
// starts checked.
type preferencesData struct {
	pageData
	Error              string
	Current            string
	Orders             []queueOrderOption
	Theme              string
	Themes             []themeOption
	TimelineOrder      string
	TimelineOrders     []timelineOrderOption
	EmailNotifications bool
	// Password is the own-password section (issue #414, S2). Shown is false
	// while the instance switch is OFF, so the whole section disappears.
	Password passwordSectionData
}

// The three fields of the own-password form. They are the route's wire
// vocabulary and the field each refusal is reported on.
const (
	passwordFieldCurrent = "current_password"
	passwordFieldNew     = "new_password"
	passwordFieldConfirm = "confirm_password"
)

// passwordSectionData is the own-password section's payload: whether it renders,
// the instance minimum its help teaches, and one refusal with the field it
// belongs to. No password value ever travels in this payload, so a rejected
// submission cannot echo one back into the form.
type passwordSectionData struct {
	Shown      bool
	MinLength  int
	Error      string
	ErrorField string
}

// queueOrderOption is one selectable queue order.
type queueOrderOption struct {
	Value string
	Label string
}

// queueOrderOptions lists the selectable orders in the closed set's order,
// reusing the exact labels the queue's own order control already shows.
func queueOrderOptions() []queueOrderOption {
	// Catalogue keys, not English labels (issue #335).
	labels := map[string]string{
		application.QueueOrderNewest:   "preferences.order.newest",
		application.QueueOrderPriority: "preferences.order.priority",
		application.QueueOrderUrgency:  "preferences.order.urgency",
	}
	orders := application.AllowedQueueOrders()
	out := make([]queueOrderOption, 0, len(orders))
	for _, order := range orders {
		out = append(out, queueOrderOption{Value: order, Label: labels[order]})
	}
	return out
}

// themeOption is one selectable interface theme.
type themeOption struct {
	Value string
	Label string
}

// themeOptions lists the selectable themes in the closed set's order: the
// system default first, then the two explicit choices.
func themeOptions() []themeOption {
	// Catalogue keys, not English labels (issue #335, issue #378 S1).
	labels := map[string]string{
		application.ThemeSystem: "preferences.theme.system",
		application.ThemeLight:  "preferences.theme.light",
		application.ThemeDark:   "preferences.theme.dark",
	}
	themes := application.AllowedThemes()
	out := make([]themeOption, 0, len(themes))
	for _, theme := range themes {
		out = append(out, themeOption{Value: theme, Label: labels[theme]})
	}
	return out
}

// timelineOrderOption is one selectable ticket activity order (issue #438,
// S1).
type timelineOrderOption struct {
	Value string
	Label string
}

// timelineOrderOptions lists the selectable activity orders in the closed set's
// order: the newest-first default first, then oldest first. The labels are
// deliberately their own catalogue keys — the queue's "Newest first" means the
// ticket LIST's order, and one control must never borrow the other's wording.
func timelineOrderOptions() []timelineOrderOption {
	// Catalogue keys, not English labels (issue #335).
	labels := map[string]string{
		application.TimelineOrderNewest: "preferences.timeline.newest",
		application.TimelineOrderOldest: "preferences.timeline.oldest",
	}
	orders := application.AllowedTimelineOrders()
	out := make([]timelineOrderOption, 0, len(orders))
	for _, order := range orders {
		out = append(out, timelineOrderOption{Value: order, Label: labels[order]})
	}
	return out
}

// index renders the preferences page for the session user. The stored value
// is normalized by the service, so an unknown value can never reach the
// selected option or the queue.
func (h *PreferencesHandlers) index(w http.ResponseWriter, r *http.Request) {
	current, err := h.preferences.GetDefaultQueueOrder(r.Context(), *userFromContext(r.Context()))
	if err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	data, err := h.data(r, current, "")
	if err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	h.renderer.Render(w, r, "preferences", "", data, http.StatusOK)
}

// update persists the submitted queue order. A rejected value re-renders the
// page with an inline error (422) and changes nothing; success redirects 303
// back to /preferences with the shared save feedback.
func (h *PreferencesHandlers) update(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	err := h.preferences.SetDefaultQueueOrder(r.Context(), *userFromContext(r.Context()), r.Form.Get("queue_order"))
	if err != nil {
		status, msg := mapErrorFor(r, err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		// The rejected value is not echoed into the control: the page shows
		// the still-stored (normalized) value, so an invalid submission can
		// never render an unvalidated order as selected.
		current, getErr := h.preferences.GetDefaultQueueOrder(r.Context(), *userFromContext(r.Context()))
		if getErr != nil {
			httpError(w, r, "error.internal_server", http.StatusInternalServerError)
			return
		}
		data, getErr := h.data(r, current, msg)
		if getErr != nil {
			httpError(w, r, "error.internal_server", http.StatusInternalServerError)
			return
		}
		h.renderer.Render(w, r, "preferences", "", data, status)
		return
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, "/preferences")
}

// updateEmail persists the notification-email toggle (issue #362, S5). The
// checkbox has no invalid value to reject (checked means on, absent means
// off), so the only failure left is a storage error. Like the queue-order
// route it answers with the shared save feedback and a 303 back to the page,
// and it writes ONLY the email key: the stored queue order is untouched.
func (h *PreferencesHandlers) updateEmail(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	enabled := r.Form.Get("email_notifications") == "1"
	if err := h.preferences.SetEmailNotifications(r.Context(), *userFromContext(r.Context()), enabled); err != nil {
		status, msg := mapErrorFor(r, err)
		http.Error(w, msg, status)
		return
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, "/preferences")
}

// updateTheme persists the submitted interface theme (issue #378, S1). It is a
// native form like the queue order — a closed set of three values, one of them
// selected — so it answers exactly the same way: a rejected value re-renders
// the page with the inline error (422) and changes nothing, and success
// redirects 303 back to /preferences with the shared save feedback. The route
// writes ONLY the theme key.
func (h *PreferencesHandlers) updateTheme(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	err := h.preferences.SetTheme(r.Context(), *userFromContext(r.Context()), r.Form.Get("theme"))
	if err != nil {
		status, msg := mapErrorFor(r, err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		// The rejected value is not echoed into the control: the page shows the
		// still-stored (normalized) theme, so an invalid submission can never
		// render an unvalidated theme as selected.
		current, getErr := h.preferences.GetDefaultQueueOrder(r.Context(), *userFromContext(r.Context()))
		if getErr != nil {
			httpError(w, r, "error.internal_server", http.StatusInternalServerError)
			return
		}
		data, getErr := h.data(r, current, msg)
		if getErr != nil {
			httpError(w, r, "error.internal_server", http.StatusInternalServerError)
			return
		}
		h.renderer.Render(w, r, "preferences", "", data, status)
		return
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, "/preferences")
}

// updateTimelineOrder persists the submitted ticket activity order (issue #438,
// S1). It is a native form like the queue order and the theme — a closed set of
// two values, one of them selected — so it answers exactly the same way: a
// rejected value re-renders the page with the inline error (422) and changes
// nothing, and success redirects 303 back to /preferences with the shared save
// feedback. The route writes ONLY the activity-order key, and it writes it
// through the same use case the Activity header toggle calls, so both entry
// points can never store two different things under one name.
func (h *PreferencesHandlers) updateTimelineOrder(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	err := h.preferences.SetTimelineOrder(r.Context(), *userFromContext(r.Context()), r.Form.Get("timeline_order"))
	if err != nil {
		status, msg := mapErrorFor(r, err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		// The rejected value is not echoed into the control: the page shows the
		// still-stored (normalized) order, so an invalid submission can never
		// render an unvalidated order as selected.
		current, getErr := h.preferences.GetDefaultQueueOrder(r.Context(), *userFromContext(r.Context()))
		if getErr != nil {
			httpError(w, r, "error.internal_server", http.StatusInternalServerError)
			return
		}
		data, getErr := h.data(r, current, msg)
		if getErr != nil {
			httpError(w, r, "error.internal_server", http.StatusInternalServerError)
			return
		}
		h.renderer.Render(w, r, "preferences", "", data, status)
		return
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, "/preferences")
}

// sidebarStateCollapsed and sidebarStateExpanded are the only two values the
// sidebar toggle accepts and the only two the store persists (issue #377,
// S2). They are the wire vocabulary of POST /preferences/sidebar, so an
// unknown value is refused here rather than reaching the store.
const (
	sidebarStateCollapsed = "collapsed"
	sidebarStateExpanded  = "expanded"
)

// updateSidebar persists the collapsed sidebar (issue #377, S2). The route is
// called from fetch, never from a navigation, so success answers 204 No
// Content with NO body: there is no page to render and nothing to swap, the
// browser flips the class itself. A value outside the two documented states is
// a 422 through the shared error path and writes nothing.
func (h *PreferencesHandlers) updateSidebar(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	collapsed := false
	switch r.Form.Get("sidebar") {
	case sidebarStateCollapsed:
		collapsed = true
	case sidebarStateExpanded:
		collapsed = false
	default:
		httpError(w, r, "error.invalid_sidebar_state", http.StatusUnprocessableEntity)
		return
	}
	if err := h.preferences.SetSidebarCollapsed(r.Context(), *userFromContext(r.Context()), collapsed); err != nil {
		status, msg := mapErrorFor(r, err)
		http.Error(w, msg, status)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// data builds the page payload from the normalized current order and an
// optional error message, reading the actor's own email preference and theme
// for their controls. A storage failure is returned so the caller can answer
// 500 instead of rendering a guessed checkbox state or an unvalidated theme.
func (h *PreferencesHandlers) data(r *http.Request, current, msg string) (preferencesData, error) {
	actor := *userFromContext(r.Context())
	enabled, err := h.preferences.GetEmailNotifications(r.Context(), actor)
	if err != nil {
		return preferencesData{}, err
	}
	theme, err := h.preferences.GetTheme(r.Context(), actor)
	if err != nil {
		return preferencesData{}, err
	}
	timelineOrder, err := h.preferences.GetTimelineOrder(r.Context(), actor)
	if err != nil {
		return preferencesData{}, err
	}
	section, err := h.passwordChangeSection(r)
	if err != nil {
		return preferencesData{}, err
	}
	return preferencesData{
		pageData:           pageDataFrom(r, "preferences"),
		Error:              msg,
		Current:            current,
		Orders:             queueOrderOptions(),
		Theme:              theme,
		Themes:             themeOptions(),
		TimelineOrder:      timelineOrder,
		TimelineOrders:     timelineOrderOptions(),
		EmailNotifications: enabled,
		Password:           section,
	}, nil
}

// passwordChangeSection reads the instance policy the own-password section
// follows (issue #414, S1/S4): the section renders only while the switch is ON,
// and the minimum it teaches is the one every new password must reach. A handler
// set built without the password use cases keeps the section hidden rather than
// offering a path it cannot complete.
func (h *PreferencesHandlers) passwordChangeSection(r *http.Request) (passwordSectionData, error) {
	if h.users == nil || h.passwords == nil {
		return passwordSectionData{}, nil
	}
	policy, err := h.passwords.Get(r.Context())
	if err != nil {
		return passwordSectionData{}, err
	}
	// Issue #419, S5: while password sign-in is off, only root keeps the
	// section.
	allowed, err := h.users.PasswordSignInAllowed(r.Context(), *userFromContext(r.Context()))
	if err != nil {
		return passwordSectionData{}, err
	}
	// An account without a password (a company sign-in created it) has no current
	// password to ask for, so the section is not offered.
	hasPassword := userFromContext(r.Context()).PasswordHash != ""
	return passwordSectionData{Shown: policy.ChangeEnabled && allowed && hasPassword, MinLength: policy.MinLength}, nil
}

// updatePassword replaces the session user's own password (issue #414, S2). The
// current password, the new one and its confirmation arrive as one native
// submission, so success answers the same 303 back to /preferences with the
// shared save feedback the other preference forms use.
//
// The order of the guards matters. The switch is read first, because while it is
// OFF the path does not exist — a forged submission is refused with 403 before a
// single form value is looked at, without writing anything. The confirmation is
// compared next: the two fields are compared here, before the use case runs, so
// a mistyped confirmation can never cost the person their current password. Only
// then does the application verify the current password, enforce the minimum and
// write the new hash together with the sign-out of every other session.
func (h *PreferencesHandlers) updatePassword(w http.ResponseWriter, r *http.Request) {
	if h.users == nil || h.passwords == nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	policy, err := h.passwords.Get(r.Context())
	if err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	if !policy.ChangeEnabled {
		httpError(w, r, "error.forbidden", http.StatusForbidden)
		return
	}
	allowed, err := h.users.PasswordSignInAllowed(r.Context(), *userFromContext(r.Context()))
	if err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	if !allowed {
		httpError(w, r, "error.forbidden", http.StatusForbidden)
		return
	}

	// S2 keeps the session in use signed in, so the use case is handed the very
	// session making the request, read from the cookie the browser sent.
	sessionID := ""
	if cookie, cookieErr := r.Cookie(sessionCookie); cookieErr == nil {
		sessionID = cookie.Value
	}
	newPassword := r.Form.Get(passwordFieldNew)
	if newPassword != r.Form.Get(passwordFieldConfirm) {
		err = &domain.ValidationError{
			Field:   passwordFieldConfirm,
			Message: "the new passwords do not match",
			Key:     "error.password_confirm_mismatch",
		}
	} else {
		err = h.users.ChangeOwnPassword(r.Context(), *userFromContext(r.Context()), sessionID, r.Form.Get(passwordFieldCurrent), newPassword)
	}
	if err != nil {
		status, msg := mapErrorFor(r, err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		// A refused submission re-renders the page with the refusal beside the
		// field it belongs to and changes nothing. No password value is echoed
		// back into the form.
		current, getErr := h.preferences.GetDefaultQueueOrder(r.Context(), *userFromContext(r.Context()))
		if getErr != nil {
			httpError(w, r, "error.internal_server", http.StatusInternalServerError)
			return
		}
		data, getErr := h.data(r, current, "")
		if getErr != nil {
			httpError(w, r, "error.internal_server", http.StatusInternalServerError)
			return
		}
		data.Password.Error = msg
		data.Password.ErrorField = passwordErrorField(err)
		h.renderer.Render(w, r, "preferences", "", data, status)
		return
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, "/preferences")
}

// passwordErrorField maps a refused password submission onto the field it
// belongs to. The use cases name their finding "password" because they store ONE
// password; on this form that is the new-password field, while the current
// password and the confirmation already carry their own names.
func passwordErrorField(err error) string {
	var validation *domain.ValidationError
	if !errors.As(err, &validation) {
		return ""
	}
	if validation.Field == "password" {
		return passwordFieldNew
	}
	return validation.Field
}
