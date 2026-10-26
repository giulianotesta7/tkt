package application

import (
	"context"
	"errors"
	"strings"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// ErrMsgCurrentPasswordIncorrect is the ONE refusal for a wrong current
// password on the self-service path (issue #414, S2). It says what is wrong
// with the submission, never whether the account or the stored hash exists, so
// it cannot be used to probe an account.
const ErrMsgCurrentPasswordIncorrect = "the current password is incorrect"

// CurrentPasswordRefusal builds the field refusal that carries
// ErrMsgCurrentPasswordIncorrect. Two situations answer with it: a current
// password that does not verify, and a submission whose account was changed
// after it verified — the write port's compare-and-set reports the latter, so a
// lost race is indistinguishable from a mistyped password and the person simply
// retries.
func CurrentPasswordRefusal() *domain.ValidationError {
	return &domain.ValidationError{
		Field:   "current_password",
		Message: ErrMsgCurrentPasswordIncorrect,
		Key:     "error.current_password_incorrect",
	}
}

// WithPasswordChangeStore wires the atomic write an own-password change needs
// (issue #414, S2): the new hash and the sign-out of every other session commit
// together. Without it the use case refuses instead of hashing a secret nobody
// can store.
func (s *UserService) WithPasswordChangeStore(changes PasswordChangeStore) *UserService {
	s.passwordChanges = changes
	return s
}

// WithCompanySignIn wires the company sign-in configuration the own-password
// change reads (issue #419, S5). Without it password sign-in stays on.
func (s *UserService) WithCompanySignIn(store CompanySignInSettingsStore) *UserService {
	s.companySignIn = store
	return s
}

// errMsgPasswordChangeDisabled is the ONE refusal for the own-password path
// while the instance does not offer it (issue #414 S1, issue #419 S5): both
// switches remove the same path, so they answer the same sentence.
const errMsgPasswordChangeDisabled = "changing your own password is disabled on this instance"

// ChangeOwnPassword lets the SIGNED-IN user replace their own password (issue
// #414, S2): the current password is required, the session in use stays signed
// in, and every other session of that account is signed out.
//
// The order of the guards is deliberate. The instance policy is read first and
// its failure is returned instead of defaulted away, so an unreadable policy
// refuses the change (fail closed, as every password write does) rather than
// assuming the switch is ON. The switch then refuses the whole path while it is
// OFF (S1) — nothing is reloaded, hashed, or written. Only then is the ACTOR
// RELOADED from the store: the actor carries the session's identity, never a
// trusted hash, so an account deactivated after signing in is refused here.
// The reloaded account's role then decides the second switch, password sign-in
// (issue #419, S5): off for everyone but root, it removes this path with the
// same refusal the S1 switch uses. The current password is verified against
// that stored hash before the new one is even validated, so a wrong current
// password answers with its own field refusal and changes nothing.
func (s *UserService) ChangeOwnPassword(ctx context.Context, actor domain.User, currentSessionID string, currentPassword, newPassword string) error {
	policy, err := s.passwordPolicy(ctx)
	if err != nil {
		return err
	}
	if !policy.ChangeEnabled {
		return domain.NewForbiddenError(errMsgPasswordChangeDisabled)
	}
	user, err := s.users.GetByID(ctx, actor.ID)
	if err != nil {
		return err
	}
	if !user.Active {
		return domain.NewForbiddenError("an inactive account cannot change its password")
	}
	// An account a company sign-in created has no password, so there is no
	// current one to verify and the section is never offered to it; the route
	// refuses it the same way instead of answering "incorrect password".
	if user.PasswordHash == "" {
		return domain.NewForbiddenError(errMsgPasswordChangeDisabled)
	}
	allowed, err := passwordSignInAllowed(ctx, s.companySignIn, user.Role)
	if err != nil {
		return err
	}
	if !allowed {
		return domain.NewForbiddenError(errMsgPasswordChangeDisabled)
	}
	// S2 keeps the session IN USE signed in, so a caller that cannot name it
	// would sign out every session instead, the current one included.
	if strings.TrimSpace(currentSessionID) == "" {
		return domain.NewForbiddenError("a password change needs the session making the request")
	}
	if !VerifyPassword(user.PasswordHash, currentPassword) {
		return CurrentPasswordRefusal()
	}
	if err := ValidateNewPassword(newPassword, policy.MinLength); err != nil {
		return err
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	if s.passwordChanges == nil {
		// A composition-root defect, not a user mistake: reporting a change that
		// was never persisted is the one outcome the store exists to prevent.
		return errors.New("the password change store is not wired")
	}
	// The hash the current password was just verified against travels with the
	// write, so the store only applies it while that hash is still the stored
	// one: two submissions that both verified the old password cannot both be
	// stored, and the one that lost answers the current-password refusal.
	return s.passwordChanges.ChangePasswordAndSignOutOtherSessions(ctx, user.ID, user.PasswordHash, hash, currentSessionID)
}

// PasswordSignInAllowed answers whether the actor's password paths are still
// offered (issue #419, S5): root always keeps them, and every other account
// keeps them only while password sign-in is on. The Preferences page hides the
// own-password section with it, and the route refuses with it.
func (s *UserService) PasswordSignInAllowed(ctx context.Context, actor domain.User) (bool, error) {
	return passwordSignInAllowed(ctx, s.companySignIn, actor.Role)
}
