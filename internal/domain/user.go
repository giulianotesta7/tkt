package domain

import "time"

// User is a managed user used for ticket assignment and authentication
// (user-management spec). There are NO roles: every active user can perform
// every operation. PasswordHash stores only the bcrypt hash (D15).
// Active=false is deactivation: historical ticket assignments are preserved,
// but the user cannot be assigned to new tickets or log in.
type User struct {
	ID           int64
	Name         string
	Email        string
	PasswordHash string
	Active       bool
	CreatedAt    time.Time
}
