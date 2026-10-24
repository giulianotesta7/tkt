package httpadapter

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Self-service password recovery, public side (issue #414, S3/S5/S7/S9).
//
// Two pages live under /login/ beside the sign-in form, so a visitor who cannot
// sign in never leaves the entry shell: /login/forgot asks for the address and
// always answers the same neutral confirmation, and /login/reset takes the link
// the instance mailed and stores the new password. The link carries a
// single-use token in its query string, which shapes the whole surface:
//
//   - the token is only ever echoed into the form's hidden field, never into a
//     redirect Location and never into a log line;
//   - both pages answer no-store and same-origin: no cache keeps them, and no
//     request to another origin carries the token as a referrer. The pages load
//     nothing from a third party, so the token never leaves the site, and
//     serving them same-origin (rather than no-referrer) is what lets their
//     forms submit with a real Origin header the shared gate can check;
//   - an unknown, spent and expired link answer ONE refusal, so a visitor
//     holding a stale link learns nothing about another one.
//
// Recovery is OFFERED only while the instance switch is ON AND email delivery is
// configured (S1/S5): otherwise the sign-in page shows no link and every
// recovery route answers 404, because a path that cannot mail a link does not
// exist for the visitor.
const (
	// passwordResetTokenField is the hidden field the reset form carries, so the
	// submission that redeems the link travels with the same token the link did.
	passwordResetTokenField = "token"
	// passwordResetTokenParam is the query parameter a mailed link carries.
	passwordResetTokenParam = "token"
	// loginPasswordChangedQuery is the flag the successful reset redirects with:
	// it carries no token, so the sign-in page can banner the outcome without
	// putting a credential in a URL that lands in history.
	loginPasswordChangedQuery = "password_changed"
)

// forgotData is the /login/forgot payload. Sent renders the neutral
// confirmation IN PLACE of the form, in the same card: the page never navigates
// away from the address the visitor submitted to and never echoes it back.
type forgotData struct {
	loginData
	Sent bool
}

// resetData is the /login/reset payload: the form that asks for the new
// password twice, or the refusal state a dead link renders.
type resetData struct {
	loginData
	// Refused renders the refusal state, with its action back to the public
	// form. It carries no token, so there is nothing left to submit.
	Refused bool
	// Token is the link's token, rendered only into the form's hidden field.
	Token string
	// MinLength is the instance minimum the helper text teaches (S4).
	MinLength int
	// ErrorField names the field a refused submission belongs to.
	ErrorField string
}

// passwordRecoveryReadLog deduplicates the recovery-availability read failure
// with the same policy every other request-level read uses (R4-002): the first
// failure logs, the next success re-arms the counter, and a long outage logs
// once every interfaceLanguageEveryNthFailure attempts.
type passwordRecoveryReadLog struct {
	mu      sync.Mutex
	failing bool
	count   int
}

func (l *passwordRecoveryReadLog) failed(err error) {
	l.mu.Lock()
	l.count++
	report := !l.failing || l.count%interfaceLanguageEveryNthFailure == 0
	l.failing = true
	l.mu.Unlock()
	if report {
		log.Printf("httpadapter: read password recovery settings: %v (recovery is not offered)", err)
	}
}

func (l *passwordRecoveryReadLog) recovered() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failing = false
	l.count = 0
}

// passwordRecoveryOfferOf answers whether the public recovery path is offered
// (S1/S5): the instance switch is ON and email delivery is configured, so a link
// could actually be mailed. The policy is returned with it, so a page teaches the
// same minimum the write enforces. A handler set wired without these use cases
// offers nothing, which is the same answer an unconfigured instance gives.
func passwordRecoveryOfferOf(ctx context.Context, passwords *application.PasswordSettingsService, email application.DeliveryConfigSource) (application.PasswordSettings, bool, error) {
	if passwords == nil || email == nil {
		return application.PasswordSettings{}, false, nil
	}
	policy, err := passwords.Get(ctx)
	if err != nil {
		return application.PasswordSettings{}, false, err
	}
	if !policy.RecoveryEnabled {
		return policy, false, nil
	}
	_, ok, err := email.DeliveryConfig(ctx)
	if err != nil {
		return policy, false, err
	}
	return policy, ok, nil
}

// passwordRecoveryOffer answers the availability of the recovery path for one
// request.
func (h *AuthHandlers) passwordRecoveryOffer(ctx context.Context) (application.PasswordSettings, bool, error) {
	if h.reset == nil {
		return application.PasswordSettings{}, false, nil
	}
	return passwordRecoveryOfferOf(ctx, h.passwords, h.email)
}

// recoveryOffered answers whether the sign-in page offers the recovery link. A
// failing read is NOT fatal: the page renders without the link and the failure is
// logged under the dedup policy, because losing a shortcut must never cost the
// last screen an operator can afford to lose.
func (h *AuthHandlers) recoveryOffered(ctx context.Context) bool {
	_, ok, err := h.passwordRecoveryOffer(ctx)
	if err != nil {
		h.recoveryFailures.failed(err)
		return false
	}
	h.recoveryFailures.recovered()
	return ok
}

// recoveryChrome builds the shell payload the two public pages share with the
// sign-in form: the instance identity and the presentation panel, so the entry
// screen does not change its shape halfway through a recovery.
func (h *AuthHandlers) recoveryChrome(r *http.Request) loginData {
	return loginData{Brand: brandFromContext(r.Context()), Panel: h.panel(r, true)}
}

// setRecoveryHeaders marks a public recovery response as uncacheable and as
// keeping its URL inside the site: the reset link carries a single-use token in
// the query string, so no cache may keep the page and no request to another
// origin may carry the token as a Referer. The pages reference nothing but their
// own assets, so same-origin reaches nobody outside the instance — and, unlike
// no-referrer, it leaves a browser submitting these forms with its real Origin
// header, which the shared Origin gate checks.
func setRecoveryHeaders(w http.ResponseWriter) {
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Cache-Control", "no-store")
}

// refuseRecovery answers a route the instance does not offer (S1/S5). It is the
// same 404 an unknown path answers, so a visitor cannot tell a switched-off
// recovery path from a route that never existed.
func refuseRecovery(w http.ResponseWriter, r *http.Request) {
	httpError(w, r, "error.page_not_found", http.StatusNotFound)
}

// forgotForm serves the public recovery form.
func (h *AuthHandlers) forgotForm(w http.ResponseWriter, r *http.Request) {
	setRecoveryHeaders(w)
	_, offered, err := h.passwordRecoveryOffer(r.Context())
	if err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	if !offered {
		refuseRecovery(w, r)
		return
	}
	h.renderer.Render(w, r, "login_forgot", "", forgotData{loginData: h.recoveryChrome(r)}, http.StatusOK)
}

// forgot asks for a link to be mailed (S3/S9). Every answer about an address is
// the SAME neutral confirmation with the SAME status — known, unknown, inactive
// and throttled alike — because the use case resolves all four to the same nil.
// Only an operational failure differs, and it answers 500 rather than a sentence
// that would read as a fact about the address.
func (h *AuthHandlers) forgot(w http.ResponseWriter, r *http.Request) {
	setRecoveryHeaders(w)
	_, offered, err := h.passwordRecoveryOffer(r.Context())
	if err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	if !offered {
		refuseRecovery(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	// The submitted value is handed over and then forgotten: the confirmation
	// never echoes it, so one response cannot be told apart from another.
	if err := h.reset.RequestPasswordReset(r.Context(), strings.TrimSpace(r.Form.Get("email"))); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	h.renderer.Render(w, r, "login_forgot", "", forgotData{loginData: h.recoveryChrome(r), Sent: true}, http.StatusOK)
}

// renderResetRefused renders the ONE refusal state a dead link answers (S3). It
// keeps the visitor on the entry shell with an action that asks for a new link,
// and it carries no token at all.
func (h *AuthHandlers) renderResetRefused(w http.ResponseWriter, r *http.Request) {
	h.renderer.Render(w, r, "login_reset", "", resetData{loginData: h.recoveryChrome(r), Refused: true}, http.StatusGone)
}

// resetForm serves the new-password form for a link that still works. The
// check does not consume the link, so a reload, a shared device or a browser that
// prefetches the address cannot burn it before the visitor submits.
func (h *AuthHandlers) resetForm(w http.ResponseWriter, r *http.Request) {
	setRecoveryHeaders(w)
	policy, offered, err := h.passwordRecoveryOffer(r.Context())
	if err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	if !offered {
		refuseRecovery(w, r)
		return
	}
	token := r.URL.Query().Get(passwordResetTokenParam)
	usable, err := h.reset.CheckPasswordResetToken(r.Context(), token)
	if err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	if !usable {
		h.renderResetRefused(w, r)
		return
	}
	h.renderer.Render(w, r, "login_reset", "", resetData{loginData: h.recoveryChrome(r), Token: token, MinLength: policy.MinLength}, http.StatusOK)
}

// resetPassword stores the new password the link's owner chose (S3/S4).
//
// The confirmation is compared HERE, before the use case runs, so a mistyped
// confirmation costs nothing. The use case then enforces the instance minimum
// before it touches the link, so a too-short password leaves the link usable and
// the visitor can simply try again. Success answers 303 to the sign-in page with a
// flag that carries no token — every session of the account is already gone, so
// the visitor signs in again with the password they just chose.
func (h *AuthHandlers) resetPassword(w http.ResponseWriter, r *http.Request) {
	setRecoveryHeaders(w)
	policy, offered, err := h.passwordRecoveryOffer(r.Context())
	if err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	if !offered {
		refuseRecovery(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	token := strings.TrimSpace(r.Form.Get(passwordResetTokenField))
	newPassword := r.Form.Get(passwordFieldNew)
	var failure error
	if newPassword != r.Form.Get(passwordFieldConfirm) {
		failure = &domain.ValidationError{
			Field:   passwordFieldConfirm,
			Message: "the new passwords do not match",
			Key:     "error.password_confirm_mismatch",
		}
	} else {
		failure = h.reset.ResetPassword(r.Context(), token, newPassword)
	}
	if failure == nil {
		redirect(w, r, "/login?"+loginPasswordChangedQuery+"=1")
		return
	}

	// A dead link — and a path switched off between the two requests — answer the
	// refusal state: there is no field to correct and nothing was written.
	var linkError *application.PasswordResetLinkError
	var forbidden *domain.ForbiddenError
	if errors.As(failure, &linkError) || errors.As(failure, &forbidden) {
		h.renderResetRefused(w, r)
		return
	}

	status, msg := mapErrorFor(r, failure)
	if status == http.StatusInternalServerError {
		http.Error(w, msg, status)
		return
	}
	// A refused submission re-renders the form with the refusal beside the field
	// it belongs to. The token stays in the hidden field and no password value is
	// echoed back, so the link is still usable and nothing typed leaks.
	chrome := h.recoveryChrome(r)
	chrome.Error = msg
	h.renderer.Render(w, r, "login_reset", "", resetData{
		loginData:  chrome,
		Token:      token,
		MinLength:  policy.MinLength,
		ErrorField: passwordErrorField(failure),
	}, status)
}
