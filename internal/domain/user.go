package domain

import "time"

// User is a managed user used for ticket assignment and authentication
// (user-management spec). PasswordHash stores only the bcrypt hash (D15).
// Active=false is deactivation: historical ticket assignments are preserved,
// but the user cannot be assigned to new tickets or log in.
//
// Role carries the user's single position in the closed hierarchy
// (role-authorization spec). The zero value (empty Role) is unset and must
// never pass a capability check — the application assigns a role at
// creation/bootstrap, and the migration backfill assigns legacy users.
type User struct {
	ID           int64
	Name         string
	Email        string
	PasswordHash string
	Role         Role
	Active       bool
	CreatedAt    time.Time
}
