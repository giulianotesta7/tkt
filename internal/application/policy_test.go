package application

import (
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Task 1.3: central server-side policy (role-authorization spec). Written
// RED against the Policy API that does not exist yet: capabilities derive
// from the actor's role, every capability check runs before any query or
// view composition, and ticket scope follows the access contract.

func TestPolicyCapabilitiesPerRole(t *testing.T) {
	p := NewPolicy()

	tests := []struct {
		name    string
		role    domain.Role
		granted []Capability
		denied  []Capability
	}{
		{
			name: "user",
			role: domain.RoleUser,
			granted: []Capability{
				CapCreateTicket, CapCommentPublic,
			},
			denied: []Capability{
				CapEditTicket, CapAssignTicket, CapCommentInternal,
				CapManageUsers, CapChangeRole, CapGrantAdmin,
				CapManageGroups, CapManageCategories,
			},
		},
		{
			name: "agent",
			role: domain.RoleAgent,
			granted: []Capability{
				CapCreateTicket, CapCommentPublic, CapEditTicket,
				CapAssignTicket, CapCommentInternal,
			},
			denied: []Capability{
				CapManageUsers, CapChangeRole, CapGrantAdmin,
				CapManageGroups, CapManageCategories,
			},
		},
		{
			name: "admin",
			role: domain.RoleAdmin,
			granted: []Capability{
				CapCreateTicket, CapEditTicket, CapAssignTicket,
				CapCommentPublic, CapCommentInternal, CapManageUsers,
				CapChangeRole, CapManageGroups, CapManageCategories,
			},
			denied: []Capability{CapGrantAdmin},
		},
		{
			name: "root",
			role: domain.RoleRoot,
			granted: []Capability{
				CapCreateTicket, CapEditTicket, CapAssignTicket,
				CapCommentPublic, CapCommentInternal, CapManageUsers,
				CapChangeRole, CapGrantAdmin, CapManageGroups,
				CapManageCategories,
			},
			denied: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caps := p.Capabilities(tt.role)
			for _, c := range tt.granted {
				if !caps.Require(c) {
					t.Errorf("%s should grant %q", tt.role, c)
				}
			}
			for _, c := range tt.denied {
				if caps.Require(c) {
					t.Errorf("%s must NOT grant %q", tt.role, c)
				}
			}
		})
	}
}

func TestPolicyUnknownRoleFailsClosed(t *testing.T) {
	p := NewPolicy()
	// An unset/unknown role must grant nothing — never silently escalate.
	caps := p.Capabilities(domain.Role(""))
	for _, c := range []Capability{
		CapCreateTicket, CapEditTicket, CapAssignTicket, CapCommentPublic,
		CapCommentInternal, CapManageUsers, CapChangeRole, CapGrantAdmin,
		CapManageGroups, CapManageCategories,
	} {
		if caps.Require(c) {
			t.Errorf("unknown role must not grant %q", c)
		}
	}
}

func TestPolicyTicketScope(t *testing.T) {
	p := NewPolicy()
	cases := []struct {
		role domain.Role
		want TicketScope
	}{
		{domain.RoleUser, ScopeOwned},    // requester = self only
		{domain.RoleAgent, ScopeAssigned}, // assigned to self only
		{domain.RoleAdmin, ScopeAll},      // full queue, incl. unassigned
		{domain.RoleRoot, ScopeAll},
		{domain.Role(""), ScopeNone}, // unknown role: no access (fail closed)
	}
	for _, tc := range cases {
		if got := p.TicketScope(tc.role); got != tc.want {
			t.Errorf("TicketScope(%s) = %v, want %v", tc.role, got, tc.want)
		}
	}
}
