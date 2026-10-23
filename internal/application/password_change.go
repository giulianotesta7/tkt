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

// WithPasswordChangeStore wires the atomic write an own-password change needs
// (issue #414, S2): the new hash and the sign-out of every other session commit
// together. Without it the use case refuses instead of hashing a secret nobody
// can store.
func (s *UserService) WithPasswordChangeStore(changes PasswordChangeStore) *UserService {
	s.passwordChanges = changes
	return s
}

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
// trusted hash, so an account deactivated after signing in is refused here. The
// current password is verified against that stored hash before the new one is
// even validated, so a wrong current password answers with its own field
// refusal and changes nothing.
func (s *UserService) ChangeOwnPassword(ctx context.Context, actor domain.User, currentSessionID string, currentPassword, newPassword string) error {
	policy, err := s.passwordPolicy(ctx)
	if err != nil {
		return err
	}
	if !policy.ChangeEnabled {
		return domain.NewForbiddenError("changing your own password is disabled on this instance")
	}
	user, err := s.users.GetByID(ctx, actor.ID)
	if err != nil {
		return err
	}
	if !user.Active {
		return domain.NewForbiddenError("an inactive account cannot change its password")
	}
	// S2 keeps the session IN USE signed in, so a caller that cannot name it
	// would sign out every session instead, the current one included.
	if strings.TrimSpace(currentSessionID) == "" {
		return domain.NewForbiddenError("a password change needs the session making the request")
	}
	if !VerifyPassword(user.PasswordHash, currentPassword) {
		return &domain.ValidationError{
			Field:   "current_password",
			Message: ErrMsgCurrentPasswordIncorrect,
			Key:     "error.current_password_incorrect",
		}
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
	return s.passwordChanges.ChangePasswordAndSignOutOtherSessions(ctx, user.ID, hash, currentSessionID)
}
