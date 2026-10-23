package application

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/giulianotesta7/tkt/internal/domain"
	"golang.org/x/crypto/bcrypt"
)

// BcryptCost is the work factor for password hashing (D15): the library
// default of 10.
const BcryptCost = 10

// HashPassword returns the bcrypt hash of plain (D15). An empty or
// whitespace-only password is rejected before hashing (user-management
// create-user rule).
func HashPassword(plain string) (string, error) {
	if strings.TrimSpace(plain) == "" {
		return "", &domain.ValidationError{Field: "password", Message: domain.ErrMsgPasswordRequired, Key: "error.password_required"}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), BcryptCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// VerifyPassword reports whether plain matches the stored bcrypt hash.
// bcrypt.CompareHashAndPassword runs in constant time; malformed or empty
// hashes simply never verify.
func VerifyPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// ValidateNewPassword enforces the rule every NEW password obeys (issue #414,
// S4): a blank submission keeps the historical refusal, and the rest must reach
// the instance's configured minimum. The minimum counts CHARACTERS, never
// bytes, so a password in any script is measured the way the person who chose
// it counts it.
//
// The rule applies to NEW passwords only: a password stored before the policy
// changed keeps verifying at sign-in, because Login never consults this
// function.
func ValidateNewPassword(plain string, minLength int) error {
	if strings.TrimSpace(plain) == "" {
		return &domain.ValidationError{Field: "password", Message: domain.ErrMsgPasswordRequired, Key: "error.password_required"}
	}
	// A missing or nonsensical minimum falls back to the documented default
	// rather than accepting anything: the floor is never relaxed by accident.
	if minLength <= 0 {
		minLength = DefaultPasswordMinLength
	}
	if utf8.RuneCountInString(plain) < minLength {
		return &domain.ValidationError{
			Field:   "password",
			Message: fmt.Sprintf("the password must be at least %d characters long", minLength),
			Key:     "error.password_too_short",
			Args:    []any{minLength},
		}
	}
	return nil
}
