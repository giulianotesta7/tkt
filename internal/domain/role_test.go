package domain

import "testing"

// Task 1.1: role hierarchy denies (role-authorization spec "Hierarchy
// enforced": user and agent are denied agent+/admin+ capabilities, admin
// and root are allowed). Tests are written against the Role API that
// does not exist yet (strict TDD RED).

func TestRoleAtLeastHierarchy(t *testing.T) {
	cases := []struct {
		name  string
		role  Role
		other Role
		want  bool
	}{
		{"user at least user", RoleUser, RoleUser, true},
		{"user denied agent", RoleUser, RoleAgent, false},
		{"user denied admin", RoleUser, RoleAdmin, false},
		{"user denied root", RoleUser, RoleRoot, false},
		{"agent at least user", RoleAgent, RoleUser, true},
		{"agent at least agent", RoleAgent, RoleAgent, true},
		{"agent denied admin", RoleAgent, RoleAdmin, false},
		{"agent denied root", RoleAgent, RoleRoot, false},
		{"admin at least user", RoleAdmin, RoleUser, true},
		{"admin at least agent", RoleAdmin, RoleAgent, true},
		{"admin denied root", RoleAdmin, RoleRoot, false},
		{"root at least user", RoleRoot, RoleUser, true},
		{"root at least agent", RoleRoot, RoleAgent, true},
		{"root at least admin", RoleRoot, RoleAdmin, true},
		{"root at least root", RoleRoot, RoleRoot, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.role.AtLeast(tc.other); got != tc.want {
				t.Errorf("%s.AtLeast(%s) = %v, want %v", tc.role, tc.other, got, tc.want)
			}
		})
	}
}

func TestRoleValidAndParse(t *testing.T) {
	for _, r := range []Role{RoleUser, RoleAgent, RoleAdmin, RoleRoot} {
		if !r.Valid() {
			t.Errorf("%s.Valid() = false, want true", r)
		}
		got, err := ParseRole(string(r))
		if err != nil {
			t.Fatalf("ParseRole(%q): %v", r, err)
		}
		if got != r {
			t.Errorf("ParseRole(%q) = %q, want %q", r, got, r)
		}
	}

	// Unknown or malformed roles are invalid and unparsable: the closed
	// hierarchy must never accept a free-form role string (fail closed).
	for _, s := range []string{"superuser", "", "ADMIN", "owner", " user"} {
		if r := Role(s); r.Valid() {
			t.Errorf("Role(%q).Valid() = true, want false", s)
		}
		if _, err := ParseRole(s); err == nil {
			t.Errorf("ParseRole(%q) succeeded, want error", s)
		}
	}
}

// Task 1.2 RED companion: User carries exactly one role (the zero value is
// the unset/empty role — never silently a valid one).
func TestUserCarriesRole(t *testing.T) {
	u := User{Name: "Ana", Role: RoleAgent}
	if u.Role != RoleAgent {
		t.Errorf("u.Role = %q, want %q", u.Role, RoleAgent)
	}
	var zero User
	if zero.Role != "" {
		t.Errorf("zero User.Role = %q, want empty (unset)", zero.Role)
	}
}
