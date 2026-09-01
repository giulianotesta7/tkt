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

// WorkflowUnitOfWork persists application-planned workflow mutations as ONE
// atomic unit (design S5/S6). The application decides transitions, operations,
// and their literal order; implementations re-read and recheck every expected
// fact inside one BEGIN IMMEDIATE and then apply only fixed writes/CAS/audits —
// they never choose a transition, assignment, or step behavior themselves.
// The SQLite adapter lands with PR5 Batch B; the application contract is served
// by fakes in Batch A.
type WorkflowUnitOfWork interface {
	// CreateTicketWithRun persists the ticket (pinned to the expected current
	// version), the created audit, the fresh active run, and the planned
	// initial automatic operations as one atomic unit. It rechecks the
	// category's current version still equals in.ExpectedVersionID before any
	// write and refuses a stale plan with no writes. Returns the persisted
	// ticket (store-assigned ID and Number, D8) in the application-decided
	// final state.
	CreateTicketWithRun(ctx context.Context, in CreateTicketWithRunInput) (*domain.Ticket, error)

	// ApplyWorkflowPlan reloads the ticket, its run, and the pinned workflow
	// snapshot inside one immediate transaction, then rechecks EVERY expected
	// immutable fact the plan carries (pinned workflow version id + canonical
	// immutable content, requester/assignee identity, run cursor/status, ticket
	// state, and the relevant user/desk-membership preconditions). Any mismatch
	// is a typed ErrWorkflowPositionConflict with ZERO writes — the adapter never
	// overwrites or chooses a different fact. On success it applies only the
	// fixed data-only operations (form/manual/known-claim/transition) plus the
	// ticket-write and cursor CAS already decided by the plan, and returns a
	// REFRESHED WorkflowExecutionResult read back after the writes — never the
	// caller-provided Result.
	ApplyWorkflowPlan(ctx context.Context, in WorkflowMutationPlan) (WorkflowExecutionResult, error)
}

// CreateTicketWithRunInput is the fixed, data-only create+pin+run plan (design
// S5):
//   - the expected category/current-version facts the adapter rechecks
//     atomically, so a concurrent publish aborts creation,
//   - the pinned immutable workflow definition the run executes — an
//     application-OWNED deep snapshot (domain.WorkflowDefinition.Clone at the
//     trust boundary) that never aliases version-store/caller memory and is
//     never shared with runner-side work,
//   - the ticket to insert (state new, version pinned; the store assigns ID and
//     Number, D8) and the created audit carrying the creating session actor,
//   - the run starting facts (active at cursor 0, StartedAt),
//   - the application-planned initial automatic operations, applied as fixed
//     writes/audits in literal order, with the resulting cursor/status/state
//     and the completion timestamp when the run completes at creation.
//
// There is no callback, function, generic transaction API, or step-type
// dispatch: the adapter only rechecks and applies.
type CreateTicketWithRunInput struct {
	CategoryID        int64
	ExpectedVersionID int64
	Workflow          domain.WorkflowDefinition
	Ticket            *domain.Ticket
	CreatedAudit      domain.AuditEvent
	StartedAt         time.Time
	ExpectedCursor    int
	ExpectedRunStatus string
	Operations        []WorkflowOperation
	NextCursor        int
	NextRunStatus     string
	NextTicketState   domain.State
	CompletedAt       *time.Time
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
	// DowngradeToUser applies the agent-to-user downgrade lifecycle (issue #47)
	// atomically: desk memberships are removed, every open (new/in_progress)
	// ticket assigned to u is handed off per the deterministic least-loaded
	// rule of the resolved desk (or left unassigned when no eligible member or
	// desk context exists), the guarded role update persists, and the role
	// audit is appended — all in ONE transaction, all or nothing.
	DowngradeToUser(ctx context.Context, u *domain.User, expectedRole domain.Role, actorID int64, at time.Time) (*domain.User, error)
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

// CatalogStore persists the fixed-depth ticket catalog hierarchy. Category
// workflow ownership remains in WorkflowStore and is never moved to a
// department or area.
type CatalogStore interface {
	ListDepartments(ctx context.Context) ([]domain.CatalogDepartment, error)
	ListAreas(ctx context.Context, departmentID int64) ([]domain.CatalogArea, error)
	ListCatalogCategories(ctx context.Context, areaID int64) ([]domain.CatalogCategory, error)
	SearchCatalog(ctx context.Context, query string) ([]domain.CatalogCategory, error)
	CreateDepartment(ctx context.Context, d *domain.Department) error
	UpdateDepartment(ctx context.Context, d *domain.Department) error
	DeleteDepartment(ctx context.Context, id int64) error
	CreateArea(ctx context.Context, a *domain.Area) error
	UpdateArea(ctx context.Context, a *domain.Area) error
	DeleteArea(ctx context.Context, id int64) error
	MoveCategory(ctx context.Context, categoryID, areaID int64) error
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
	// (semantic workflow completion and contextual workflow_assignment),
	// matching TicketService.Assign (audit spec D14). It is
	// session/command-derived and never taken from RawAnswers.
	ActorName        string
	ExpectedPosition int
	RawAnswers       RawPositionalValues
	// Solution is the OPTIONAL manual-task solution (Amendment 2): trimmed by
	// the HTTP layer, so whitespace-only submissions arrive empty and complete
	// normally without one. Only a manual_task completion may carry a non-empty
	// value — any other step type receiving one is a plan contradiction.
	Solution string
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

// ClaimAssignmentOperation persists a known pinned workflow claim: desk,
// authenticated claimant, and one contextual workflow_assignment audit. A claim
// has no caller-controlled target or reason; manual reassignment uses the separate
// TicketService.Assign contract. Every claim carries this operation, including
// same-person claims, and never emits a trailing workflow_step audit.
type ClaimAssignmentOperation struct {
	StepIndex       int
	DeskID          int64
	AssigneeUserID  int64
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

// WorkflowStepOperation records human step completion with its semantic
// workflow-completion audit (workflow_manual_task, workflow_requester_form, or
// workflow_assignee_form — human actor, step index, timestamp). The legacy
// workflow_step action is read-only history and is never written to new events.
// Solution is the optional manual-task completion solution (Amendment 2): it
// persists in ticket_manual_solutions reusing the audit's actor-user-id and
// created-at facts; empty means none, and a form-step operation must never
// carry one.
type WorkflowStepOperation struct {
	StepIndex int
	Audit     domain.AuditEvent
	Solution  string
}

func (FormAnswerOperation) isWorkflowOperation()            {}
func (ClaimAssignmentOperation) isWorkflowOperation()       {}
func (LeastLoadedAssignmentOperation) isWorkflowOperation() {}
func (TransitionOperation) isWorkflowOperation()            {}
func (WorkflowStepOperation) isWorkflowOperation()          {}

// WorkflowMutationPlan is the concrete, data-only one-request mutation the
// application submits to WorkflowUnitOfWork.ApplyWorkflowPlan. It names every
// expected persisted fact the adapter MUST recheck before any write, alongside
// the already-decided operations and final cursor/status/state. Contradictory
// duplicated facts (operations that would not produce NextTicketState /
// NextAssigneeUserID, an assignment audit that disagrees with current/next
// facts, completion facts inconsistent with NextRunStatus) are rejected with a
// typed ErrWorkflowPositionConflict and no writes, never silently overwritten.
//
// Identity pointers follow nil-safe semantics: two nil pointers are equal, a
// nil vs a non-nil pointer is a mismatch. RequesterUserID/AssigneeUserID are
// expected CURRENT identities read from the persisted ticket's
// requester_user_id / user_id; Workflow is the immutable deep snapshot
// (WorkflowDefinition.Clone) the run executes; ExpectedVersionID is the pinned
// workflow version the ticket references. All values are immutable — no
// callback, function payload, or generic transaction API.
type WorkflowMutationPlan struct {
	TicketID           int64
	ExpectedVersionID  int64
	Workflow           domain.WorkflowDefinition
	RequesterUserID    *int64
	AssigneeUserID     *int64
	ActorUserID        int64
	ActorName          string
	ExpectedCursor     int
	ExpectedRunStatus  string
	TicketBeforeState  domain.State
	Operations         []WorkflowOperation
	NextCursor         int
	NextRunStatus      string
	NextTicketState    domain.State
	NextAssigneeUserID *int64
	Result             WorkflowExecutionResult
}
type WorkflowExecutionResult struct {
	Ticket *domain.Ticket
	Run    *WorkflowRun
}

// WorkflowResponse is retained only as the legacy list projection type; the
// timeline consumes WorkflowStepContext below (PR10 removed the standalone
// responses card).
type WorkflowResponse struct {
	StepIndex   int
	SubmittedAt time.Time
	Fields      []WorkflowResponseField
}

type WorkflowResponseField struct {
	Label string
	Kind  string
	Value string
}

// WorkflowStepContext is the read-only, trusted projection of ONE pinned step's
// presentation context, resolved strictly by an audit row's persisted step_index.
// Kind is "form" or "manual"; a form context carries FormActor and the decoded,
// definition-validated Fields; a manual context carries the pinned Instruction.
type WorkflowStepContext struct {
	Kind        string
	FormActor   domain.FormActor
	Fields      []WorkflowResponseField
	Instruction string
	// Solution is the stored manual-task solution (Amendment 2), joined only in
	// the manual branch by the event's exact persisted step index; a missing or
	// legacy row yields an empty value — never a fabricated placeholder.
	Solution string
}

// WorkflowResponseStore projects completed workflow form answers for an
// already-authorized ticket read and resolves one pinned step's presentation
// context for the merged ticket timeline (PR10). Implementations must validate
// persisted row indexes and typed values against the immutable pinned
// definition and fail closed on corrupt persisted answer rows.
type WorkflowResponseStore interface {
	ListWorkflowResponses(ctx context.Context, ticketID int64) ([]WorkflowResponse, error)
	WorkflowStepContextStore
}

// WorkflowStepContextStore is the timeline-facing half of the response store:
// it resolves one pinned workflow step's presentation context for an
// already-authorized ticket read, joined ONLY by the exact persisted
// audit_events.step_index (PR10 task 10.2) — never by timestamps or occurrence
// order. Implementations must reuse the immutable pinned-definition decoding
// path and fail closed on corrupt persisted answer rows. A missing pin, run,
// or answers row — or an index outside the pinned bounds — degrades to
// (nil, nil) so the timeline renders the safe summary alone.
type WorkflowStepContextStore interface {
	WorkflowStepContext(ctx context.Context, ticketID int64, stepIndex int) (*WorkflowStepContext, error)
}

// WorkflowRunStore resolves a ticket's workflow execution in one consistent
// read: the ticket, its run (current cursor/status), and the pinned immutable
// definition. It returns (nil, nil) when the ticket has NO workflow execution
// (a legacy unpinned ticket, or a pinned ticket with no run) so callers can
// distinguish "no pending workflow" from an error. Used by the completion
// route to build the runner snapshot and by the detail renderer to compute the
// Pending Actions card.
type WorkflowRunStore interface {
	GetWorkflowExecution(ctx context.Context, ticketID int64) (*WorkflowExecutionSnapshot, error)
}

// InitialAutomaticPlan is the creation-time automatic advancement of a fresh
// run (design S5): from cursor 0, every automatic step (least_loaded,
// resolve_ticket, close_ticket) is planned until a human-pending step (claim,
// form, manual_task) stops the walk or the definition ends. NextRunStatus is
// "completed" when the walk reaches the end of the pinned definition at
// creation; NextTicketState is the application-decided final state after the
// planned transitions.
type InitialAutomaticPlan struct {
	Operations      []WorkflowOperation
	NextCursor      int
	NextRunStatus   string
	NextTicketState domain.State
}

// WorkflowSummary is the derived badge for the category list (none | Draft | Published).
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

// PublishedWorkflow is the immutable current version a new ticket pins at
// creation (design S5): the category's current version id and its validated
// definition. New tickets read current_version_id only, never draft_json. The
// Workflow field is the store/caller-owned object: the application MUST
// deep-snapshot it (domain.WorkflowDefinition.Clone) at the trust boundary
// before planning or capture and must never alias it into the runner or a
// persisted plan.
type PublishedWorkflow struct {
	CategoryID int64
	VersionID  int64
	Workflow   domain.WorkflowDefinition
}

// WorkflowVersionStore resolves a category's current published workflow for
// ticket creation (design S5: availability = a published version exists). A
// category without a published version (no workflow row or draft-only) returns
// (nil, nil) and is unavailable for new tickets — the application answers the
// exact 422 category message and performs no writes. The SQLite adapter for
// this port lands with PR5 Batch B; the application contract is served by fakes
// in Batch A.
type WorkflowVersionStore interface {
	GetCurrentVersion(ctx context.Context, categoryID int64) (*PublishedWorkflow, error)
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
