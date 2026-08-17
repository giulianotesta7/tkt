package httpadapter

import (
	"net/http"

	"github.com/giulianotesta7/tkt/internal/application"
)

// SettingsHandlers expose the instance appearance settings (appearance
// panel): GET /settings renders the page, POST /settings/appearance
// persists the internal-comment background color. Both routes are gated on
// CapManageUsers (admin/root) at the HTTP boundary; the application service
// re-enforces the same capability before mutating anything.
type SettingsHandlers struct {
	settings *application.SettingsService
	renderer *Renderer
}

// NewSettingsHandlers wires the settings routes against the appearance use
// cases and the renderer.
func NewSettingsHandlers(settings *application.SettingsService, renderer *Renderer) *SettingsHandlers {
	return &SettingsHandlers{settings: settings, renderer: renderer}
}

// Register mounts the settings routes.
func (h *SettingsHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /settings", h.index)
	mux.HandleFunc("POST /settings/appearance", h.updateAppearance)
}

// settingsIndexData is the appearance panel payload; Error carries a
// rejected-update message (422 re-render).
type settingsIndexData struct {
	pageData
	Error   string
	Current string
	Colors  []appearanceOption
}

// appearanceOption pairs a selectable color with its display label.
type appearanceOption struct {
	Value string
	Label string
}

// appearanceOptions lists the four selectable internal-comment background
// colors in the canonical order (azul/verde/violeta/amarillo).
func appearanceOptions() []appearanceOption {
	labels := map[string]string{
		application.DefaultInternalCommentBg: "Azul",
		"#E2F2EA":                            "Verde",
		"#EFE9FB":                            "Violeta",
		"#FFF6DC":                            "Amarillo",
	}
	colors := application.AllowedInternalCommentBg()
	out := make([]appearanceOption, 0, len(colors))
	for _, c := range colors {
		out = append(out, appearanceOption{Value: c, Label: labels[c]})
	}
	return out
}

func (h *SettingsHandlers) index(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageUsers) {
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
	// The shell CSS must carry the same color the panel shows; stamp the
	// authoritative read into the page data.
	data.InternalCommentBg = current
	h.renderer.Render(w, r, "settings_index", "", data, http.StatusOK)
}

// updateAppearance persists the chosen color. A rejected color re-renders
// the page with an inline error (422) and changes nothing; success
// redirects 303 back to /settings (HTMX follows it natively).
func (h *SettingsHandlers) updateAppearance(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageUsers) {
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
		data.InternalCommentBg = current
		h.renderer.Render(w, r, "settings_index", "", data, status)
		return
	}
	redirect(w, r, "/settings")
}
