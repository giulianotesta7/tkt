package httpadapter

import (
	"errors"
	"net/http"
	"strings"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// AuthHandlers implement the authentication and bootstrap routes (design
// "HTTP Layer" route table): GET/POST /login, POST /logout, GET/POST /setup
// (D16 first-user bootstrap). Session enforcement, /setup availability, and
// the Origin gate live in the SessionMiddleware; these handlers only parse,
// call the use cases, and render.
type AuthHandlers struct {
	auth     *application.AuthService
	users    *application.UserService
	renderer *Renderer
	// signin is the OPTIONAL sign-in page reader (issue #383, S9/S10): the split
	// shell's presentation panel renders its stored customization, or the
	// catalogue defaults when nothing is stored. A handler set built without it
	// renders the defaults, so a caller that does not care keeps working.
	signin *application.SigninService
	// signinFailures deduplicates the panel read failure (issue #383, S9).
	signinFailures signinReadLog
	// reset is the OPTIONAL self-service recovery use case (issue #414, S3):
	// with it the handler set serves the two public recovery pages. A handler
	// set built without it (a focused test, a surface that does not offer
	// recovery) keeps the routes answering 404, so the path is never half
	// wired.
	reset *application.PasswordResetService
	// passwords and email answer whether the public recovery path is offered at
	// all (S1/S5): the instance switch and the email-delivery configuration.
	passwords *application.PasswordSettingsService
	email     application.DeliveryConfigSource
	// recoveryFailures deduplicates the availability read failure.
	recoveryFailures passwordRecoveryReadLog
}

// NewAuthHandlers wires the auth handlers against the auth and user use
// cases and the renderer.
func NewAuthHandlers(auth *application.AuthService, users *application.UserService, renderer *Renderer) *AuthHandlers {
	return &AuthHandlers{auth: auth, users: users, renderer: renderer}
}

// WithSignin wires the sign-in page use cases into the auth shell (issue #383,
// S9/S10).
func (h *AuthHandlers) WithSignin(signin *application.SigninService) *AuthHandlers {
	h.signin = signin
	return h
}

// WithPasswordRecovery wires the self-service recovery path (issue #414, S3):
// the use cases behind the two public pages, plus the instance policy and the
// email configuration that decide whether the path is offered at all (S1/S5).
func (h *AuthHandlers) WithPasswordRecovery(reset *application.PasswordResetService, passwords *application.PasswordSettingsService, email application.DeliveryConfigSource) *AuthHandlers {
	h.reset = reset
	h.passwords = passwords
	h.email = email
	return h
}

// panel answers the presentation panel of one auth page: the stored
// customization for the sign-in page, the catalogue defaults for the setup page
// (which runs before an instance is configured). A failing read is not fatal —
// the page renders the defaults and the failure is logged — because losing the
// panel must never cost the sign-in screen.
func (h *AuthHandlers) panel(r *http.Request, stored bool) signinPanelData {
	brand := brandFromContext(r.Context())
	configuration := application.SigninConfiguration{}
	if stored && h.signin != nil {
		read, err := h.signin.GetSigninConfiguration(r.Context())
		if err != nil {
			h.signinFailures.failed(err)
		} else {
			h.signinFailures.recovered()
			configuration = read
		}
	}
	return signinPanelDataFor(configuration, brand, requestLocale(r))
}

// Register mounts the auth routes (D9 method+path patterns).
func (h *AuthHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /login", h.loginForm)
	mux.HandleFunc("POST /login", h.login)
	mux.HandleFunc("POST /logout", h.logout)
	mux.HandleFunc("GET /setup", h.setupForm)
	mux.HandleFunc("POST /setup", h.setup)
	// Issue #414: the two public recovery pages. They live under /login/, which
	// the session middleware already treats as public, so a signed-out visitor
	// reaches them and a signed-in one is sent on to /tickets like everywhere
	// else in that branch.
	mux.HandleFunc("GET /login/forgot", h.forgotForm)
	mux.HandleFunc("POST /login/forgot", h.forgot)
	mux.HandleFunc("GET /login/reset", h.resetForm)
	mux.HandleFunc("POST /login/reset", h.resetPassword)
}

// loginData is the login page payload: the generic error message (D5) and
// the email prefilled for a failed attempt. InternalCommentBg keeps the
// shared styles partial happy (auth pages render the static default).
type loginData struct {
	Error string
	Email string
	// RecoveryOffered renders the "Forgot your password?" link under the
	// password field (issue #414): it is shown only while the instance
	// switch is ON and email delivery is configured (S1/S5).
	RecoveryOffered bool
	// PasswordChanged renders the banner a completed recovery lands with (S3):
	// the password was replaced and every session was signed out.
	PasswordChanged bool
	// Brand is the instance identity, stamped by the session middleware.
	Brand brandData
	// Panel is the split shell's presentation panel (issue #383, S9): the stored
	// customization or the catalogue defaults.
	Panel             signinPanelData
	CategoryAssets    bool
	InternalCommentBg string
}

// loginPageData builds the sign-in page payload: the shared shell chrome, the
// recovery link the instance offers, and the banner a completed password reset
// lands with. The banner travels as a query flag that carries no token, so a
// redirect can never leak the link a visitor just used.
func (h *AuthHandlers) loginPageData(r *http.Request) loginData {
	return loginData{
		Brand:           brandFromContext(r.Context()),
		Panel:           h.panel(r, true),
		RecoveryOffered: h.recoveryOffered(r.Context()),
		PasswordChanged: r.URL.Query().Get(loginPasswordChangedQuery) == "1",
	}
}

func (h *AuthHandlers) loginForm(w http.ResponseWriter, r *http.Request) {
	if count, err := h.auth.UserCount(r.Context()); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	} else if count == 0 {
		redirect(w, r, "/setup")
		return
	}
	h.renderer.Render(w, r, "login", "", h.loginPageData(r), http.StatusOK)
}

func (h *AuthHandlers) login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	email := strings.TrimSpace(r.Form.Get("email"))
	password := r.Form.Get("password")

	session, err := h.auth.Login(r.Context(), email, password)
	if err != nil {
		status, msg := mapErrorFor(r, err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		data := h.loginPageData(r)
		data.Error = msg
		data.Email = email
		h.renderer.Render(w, r, "login", "", data, status)
		return
	}

	http.SetCookie(w, sessionCookieValue(session, r.TLS != nil))
	redirect(w, r, "/tickets")
}

func (h *AuthHandlers) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if err := h.auth.Logout(r.Context(), c.Value); err != nil {
			// Session revocation failed: never report a successful logout
			// while the server-side session survives — answer a recoverable
			// 500 and keep the client cookie so the user can retry.
			httpError(w, r, "error.internal_server", http.StatusInternalServerError)
			return
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
	redirect(w, r, "/login")
}

// setupData is the first-user form payload (D16). InternalCommentBg keeps
// the shared styles partial happy (auth pages render the static default).
type setupData struct {
	Error string
	Name  string
	Email string
	// Brand is the instance identity, stamped by the session middleware.
	Brand brandData
	// Panel is the split shell's presentation panel. Setup renders the defaults.
	Panel             signinPanelData
	CategoryAssets    bool
	InternalCommentBg string
}

func (h *AuthHandlers) setupForm(w http.ResponseWriter, r *http.Request) {
	// Defense in depth: the middleware already gates /setup*, but a direct
	// handler call must never expose the bootstrap form with users present.
	if count, err := h.auth.UserCount(r.Context()); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	} else if count > 0 {
		redirect(w, r, "/login")
		return
	}
	h.renderer.Render(w, r, "setup", "", setupData{Brand: brandFromContext(r.Context()), Panel: h.panel(r, false)}, http.StatusOK)
}

func (h *AuthHandlers) setup(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	name := r.Form.Get("name")
	email := r.Form.Get("email")
	password := r.Form.Get("password")

	// The setup flow bootstraps the ROOT atomically (role-authorization
	// first-user bootstrap): concurrent submissions serialize on the store's
	// immediate transaction and exactly one root survives. A loser or a
	// late-comer hits ErrBootstrapUnavailable and is sent to login — the
	// bootstrap is gone, not an error page.
	_, err := h.users.BootstrapRoot(r.Context(), application.CreateUserInput{Name: name, Email: email, Password: password})
	if err != nil {
		if errors.Is(err, domain.ErrBootstrapUnavailable) {
			redirect(w, r, "/login")
			return
		}
		status, msg := mapErrorFor(r, err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		h.renderer.Render(w, r, "setup", "", setupData{Error: msg, Name: name, Email: email, Brand: brandFromContext(r.Context()), Panel: h.panel(r, false)}, status)
		return
	}

	redirect(w, r, "/login")
}
