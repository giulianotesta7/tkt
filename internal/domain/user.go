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
//
// Pending marks an account a company sign-in created that waits for an
// administrator's approval (issue #419, S3). It is always inactive as well:
// the stored check refuses a row that is pending and active, so every guard
// written for Active already keeps a pending account out of tickets and
// sessions. Approval clears Pending and activates the account; rejection
// clears Pending and leaves it deactivated. Pending is a column of its own
// rather than a role value because role is a closed set (0003) whose change
// would rebuild the users table.
//
// Kind tells a person from a service user (issue #488). A service user is the
// identity an API key acts as: it holds the user or agent role only, never signs
// in, is never notified, mentioned, listed among people or offered as an
// assignee. The zero value is read as a person, so every account written before
// service users existed keeps meaning what it meant.
type User struct {
	ID           int64
	Name         string
	Email        string
	PasswordHash string
	Role         Role
	Active       bool
	Pending      bool
	Kind         UserKind
	CreatedAt    time.Time
}

// UserKind is the closed account kind (migration 0037).
type UserKind string

const (
	UserKindPerson  UserKind = "person"
	UserKindService UserKind = "service"
)

// IsService reports whether the account is a service user.
func (u User) IsService() bool { return u.Kind == UserKindService }
