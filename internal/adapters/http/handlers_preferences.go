package httpadapter

import (
	"net/http"

	"github.com/giulianotesta7/tkt/internal/application"
)

// PreferencesHandlers expose the per-user preferences page (issue #210):
// GET /preferences renders the page for the session user, POST /preferences
// persists the default queue order, and POST /preferences/email persists the
// notification-email toggle (issue #362, S5).
//
// There is deliberately NO capability gate. A preference is personal, not
// instance configuration: every authenticated user owns the row, and the
// session middleware is what keeps anonymous callers out (an unauthenticated
// request is redirected to /login before this handler runs).
//
// One form per preference, each posting to its own route. The two preferences
// share the user_preferences table, and a single form would have to guess the
// unsent half: an unchecked checkbox is simply absent from a form body, so a
// queue-order submission carrying no email field would be indistinguishable
// from "turn email off". Separate actions remove the ambiguity at the wire
// level — a route only ever writes the one key it names — instead of relying
// on a hidden companion field nobody may forget.
type PreferencesHandlers struct {
	preferences *application.PreferencesService
	renderer    *Renderer
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
}

// preferencesData is the preferences page payload. Current is already
// normalized to the closed set, so the control always has a selected option
// even when the stored row is unknown (fail closed). EmailNotifications
// carries the stored opt-out; absent means on, so the box starts checked.
type preferencesData struct {
	pageData
	Error              string
	Current            string
	Orders             []queueOrderOption
	EmailNotifications bool
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

// data builds the page payload from the normalized current order and an
// optional error message, reading the actor's own email preference for the
// toggle. A storage failure is returned so the caller can answer 500 instead
// of rendering a guessed checkbox state.
func (h *PreferencesHandlers) data(r *http.Request, current, msg string) (preferencesData, error) {
	enabled, err := h.preferences.GetEmailNotifications(r.Context(), *userFromContext(r.Context()))
	if err != nil {
		return preferencesData{}, err
	}
	return preferencesData{
		pageData:           pageDataFrom(r, "preferences"),
		Error:              msg,
		Current:            current,
		Orders:             queueOrderOptions(),
		EmailNotifications: enabled,
	}, nil
}
