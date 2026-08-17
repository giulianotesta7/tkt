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
}

// NewAuthHandlers wires the auth handlers against the auth and user use
// cases and the renderer.
func NewAuthHandlers(auth *application.AuthService, users *application.UserService, renderer *Renderer) *AuthHandlers {
	return &AuthHandlers{auth: auth, users: users, renderer: renderer}
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
	Error             string
	Email             string
	InternalCommentBg string
}

func (h *AuthHandlers) loginForm(w http.ResponseWriter, r *http.Request) {
	if count, err := h.auth.UserCount(r.Context()); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	} else if count == 0 {
		redirect(w, r, "/setup")
		return
	}
	h.renderer.Render(w, r, "login", "", loginData{}, http.StatusOK)
}

func (h *AuthHandlers) login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	email := strings.TrimSpace(r.Form.Get("email"))
	password := r.Form.Get("password")

	session, err := h.auth.Login(r.Context(), email, password)
	if err != nil {
		status, msg := mapError(err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		h.renderer.Render(w, r, "login", "", loginData{Error: msg, Email: email}, status)
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
			http.Error(w, "Internal server error", http.StatusInternalServerError)
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
	Error             string
	Name              string
	Email             string
	InternalCommentBg string
}

func (h *AuthHandlers) setupForm(w http.ResponseWriter, r *http.Request) {
	// Defense in depth: the middleware already gates /setup*, but a direct
	// handler call must never expose the bootstrap form with users present.
	if count, err := h.auth.UserCount(r.Context()); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	} else if count > 0 {
		redirect(w, r, "/login")
		return
	}
	h.renderer.Render(w, r, "setup", "", setupData{}, http.StatusOK)
}

func (h *AuthHandlers) setup(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
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
		status, msg := mapError(err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		h.renderer.Render(w, r, "setup", "", setupData{Error: msg, Name: name, Email: email}, status)
		return
	}

	redirect(w, r, "/login")
}
