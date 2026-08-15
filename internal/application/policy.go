package application

import "github.com/giulianotesta7/tkt/internal/domain"

// Capability identifies a single server-side permission (role-authorization
// spec: every authorization check is enforced at the application boundary
// BEFORE any query or view composition; template gating never substitutes
// for a server check). Capabilities follow the closed role hierarchy:
// each role inherits every lower-role capability.
type Capability string

const (
	// CapCreateTicket allows creating tickets (any authenticated role; a
	// user-role actor's tickets start unassigned — ticket-management spec).
	CapCreateTicket Capability = "tickets.create"
	// CapEditTicket allows editing tickets and transitioning state
	// (agent+: assigned-only within scope; admin/root: any ticket).
	CapEditTicket Capability = "tickets.edit"
	// CapAssignTicket allows assignment (agent+ only; assignment must
	// target an active agent-plus person — ticket-access spec).
	CapAssignTicket Capability = "tickets.assign"
	// CapCommentPublic allows public comments on tickets within scope.
	CapCommentPublic Capability = "comments.public"
	// CapCommentInternal allows internal (staff-only) comments (agent+).
	CapCommentInternal Capability = "comments.internal"
	// CapManageUsers allows user create/update/deactivate/delete (admin+).
	CapManageUsers Capability = "users.manage"
	// CapChangeRole allows role changes between user and agent (admin+;
	// root additionally grants and removes admin).
	CapChangeRole Capability = "users.change_role"
	// CapGrantAdmin allows granting or removing the admin role (root only).
	CapGrantAdmin Capability = "users.grant_admin"
	// CapManageGroups allows group and membership management (admin+).
	CapManageGroups Capability = "groups.manage"
	// CapManageCategories allows category management (admin+).
	CapManageCategories Capability = "categories.manage"
)

// capabilityMatrix maps each role to its granted capabilities. The empty
// role grants nothing (fail closed); admin deliberately excludes
// CapGrantAdmin (root-only — role-authorization matrix).
var capabilityMatrix = map[domain.Role][]Capability{
	domain.RoleUser: {
		CapCreateTicket,
		CapCommentPublic,
	},
	domain.RoleAgent: {
		CapCreateTicket,
		CapEditTicket,
		CapAssignTicket,
		CapCommentPublic,
		CapCommentInternal,
	},
	domain.RoleAdmin: {
		CapCreateTicket,
		CapEditTicket,
		CapAssignTicket,
		CapCommentPublic,
		CapCommentInternal,
		CapManageUsers,
		CapChangeRole,
		CapManageGroups,
		CapManageCategories,
	},
	domain.RoleRoot: {
		CapCreateTicket,
		CapEditTicket,
		CapAssignTicket,
		CapCommentPublic,
		CapCommentInternal,
		CapManageUsers,
		CapChangeRole,
		CapGrantAdmin,
		CapManageGroups,
		CapManageCategories,
	},
}

// Capabilities is the immutable set of permissions granted to an actor.
type Capabilities struct {
	caps map[Capability]bool
}

// Require reports whether the capability is granted. Unknown capabilities
// and an empty (unset) capability set are denied — authorization fails
// closed, never silently.
func (c Capabilities) Require(cap Capability) bool {
	return c.caps[cap]
}

// TicketScope is the actor's ticket read scope (ticket-access-assignment
// spec), derived from the role before any store query:
//
//	ScopeOwned    user:  only tickets the actor created (requester = self)
//	ScopeAssigned agent: only tickets assigned to the actor
//	ScopeAll      admin/root: the full queue, including unassigned tickets
type TicketScope int

const (
	ScopeNone     TicketScope = iota // no ticket access (unknown role)
	ScopeOwned                       // requester = self
	ScopeAssigned                    // user_id = self
	ScopeAll                         // full queue
)

// Policy is the single authorization authority. Handlers and services
// derive the actor's capabilities and ticket scope from the session role
// and enforce them BEFORE calling any store or composing any view
// (role-authorization spec: central server-side checks, not HTTP-only
// guards and not template hiding).
type Policy struct{}

// NewPolicy returns the shared policy instance. It is stateless and safe
// for concurrent use.
func NewPolicy() *Policy { return &Policy{} }

// Capabilities returns the permission set granted to the actor's role.
// Every role inherits all lower-role capabilities; the empty role grants
// nothing.
func (p *Policy) Capabilities(role domain.Role) Capabilities {
	raw, ok := capabilityMatrix[role]
	if !ok {
		return Capabilities{caps: map[Capability]bool{}}
	}
	caps := make(map[Capability]bool, len(raw))
	for _, c := range raw {
		caps[c] = true
	}
	return Capabilities{caps: caps}
}

// TicketScope returns the actor's ticket access scope for their role.
// Unknown roles get ScopeNone so reads return nothing (fail closed).
func (p *Policy) TicketScope(role domain.Role) TicketScope {
	switch role {
	case domain.RoleUser:
		return ScopeOwned
	case domain.RoleAgent:
		return ScopeAssigned
	case domain.RoleAdmin, domain.RoleRoot:
		return ScopeAll
	default:
		return ScopeNone
	}
}

// scopedQuery returns q restricted to the actor's ticket access scope
// (ticket-access spec): the policy derives the scope from the session role
// and stamps the query BEFORE any store call, so scoped store methods
// exclude unauthorized rows and the actor never sees tickets outside their
// scope (design "Domain, Contracts": policy → scoped store query).
func scopedQuery(actor domain.User, q TicketQuery) TicketQuery {
	q.Scope = NewPolicy().TicketScope(actor.Role)
	q.ActorID = actor.ID
	return q
}
