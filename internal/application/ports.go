// Package application implements the use cases of the ticketing system
// behind store ports (hexagonal-lite, D13). It imports the domain only;
// persistence and presentation live in adapters that implement these ports.
package application

import (
	"context"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// TicketStore persists tickets and answers list/count queries (D2, D13).
// It serves READ paths: GetByID, List, Count, and aggregate counts.
//
// Numbering is a store concern (D8): Create assigns the ticket's unique
// readable Number atomically (MAX+1 inside the store transaction) and its ID.
// The service never computes numbers.
//
// Mutation boundary: ticket writes (Create/Update) are NOT issued through
// this port — the application routes every ticket mutation through
// TicketUnitOfWork together with its audit events, so ticket state and its
// audit trail persist atomically (audit-log no-silent-mutations contract).
// Create/Update remain part of the port for direct store use (seeding,
// system operations); the application layer never calls them in isolation.
type TicketStore interface {
	// Create persists t, assigning t.ID and t.Number (MAX+1, atomic).
	Create(ctx context.Context, t *domain.Ticket) error
	// Update persists the ticket's fields, state, and timestamps.
	Update(ctx context.Context, t *domain.Ticket) error
	// GetByID returns the ticket or ErrNotFound, restricted to the actor's
	// ticket access scope carried in q (ticket-access spec): a ticket
	// outside the actor's scope is indistinguishable from a missing one
	// (ErrNotFound — no existence leak). Only the scope restriction of q
	// applies; the filter fields are ignored.
	GetByID(ctx context.Context, id int64, q TicketQuery) (*domain.Ticket, error)
	// List returns tickets matching q, ordered created_at DESC, id DESC (D2),
	// limited by p.
	List(ctx context.Context, q TicketQuery, p Page) ([]domain.Ticket, error)
	// Count returns the number of tickets matching q (no pagination).
	Count(ctx context.Context, q TicketQuery) (int, error)
	// CountsByState returns counts per state for tickets matching q.
	CountsByState(ctx context.Context, q TicketQuery) (map[domain.State]int, error)
	// CountsByPriority returns counts per priority for tickets matching q.
	CountsByPriority(ctx context.Context, q TicketQuery) (map[domain.Priority]int, error)
}

// TicketUnitOfWork persists a ticket mutation and its audit events as ONE
// atomic unit (audit-log no-silent-mutations contract, C1). The application
// issues a single call per mutation — implementations MUST apply the ticket
// write and the event appends in one transaction and MUST roll the ticket
// write back when any append fails, so a failed audit can never leave a
// committed ticket mutation behind. Slice 4 implements this port over a real
// SQLite transaction; the in-memory fakes simulate the rollback.
type TicketUnitOfWork interface {
	// Create persists t (assigning t.ID and t.Number, MAX+1, atomic, D8)
	// and appends the created audit event atomically. The implementation
	// stamps event.TicketID from the assigned t.ID before persisting.
	Create(ctx context.Context, t *domain.Ticket, event domain.AuditEvent) error
	// Update persists t and appends the event batch (a transition or
	// field-edit batch) atomically. events may be empty: a plain ticket
	// write is still atomic by construction.
	Update(ctx context.Context, t *domain.Ticket, events ...domain.AuditEvent) error
}

// SearchStore provides FTS5 full-text search over title, description, and
// comment bodies (ticket-search spec). TicketQuery.Text carries the
// D4-tokenized expression: each token double-quoted with embedded quotes
// escaped, joined with AND; empty means no text filter.
type SearchStore interface {
	// Search returns tickets matching q (text AND filters), ordered
	// created_at DESC, id DESC, limited by p.
	Search(ctx context.Context, q TicketQuery, p Page) ([]domain.Ticket, error)
	// SearchCount returns the number of matches (no pagination).
	SearchCount(ctx context.Context, q TicketQuery) (int, error)
}

// CommentStore persists the append-only comment timeline
// (comment-timeline spec).
type CommentStore interface {
	// Add stores c, assigning c.ID. c.Visibility is persisted; an empty
	// visibility falls back to 'public' (migration 0003 default — legacy
	// comments backfill to public, and legacy callers keep producing
	// public comments).
	Add(ctx context.Context, c *domain.Comment) error
	// ListByTicket returns the ticket's comments in creation order (ASC).
	// includeInternal controls the internal (staff-only) rows: false
	// excludes them at the SQL boundary, so a user-role actor never
	// receives internal content (comment-visibility spec — filtering
	// precedes composition, it is not markup hiding).
	ListByTicket(ctx context.Context, ticketID int64, includeInternal bool) ([]domain.Comment, error)
}

// AuditStore persists the append-only audit trail (audit-log spec).
type AuditStore interface {
	// Append stores all events in occurrence order (one mutation batch).
	Append(ctx context.Context, events ...domain.AuditEvent) error
	// ListByTicket returns the ticket's audit events in occurrence order (ASC).
	ListByTicket(ctx context.Context, ticketID int64) ([]domain.AuditEvent, error)
}

// UserStore persists managed users (user-management spec).
type UserStore interface {
	// Create stores u, assigning u.ID; ErrDuplicate when the email exists.
	// u.Role is persisted when set; a zero Role falls back to the migration
	// default ('agent') so legacy callers keep working.
	Create(ctx context.Context, u *domain.User) error
	// Update persists the user's fields, including deactivation (Active).
	Update(ctx context.Context, u *domain.User) error
	// UpdateManagedUser persists identity, role, and active state together. It
	// guards the expected current role and appends a role audit only on change.
	UpdateManagedUser(ctx context.Context, u *domain.User, expectedRole domain.Role, actorID int64, at time.Time) error
	// UpdatePasswordHash changes only the stored password hash.
	UpdatePasswordHash(ctx context.Context, id int64, passwordHash string) error
	// Delete removes an unreferenced user; ErrReferenced when the user is
	// assigned to tickets (deactivation is the removal path then).
	Delete(ctx context.Context, id int64) error
	// GetByID returns the user, including inactive ones (historical display);
	// ErrNotFound when absent.
	GetByID(ctx context.Context, id int64) (*domain.User, error)
	// GetByEmail returns the user by email; ErrNotFound when absent.
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
	// Count returns the number of users (first-user bootstrap check, D16).
	Count(ctx context.Context) (int, error)
	// List returns all users.
	List(ctx context.Context) ([]domain.User, error)
	// ListActive returns only active users.
	ListActive(ctx context.Context) ([]domain.User, error)
	// BootstrapRoot creates the very first user with role root ATOMICALLY
	// (role-authorization "First-User Root Bootstrap"). The count check and
	// the insert share one immediate transaction, so concurrent calls yield
	// exactly one root; every later call fails with
	// ErrBootstrapUnavailable without creating an account. BootstrapRoot is
	// the ONLY store operation that may insert a root — user creation and
	// role-grant flows must never do so.
	BootstrapRoot(ctx context.Context, u *domain.User) error
	// RecoverRoot is the one-shot operator-selected root recovery (design
	// "Persistence and Recovery"; role-authorization "Operator-Selected Root
	// Recovery"). In one immediate transaction it verifies NO root exists
	// and the selected user exists, activates and promotes that user to
	// root, records the recovery in role_changes (actor NULL, reason
	// "operator-selected root recovery"), and returns the promoted user.
	// It fails closed when a root already exists or the user is unknown —
	// recovery never guesses and never creates a second root.
	RecoverRoot(ctx context.Context, id int64) (*domain.User, error)
}

// SessionStore persists server-side login sessions (D14).
type SessionStore interface {
	// Create stores s.
	Create(ctx context.Context, s *domain.Session) error
	// GetByID returns the session or ErrNotFound when missing or expired.
	GetByID(ctx context.Context, id string) (*domain.Session, error)
	// Delete removes the session (logout).
	Delete(ctx context.Context, id string) error
}

// CategoryStore persists managed categories (category-management spec).
type CategoryStore interface {
	// Create stores c, assigning c.ID; ErrDuplicate when the name exists.
	Create(ctx context.Context, c *domain.Category) error
	// Update persists the category (rename); ErrDuplicate when the new name
	// is taken by another category.
	Update(ctx context.Context, c *domain.Category) error
	// Delete removes an unreferenced category; ErrReferenced when tickets
	// use it.
	Delete(ctx context.Context, id int64) error
	// GetByID returns the category; ErrNotFound when absent.
	GetByID(ctx context.Context, id int64) (*domain.Category, error)
	// List returns all categories.
	List(ctx context.Context) ([]domain.Category, error)
}

// DeskStore persists named desks and their N:N memberships. Membership is
// limited to agent-plus users by both the application and SQLite triggers.
type DeskStore interface {
	Create(ctx context.Context, g *domain.Desk) error
	Update(ctx context.Context, g *domain.Desk) error
	Delete(ctx context.Context, id int64) error
	GetByID(ctx context.Context, id int64) (*domain.Desk, error)
	List(ctx context.Context) ([]domain.Desk, error)
	AddMember(ctx context.Context, deskID, userID int64, createdAt time.Time) error
	RemoveMember(ctx context.Context, deskID, userID int64) error
	ListMembers(ctx context.Context, deskID int64) ([]domain.User, error)
}

type WorkflowRun struct {
	TicketID         int64
	CurrentStepIndex int
	Status           string
	StartedAt        time.Time
	CompletedAt      *time.Time
}
type WorkflowExecutionSnapshot struct {
	Ticket   *domain.Ticket
	Run      *WorkflowRun
	Workflow domain.WorkflowDefinition
}
type RawPositionalValue struct {
	Position int
	Values   []string
}
type RawPositionalValues []RawPositionalValue
type CompleteWorkflowCommand struct {
	TicketID    int64
	ActorUserID int64
	// ActorName is the human actor's display name at submission time. The
	// application stamps it together with ActorUserID on every human audit
	// (workflow_step and assignment), matching TicketService.Assign (audit
	// spec D14). It is session/command-derived and never taken from
	// RawAnswers.
	ActorName        string
	ExpectedPosition int
	Reason           string
	RawAnswers       RawPositionalValues
}

// WorkflowOperation is the sealed, ordered, data-only mutation contract: only
// value structs below implement it (unexported marker, closed at compile
// time — no callbacks/functions/registries). Applied in literal slice order.
type WorkflowOperation interface {
	isWorkflowOperation()
}

// FormAnswerOperation persists one form step's typed positional JSON answers
// with the submitting human actor and timestamp.
type FormAnswerOperation struct {
	StepIndex         int
	AnswersJSON       []byte
	SubmittedByUserID int64
	SubmittedAt       time.Time
}

// ClaimAssignmentOperation persists a known claim: desk, claimant as assignee,
// optional reassignment reason, and the required assignment audit (populated
// whenever the assignee changes; same-person claims emit no assignment op).
type ClaimAssignmentOperation struct {
	StepIndex       int
	DeskID          int64
	AssigneeUserID  int64
	Reason          string
	AssignmentAudit domain.AuditEvent
}

// LeastLoadedAssignmentOperation is the explicit automatic least_loaded intent;
// the chosen user/audit are persistence facts owned by the WorkflowUnitOfWork
// (S6), so no assignee or audit exists by construction.
type LeastLoadedAssignmentOperation struct {
	StepIndex int
	DeskID    int64
}

// TransitionOperation carries the exact domain.Ticket.Transition audit.
type TransitionOperation struct {
	StepIndex int
	Audit     domain.AuditEvent
}

// WorkflowStepOperation records human step completion with its workflow_step
// audit (human actor, step index, timestamp).
type WorkflowStepOperation struct {
	StepIndex int
	Audit     domain.AuditEvent
}

func (FormAnswerOperation) isWorkflowOperation()            {}
func (ClaimAssignmentOperation) isWorkflowOperation()       {}
func (LeastLoadedAssignmentOperation) isWorkflowOperation() {}
func (TransitionOperation) isWorkflowOperation()            {}
func (WorkflowStepOperation) isWorkflowOperation()          {}

type WorkflowMutationPlan struct {
	TicketID          int64
	ExpectedCursor    int
	ExpectedRunStatus string
	TicketBeforeState domain.State
	Operations        []WorkflowOperation
	NextCursor        int
	NextRunStatus     string
	NextTicketState   domain.State
	Result            WorkflowExecutionResult
}
type WorkflowExecutionResult struct {
	Ticket *domain.Ticket
	Run    *WorkflowRun
}

// WorkflowSummary is the derived badge for the category list (none | Draft | Published vN).
type WorkflowSummary struct {
	CategoryID   int64
	CategoryName string
	Badge        string
}

// WorkflowStore persists the category workflow draft and published versions (category-workflows spec).
// GetDraft is safe: absent row returns nil,nil and creates no row. UpsertDraft lazily creates on first mutation.
// Publish validates via domain, rechecks desks, allocates version_no, and switches current pointer atomically.
type WorkflowStore interface {
	GetDraft(ctx context.Context, categoryID int64) ([]byte, error)
	UpsertDraft(ctx context.Context, categoryID int64, draft []byte) error
	Publish(ctx context.Context, categoryID int64, draft []byte, publishedByUserID *int64) (int64, []domain.WorkflowValidationIssue, error)
	ListSummaries(ctx context.Context) ([]WorkflowSummary, error)
	ListAvailableCategories(ctx context.Context) ([]domain.Category, error)
}

// SettingsStore persists single-row instance settings (appearance-settings
// spec). The internal-comment background row is seeded by migration 0005;
// a missing row reads back the application default.
type SettingsStore interface {
	// GetInternalCommentBg returns the configured internal-comment
	// background color, or DefaultInternalCommentBg when the row is absent.
	GetInternalCommentBg(ctx context.Context) (string, error)
	// SetInternalCommentBg persists the internal-comment background color.
	SetInternalCommentBg(ctx context.Context, color string) error
}

// TicketQuery is the filter set shared by list, count, and search queries
// (ticket-search spec). All active filters compose with AND semantics; an
// empty filter set returns all tickets within the actor's scope.
//
// Scope carries the actor's ticket access scope (ticket-access spec) and is
// stamped by the application use cases from the session role BEFORE any
// store call — scoped methods exclude unauthorized rows so the store never
// returns tickets outside the actor's scope. The zero value (ScopeNone)
// fails closed: an unscoped query returns no rows.
type TicketQuery struct {
	State      *domain.State
	Priority   *domain.Priority
	CategoryID *int64
	UserID     *int64
	// Text is the D4-tokenized, title-scoped FTS expression ("" = no title
	// filter). The search box matches ONLY ticket titles or IDs.
	Text string
	// Numbers holds the exact positive ticket numbers (TKT-N) extracted
	// from the raw text filter; the ID-search side of the text clause.
	Numbers []int64
	// SortByPriority orders results by the D11 priority rank
	// (critical > high > medium > low) before the created/id tiebreak.
	SortByPriority bool
	// Scope restricts the query to the actor's ticket access scope
	// (ticket-access spec): ScopeOwned → requester = self, ScopeAssigned →
	// assignee = self, ScopeAll → full queue. ScopeNone (zero) denies all.
	Scope TicketScope
	// ActorID is the session user whose scope applies (the requester for
	// ScopeOwned, the assignee for ScopeAssigned).
	ActorID int64
}

// Page is the pagination window (D2). Limit is FIXED at 10 — the service
// always sets it; there is no configuration knob.
type Page struct {
	Offset int
	Limit  int
}
