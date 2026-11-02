package httpadapter

import (
	"net/http"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/version"
)

func (h *SettingsHandlers) WithInstance(instance *application.InstanceSettingsService) *SettingsHandlers {
	h.instance = instance
	return h
}

type settingsInstanceData struct {
	settingsSectionData
	Address                string
	PreviousCompanyAddress string
	CurrentLanguage        string
	Languages              []interfaceLanguageOption
	Version                string
}

func (h *SettingsHandlers) requireInstance(w http.ResponseWriter, r *http.Request) bool {
	if h.instance == nil {
		http.NotFound(w, r)
		return false
	}
	// CapManageEmail is root-only; the instance service independently enforces root.
	return requireCapability(w, r, application.CapManageEmail)
}

func (h *SettingsHandlers) renderInstance(w http.ResponseWriter, r *http.Request, status int, message string, echo *string) {
	stored, err := h.instance.Get(r.Context(), *userFromContext(r.Context()))
	if err != nil {
		status, message := mapErrorFor(r, err)
		http.Error(w, message, status)
		return
	}
	language, err := h.settings.GetInterfaceLanguage(r.Context())
	if err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	data := settingsInstanceData{
		settingsSectionData:    h.sectionData(r, settingsSectionInstance),
		Address:                stored.Address,
		PreviousCompanyAddress: stored.PreviousCompanyAddress,
		CurrentLanguage:        language,
		Languages:              interfaceLanguageOptions(),
		Version:                version.String(),
	}
	data.Error = message
	if echo != nil {
		data.Address = *echo
	}
	h.renderer.Render(w, r, "settings_instance", "", data, status)
}

func (h *SettingsHandlers) updateInstance(w http.ResponseWriter, r *http.Request) {
	if !h.requireInstance(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.bad_request", http.StatusBadRequest)
		return
	}
	address := r.Form.Get("instance_address")
	if err := h.instance.Save(r.Context(), *userFromContext(r.Context()), address); err != nil {
		status, message := mapErrorFor(r, err)
		if status == http.StatusInternalServerError {
			http.Error(w, message, status)
			return
		}
		h.renderInstance(w, r, status, message, &address)
		return
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, settingsSectionHref(settingsSectionInstance))
}
