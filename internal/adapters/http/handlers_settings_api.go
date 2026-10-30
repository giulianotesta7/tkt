package httpadapter

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// The API section (issue #488, part 1) is where an administrator manages the
// service users the API acts as and the keys they own, and where root switches
// the API on or off. It is a Settings section of its own because both are
// instance configuration: service users are not people, so they never appear on
// the Users screen.
//
// Every route is gated on CapManageAPI (admin, root); the switch additionally
// needs CapToggleAPI (root only), and an administrator sees its state without
// the control. A created key's token is rendered once, on the response to the
// POST that created it, and never again: only the SHA-256 of its secret is
// stored, so there is nothing to show later.

const settingsSectionAPI = "api"

// apiScopeOptions lists the v1 scopes in the order the key form offers them,
// with the catalogue key of each label.
type apiScopeOption struct {
	Scope domain.APIKeyScope
	Label string
}

var apiScopeOptions = []apiScopeOption{
	{domain.ScopeCatalogRead, "settings.api.scope.catalog_read"},
	{domain.ScopeTicketsRead, "settings.api.scope.tickets_read"},
	{domain.ScopeTicketsWrite, "settings.api.scope.tickets_write"},
	{domain.ScopeCommentsWrite, "settings.api.scope.comments_write"},
	{domain.ScopeCommentsInternal, "settings.api.scope.comments_internal"},
	{domain.ScopeTicketsAll, "settings.api.scope.tickets_all"},
}

// WithAPIAccess wires the service users and API keys use cases behind the API
// section (issue #488).
func (h *SettingsHandlers) WithAPIAccess(api *application.APIAccessService) *SettingsHandlers {
	h.apiAccess = api
	return h
}

// WithOAuth wires the MCP clients section (issue #493, S2): the switch use
// cases, and the email settings port the connection URL derives from. The port
// is read directly because administrators see the URL too, and the Email use
// cases are root's.
func (h *SettingsHandlers) WithOAuth(oauth *application.OAuthService, publicURL application.EmailSettingsStore) *SettingsHandlers {
	h.oauth = oauth
	h.publicURL = publicURL
	return h
}

func (h *SettingsHandlers) registerAPI(mux *http.ServeMux) {
	mux.HandleFunc("POST /settings/api/toggle", h.toggleAPI)
	mux.HandleFunc("POST /settings/api/mcp", h.toggleMCP)
	mux.HandleFunc("POST /settings/api/service-users", h.createServiceUser)
	mux.HandleFunc("POST /settings/api/service-users/{id}/active", h.setServiceUserActive)
	mux.HandleFunc("POST /settings/api/keys", h.createAPIKey)
	mux.HandleFunc("POST /settings/api/keys/{id}/revoke", h.revokeAPIKey)
}

// settingsAPIData is the API section payload.
type settingsAPIData struct {
	settingsSectionData
	Enabled      bool
	CanToggle    bool
	ServiceUsers []apiServiceUserRow
	ScopeOptions []apiScopeOption
	// NewToken is the token of the key this response created, shown once.
	NewToken        string
	NewTokenFor     string
	SelectedID      int64
	CreateUserError bool
	KeyError        bool
	DraftName       string
	DraftRole       string
	DraftExpiry     string
	DraftScopes     map[domain.APIKeyScope]bool
	// MCP is the MCP clients section (issue #493, S2).
	MCP settingsMCPData
}

// settingsMCPData is the MCP clients section: the switch, who may flip it, the
// connection URL people give their assistant ("" while the public URL is
// unset), whether the viewer may open Email settings to set that URL, and the
// refusal of the last attempt to flip it.
type settingsMCPData struct {
	Shown           bool
	Enabled         bool
	CanToggle       bool
	URL             string
	CanSetPublicURL bool
	Error           string
}

type apiServiceUserRow struct {
	ID        int64
	Name      string
	RoleLabel string
	Active    bool
	CreatedOn string
	Keys      []apiKeyRow
	// CanInternal says whether the comments:internal scope applies (agents).
	CanInternal bool
}

type apiKeyRow struct {
	ID       int64
	Name     string
	PublicID string
	Scopes   []string
	// Status is the catalogue key of the key's state: active, expired, revoked.
	Status    string
	Live      bool
	CreatedOn string
	ExpiresOn string
	LastUsed  string
}

// requireAPIAccess gates the section on CapManageAPI and on the use cases being
// wired; a handler set built without them answers 404 rather than a nil panic.
func (h *SettingsHandlers) requireAPIAccess(w http.ResponseWriter, r *http.Request) bool {
	if h.apiAccess == nil {
		http.NotFound(w, r)
		return false
	}
	return requireCapability(w, r, application.CapManageAPI)
}

func apiDate(t time.Time) string { return t.UTC().Format("2006-01-02") }

func (h *SettingsHandlers) renderAPI(w http.ResponseWriter, r *http.Request, status int, message, newToken, newTokenFor string) {
	h.renderAPIWith(w, r, status, message, newToken, newTokenFor, "")
}

// renderAPIWith renders the section; mcpError is the refusal of the last
// attempt to flip the MCP switch, shown inside the MCP clients section.
func (h *SettingsHandlers) renderAPIWith(w http.ResponseWriter, r *http.Request, status int, message, newToken, newTokenFor, mcpError string) {
	actor := *userFromContext(r.Context())
	views, err := h.apiAccess.ListServiceUsers(r.Context(), actor)
	if err != nil {
		st, msg := mapErrorFor(r, err)
		http.Error(w, msg, st)
		return
	}
	enabled, err := h.apiAccess.APIEnabled(r.Context())
	if err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	now := time.Now()
	data := settingsAPIData{
		settingsSectionData: h.sectionData(r, settingsSectionAPI),
		Enabled:             enabled,
		CanToggle:           application.NewPolicy().Capabilities(actor.Role).Require(application.CapToggleAPI),
		NewToken:            newToken,
		NewTokenFor:         newTokenFor,
		ScopeOptions:        apiScopeOptions,
	}
	data.Error = message
	data.SelectedID, _ = strconv.ParseInt(r.URL.Query().Get("service_user"), 10, 64)
	if r.Method == http.MethodPost && r.URL.Path == "/settings/api/keys" {
		data.SelectedID, _ = strconv.ParseInt(r.PostFormValue("service_user_id"), 10, 64)
	}
	if r.Method == http.MethodPost && message != "" {
		switch r.URL.Path {
		case "/settings/api/service-users":
			data.CreateUserError = true
			data.DraftName, data.DraftRole = r.PostFormValue("name"), r.PostFormValue("role")
		case "/settings/api/keys":
			data.KeyError = true
			data.SelectedID, _ = strconv.ParseInt(r.PostFormValue("service_user_id"), 10, 64)
			data.DraftName, data.DraftExpiry = r.PostFormValue("name"), r.PostFormValue("expires_on")
			data.DraftScopes = make(map[domain.APIKeyScope]bool)
			for _, scope := range r.PostForm["scopes"] {
				data.DraftScopes[domain.APIKeyScope(scope)] = true
			}
		}
	}
	found := false
	if h.oauth != nil {
		mcp, err := h.mcpData(r, actor)
		if err != nil {
			httpError(w, r, "error.internal_server", http.StatusInternalServerError)
			return
		}
		mcp.Error = mcpError
		data.MCP = mcp
	}
	for _, v := range views {
		if v.User.ID == data.SelectedID {
			found = true
		}
		row := apiServiceUserRow{
			ID: v.User.ID, Name: v.User.Name, RoleLabel: userRoleLabel(v.User.Role), Active: v.User.Active,
			CreatedOn: apiDate(v.User.CreatedAt), CanInternal: v.User.Role.AtLeast(domain.RoleAgent),
		}
		for _, k := range v.Keys {
			kr := apiKeyRow{ID: k.ID, Name: k.Name, PublicID: k.PublicID, CreatedOn: apiDate(k.CreatedAt)}
			for _, s := range k.Scopes {
				kr.Scopes = append(kr.Scopes, string(s))
			}
			switch {
			case k.RevokedAt != nil:
				kr.Status = "settings.api.key.revoked"
			case k.ExpiresAt != nil && !now.Before(*k.ExpiresAt):
				kr.Status = "settings.api.key.expired"
			default:
				kr.Status = "settings.api.key.active"
				kr.Live = true
			}
			if k.ExpiresAt != nil {
				kr.ExpiresOn = apiDate(*k.ExpiresAt)
			}
			if k.LastUsedAt != nil {
				kr.LastUsed = formatDisplayTime(*k.LastUsedAt)
			}
			row.Keys = append(row.Keys, kr)
		}
		data.ServiceUsers = append(data.ServiceUsers, row)
	}
	if !found && len(data.ServiceUsers) > 0 {
		data.SelectedID = data.ServiceUsers[0].ID
	}
	h.renderer.Render(w, r, "settings_api", "", data, status)
}

// mcpData reads the MCP clients section: the switch, and the connection URL
// derived from the public URL exactly as the OAuth endpoints derive it.
func (h *SettingsHandlers) mcpData(r *http.Request, actor domain.User) (settingsMCPData, error) {
	enabled, err := h.oauth.MCPEnabled(r.Context())
	if err != nil {
		return settingsMCPData{}, err
	}
	stored, err := h.publicURL.GetEmailSettings(r.Context())
	if err != nil {
		return settingsMCPData{}, err
	}
	caps := application.NewPolicy().Capabilities(actor.Role)
	data := settingsMCPData{
		Shown:           true,
		Enabled:         enabled,
		CanToggle:       caps.Require(application.CapToggleAPI),
		CanSetPublicURL: h.email != nil && caps.Require(application.CapManageEmail),
	}
	if base := strings.TrimRight(stored.BaseURL, "/"); base != "" {
		data.URL = base + "/mcp"
	}
	return data, nil
}

// apiMutationFailed renders a refused mutation: an operational failure is a
// plain 500, anything else re-renders the section with the localized reason.
func (h *SettingsHandlers) apiMutationFailed(w http.ResponseWriter, r *http.Request, err error) {
	status, msg := mapErrorFor(r, err)
	if status == http.StatusInternalServerError {
		http.Error(w, msg, status)
		return
	}
	h.renderAPI(w, r, status, msg, "", "")
}

func (h *SettingsHandlers) apiSaved(w http.ResponseWriter, r *http.Request) {
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, settingsSectionHref(settingsSectionAPI))
}

func (h *SettingsHandlers) apiSavedFor(w http.ResponseWriter, r *http.Request, id int64) {
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, settingsSectionHref(settingsSectionAPI)+"?service_user="+strconv.FormatInt(id, 10))
}

// formSwitch reads a 0/1 switch field submitted exactly once. Any other value,
// an absent or repeated field included, is refused rather than read as "off":
// an explicit request is never silently turned into its opposite.
func formSwitch(r *http.Request, field string) (bool, bool) {
	values := r.PostForm[field]
	if len(values) != 1 {
		return false, false
	}
	switch values[0] {
	case "1":
		return true, true
	case "0":
		return false, true
	default:
		return false, false
	}
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func (h *SettingsHandlers) toggleAPI(w http.ResponseWriter, r *http.Request) {
	if !h.requireAPIAccess(w, r) || !requireCapability(w, r, application.CapToggleAPI) {
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	enabled, ok := formSwitch(r, "enabled")
	if !ok {
		httpError(w, r, "error.bad_request", http.StatusBadRequest)
		return
	}
	if err := h.apiAccess.SetAPIEnabled(r.Context(), *userFromContext(r.Context()), enabled); err != nil {
		h.apiMutationFailed(w, r, err)
		return
	}
	h.apiSaved(w, r)
}

// toggleMCP switches MCP clients on or off (issue #493, S2). It mirrors the
// API switch: root only, one strict mcp_enabled field. A refusal the person can
// act on, turning it on before the public URL is set, re-renders the section
// with the reason beside the switch.
func (h *SettingsHandlers) toggleMCP(w http.ResponseWriter, r *http.Request) {
	if h.oauth == nil {
		http.NotFound(w, r)
		return
	}
	if !h.requireAPIAccess(w, r) || !requireCapability(w, r, application.CapToggleAPI) {
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	enabled, ok := formSwitch(r, "mcp_enabled")
	if !ok {
		httpError(w, r, "error.bad_request", http.StatusBadRequest)
		return
	}
	if err := h.oauth.SetMCPEnabled(r.Context(), *userFromContext(r.Context()), enabled); err != nil {
		status, msg := mapErrorFor(r, err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		h.renderAPIWith(w, r, status, "", "", "", msg)
		return
	}
	h.apiSaved(w, r)
}

func (h *SettingsHandlers) createServiceUser(w http.ResponseWriter, r *http.Request) {
	if !h.requireAPIAccess(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	_, err := h.apiAccess.CreateServiceUser(r.Context(), *userFromContext(r.Context()),
		r.PostFormValue("name"), domain.Role(r.PostFormValue("role")))
	if err != nil {
		h.apiMutationFailed(w, r, err)
		return
	}
	h.apiSaved(w, r)
}

func (h *SettingsHandlers) setServiceUserActive(w http.ResponseWriter, r *http.Request) {
	if !h.requireAPIAccess(w, r) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	active, ok := formSwitch(r, "active")
	if !ok {
		httpError(w, r, "error.bad_request", http.StatusBadRequest)
		return
	}
	if err := h.apiAccess.SetServiceUserActive(r.Context(), *userFromContext(r.Context()), id, active); err != nil {
		h.apiMutationFailed(w, r, err)
		return
	}
	h.apiSavedFor(w, r, id)
}

// createAPIKey answers 200 with the section and the new token instead of a
// redirect: the token exists only in this response.
func (h *SettingsHandlers) createAPIKey(w http.ResponseWriter, r *http.Request) {
	if !h.requireAPIAccess(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	owner, err := strconv.ParseInt(r.PostFormValue("service_user_id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	in := application.CreateAPIKeyInput{ServiceUserID: owner, Name: r.PostFormValue("name"), Scopes: r.PostForm["scopes"]}
	if raw := strings.TrimSpace(r.PostFormValue("expires_on")); raw != "" {
		day, err := time.Parse("2006-01-02", raw)
		if err != nil {
			h.apiMutationFailed(w, r, &domain.ValidationError{Field: "expires_at", Message: application.ErrMsgAPIKeyExpiryPast, Key: "error.api.expiry_past"})
			return
		}
		in.ExpiresAt = &day
	}
	key, token, err := h.apiAccess.CreateAPIKey(r.Context(), *userFromContext(r.Context()), in)
	if err != nil {
		h.apiMutationFailed(w, r, err)
		return
	}
	// The token exists only in this response; no cache may keep a copy.
	w.Header().Set("Cache-Control", "no-store")
	h.renderAPI(w, r, http.StatusOK, "", token, key.Name)
}

func (h *SettingsHandlers) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	if !h.requireAPIAccess(w, r) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	actor := *userFromContext(r.Context())
	views, err := h.apiAccess.ListServiceUsers(r.Context(), actor)
	if err != nil {
		h.apiMutationFailed(w, r, err)
		return
	}
	var owner int64
	for _, view := range views {
		for _, key := range view.Keys {
			if key.ID == id {
				owner = view.User.ID
			}
		}
	}
	if err := h.apiAccess.RevokeAPIKey(r.Context(), actor, id); err != nil {
		h.apiMutationFailed(w, r, err)
		return
	}
	if owner != 0 {
		h.apiSavedFor(w, r, owner)
	} else {
		h.apiSaved(w, r)
	}
}
