package application

import (
	"strings"

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
		return "", &domain.ValidationError{Field: "password", Message: domain.ErrMsgPasswordRequired}
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
