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
	// CapManageDesks allows desk and membership management (admin+).
	CapManageDesks Capability = "desks.manage"
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
		CapManageDesks,
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
		CapManageDesks,
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
//	ScopeOwned       user:  only tickets the actor created (requester = self)
//	ScopeAssigned    agent: only tickets assigned to the actor
//	ScopeAll         admin/root: the full queue, including unassigned tickets
//	ScopeAssignable  agent: unassigned tickets OR tickets assigned to the
//	                 actor — the assignment read scope (agents may claim an
//	                 unassigned ticket or reassign their own, never touch
//	                 another agent's ticket)
type TicketScope int

const (
	ScopeNone       TicketScope = iota // no ticket access (unknown role)
	ScopeOwned                         // requester = self
	ScopeAssigned                      // user_id = self
	ScopeAll                           // full queue
	ScopeAssignable                    // agent assignment scope: self or unassigned
	// ScopeAssignedOrClaimable is the READ scope for list/detail (design S6):
	// assigned to the actor OR an active workflow run whose pinned immutable step at
	// the current cursor is assign_to_desk[claim] whose desk contains the actor. It
	// is a READ-ONLY widening for agent list/detail so agents see tickets waiting on
	// a claim to their desk. It is NEVER the mutation scope — edit/comment/
	// transition/assignment helpers retain ScopeAssigned/ScopeAll so claim
	// visibility cannot authorize generic mutations.
	ScopeAssignedOrClaimable
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

// ReadScope returns the READ-only ticket scope for list/detail (design S6): an
// agent widens to ScopeAssignedOrClaimable so they see tickets pending a claim on
// their desk, while user and admin/root keep their read scope. This is the scope
// stamped on read-only queries (GetByID/List/Search). Mutation paths must keep
// using TicketScope/scopedQuery (strict assigned/all) so claim visibility never
// authorizes an edit, comment, transition, or generic assignment.
func (p *Policy) ReadScope(role domain.Role) TicketScope {
	switch role {
	case domain.RoleUser:
		return ScopeOwned
	case domain.RoleAgent:
		return ScopeAssignedOrClaimable
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
// scope (design "Domain, Contracts": policy → scoped store query). It is
// the STRICT mutation/read-baseline scope: an agent is ScopeAssigned only.
func scopedQuery(actor domain.User, q TicketQuery) TicketQuery {
	q.Scope = NewPolicy().TicketScope(actor.Role)
	q.ActorID = actor.ID
	return q
}

// readQuery returns q restricted to the actor's READ scope (design S6): an
// agent widens to ScopeAssignedOrClaimable so list/detail reads surface tickets
// waiting on a claim to the actor's desk, while the strict TicketScope remains
// the mutation baseline (never widened here). Used only by read-only paths.
func readQuery(actor domain.User, q TicketQuery) TicketQuery {
	q.Scope = NewPolicy().ReadScope(actor.Role)
	q.ActorID = actor.ID
	return q
}

// assignQuery returns the assignment read scope (ticket-access-assignment
// spec): agents may claim an unassigned ticket or reassign a ticket already
// assigned to them (ScopeAssignable); admin/root may assign any ticket
// (ScopeAll). The capability gate already rejects user-role actors before
// this query is built.
func assignQuery(actor domain.User) TicketQuery {
	q := TicketQuery{ActorID: actor.ID}
	switch actor.Role {
	case domain.RoleAgent:
		q.Scope = ScopeAssignable
	case domain.RoleAdmin, domain.RoleRoot:
		q.Scope = ScopeAll
	default:
		q.Scope = ScopeNone
	}
	return q
}
