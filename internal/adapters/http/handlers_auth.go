package httpadapter

import (
	"errors"
	"net/http"
	"strings"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/internal/i18n"
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
}

// loginData is the login page payload: the generic error message (D5) and
// the email prefilled for a failed attempt. InternalCommentBg keeps the
// shared styles partial happy (auth pages render the static default).
type loginData struct {
	Error string
	Email string
	// Brand is the instance identity, stamped by the session middleware.
	Brand brandData
	// Panel is the split shell's presentation panel (issue #383, S9): the stored
	// customization or the catalogue defaults.
	Panel             signinPanelData
	CategoryAssets    bool
	InternalCommentBg string
}

func (h *AuthHandlers) loginForm(w http.ResponseWriter, r *http.Request) {
	if count, err := h.auth.UserCount(r.Context()); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	} else if count == 0 {
		redirect(w, r, "/setup")
		return
	}
	h.renderer.Render(w, r, "login", "", loginData{Brand: brandFromContext(r.Context()), Panel: h.panel(r, true)}, http.StatusOK)
}

// passwordSignInOffKey is the catalogue key of the sentence a verified
// non-root password answers once password sign-in is turned off (issue #419,
// S4). It renders on the sign-in page itself, the same path the generic
// credential failure uses, so the person learns which way to sign in instead of
// being told their password is wrong.
const passwordSignInOffKey = "error.password_signin_off"

func (h *AuthHandlers) login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	email := strings.TrimSpace(r.Form.Get("email"))
	password := r.Form.Get("password")

	session, err := h.auth.Login(r.Context(), email, password)
	if err != nil {
		// A verified password of a non-root account is refused while password
		// sign-in is off (issue #419, S4): its own sentence, the same page, and
		// no session. The generic credential failure keeps its own status.
		var signInOff *application.PasswordSignInOffError
		if errors.As(err, &signInOff) {
			h.renderer.Render(w, r, "login", "", loginData{Error: i18n.T(requestLocale(r), passwordSignInOffKey), Email: email, Brand: brandFromContext(r.Context()), Panel: h.panel(r, true)}, http.StatusForbidden)
			return
		}
		status, msg := mapErrorFor(r, err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		h.renderer.Render(w, r, "login", "", loginData{Error: msg, Email: email, Brand: brandFromContext(r.Context()), Panel: h.panel(r, true)}, status)
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
