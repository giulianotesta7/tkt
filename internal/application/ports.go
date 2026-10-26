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

// TicketUnitOfWork persists a ticket mutation as ONE atomic unit (audit-log
// no-silent-mutations contract, C1; notification atomicity S11). The
// application issues a single call per mutation — implementations MUST apply
// the ticket write, the audit appends, and the notification appends in one
// transaction and MUST roll the ticket write back when any append fails, so a
// failed audit or notification can never leave a committed ticket mutation
// behind, and a notification can never exist without the change it describes.
// The SQLite adapter uses a real immediate transaction; the in-memory fakes
// simulate the same rollback.
type TicketUnitOfWork interface {
	// Create persists m.Ticket (assigning ID and Number, MAX+1, atomic, D8)
	// and appends the audit events and notifications atomically. The
	// implementation stamps every event's TicketID from the assigned ID before
	// persisting.
	Create(ctx context.Context, m TicketMutation) error
	// Update persists m.Ticket and appends the event and notification batches
	// atomically. Either batch may be empty: a plain ticket write is still
	// atomic by construction.
	Update(ctx context.Context, m TicketMutation) error
	// UpdateHold persists one hold change (issue #352) as ONE atomic unit:
	// the ticket row, its hold columns, the optional shifted SLA, the event
	// batch, and the notifications (#344: a notification never exists
	// without its change). The hold columns are written
	// ONLY here — Update never writes them — and only when the stored
	// on_hold_since still equals w.ExpectedOnHoldSince (compare-and-set), so
	// two concurrent resumes cannot shift the SLA twice and a stale read can
	// never resurrect a hold. A mismatch is a *domain.HoldConflictError with
	// no writes. Update refuses, with the same error, to write a terminal
	// state over a row that is still on hold.
	UpdateHold(ctx context.Context, w HoldWrite) error
	// RemindHold appends ONE reminder batch for a held ticket (issue #353,
	// S3) in one immediate transaction, and ONLY while the stored hold is
	// still the one the caller read: the stored on_hold_since must equal
	// expectedOnHoldSince. A mismatch — the requester replied meanwhile, or
	// another hold replaced this one — is a *domain.HoldConflictError with
	// nothing written, so a reminder can never describe a hold that already
	// ended. Unlike UpdateHold it writes no ticket column at all: a reminder
	// changes nothing about the hold it announces.
	RemindHold(ctx context.Context, ticketID int64, expectedOnHoldSince time.Time, notifications []domain.Notification) error
}

// TicketMutation is ONE atomic ticket write and everything that must commit
// with it: the ticket row, the audit events that describe the change, and the
// notifications addressed to the recipients the APPLICATION computed for it
// (S10/S11). Splitting these into separate calls would allow a notification
// without its change, or a change nobody was told about.
type TicketMutation struct {
	Ticket *domain.Ticket
	// Events are appended in occurrence order (created, transition, field
	// edits) — the same batch the audit trail already received.
	Events []domain.AuditEvent
	// Notifications are stamped by the application layer from policy and
	// roles: who must learn about this change. Empty means nobody is told (the
	// actor did it alone, or the change notifies no one by design).
	Notifications []domain.Notification
	// Attachments are the files of a ticket CREATION (issue #366): they are
	// inserted with the ticket, stamped with its assigned id and no comment,
	// in the same transaction. Update ignores them.
	Attachments []domain.Attachment
}

// NotificationRecipient is the policy input for ONE candidate notification
// recipient: the identity, the role that derives their ticket read scope, and
// whether the account is active. S10 forbids notifying someone who cannot open
// the ticket, so a recipient is only ever addressed when these facts prove
// access — an inactive account or a role without scope is dropped instead of
// being told about a ticket it cannot open.
type NotificationRecipient struct {
	UserID int64
	Role   domain.Role
	Active bool
}

// HoldWrite is one hold change for TicketUnitOfWork.UpdateHold (issue
// #352). Ticket carries the new hold columns (OnHoldSince, OnHoldReason)
// and any field the same change wrote (a resolve or cancel that ends the
// hold). ShiftedSLA, when non-nil, replaces the frozen RESOLUTION warning
// and due instants — the one sanctioned update of a frozen commitment.
// The requester reply that ends a hold is NOT written here: it rides the
// comment unit of work (#344) so the comment, its notifications and the
// resume share that one transaction.
type HoldWrite struct {
	Ticket              *domain.Ticket
	ExpectedOnHoldSince *time.Time
	ShiftedSLA          *domain.TicketSLA
	Events              []domain.AuditEvent
	Notifications       []domain.Notification
}

// HoldExpiryStore is the READ half of the automatic hold expiry pass (issue
// #353): the held tickets one pass walks and the once-per-hold check that keeps
// a reminder from being sent twice. It is a read-only port — closing a hold and
// writing a reminder both go through TicketUnitOfWork, so a reminder always
// commits with the guard that proves its hold still exists.
//
// The pass runs outside any request: there is no session, so the reads are
// UNSCOPED on purpose (the instance acts on every ticket), and they answer only
// the facts the rules need.
type HoldExpiryStore interface {
	// HeldTickets returns one page of the OPEN tickets that are on hold: at
	// most limit tickets with an id above afterID, ordered by id, so the pass
	// pages through the whole set (keyset) instead of re-reading a first page
	// that holds not yet due would fill forever. A ticket whose state is
	// already terminal is not returned: it has nothing left to close.
	HeldTickets(ctx context.Context, afterID int64, limit int) ([]domain.Ticket, error)
	// ReminderSent reports whether this HOLD already told the requester it is
	// about to close: a hold_reminder notification for the ticket exists with
	// created_at at or after since (the instant the hold began). Scoping to the
	// hold is what makes "once per hold" true — a reminder from an earlier
	// hold, before a reply resumed it, never suppresses the new one.
	ReminderSent(ctx context.Context, ticketID int64, since time.Time) (bool, error)
}

// AlertCandidate is ONE open, assigned ticket with every fact the operational
// alert rules need (issue #408), read in a single statement so a pass never
// issues one query per ticket.
type AlertCandidate struct {
	Ticket domain.Ticket
	// SLA is the commitment FROZEN onto the ticket, nil when it has none
	// (legacy tickets, or tickets created while SLA was disabled). The alert
	// rules compare against these instants and never recompute a threshold.
	SLA *domain.TicketSLA
	// Milestones are the observed first public staff response and first
	// resolution, plus the pause instant of an active hold, exactly as the
	// SLA projection expects them.
	Milestones domain.SLAMilestones
	// LastActivityAt is the most recent movement on the ticket: the latest of
	// its own updated_at and its newest comment. It is the instant the
	// inactivity window counts from.
	LastActivityAt time.Time
}

// AlertStore is the READ half of the operational alert pass (issue #408). Like
// the hold expiry reads it is unscoped on purpose: the pass runs outside any
// request, so the instance acts on every ticket. It never writes — alerts are
// appended through AlertUnitOfWork, which re-checks the same once-per-condition
// rule inside its transaction.
type AlertStore interface {
	// AlertCandidates returns one page of the OPEN tickets that HAVE an
	// assignee, with an id above afterID and ordered by id, so the pass keyset
	// pages through the whole set. Tickets nobody is assigned to are excluded
	// at the source: the maintainer chose the assigned agent as the only
	// recipient, so a candidate without one has nobody to tell.
	AlertCandidates(ctx context.Context, afterID int64, limit int) ([]AlertCandidate, error)
	// AlertSent reports whether this ticket was already alerted about this
	// condition: a notification of that kind exists, and — when since is not
	// nil — it was created at or after that instant. The since argument is
	// what makes a REPEATABLE condition repeatable: an inactivity alert is
	// scoped to the current period of silence, so fresh activity allows a new
	// one, while an SLA alert passes nil and is sent once per ticket.
	AlertSent(ctx context.Context, ticketID int64, kind domain.NotificationKind, since *time.Time) (bool, error)
}

// AlertUnitOfWork is the WRITE half of the operational alert pass (issue #408).
// It is separate from TicketUnitOfWork because an alert changes no ticket: it
// only reports a condition the ticket already reached.
type AlertUnitOfWork interface {
	// AppendAlert appends the rows of ONE alert in one immediate transaction,
	// and ONLY when the same condition has not been recorded yet — the exact
	// AlertSent predicate, re-evaluated inside the transaction, so two passes
	// that overlap can never write the same alert twice. An alert that is
	// already recorded is a silent no-op, never an error.
	AppendAlert(ctx context.Context, ticketID int64, kind domain.NotificationKind, since *time.Time, notifications []domain.Notification) error
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
	// Attachments are the files of the creation (issue #366), inserted with
	// the ticket in the same transaction; a failure rolls the ticket back.
	Attachments []domain.Attachment
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

// AgentTicketRowContext is the batched per-row read model behind the agent
// queue (issue #122): one entry per page ticket with the authoritative desk
// display name, the current workflow task text, and the current claim step's
// 1-based position. Absent facts stay zero/nil — callers render their own
// empty states (e.g. "No desk assigned"); the read model never fabricates
// placeholders.
type AgentTicketRowContext struct {
	// DeskName is a surviving desk's current name. For a ticket assigned to a
	// person it is the desk of the LATEST desk-bearing audit event (no
	// category-desk or older-history inference beyond it); for a ticket
	// unassigned at an active claim step it is that claim step's pinned desk.
	// A missing audit, a deleted desk, or a deleted claim desk yields "".
	DeskName string
	// CurrentTask is the resolved current pinned step's task text for an
	// ACTIVE run (the manual-task instruction verbatim, the form's field
	// labels, or the step's operational label). A completed or missing run,
	// a legacy unpinned ticket, or an unresolvable step yields "".
	CurrentTask string
	// Position is the 1-based position of the CURRENT pinned step, set ONLY
	// when that exact active step is an assign_to_desk step with the claim
	// strategy AND the ticket is currently unassigned (claimable context).
	// Every other state leaves it nil.
	Position *int
}

// MaxAgentQueueContextBatch bounds the agent queue row-context read (issue
// #122): one page of tickets is answered by ONE bounded batched query, and a
// caller requesting more than this many ids in a single call is a bug that
// implementations must refuse (fail closed, never silently truncate).
const MaxAgentQueueContextBatch = 20

// AgentQueueContextStore is the OPTIONAL batched read-model capability behind
// the agent queue rows. SearchService discovers it by type assertion on its
// ticket store port, so existing stores and lightweight fakes never have to
// implement it; the production SQLite adapter does. A caller that requests
// agent queue context from a store without the capability gets a clear error
// — never silently empty rows.
type AgentQueueContextStore interface {
	// AgentQueueContext returns one row context per requested ticket ID.
	// Implementations MUST answer the whole batch with one bounded query (no
	// per-ticket N+1), MUST NOT exceed MaxAgentQueueContextBatch ids, and
	// return an empty (non-nil) map for empty input WITHOUT querying.
	// Unknown ids simply have no entry.
	AgentQueueContext(ctx context.Context, ticketIDs []int64) (map[int64]AgentTicketRowContext, error)
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

// CommentUnitOfWork persists a comment and the notifications it produces as
// ONE atomic unit (issue #344 specs S10, S12; S11 is the general contract it
// instantiates for comments). The application issues a single call per
// comment; implementations MUST insert the comment, stamp the assigned comment
// id onto every notification, and append the notifications in ONE transaction,
// rolling the comment back when any append fails — so a comment can never be
// stored without the news about it, and a notification about a comment can
// never outlive it.
//
// Recipients are computed by the APPLICATION layer from policy and roles; the
// store only inserts what it is handed.
type CommentUnitOfWork interface {
	// Add persists m.Comment, assigning m.Comment.ID, and appends
	// m.Notifications in the SAME transaction. Implementations stamp each
	// notification's CommentID from the assigned id before persisting (the
	// column's foreign key needs the comment row, which is exactly why both
	// writes share the transaction).
	Add(ctx context.Context, m CommentMutation) error
}

// CommentMutation is ONE atomic comment write and everything that must commit
// with it: the comment row and the notifications addressed to the recipients
// the application computed for it. Splitting these into separate calls would
// allow a comment nobody was told about, or a notification pointing at a
// comment that failed to persist.
type CommentMutation struct {
	Comment *domain.Comment
	// Notifications carry the comment kind, the recipients policy allowed, and
	// the author as actor. CommentID is left nil: the id does not exist until
	// the insertion, so the unit of work stamps it.
	Notifications []domain.Notification
	// Resume, when non-nil, is the hold the requester's reply ends (issue
	// #352, S1): it commits in the SAME transaction as the comment. It is
	// applied under the same compare-and-set as TicketUnitOfWork.UpdateHold;
	// when the stored hold no longer matches (someone already resumed or
	// changed it) the resume is skipped and the comment STILL commits — the
	// requester's reply is never lost to a race.
	Resume *HoldResume
	// Attachments are the comment's files (issue #366), inserted with the
	// comment, stamped with its id and ticket, in the same transaction.
	Attachments []domain.Attachment
}

// HoldResume is the hold change that rides a comment (issue #352): Ticket is
// the ticket after Ticket.ResumeFromHold, ExpectedOnHoldSince the hold the
// caller read, ShiftedSLA the resolution instants moved by the working time
// on hold (nil without a frozen SLA), and Events the resume audit event.
type HoldResume struct {
	Ticket              *domain.Ticket
	ExpectedOnHoldSince *time.Time
	ShiftedSLA          *domain.TicketSLA
	Events              []domain.AuditEvent
}

// AttachmentStore reads ticket attachments (issue #366). Attachments are
// written only by the units of work that create a ticket or a comment, in
// their transaction; this port never writes.
type AttachmentStore interface {
	// ListByTicket returns the ticket's attachments in upload order, WITHOUT
	// their data. includeInternal=false excludes the files of internal
	// comments at the SQL boundary — the same rule the comment list applies —
	// so a requester never learns that a staff-only file exists.
	ListByTicket(ctx context.Context, ticketID int64, includeInternal bool) ([]domain.Attachment, error)
	// Get returns one attachment WITH its data and the visibility of what it
	// is attached to (empty for a ticket-creation file), so the caller can
	// authorise the download; a missing id is ErrNotFound.
	Get(ctx context.Context, id int64) (*StoredAttachment, error)
}

// StoredAttachment is an attachment read back for download: the file and the
// visibility that decides who may have it (issue #366, S3). Visibility is
// empty for a file attached at ticket creation.
type StoredAttachment struct {
	domain.Attachment
	Visibility domain.CommentVisibility
}

// AuditStore persists the append-only audit trail (audit-log spec).
type AuditStore interface {
	// Append stores all events in occurrence order (one mutation batch).
	Append(ctx context.Context, events ...domain.AuditEvent) error
	// ListByTicket returns the ticket's audit events in occurrence order (ASC).
	ListByTicket(ctx context.Context, ticketID int64) ([]domain.AuditEvent, error)
}

// NotificationStore serves the in-app notification center (issue #344): the
// bell's unread count, the panel's newest-first list, and the two read
// writes. It is a READ/WRITE port for the UI only.
//
// Notifications are never appended through this port: the rows are written by
// the ticket and workflow units of work, in the SAME transaction as the ticket
// change they describe, so a change that fails leaves no notification and a
// notification never exists without its change (spec S11). Recipients are
// computed by the application layer from policy and roles; the store only
// inserts what the unit of work hands it.
//
// Every method is scoped to ONE recipient. A caller can never read or mark
// another user's rows: an id that does not belong to userID is
// indistinguishable from a missing one, so the center leaks no existence.
type NotificationStore interface {
	// ListForUser returns the recipient's notifications, newest first
	// (created_at DESC, id DESC), at most limit rows. A non-positive limit
	// falls back to a bounded default rather than scanning the whole table.
	// Read and unread rows are both returned: the panel marks the unread ones.
	ListForUser(ctx context.Context, userID int64, limit int) ([]domain.Notification, error)
	// UnreadCount returns how many of the recipient's rows are unread — the
	// bell's number (spec S13), never computed while rendering an ordinary
	// page.
	UnreadCount(ctx context.Context, userID int64) (int, error)
	// MarkRead marks ONE of the recipient's notifications read at instant at.
	// Marking an already-read row is a no-op that keeps its first instant.
	// An id owned by another user (or absent) returns domain.ErrNotFound and
	// changes nothing — never a cross-user write (spec S14).
	MarkRead(ctx context.Context, userID, id int64, at time.Time) error
	// MarkAllRead marks every unread notification of the recipient read at
	// instant at, and touches nobody else's rows (spec S14 "Mark all as
	// read").
	MarkAllRead(ctx context.Context, userID int64, at time.Time) error
}

// VisitMark is how much of one ticket's history a reader had on screen: the
// newest audit event id and the newest comment id when it was read (issue
// #415). Ids, not instants, because instants are stored to the second.
type VisitMark struct {
	AuditID   int64
	CommentID int64
}

// TicketVisitStore records when each user last opened each ticket and answers
// which tickets changed for them since (issue #415). Every method is scoped to
// ONE reader; access to the ticket is checked by the caller before a visit is
// recorded.
type TicketVisitStore interface {
	// Mark returns the ticket's current history mark. The caller takes it
	// BEFORE reading the ticket, so anything written during the read stays
	// new instead of being swallowed by the visit.
	Mark(ctx context.Context, ticketID int64) (VisitMark, error)
	// RecordVisit stores the mark for userID and, in the same transaction,
	// marks userID's unread notifications about that ticket read at instant
	// at. A mark never moves backwards.
	RecordVisit(ctx context.Context, userID, ticketID int64, mark VisitMark, at time.Time) error
	// Changed reports which of ticketIDs have, past userID's mark (or since
	// creation when userID never opened them), an audit event or a comment
	// made by someone else. Internal comments count only when includeInternal
	// is true. Tickets with nothing new are absent from the map.
	Changed(ctx context.Context, userID int64, ticketIDs []int64, includeInternal bool) (map[int64]bool, error)
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
	// UpdatePasswordHash replaces the stored password hash and voids the
	// account's pending password reset links, all or nothing (issue #414: every
	// password change voids pending links).
	UpdatePasswordHash(ctx context.Context, id int64, passwordHash string) error
	// Delete removes an unreferenced user; ErrReferenced when the user is
	// assigned to tickets (deactivation is the removal path then).
	Delete(ctx context.Context, id int64) error
	// GetByID returns the user, including inactive ones (historical display),
	// with the pending flag an account waiting for approval carries; ErrNotFound
	// when absent.
	GetByID(ctx context.Context, id int64) (*domain.User, error)
	// GetByEmail returns the user by email; ErrNotFound when absent.
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
	// Count returns the number of users (first-user bootstrap check, D16).
	Count(ctx context.Context) (int, error)
	// List returns all managed users. An account that waits for approval is
	// not one of them: it belongs to PendingUserStore.ListPending (issue #419,
	// S3), so no ordinary user list exposes it.
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

// PendingSignIn is one provider identity held by an account that waits for
// approval (issue #419, S3/S12). It carries the account and the provider only:
// the issuer, the subject and the address last seen at the provider are the
// identity's own record and never travel to a screen.
type PendingSignIn struct {
	UserID   int64
	Provider CompanyProvider
}

// NotificationAccountSubject is the stored account an account notice names, read
// as FACTS (issue #419, S3/S12): the account's own name and address, whether it
// still waits for approval, and the providers its stored identities carry. The
// issuer, the subject and the address last seen at the provider stay in the
// identity's own record and never travel to a screen.
type NotificationAccountSubject struct {
	UserID    int64
	Name      string
	Email     string
	Pending   bool
	Providers []CompanyProvider
}

// NotificationSubjectStore answers the account facts behind the account kind's
// notices (issue #419, S3/S12). It is a read of its own rather than more methods
// on PendingUserStore because a notice OUTLIVES the state that produced it: the
// account it names may have been approved since, and the row still has to render
// the facts it was written about instead of vanishing or claiming it still
// waits. It is a factual projection, not a decision: the caller applies the
// reader's capability before any fact is shown.
type NotificationSubjectStore interface {
	// ListNotificationSubjects answers one row per requested account id that
	// still exists, in the order requested and with each account's providers in
	// the closed provider set's display order. Both waiting and settled accounts
	// are answered — including inactive ones — because the notice is already
	// addressed to its recipient. An id with no account is absent, and an account
	// with no stored identity carries no provider rather than a guessed one.
	ListNotificationSubjects(ctx context.Context, userIDs []int64) ([]NotificationAccountSubject, error)
}

// PendingUserStore persists the company sign-in accounts that wait for an
// administrator's approval (issue #419, S3). It is a port of its own rather
// than more methods on UserStore because the Pending tab is the only surface
// that reaches such an account: ordinary user management never does.
type PendingUserStore interface {
	// ListPending answers every account waiting for approval, ordered by id.
	// Those accounts are inactive as well, so no other list reaches them.
	ListPending(ctx context.Context) ([]domain.User, error)
	// ListPendingSignIn answers one row per provider identity held by an account
	// that waits for approval, ordered by account id and then provider. It is
	// the factual half of the Pending tab's Sign-in column: the provider comes
	// from the stored identity, never from the address or the configuration. An
	// account with no stored identity has no row, and one that reached two
	// providers has two.
	ListPendingSignIn(ctx context.Context) ([]PendingSignIn, error)
	// ApprovePending activates each named account, clears its pending flag, and
	// grants the chosen role in ONE immediate transaction: an unknown id, an
	// account that is no longer pending, a repeated id, an invalid or root
	// role, or an empty selection changes nothing at all. Approval never grants
	// a privilege of its own (S9) and never creates or promotes a root.
	ApprovePending(ctx context.Context, approvals []PendingApproval, actorID int64, at time.Time) ([]domain.User, error)
	// RejectPending clears pending and leaves inactive, preserving the account,
	// role and identities. The application supplies the manageable target roles;
	// the store checks them inside the same all-or-nothing transaction.
	RejectPending(ctx context.Context, ids []int64, manageableRoles []domain.Role) ([]domain.User, error)
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

// CompanyIdentity is one provider identity linked to an account (issue #419,
// S7). Issuer and Subject are what later sign-ins resolve on — the pair the ID
// token calls iss and sub — so a changed email at the provider still reaches
// this row. Email is the address last seen at the provider, kept for
// administration.
type CompanyIdentity struct {
	ID           int64
	UserID       int64
	Provider     CompanyProvider
	Issuer       string
	Subject      string
	Email        string
	CreatedAt    time.Time
	LastSignInAt time.Time
}

// CompanySignInAttempt is one authorization in flight (issue #419). It is
// addressed by the SHA-256 hex digest of the flow's random state, never by the
// state itself, and it is deleted by the callback that consumes it, which is
// what makes a callback single use.
type CompanySignInAttempt struct {
	StateHash    string
	Provider     CompanyProvider
	Nonce        string
	CodeVerifier string
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

// CompanyIdentityStore persists the provider links and the in-flight
// authorizations of the company sign-in flow (issue #419).
type CompanyIdentityStore interface {
	// CreateAttempt stores one authorization in flight, keyed by its state
	// hash. The row is never returned to a browser; the flow keeps the state.
	CreateAttempt(ctx context.Context, attempt *CompanySignInAttempt) error
	// ConsumeAttempt deletes the attempt for stateHash and returns it, so a
	// callback can be answered at most once. ErrNotFound when no attempt
	// exists, including one already consumed. Expiry is the caller's check:
	// both instants travel with the row and the decision uses the injected
	// clock.
	ConsumeAttempt(ctx context.Context, stateHash string) (*CompanySignInAttempt, error)
	// PurgeExpiredAttempts deletes every attempt whose ExpiresAt is before
	// the given instant and reports how many it removed. A flow that was
	// started and never came back leaves nothing behind for long.
	PurgeExpiredAttempts(ctx context.Context, before time.Time) (int, error)
	// ResolveIdentity answers the account and the identity linked to
	// (provider, issuer, subject), or ErrNotFound when that provider identity
	// was never seen. The account is returned with its pending state, which
	// the sign-in decision needs.
	ResolveIdentity(ctx context.Context, provider CompanyProvider, issuer, subject string) (*CompanyIdentity, *domain.User, error)
	// UsersByEmail answers every account whose email matches case-
	// insensitively, ordered by id. The caller links only when exactly one
	// account matches; two accounts differing in case are an ambiguity, not a
	// choice.
	UsersByEmail(ctx context.Context, email string) ([]domain.User, error)
	// ActiveUsers answers every ACTIVE account, ordered by id, with the role and
	// the pending state the caller's policy reads. It is a roster, not a
	// decision: who must learn that an account waits for approval is computed by
	// the application layer from these facts (issue #419, S3), and the store
	// never selects recipients itself. A pending account is inactive, so it is
	// absent here.
	ActiveUsers(ctx context.Context) ([]domain.User, error)
	// CreateUserWithIdentity stores the account and its provider identity in
	// ONE transaction: an account created for a first sign-in is never left
	// without the link that identifies it. It returns
	// ErrCompanyIdentityTaken when either the account's email or the identity
	// pair was created concurrently, and then stores nothing.
	//
	// notifications are the notices that must commit with the account — the
	// pending-account announcements the application computed for it (issue #419,
	// S3/S11). They are appended in the SAME transaction, after the account's id
	// exists, and each one carries its recipient, its kind, and its instant: the
	// implementation stamps the created account as the subject, exactly as the
	// ticket units of work stamp the ticket id. An empty batch stores no notice,
	// and a rejected notice rolls the account and the identity back with it.
	CreateUserWithIdentity(ctx context.Context, user *domain.User, identity *CompanyIdentity, notifications []domain.Notification) error
	// LinkIdentity attaches a provider identity to an existing account. It
	// returns ErrCompanyIdentityTaken when that account already holds an
	// identity for the same provider.
	LinkIdentity(ctx context.Context, identity *CompanyIdentity) error
	// TouchIdentity records the email last seen at the provider and the
	// instant of this sign-in on an existing identity.
	TouchIdentity(ctx context.Context, identityID int64, email string, at time.Time) error
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
// Department or Desk.
type CatalogStore interface {
	ListDepartments(ctx context.Context) ([]domain.CatalogDepartment, error)
	ListDesks(ctx context.Context, departmentID int64) ([]domain.CatalogDesk, error)
	ListCatalogCategories(ctx context.Context, deskID int64) ([]domain.CatalogCategory, error)
	SearchCatalog(ctx context.Context, query string) ([]domain.CatalogCategory, error)
	CreateDepartment(ctx context.Context, d *domain.Department) error
	UpdateDepartment(ctx context.Context, d *domain.Department) error
	DeleteDepartment(ctx context.Context, id int64) error
	MoveCategory(ctx context.Context, categoryID, deskID int64) error
}

// CatalogTreeStore is the optional bounded-read projection the administration
// catalog tree renders from (issue #387, S8). The tree used to fan out one
// ListDesks call per department and still could not answer two questions the
// rows now show, so the index costs a constant number of queries instead.
//
// Both reads are presentation facts, which is why they live beside the catalog
// hierarchy and not on the workflow store: the desk agent count deliberately
// reuses the publish gate's predicate (active, agent or above) so the tree and
// the gate can never disagree about who can take a ticket.
type CatalogTreeStore interface {
	// ListTreeDesks returns every desk with its department, category count,
	// agent count and open-ticket count in ONE read. A legacy desk with no
	// department is reported with DepartmentID 0 and DepartmentName
	// "Unassigned", which is how the handler places it in its virtual group.
	ListTreeDesks(ctx context.Context) ([]domain.CatalogDesk, error)
	// ListDefaultSLACategories returns the ids of the categories whose
	// materialized SLA matrix still equals the current instance defaults. A
	// category absent from the result has custom targets (or none), which is the
	// only distinction the tree draws.
	ListDefaultSLACategories(ctx context.Context) ([]int64, error)
}

// CatalogDeskStore is the optional unified administration port implemented by
// the production SQLite catalog store. It reuses the existing Desk records and
// membership rows rather than creating a second persistence model.
type CatalogDeskStore interface {
	GetDeskByID(ctx context.Context, id int64) (*domain.Desk, error)
	CreateDesk(ctx context.Context, d *domain.Desk) error
	UpdateDesk(ctx context.Context, d *domain.Desk) error
	DeleteDesk(ctx context.Context, id int64) error
	ListDeskMembers(ctx context.Context, deskID int64) ([]domain.User, error)
	ListEligibleDeskMembers(ctx context.Context) ([]domain.User, error)
	AddDeskMember(ctx context.Context, deskID, userID int64, createdAt time.Time) error
	RemoveDeskMember(ctx context.Context, deskID, userID int64) error
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
	// Requester and Assignee are the notification policy facts about the two
	// candidate recipients of a workflow-driven ticket change (S10): identity,
	// role, and active state. The READ model answers them because they are
	// persisted facts about the people the ticket names; the APPLICATION decides
	// from them who is actually notified. nil means "no such person" or "not
	// provable", and an unprovable recipient is never notified.
	Requester *NotificationRecipient
	Assignee  *NotificationRecipient
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
	// NotifyAssignee, when set, is the S10 "assigned" notification for the
	// assignee THIS operation selects. The recipient is the one fact the
	// application cannot compute — the least_loaded selection ranges over the
	// desk inside the transaction (design S6) — so the unit of work fills it
	// from the user it selected. The application still decides the event, its
	// instant, and its actor: the actor is the human whose completion ran this
	// routing step, because the selection can land BACK on that person and the
	// implementation must then notify nobody (S10 "never the person who
	// performed the action").
	NotifyAssignee *AssigneeNotification
}

// AssigneeNotification is a notification whose recipient is the assignee a
// least_loaded step selects inside the unit of work. It carries every fact the
// stored row needs except the recipient, which the adapter supplies from its
// own selection — and the actor, which lets the adapter drop the rows when the
// selection is the actor himself.
type AssigneeNotification struct {
	TicketID int64
	// ActorUserID is the human whose action ran this routing step (the
	// completion command's actor), never the automatic step itself: the same
	// plan stamps that id on its completion audit, so the row keeps the
	// actors-are-also-audit-actors invariant.
	ActorUserID *int64
	CreatedAt   time.Time
	// FormWaitingStepIndex, when set, is the zero-based index of the assignee
	// FORM step this plan leaves PENDING. The recipient of that row is the same
	// selection: whoever the transaction ends up putting the ticket on is the
	// person who has to act next, and a plan cannot name them (S10 "a workflow
	// form step now waits on this person", decision e6-batch-2 L20). Only the
	// LAST least_loaded operation may carry it — an earlier one selects an
	// intermediate member, who is not the plan's final assignee.
	FormWaitingStepIndex *int
}

// Assigned completes the template into the "assigned" row for the selected
// assignee. The kind is always "assigned": that is the S10 event an automatic
// routing step produces when the ticket actually moves.
func (a AssigneeNotification) Assigned(assigneeUserID int64) domain.Notification {
	return domain.Notification{
		UserID:      assigneeUserID,
		TicketID:    a.TicketID,
		Kind:        domain.NotificationAssigned,
		ActorUserID: a.ActorUserID,
		CreatedAt:   a.CreatedAt,
	}
}

// FormWaiting completes the template into the "form_waiting" row for the same
// selected assignee, when the plan leaves an assignee form pending. Unlike the
// "assigned" row this one does not depend on the ticket having moved: a
// re-selected holder is owed it too, and the plan's conditional block therefore
// never carries it (the two would duplicate).
func (a AssigneeNotification) FormWaiting(assigneeUserID int64) (domain.Notification, bool) {
	if a.FormWaitingStepIndex == nil {
		return domain.Notification{}, false
	}
	step := *a.FormWaitingStepIndex
	return domain.Notification{
		UserID:      assigneeUserID,
		TicketID:    a.TicketID,
		Kind:        domain.NotificationFormWaiting,
		ActorUserID: a.ActorUserID,
		StepIndex:   &step,
		CreatedAt:   a.CreatedAt,
	}, true
}

// ConditionalAssigneeNotification carries the rows the PRE-PLAN assignee of a
// ticket is owed, with the condition that makes them truthful: they are written
// only when the assignee the transaction ends up with is the person they name.
// A least_loaded step chooses its assignee INSIDE the transaction, so at plan
// time nobody can know whether the ticket keeps its holder (a re-selection) or
// moves (a reroute).
//
// The two outcomes S10 requires:
//   - re-selection: the holder keeps every row it is owed (state_changed, …) and
//     gets NO "assigned" row — nothing was assigned to them, and a redundant
//     row is noise in a bell that counts;
//   - reroute: the outgoing holder gets NOTHING (it may no longer be able to
//     open the ticket), and the new holder gets its rows from the operation's
//     NotifyAssignee template.
//
// A pending assignee form is never part of these rows: its recipient is the
// ticket's FINAL holder, which the template above resolves to the same
// in-transaction selection without duplicating a row.
//
// The adapter re-reads its own selection and filters on it, so the recipient
// decision never depends on a plan-time guess.
type ConditionalAssigneeNotification struct {
	// AssigneeUserID is the pre-plan assignee: the recipient every row in Rows
	// is addressed to, and the value the transaction's final assignee must equal
	// for the rows to be written.
	AssigneeUserID int64
	// Rows are already complete notifications (kind, actor, instant, ticket);
	// the application computed them exactly as it does for every other batch.
	Rows []domain.Notification
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
	// Notifications are the S10 rows the application computed for this plan:
	// who must learn about the state change it produces. They are applied
	// inside the SAME transaction as the operations, the ticket write, and the
	// cursor CAS, so a plan that fails leaves no notification behind (S11).
	Notifications []domain.Notification
	// ConditionalNotifications carry the rows addressed to the ticket's
	// PRE-PLAN assignee, which are only truthful if the routing step this plan
	// runs leaves that person in place. They are resolved against the selection
	// the transaction actually made, because a least_loaded assignee is a
	// persistence fact (design S6): re-selection keeps the holder's rows,
	// rerouting drops them (the outgoing holder may no longer be able to open
	// the ticket — S10 rule 2). See ConditionalAssigneeNotification.
	ConditionalNotifications []ConditionalAssigneeNotification
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

// WorkflowSummary is the derived state of one category's workflow, as FACTS
// rather than as a label, so every surface composes the same truth.
//
// It replaces a three-valued Badge string ("none | Draft | Published") that
// collapsed two genuinely different states into one word: a category published
// with edits not yet live, and a category never published at all, both read as
// "Draft". It also could not say that a published category is unable to move a
// ticket, so a broken category looked exactly like a healthy one.
type WorkflowSummary struct {
	CategoryID   int64
	CategoryName string
	// Version is the live published version number, or 0 when nothing is published.
	Version int
	// HasDraft reports whether an unpublished definition exists at all, which is
	// what separates "draft, never published" from "not configured".
	HasDraft bool
	// PendingSteps counts the steps that differ between the draft and the live
	// version. Non-zero means the live version is not what the draft says.
	PendingSteps int
	// CannotRun is the reason the LIVE version cannot move a ticket, or the zero
	// message when it can. It is a catalogue reference (nested under the
	// categories.badge.cannot_run sentence), so the badge follows the instance
	// language while the domain keeps the English wording.
	CannotRun Msg
	// OpenTickets counts the tickets in this category that are neither closed nor
	// cancelled. It is the number that says where the work actually is, which no
	// surface showed before: the row could tell you a category was broken but not
	// whether anything was waiting in it.
	OpenTickets int
}

// WorkflowStore persists the category workflow draft and published versions (category-workflows spec).
// GetDraft is safe: absent row returns nil,nil and creates no row. UpsertDraft lazily creates on first mutation.
// Publish validates via domain, rechecks desks, allocates version_no, and switches current pointer atomically.
type WorkflowStore interface {
	GetDraft(ctx context.Context, categoryID int64) ([]byte, error)
	UpsertDraft(ctx context.Context, categoryID int64, draft []byte) error
	// Publish keeps its historical signature for the callers that arrange a
	// published version directly (the HTTP harness and adapter fixtures). It
	// publishes against the CURRENT revision and still advances it in the same
	// transaction, so a later save at the previous revision is refused; the
	// guarded production path is PublishAtRevision.
	Publish(ctx context.Context, categoryID int64, draft []byte, publishedByUserID *int64) (int64, []domain.WorkflowValidationIssue, error)
	// PublishAtRevision is the guarded publish (issue #254). expectedRevision is
	// compared-and-swapped in the SAME immediate transaction that allocates the
	// version and switches current_version_id, so a stale tab that publishes
	// superseded bytes writes NOTHING and is refused with
	// ErrDraftRevisionConflict. On success the stored draft_revision advances by
	// exactly one and is returned, so the next save must carry the new value.
	PublishAtRevision(ctx context.Context, categoryID int64, draft []byte, expectedRevision int64, publishedByUserID *int64) (versionID int64, draftRevision int64, issues []domain.WorkflowValidationIssue, err error)
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

// SLAStore persists the per-category SLA configuration, the observed SLA
// milestone instants, and the SLA commitments frozen onto tickets (issue
// #211). The global defaults (sla_defaults, seeded by migration 0014)
// seed a category's materialized 4-priority x 2-milestone matrix
// (sla_policies) at category creation time — there is no NULL-fallback
// chain at read time. A ticket's targets are FROZEN at creation (the
// WorkflowVersionID precedent): a later policy edit never rewrites a
// committed ticket's targets.
//
// Absence is a state, not a failure: a legitimately absent row — a
// category with no matrix yet, a ticket created before SLA activation —
// reads back as nil/empty with a NIL error. An error return always means a
// real storage failure, never an absent row.
type SLAStore interface {
	// ListDefaults returns the global default targets (sla_defaults),
	// ordered by the canonical priority order: critical, high, medium, low.
	ListDefaults(ctx context.Context) ([]domain.SLAPolicy, error)
	// ListByCategory returns one category's materialized matrix (all its
	// sla_policies rows), in the same canonical priority order. A category
	// without rows returns an empty result and a nil error.
	ListByCategory(ctx context.Context, categoryID int64) ([]domain.SLAPolicy, error)
	// UpsertDefault writes one sla_defaults row (priority, both targets).
	// When the priority already exists, its targets are replaced — never
	// duplicated. It mirrors the single-row UpsertCategoryTarget.
	UpsertDefault(ctx context.Context, policy domain.SLAPolicy) error
	// UpsertDefaults writes ALL the given sla_defaults rows in ONE
	// transaction — ALL OR NOTHING: any single failing write rolls back
	// every row, leaving the table exactly as it was. A partial default
	// matrix is never persisted; the administrative default-matrix edit
	// (SetDefaultTargets) depends on this atomicity.
	UpsertDefaults(ctx context.Context, policies []domain.SLAPolicy) error
	// UpsertCategoryTarget writes one matrix row (category, priority, both
	// targets). When the (category, priority) pair already exists, its
	// targets are replaced — never duplicated.
	UpsertCategoryTarget(ctx context.Context, categoryID int64, policy domain.SLAPolicy) error
	// UpsertCategoryTargets writes ALL the given rows of ONE category's
	// matrix in ONE transaction — ALL OR NOTHING: any single failing write
	// rolls back every row, leaving the category's matrix exactly as it
	// was. A category left with a partial matrix would silently change the
	// commitments frozen onto future tickets; the administrative matrix edit
	// (SetCategoryTargets) depends on this atomicity.
	UpsertCategoryTargets(ctx context.Context, categoryID int64, policies []domain.SLAPolicy) error
	// TicketSLA returns the commitment frozen onto a ticket, or (nil, nil)
	// when the ticket has NO ticket_sla row — the legitimate state for
	// legacy tickets and for tickets created while SLA was disabled. Such a
	// ticket has no SLA commitment at all: it renders as "no SLA", never as
	// retroactively breached.
	TicketSLA(ctx context.Context, ticketID int64) (*domain.TicketSLA, error)
	// InsertTicketSLA freezes a commitment onto one ticket. This is a
	// creation-time write (the row is never updated afterwards, except the
	// resolution instants a resumed hold shifts — TicketUnitOfWork.UpdateHold,
	// issue #352); inserting
	// a second row for the same ticket is a storage failure.
	InsertTicketSLA(ctx context.Context, ticketID int64, sla domain.TicketSLA) error
	// Milestones returns the observed SLA milestone instants for one
	// ticket: the FIRST public staff response and the FIRST resolution,
	// each nil when it has not happened. Both are deliberately FIRSTS —
	// the SLA measures the commitment made at ticket creation, so a later
	// response or a reopen-and-reresolve never replaces the observed
	// first. This differs from the administrative metrics read, which
	// takes the LAST resolution of the requested interval. ResolvePausedAt
	// carries the ticket's on_hold_since while it is on hold (issue #352).
	Milestones(ctx context.Context, ticketID int64) (domain.SLAMilestones, error)
	// TicketSLAs returns the commitments frozen onto MANY tickets, keyed
	// by ticket id — the list-rendering read path (issue #211), so a page
	// of SLA badges costs one bounded read instead of the per-ticket
	// TicketSLA N+1. A ticket with NO row is ABSENT from the map: absence
	// is the "no SLA" state, never a zero-value commitment. An empty
	// ticketIDs answers an empty map WITHOUT touching the database (an
	// IN () list is a SQL syntax error) and without an error. Rows are
	// answered in no particular order — the map is keyed by id.
	TicketSLAs(ctx context.Context, ticketIDs []int64) (map[int64]*domain.TicketSLA, error)
	// MilestonesFor returns the observed SLA milestone instants for MANY
	// tickets, keyed by ticket id, using the same FIRST-response and
	// FIRST-resolution semantics as Milestones, plus ResolvePausedAt. A
	// ticket with NO observation (no response, no resolution, not on hold)
	// is ABSENT from the map; a ticket with some observation is PRESENT with
	// the others nil. An empty
	// ticketIDs answers an empty map WITHOUT touching the database (an
	// IN () list is a SQL syntax error) and without an error. Rows are
	// answered in no particular order — the map is keyed by id.
	MilestonesFor(ctx context.Context, ticketIDs []int64) (map[int64]domain.SLAMilestones, error)
}

// SettingsStore persists single-row instance settings (appearance-settings
// spec). The internal-comment background row is seeded by migration 0005;
// a missing row reads back the application default. The interface-language
// row (issue #335) is never seeded: no migration is needed, because an
// absent row reads back the same default a shipped instance renders.
type SettingsStore interface {
	// GetInternalCommentBg returns the configured internal-comment
	// background color, or DefaultInternalCommentBg when the row is absent.
	GetInternalCommentBg(ctx context.Context) (string, error)
	// SetInternalCommentBg persists the internal-comment background color.
	SetInternalCommentBg(ctx context.Context, color string) error
	// GetInterfaceLanguage returns the instance's interface language
	// (issue #335), or DefaultInterfaceLanguage when the row is absent or
	// holds a value no shipped catalogue answers. The read never fails for
	// configuration reasons: it always answers a language the binary ships,
	// so a hand-edited database degrades to English instead of dropping
	// every page.
	GetInterfaceLanguage(ctx context.Context) (string, error)
	// SetInterfaceLanguage persists the instance interface language. The
	// application service validates the value against
	// AllowedInterfaceLanguages before this call; the store refuses an
	// unsupported code rather than persisting a language the binary cannot
	// render, because this is the layer the next read will answer from.
	SetInterfaceLanguage(ctx context.Context, language string) error
	// GetSLAEnabled reports whether the per-category SLA is enabled
	// (issue #211). The row stores '0'/'1' text; an absent or unparseable
	// value falls back to disabled (fail-safe: enabling is an explicit
	// admin action, never a defaulted-on state).
	GetSLAEnabled(ctx context.Context) (bool, error)
	// GetSLAWarningPercent returns the SLA warning threshold percentage, or
	// DefaultSLAWarningPercent when the row is absent or unparseable.
	GetSLAWarningPercent(ctx context.Context) (int, error)
	// GetSLAEnabledAt returns the activation instant recorded when the SLA
	// settings were first configured. Informational display only — targets
	// are frozen onto tickets at creation, so this instant is never
	// load-bearing. An absent or unparseable row returns the zero time.
	GetSLAEnabledAt(ctx context.Context) (time.Time, error)
	// SetSLAEnabledState persists the sla_enabled setting ('0'/'1' text, the
	// same form GetSLAEnabled parses) and, when enabling, the activation
	// instant — both in ONE transaction, so an enabled flag is never
	// persisted without the instant that explains it.
	//
	// The instant records when the feature was FIRST switched on: an
	// existing value is left alone, so enabling twice does not move it, and
	// disabling never erases it.
	SetSLAEnabledState(ctx context.Context, enabled bool, at time.Time) error
	// GetTicketCreationMode returns the instance's ticket creation flow (issue
	// #391, S1), or DefaultTicketCreationMode when the row is absent or holds a
	// value no surface implements. Like the interface-language read it never
	// fails for configuration reasons: it always answers a flow the binary can
	// render, so a hand-edited database degrades to the default instead of
	// dropping the create screen.
	GetTicketCreationMode(ctx context.Context) (string, error)
	// SetTicketCreationMode persists the instance ticket creation flow. The
	// application service validates the value against
	// AllowedTicketCreationModes before this call; the store refuses an
	// unsupported mode rather than persisting a flow the binary cannot render,
	// because this is the layer the next read will answer from.
	SetTicketCreationMode(ctx context.Context, mode string) error
	// SetSLAConfiguration writes the WHOLE instance SLA panel — the enable
	// flag and its first-enable activation instant, the warning percent, and
	// all four default targets — in ONE transaction.
	//
	// The panel is one configuration, so the store applies it as one. Writing
	// its pieces separately would leave a rejected edit half-applied, the same
	// defect class as a partially applied matrix, and no caller-side
	// compensation can make sequential writes atomic: a failure between a
	// write and its undo leaves the inconsistency behind.
	SetSLAConfiguration(ctx context.Context, enabled bool, at time.Time, warningPercent int, defaults []domain.SLAPolicy) error
	// SetSLAWarningPercent persists the sla_warning_percent setting.
	SetSLAWarningPercent(ctx context.Context, percent int) error
	// GetSLACalendar returns the instance working calendar (issue #211),
	// assembled from the four sla_calendar_* settings keys of migration
	// 0017: the working weekdays, the daily window in minutes past local
	// midnight, and the IANA zone the window is interpreted in.
	//
	// Each key falls back to its documented default when its row is absent
	// or unparseable — days "1,2,3,4,5" (Monday-Friday), start 540 (09:00),
	// end 1080 (18:00) — and an unknown timezone name falls back to UTC
	// rather than failing the read. A calendar read therefore always
	// answers a usable calendar — the ASSEMBLED value is validated, not
	// just each key — and only a real storage failure is an error.
	GetSLACalendar(ctx context.Context) (domain.SLACalendar, error)
	// SetSLACalendar writes the WHOLE working calendar — the four
	// sla_calendar_* keys — in ONE transaction, ALL OR NOTHING: a partially
	// applied calendar would change when every future ticket is due, so a
	// failing write must roll every key back and leave the previous
	// calendar standing. The service use case (SetSLACalendar) validates
	// the calendar before this store call.
	SetSLACalendar(ctx context.Context, calendar domain.SLACalendar) error
	// GetHoldAutoCloseDays returns the hold auto-close window of issue #353,
	// in WORKING days, or DefaultHoldAutoCloseWorkingDays (0, the feature off)
	// when the row is absent, unparseable, or outside 0..60. Closing a ticket
	// without a human is an explicit decision, so an unusable stored value
	// fails safe to "off" rather than to some arbitrary window. The working
	// calendar the window is measured on is GetSLACalendar.
	GetHoldAutoCloseDays(ctx context.Context) (int, error)
	// SetHoldAutoCloseDays persists the hold auto-close window in working
	// days. The application service validates 0..60 before this call; the
	// store writes the value it is given, like the other single-key settings.
	SetHoldAutoCloseDays(ctx context.Context, days int) error
}

// OrganizationStore persists the instance identity (issue #383, S3): the
// name, the help desk name, an optional support email, and the logo. The three
// text facts live beside the other instance settings; the logo needs bytes, so
// it is stored in the settings_blobs table migration 0026 adds — the settings
// table's value column is TEXT and cannot hold an image.
//
// It is a port of its own rather than three more methods on SettingsStore
// because its reader is the request middleware: every page renders the brand,
// so widening SettingsStore would force every settings fake and every
// settings-only caller to carry the organization as well.
type OrganizationStore interface {
	// GetOrganization returns the stored identity. It NEVER fails for
	// configuration reasons: an absent row answers the documented default
	// (name "tkt", help desk "Help desk", no email, no logo), so a fresh or
	// hand-edited instance still renders a usable brand. The monogram is
	// derived, not stored.
	GetOrganization(ctx context.Context) (domain.Organization, error)
	// SetOrganization writes every field — the three text values and the
	// logo — in ONE transaction, so a submission can never be half applied:
	// either the name and the logo an administrator saved together are both
	// stored, or neither is. A nil Logo means the instance has no logo and
	// deletes the stored one; the caller decides whether an unsubmitted file
	// keeps the previous logo.
	SetOrganization(ctx context.Context, org domain.Organization) error
}

// PasswordSettingsStore persists the instance password policy (issue #414,
// S1/S4): the two switches that remove the self-service password paths, and the
// minimum length every NEW password obeys.
//
// It is a port of its own rather than three more methods on SettingsStore for
// the same reason OrganizationStore and SigninStore are: its readers are the
// password write paths and the sign-in page, so widening SettingsStore would
// force every settings fake and every settings-only caller to carry the
// password policy as well.
type PasswordSettingsStore interface {
	// GetPasswordSettings returns the stored policy. It NEVER fails for
	// configuration reasons: absent rows answer DefaultPasswordSettings, an
	// unparseable or out-of-band minimum reads back the default 12, and an
	// unparseable switch reads back ON — a hand-edited database degrades to
	// the shipped policy instead of dropping every password surface.
	GetPasswordSettings(ctx context.Context) (PasswordSettings, error)
	// SetPasswordSettings writes the switches and the minimum. The three values
	// are one configuration, so implementations MUST store them in ONE
	// transaction: an administrator's save is either entirely applied or not at
	// all, never half of it.
	SetPasswordSettings(ctx context.Context, settings PasswordSettings) error
}

// PasswordChangeStore persists a signed-in user's own password change (issue
// #414, S2) as ONE atomic unit: the new hash and the sign-out of every other
// session of that account commit together. A user is therefore never left with
// the new password stored while a device signed in with the old one stays
// signed in, and never signed out while the password they typed was not stored.
//
// It is a port of its own rather than a UserStore method because the unit spans
// three tables (users, sessions and the recovery links of migration 0029).
// #417, #419 and #420 build on this seam: a password write always carries the
// sessions and the pending links it voids.
type PasswordChangeStore interface {
	// ChangePasswordAndSignOutOtherSessions replaces userID's stored password
	// hash with passwordHash and deletes every session of that user EXCEPT
	// keepSessionID — the session making the request, which stays signed in
	// (S2). Both writes are ONE transaction, so a failure leaves the hash and
	// the sessions exactly as they were. Sessions belonging to any other user
	// are never touched. An unknown user is ErrNotFound with nothing deleted.
	// The user's pending recovery links are voided in the same transaction,
	// because a link mailed before the change must never set a password after
	// it.
	//
	// The hash write is a compare-and-set against expectedPasswordHash: the very
	// hash the caller verified the current password against. Two submissions can
	// both verify the old password before either writes, and without this check
	// the second one silently replaces the password the first one stored, and
	// the person who believes the change succeeded is signed out of it. A stored
	// hash that is no longer expectedPasswordHash refuses the whole unit — before
	// a session or a pending link is touched — with the shared
	// current-password refusal, the same one a wrong current password gets.
	ChangePasswordAndSignOutOtherSessions(ctx context.Context, userID int64, expectedPasswordHash, passwordHash, keepSessionID string) error
}

// PasswordResetStore persists the single-use recovery links of the self-service
// password path (issue #414, S3/S8/S9): the stored token hash, the instant it
// was issued, the instant it stops working, and the instant it was spent.
//
// The raw token a link carries is never part of this contract: the application
// hashes it and hands over the digest, so implementations never see a working
// credential. Instants are always passed in — an implementation never reads a
// wall clock, so expiry is decided by the caller's clock.
//
// It is a port of its own because its unit spans three tables (the links, the
// user's password and their sessions), and because a link that outlived a
// password change or a newer link is exactly what S3 and S9 forbid.
type PasswordResetStore interface {
	// LastPasswordResetRequest reports when this account last had a link
	// issued, or nil when it never had one. Spent and pending links count
	// alike: it is what "one email per account every five minutes" measures
	// from (S9).
	LastPasswordResetRequest(ctx context.Context, userID int64) (*time.Time, error)
	// IssuePasswordResetToken voids userID's pending links and stores
	// tokenHash with the instants that bound it, in ONE transaction: a new
	// request must never leave two working links behind, and a failure must
	// never leave the account with none. Spent links are kept as history.
	IssuePasswordResetToken(ctx context.Context, userID int64, tokenHash string, createdAt, expiresAt time.Time) error
	// PasswordResetTokenUsable reports whether tokenHash can still be redeemed
	// at now, without consuming it. An unknown, spent or expired hash answers
	// false and no error: the three are indistinguishable on purpose.
	PasswordResetTokenUsable(ctx context.Context, tokenHash string, now time.Time) (bool, error)
	// PasswordResetTokenOwner answers the account a still-usable link belongs
	// to, consuming nothing (issue #419, S5). An unknown, spent or expired hash
	// answers ok false and no error.
	PasswordResetTokenOwner(ctx context.Context, tokenHash string, now time.Time) (userID int64, ok bool, err error)
	// RedeemPasswordResetToken spends the link in ONE transaction: it stores
	// passwordHash as the owner's new password, marks the link spent, voids
	// that account's other pending links, and deletes EVERY session of that
	// account (S3: using the link signs the account out everywhere). An
	// unknown, spent or expired hash — expiry compared strictly before, so the
	// link is already refused at its expiry instant — answers domain.ErrNotFound
	// with nothing written. Concurrent redeemals of one hash succeed exactly
	// once.
	RedeemPasswordResetToken(ctx context.Context, tokenHash, passwordHash string, now time.Time) error
}

// SigninStore persists the sign-in page's customization (issue #383, S9/S10):
// the text facts as keyed settings rows — the highlighted rows as one JSON array
// decoded defensively — and the background image as a settings_blobs row, since
// the settings table's value column is TEXT.
//
// Like OrganizationStore it is its own port because its reader is the login
// page, which renders before any session exists: widening SettingsStore would
// force every settings-only caller and fake to carry the sign-in panel too.
type SigninStore interface {
	// GetSignin returns the stored panel and background image. It NEVER fails
	// for configuration reasons: absent rows answer the documented defaults
	// (steps, no notice, the dark background), and a malformed highlights value
	// reads back as "no highlights" instead of failing the login page.
	GetSignin(ctx context.Context) (SigninConfiguration, error)
	// SetSignin writes the panel's text facts AND the background image in ONE
	// transaction, so a submission can never be half applied. A nil background
	// means the instance holds no background image and deletes the stored row.
	SetSignin(ctx context.Context, panel domain.SigninPanel, background []byte) error
}

// PreferencesStore persists per-user preferences (issue #210) in the
// user_preferences table, keyed by (user_id, key). The first preference is
// the default queue order; the table is generic so later preferences reuse
// it. The store persists whatever it is given — the closed set is enforced
// by the application service before a write, and the read path normalizes an
// unknown stored value to the default.
type PreferencesStore interface {
	// GetQueueOrder returns the user's stored default queue order, or ""
	// when the user has no stored preference.
	GetQueueOrder(ctx context.Context, userID int64) (string, error)
	// SetQueueOrder upserts the user's stored default queue order.
	SetQueueOrder(ctx context.Context, userID int64, order string) error
}

// EmailPreferencesStore persists each user's email opt-out (issue #362, S5)
// in the same user_preferences table, under its own key, so the queue-order
// port above is unchanged and the two preferences can never overwrite each
// other. It embeds the read half the background sender depends on
// (EmailPreferences), so ONE adapter serves both the sender and the
// preferences page. Like the queue order this is a personal setting with no
// capability gate: the key is the acting user's id, never a caller-supplied
// one.
type EmailPreferencesStore interface {
	EmailPreferences
	// SetEmailNotificationsEnabled upserts the user's email choice.
	SetEmailNotificationsEnabled(ctx context.Context, userID int64, enabled bool) error
}

// SidebarPreferencesStore persists each user's sidebar state (issue #377, S2)
// in the same user_preferences table, under its own key, so the queue-order
// and email ports above are unchanged and no preference can overwrite another.
// Like the other two this is a personal setting with no capability gate: the
// key is the acting user's id, never a caller-supplied one, so one user can
// only ever read or write their own shell.
//
// The readers (the request middleware that stamps the state, and
// pageDataFrom) read it ONCE per authenticated request, which is why the read
// is a plain boolean instead of a rendering concern.
//
// Defaults are part of the contract, not the adapter's mood: an absent row
// means expanded (false) — the state every user starts with — and only the
// exact stored value "collapsed" means true. Every other stored value reads as
// expanded, so a hand-edited or restored-from-backup row can never collapse a
// sidebar nobody collapsed.
type SidebarPreferencesStore interface {
	// SidebarCollapsed reports whether the user's sidebar is collapsed. An
	// absent row answers false (expanded); only the exact stored value
	// "collapsed" answers true.
	SidebarCollapsed(ctx context.Context, userID int64) (bool, error)
	// SetSidebarCollapsed upserts the user's sidebar state, keeping exactly
	// one row per user ("collapsed" or "expanded").
	SetSidebarCollapsed(ctx context.Context, userID int64, collapsed bool) error
}

// ThemePreferencesStore persists each user's interface theme (issue #378, S1)
// in the same user_preferences table, under its own key, so the queue-order,
// email, and sidebar ports above are unchanged and no preference can overwrite
// another. Like the other three this is a personal setting with no capability
// gate: the key is the acting user's id, never a caller-supplied one.
//
// The readers (the request middleware that stamps the page attribute, and
// pageDataFrom) read it ONCE per authenticated request, which is why the read
// answers the bare stored string instead of a rendering concern.
//
// Defaults are part of the contract, not the adapter's mood: an absent row
// means "" — no choice yet — and the application service normalizes that, and
// every value outside the closed set, to ThemeSystem. The store persists
// whatever it is given, so a hand-edited or restored-from-backup row can never
// render a theme the stylesheet does not define.
type ThemePreferencesStore interface {
	// GetTheme returns the user's stored theme, or "" when the user has no
	// stored preference.
	GetTheme(ctx context.Context, userID int64) (string, error)
	// SetTheme upserts the user's stored theme.
	SetTheme(ctx context.Context, userID int64, theme string) error
}

// TicketSection narrows an agent's read scope into a presentation section.
// The zero value keeps the full actor scope for existing list and detail reads.
//
// The Requester sections belong to the requester's OWN list (issue #411): the
// server chooses one per section of the page, and SearchService derives the
// state set and the on-hold half of the predicate from it, so a caller can
// never pick a section or widen what a requester reads.
type TicketSection int

const (
	TicketSectionAll TicketSection = iota
	TicketSectionPersonal
	TicketSectionClaimable
	// TicketSectionRequesterAction is the requester's "Needs your action" set
	// (issue #411, S8): the active work held for their reply OR a resolved
	// ticket awaiting their confirmation. It replaces the former on-hold-only
	// waiting section, so both requests for the requester live in one list.
	TicketSectionRequesterAction
	// TicketSectionRequesterOpen is the requester's active work that is NOT on
	// hold, so a held ticket is never listed twice.
	TicketSectionRequesterOpen
	// TicketSectionRequesterClosed is the requester's CLOSED work only, folded
	// at the end of the list. A resolved ticket still awaiting confirmation
	// belongs to the Needs-your-action section, never here (issue #411, S8).
	TicketSectionRequesterClosed
	// TicketSectionRequesterExport is the requester's WHOLE list (issue #425,
	// S1/S5/S8): every ticket their three list sections show — active work on
	// hold or not, the resolved ones awaiting confirmation, and the closed
	// ones — with cancelled work left out exactly as the list leaves it out.
	// The export handler chooses it for a requester; nothing a caller sends can
	// select, widen or narrow it.
	TicketSectionRequesterExport
)

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
	State *domain.State
	// States is an internal multi-state restriction. It composes with State
	// when both are set. The requester's list sets it from the section the
	// server chose (active work, or resolved+closed for the folded section).
	States     []domain.State
	Priority   *domain.Priority
	CategoryID *int64
	UserID     *int64
	// Unassigned restricts the list to tickets with no assignee (issue #382,
	// the "Unassigned" queue view). It composes with every other filter and
	// is meaningless together with UserID; the handler never sets both.
	Unassigned bool
	// OnHold restricts the list to tickets currently on hold (issue #382, the
	// "On hold" queue view): on_hold_since is set, whatever the state.
	OnHold bool
	// NotOnHold restricts the list to tickets that are NOT on hold, so the
	// requester's Needs-your-action set and their open set never overlap
	// (issue #411, S3/S4).
	NotOnHold bool
	// RequesterNeedsAction restricts the read to the requester's "Needs your
	// action" set (issue #411, S8): active work on hold for their reply, OR a
	// resolved ticket awaiting their confirmation. States, OnHold and NotOnHold
	// cannot express an OR, so this is a predicate of its own. It is
	// SERVER-ONLY: SearchService derives it from the section it chose and
	// clears every caller-supplied value, exactly like the state set it
	// accompanies, so a crafted query can never select the section.
	RequesterNeedsAction bool
	// Text is the D4-tokenized, title-scoped FTS expression ("" = no title
	// filter). The search box matches ONLY ticket titles or IDs.
	Text string
	// Numbers holds the exact positive ticket numbers (TKT-N) extracted
	// from the raw text filter; the ID-search side of the text clause.
	Numbers []int64
	// SortByPriority orders results by the D11 priority rank
	// (critical > high > medium > low) before the created/id tiebreak.
	SortByPriority bool
	// SortByUrgency orders results by the SLA deadline that is actually
	// OUTSTANDING (issue #211, PR 4): the due instant of the FIRST milestone
	// the ticket has not achieved — the response deadline while no public
	// staff response exists, then the resolution deadline until the ticket is
	// resolved. A ticket with nothing outstanding (no frozen SLA, or both
	// milestones achieved) sorts LAST, and the D11 priority rank followed by
	// the D2 created/id tiebreak keeps page boundaries stable. It takes
	// precedence over SortByPriority when both are set, because the priority
	// rank is already its tiebreak.
	SortByUrgency bool
	// Section splits an agent's existing read scope into personal assignments
	// and current desk claims. It never widens the access scope.
	Section TicketSection
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

// CompanySignInSettingsStore persists the company sign-in configuration (issue
// #419). It is a port of its own for the reason PasswordSettingsStore is: its
// readers are the sign-in page and the sign-in flow.
type CompanySignInSettingsStore interface {
	// GetCompanySignIn returns the stored configuration with the sealed
	// secrets. It NEVER fails for configuration reasons: absent rows answer
	// DefaultCompanySignInSettings, an unknown new-account policy reads back
	// requester, and an unparseable password switch reads back ON.
	GetCompanySignIn(ctx context.Context) (CompanySignInSettings, error)
	// SetCompanySignIn writes the whole configuration in ONE transaction: an
	// administrator's save is one configuration, never half applied.
	SetCompanySignIn(ctx context.Context, settings CompanySignInSettings) error
}
