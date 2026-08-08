package application_test

import (
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
)

// TestPasswordHashAndVerify covers the bcrypt contract (D15): a hashed
// password verifies against the original plaintext.
func TestPasswordHashAndVerify(t *testing.T) {
	hash, err := application.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: unexpected error: %v", err)
	}
	if !application.VerifyPassword(hash, "correct horse battery staple") {
		t.Fatal("VerifyPassword: correct password must verify")
	}
}

// TestPasswordWrongPassword covers the failure path: a wrong plaintext never
// verifies against the stored hash.
func TestPasswordWrongPassword(t *testing.T) {
	hash, err := application.HashPassword("secret")
	if err != nil {
		t.Fatalf("HashPassword: unexpected error: %v", err)
	}
	if application.VerifyPassword(hash, "wrong") {
		t.Fatal("VerifyPassword: wrong password must not verify")
	}
}

// TestPasswordHashesDiffer covers the per-user salt: two hashes of the same
// password MUST differ, so equal passwords never reveal equal hashes.
func TestPasswordHashesDiffer(t *testing.T) {
	h1, err := application.HashPassword("same-password")
	if err != nil {
		t.Fatalf("HashPassword 1: unexpected error: %v", err)
	}
	h2, err := application.HashPassword("same-password")
	if err != nil {
		t.Fatalf("HashPassword 2: unexpected error: %v", err)
	}
	if h1 == h2 {
		t.Fatal("two hashes of the same password must differ (per-user salt)")
	}
}

// TestPasswordRejectsEmpty covers the user-management create-user rule: a
// missing password must be rejected before any hashing.
func TestPasswordRejectsEmpty(t *testing.T) {
	if _, err := application.HashPassword(""); err == nil {
		t.Fatal("HashPassword: empty password must be rejected")
	}
	if _, err := application.HashPassword("   "); err == nil {
		t.Fatal("HashPassword: whitespace-only password must be rejected")
	}
}

// TestVerifyPasswordMalformed covers defensive behavior: garbage and empty
// hashes never verify and never panic.
func TestVerifyPasswordMalformed(t *testing.T) {
	if application.VerifyPassword("not-a-bcrypt-hash", "secret") {
		t.Fatal("VerifyPassword: malformed hash must not verify")
	}
	if application.VerifyPassword("", "secret") {
		t.Fatal("VerifyPassword: empty hash must not verify")
	}
}

// TestHashIsNotPlaintext covers the storage guarantee: only the hash is
// stored, never the plaintext (user-management spec).
func TestHashIsNotPlaintext(t *testing.T) {
	hash, err := application.HashPassword("super-secret-password")
	if err != nil {
		t.Fatalf("HashPassword: unexpected error: %v", err)
	}
	if strings.Contains(hash, "super-secret-password") {
		t.Fatal("hash must not contain the plaintext")
	}
}
