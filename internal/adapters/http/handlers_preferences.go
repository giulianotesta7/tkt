package httpadapter

import (
	"net/http"

	"github.com/giulianotesta7/tkt/internal/application"
)

// PreferencesHandlers expose the per-user preferences page (issue #210):
// GET /preferences renders the page for the session user and POST
// /preferences persists the default queue order.
//
// There is deliberately NO capability gate. A preference is personal, not
// instance configuration: every authenticated user owns the row, and the
// session middleware is what keeps anonymous callers out (an unauthenticated
// request is redirected to /login before this handler runs).
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
}

// preferencesData is the preferences page payload. Current is already
// normalized to the closed set, so the control always has a selected option
// even when the stored row is unknown (fail closed).
type preferencesData struct {
	pageData
	Error   string
	Current string
	Orders  []queueOrderOption
}

// queueOrderOption is one selectable queue order.
type queueOrderOption struct {
	Value string
	Label string
}

// queueOrderOptions lists the selectable orders in the closed set's order,
// reusing the exact labels the queue's own order control already shows.
func queueOrderOptions() []queueOrderOption {
	labels := map[string]string{
		application.QueueOrderNewest:   "Newest first",
		application.QueueOrderPriority: "Priority",
		application.QueueOrderUrgency:  "Urgency",
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
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	h.renderer.Render(w, r, "preferences", "", h.data(r, current, ""), http.StatusOK)
}

// update persists the submitted queue order. A rejected value re-renders the
// page with an inline error (422) and changes nothing; success redirects 303
// back to /preferences with the shared save feedback.
func (h *PreferencesHandlers) update(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	err := h.preferences.SetDefaultQueueOrder(r.Context(), *userFromContext(r.Context()), r.Form.Get("queue_order"))
	if err != nil {
		status, msg := mapError(err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		// The rejected value is not echoed into the control: the page shows
		// the still-stored (normalized) value, so an invalid submission can
		// never render an unvalidated order as selected.
		current, getErr := h.preferences.GetDefaultQueueOrder(r.Context(), *userFromContext(r.Context()))
		if getErr != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		h.renderer.Render(w, r, "preferences", "", h.data(r, current, msg), status)
		return
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, "/preferences")
}

// data builds the page payload from the normalized current order and an
// optional error message.
func (h *PreferencesHandlers) data(r *http.Request, current, msg string) preferencesData {
	return preferencesData{
		pageData: pageDataFrom(r, "preferences"),
		Error:    msg,
		Current:  current,
		Orders:   queueOrderOptions(),
	}
}
