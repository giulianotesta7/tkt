package domain

import "fmt"

// Role is the closed four-role hierarchy (role-authorization spec):
// user < agent < admin < root. Every user has exactly one role; the empty
// Role is the zero value and is never a valid role. Capability derivation
// lives in the application policy layer — the domain only ranks roles.
type Role string

const (
	RoleUser  Role = "user"
	RoleAgent Role = "agent"
	RoleAdmin Role = "admin"
	RoleRoot  Role = "root"
)

// rank maps each role to its position in the hierarchy (0-based, user
// lowest, root highest).
var roleRank = map[Role]int{
	RoleUser:  0,
	RoleAgent: 1,
	RoleAdmin: 2,
	RoleRoot:  3,
}

// Valid reports whether r is one of the four roles of the closed hierarchy.
// The empty role and any free-form string are invalid: authorization must
// fail closed on unknown roles, never silently grant.
func (r Role) Valid() bool {
	_, ok := roleRank[r]
	return ok
}

// AtLeast reports whether r sits at the same or a higher rank than other in
// the hierarchy (role-authorization spec: each role inherits all lower-role
// capabilities). A role at least as high as the gate role passes the gate.
func (r Role) AtLeast(other Role) bool {
	return roleRank[r] >= roleRank[other]
}

// ParseRole converts a role string into a Role, rejecting anything outside
// the closed hierarchy. The application layer uses it when reading role
// values from untrusted input (forms, flags); the empty string is rejected
// rather than mapped to a default.
func ParseRole(s string) (Role, error) {
	r := Role(s)
	if !r.Valid() {
		return "", fmt.Errorf("unknown role %q", s)
	}
	return r, nil
}
