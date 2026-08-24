---
change: category-workflows
phase: design
status: completed
artifact_store: openspec
execution_mode: interactive
delivery_strategy: exception-ok
review_budget: none (maintainer size waiver — one direct final PR)
---

# Design: Linear Category Workflows

## Decision summary

Implement one category-owned editable JSON draft, immutable published JSON snapshots, one ticket pin, and one cursor per ticket. Builder reads are safe: `GET /categories/{id}/workflow` never writes, and a missing draft row is represented by an in-memory empty draft until the first mutating POST. Execution is planned by the application through one closed loop and one closed `switch` over five step types; there is no workflow engine, executor registry, graph, plugin API, callback transaction API, or per-step interface hierarchy.

The current SQLite adapter already uses `modernc.org/sqlite` (`internal/adapters/sqlite/sqlite.go` and `go.mod`). This design keeps it because the pure-Go driver already supplies the project's foreign-key, WAL, busy-timeout, immediate-transaction, FTS5, and JSON support. The driver choice in `openspec/config.yaml` is stale text, not an open decision.

Two design-level clarifications resolve ambiguous wording in the proposal and specifications without changing approved product behavior:

- “Begins configuration” means the first mutating builder POST, not a GET. An authorized GET may display an empty editable draft but creates no `category_workflows` row and no `Draft` badge merely by viewing.
- Specifications may use “task” generically for pending workflow work. Because the model has no task-instance identity, the completion route honestly identifies the pinned step position: `POST /tickets/{id}/workflow/steps/{position}/complete`.

Amendment 2 (approved product requirements: unassigned-only creation, pinned manual instructions with an optional completion solution, pending-presentation cleanup, completed-results treatment) is incorporated in place throughout this document. Its one NEW persistence decision: a manual-task solution lives in its own workflow-task record table (`ticket_manual_solutions`, migration `0009`), written by the SAME `WorkflowUnitOfWork` immediate transaction as the completion audit, cursor CAS, and every co-planned transition, and projected to the timeline ONLY through the existing pinned-context seam (`WorkflowStepContextStore` → `TimelineItem.StepSolution`). No parallel lookup path, no audit note/reason copy, no FTS entry, no backfill. All prior decisions remain valid and unchanged: linear plans over the closed five-step set, pinned-version immutability, the load-plan-recheck UoW with cursor compare-and-swap, deterministic least-loaded selection, and the application-owned closed switch. `desks-ux-polish` remains untouched.

**Amendment 3 (approved UI and claim scope):** native workflow-configurator selects receive tkt-consistent presentation without replacing native controls or their HTMX/autosave semantics. Categories and desks gain deliberately simple responsive index layouts based only on the supplied screenshots' information architecture, never their palette or decoration. Pinned `assign_to_desk[claim]` moves from Pending Actions/timeline input to the ticket Assignment sidebar: it shows Desk and current Assignee, and exposes a reasonless `Assign to me` control only to an active agent/admin/root who is currently a member of the pinned desk. The existing completion route and load-plan-recheck UoW remain the sole mutation path. Within its immediate transaction the UoW rechecks the pinned version, active cursor, actor activity/role, and current desk membership before any write; first claimant wins, and stale, removed, or deactivated actors receive a typed failure with zero writes. A successful claim creates exactly one contextual `Assigned to {person} · {desk}` timeline event and retains the existing optional `new → in_progress` transition. This narrowly removes the reason only from the workflow-claim command/operation pipeline; generic `POST /tickets/{id}/assign`, `TicketService.Assign`, and historical audit reasons retain their current contract and rendering.

## Architecture and boundaries

```text
HTTP forms / HTMX
        |
        +-----------------------+------------------------+
        |                       |                        |
        v                       v                        v
WorkflowService           WorkflowRunner            TicketService
(draft/publish)      (load -> decide -> plan)       (create + pin)
        |                       |                        |
        |                       +--> domain.Ticket.Transition
        |                       |    and closed application switch
        v                       v                        v
 WorkflowStore          WorkflowRunStore       CreateTicketWithRun plan
        |                       |                        |
        +-----------------------+------------------------+
                                |
                                v
                    SQLite WorkflowUnitOfWork
              (BEGIN IMMEDIATE, recheck, query, write, CAS)
```

Responsibilities are deliberately narrow:

| Layer | Owns | Does not own |
| --- | --- | --- |
| Domain | Closed workflow enums and definition validation; ticket lifecycle transitions through `Ticket.Transition` | Workflow advancement, actor orchestration, SQL, transaction scope |
| Application `WorkflowService` | Capability-first safe draft reads, draft mutations, preview, publish orchestration, category summaries, and available-category use cases | Ticket execution, historic-version browsing, unpublish |
| Application `WorkflowRunner` | The closed linear advancement loop and step-type `switch`; expected-position checks; actor and current-step decisions; positional form decoding and validation orchestration; terminal-state matrix orchestration; construction of a concrete data-only mutation plan | SQL, transaction management, executor registration, scheduling, retries |
| SQLite `WorkflowUnitOfWork` | `BEGIN IMMEDIATE`; persisted-state reload and exact precondition recheck; deterministic least-loaded query; fixed writes; cursor compare-and-swap; audit persistence | Step dispatch, actor-policy choice, form rules, terminal-matrix choice, callbacks, arbitrary commands |

Three ports keep the existing hexagonal-lite split without creating one port per step:

| Port | Concrete contract |
| --- | --- |
| `WorkflowStore` | Optional draft/version reads, canonical draft upsert on mutating POST, atomic publish, summaries, and published categories |
| `WorkflowRunStore` | Read the pinned execution snapshot/current step and completed public form submissions for a ticket |
| `WorkflowUnitOfWork` | Atomically apply `CreateTicketWorkflowPlan` or `WorkflowMutationPlan` after exact persisted-state rechecks |

The application-facing completion input preserves raw positional values without trusting client keys or types:

```go
type RawPositionalValue struct {
    Position int      // zero-based answer_N position parsed by HTTP
    Values   []string // zero or one accepted after ambiguity checks
}

type RawPositionalValues []RawPositionalValue

    type CompleteWorkflowCommand struct {
        TicketID         int64
        ActorUserID      int64
        ExpectedPosition int // one-based route position
        RawAnswers       RawPositionalValues
        Solution         string // optional manual-task solution (Amendment 2): trimmed by the handler;
                                // whitespace-only means absent; bounded at 2,000 characters post-trim
    }
```

`WorkflowRunner` loads a `WorkflowExecutionSnapshot`, converts `ExpectedPosition` from one-based external numbering to the zero-based cursor, checks that it matches the current pinned step, and walks the immutable definition. It uses the persisted snapshot to make actor and step decisions, invokes `Ticket.Transition` on an in-memory ticket copy for every planned lifecycle change, and emits one concrete `WorkflowMutationPlan`. The plan has fixed fields for expected persisted facts, optional form-answer data, assignment requests, already-decided lifecycle transitions, required audit records, and the final cursor/run status. It is not an arbitrary operation list.

```go
type WorkflowUnitOfWork interface {
    CreateTicketWithRun(context.Context, CreateTicketWorkflowPlan) (WorkflowExecutionResult, error)
    ApplyWorkflowPlan(context.Context, WorkflowMutationPlan) (WorkflowExecutionResult, error)
}
```

`ApplyWorkflowPlan` starts one immediate transaction, reloads the ticket, run, pinned snapshot, users, and memberships named by the plan's concrete preconditions, and rejects any mismatch before writing. It then performs only the writes already selected by the application. A least-loaded assignment request is a closed data field in the plan: SQLite performs the fixed deterministic query inside the transaction and persists its result, but it does not decide whether the current step is an assignment or what follows it. The adapter fills persistence-derived values such as the selected user ID and generated row IDs while preserving the audit set required by the plan.

This load-plan-recheck shape keeps the application-owned command atomic without callbacks or a generic transaction API. A stale snapshot returns a typed conflict with no writes; the application does not let the adapter reinterpret the step. `WorkflowExecutionResult` is refreshed data only and cannot carry behavior, executors, functions, or registrations. `WorkflowUnitOfWork` is a use-case-specific persistence boundary, not an extension point. Adding a step type always requires changing the closed domain enum, definition validation, the application `WorkflowRunner` switch, and any required fixed plan fields; it is never accomplished by registration.

`TicketUnitOfWork.Update` remains the manual ticket mutation boundary. `CreateTicketWithRun` replaces `TicketUnitOfWork.Create` only in `TicketService.Create`, because every new ticket must now pin and create a run atomically. `TicketService` asks `WorkflowRunner` to plan initial automatic advancement before submitting the concrete creation plan; the UoW rechecks that the planned version remains current before writing.

## Typed domain model

`internal/domain/workflow.go` defines closed string enums and data structs, not step interfaces:

```go
type StepType string
const (
    StepAssignToDesk StepType = "assign_to_desk"
    StepForm         StepType = "form"
    StepManualTask   StepType = "manual_task"
    StepResolve      StepType = "resolve_ticket"
    StepClose        StepType = "close_ticket"
)

type WorkflowDefinition []WorkflowStep

type WorkflowStep struct {
    Type         StepType          `json:"type"`
    AssignToDesk *AssignToDeskStep `json:"assign_to_desk,omitempty"`
    Form         *FormStep         `json:"form,omitempty"`
    ManualTask   *ManualTaskStep   `json:"manual_task,omitempty"`
}

type AssignToDeskStep struct {
    DeskID   int64              `json:"desk_id"`
    Strategy AssignmentStrategy `json:"strategy"`
}
type FormStep struct {
    Actor  FormActor   `json:"actor"`
    Fields []FormField `json:"fields"`
}
type ManualTaskStep struct { Instructions string `json:"instructions"` }
type FormField struct {
    Key      string    `json:"key"`
    Label    string    `json:"label"`
    Kind     FieldKind `json:"kind"`
    Required bool      `json:"required"`
    Options  []string  `json:"options,omitempty"`
}
```

Terminal steps have no config pointer. A non-terminal step must have exactly its matching config pointer and no others. Published JSON therefore remains readable and rejects contradictory configuration without introducing polymorphic executors.

### Definition validation and canonicalization

Domain definition validation returns ordered `WorkflowValidationIssue{Step, Field, Message}` values so the builder can render every issue at the relevant step.

1. Trim definition text before validation and canonical encoding.
2. Require at least one published step.
3. Accept only the five step types and exactly one matching config shape.
4. Use the JSON array offset as `step_index`; array order makes duplicate or gapped published positions unrepresentable. HTTP draft decoding also rejects missing or duplicate submitted numeric positions before constructing the array. A normalized `workflow_steps` table solely to index a redundant position is rejected.
5. Permit at most one terminal step; `resolve_ticket` and `close_ticket` are mutually exclusive and final.
6. Require `assign_to_desk.desk_id > 0` and strategy `claim` or `least_loaded`. `WorkflowService.Publish` additionally verifies the persisted desk inside the publish transaction.
7. Require `form.actor` to be `requester` or `assignee`, at least one field, non-empty field keys/labels, and field keys unique across the entire workflow after trimming.
8. Accept only `short_text`, `long_text`, `checkbox`, or `single_select`. Single-select options must contain at least two non-empty values and be unique after trimming. Options on other field kinds are rejected.
9. Require non-empty manual-task instructions. This domain rule IS the publish-time gate (proposal default D13, confirmed): because rule 14 removes the generic pending message there is deliberately NO runtime fallback copy, so publishing MUST reject an empty/whitespace `manual_task.instructions` — `validateManual` returns the ordered issue `{Step: N, Field: "instructions", Message: "Step N: instructions are required"}`, `WorkflowService.Publish` persists no version while any issue exists, and the builder surfaces it inline (422 in both HTMX-fragment and full-page modes). `recheckSnapshot` re-validates every persisted definition on each apply as defense in depth, so a published version always carries renderable pinned instructions.
10. Reject unknown JSON fields when a snapshot is decoded. Database JSON checks are defense in depth; domain definition validation remains authoritative.

Canonical JSON is produced only by `encoding/json` from the normalized typed model. Draft/published equality is byte equality over these canonical bytes. Republishing identical bytes still creates a new version.

### Positional form-answer decoding

The HTTP form names answers only by zero-based pinned position: `answer_0`, `answer_1`, and so on. It never accepts caller-supplied field keys or types. The HTTP decoder preserves submitted positions and raw occurrences in `RawPositionalValues`; `WorkflowRunner` derives each field key, kind, requirement, and option set from the ticket's pinned workflow snapshot.

Before building a mutation plan, the runner rejects malformed `answer_*` names, negative or non-numeric positions, duplicate positions, more than one raw value for a position, and any position outside the pinned field count. Missing positions are interpreted only according to their pinned field definition; callers cannot append an unknown answer or alter a type.

Canonical input mapping is exact:

| Pinned field kind | Raw mapping | Required rule | Persisted JSON scalar |
| --- | --- | --- | --- |
| `checkbox` | Absent or empty is `false`; `on` or `true` is `true`; every other non-empty value is rejected | Required checkbox must decode to `true` | JSON boolean |
| `short_text` or `long_text` | Use the single submitted string after trimming surrounding whitespace; absent is treated as blank | Required text rejects blank after trimming | JSON string |
| `single_select` | Empty or absent is empty; a non-empty value must exactly equal one canonical option from the pinned snapshot | Required select rejects empty | JSON string |

One value is persisted for every pinned field, including `false` or an empty optional string. `answers_json` is a positional JSON array of typed scalar values, for example `["api-01", true, "eu-west-1"]`. It is never a string-only answer object. Rendering zips that array with the pinned form fields to recover trusted labels, keys, types, and order. Application validation must succeed and the UoW must recheck the same pinned snapshot and cursor before the typed array is written.

## SQLite migration `0006_category_workflows.sql`

The migration is additive, is recorded by the existing immediate migration transaction, and performs no data backfill.

```sql
CREATE TABLE workflow_versions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  category_id INTEGER NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
  version_no INTEGER NOT NULL CHECK(version_no > 0),
  steps_json TEXT NOT NULL
    CHECK(json_valid(steps_json) AND json_type(steps_json) = 'array'
          AND json_array_length(steps_json) > 0),
  published_by_user_id INTEGER REFERENCES users(id),
  published_at TEXT NOT NULL,
  UNIQUE(category_id, version_no),
  UNIQUE(category_id, id)
);

CREATE TABLE category_workflows (
  category_id INTEGER PRIMARY KEY REFERENCES categories(id) ON DELETE CASCADE,
  draft_json TEXT NOT NULL DEFAULT '[]'
    CHECK(json_valid(draft_json) AND json_type(draft_json) = 'array'),
  current_version_id INTEGER,
  FOREIGN KEY(category_id, current_version_id)
    REFERENCES workflow_versions(category_id, id)
);

ALTER TABLE tickets ADD COLUMN workflow_version_id INTEGER
  REFERENCES workflow_versions(id);
CREATE INDEX idx_tickets_workflow_version ON tickets(workflow_version_id);

CREATE TABLE ticket_workflow_runs (
  ticket_id INTEGER PRIMARY KEY REFERENCES tickets(id) ON DELETE CASCADE,
  current_step_index INTEGER NOT NULL DEFAULT 0 CHECK(current_step_index >= 0),
  status TEXT NOT NULL CHECK(status IN ('active', 'completed')),
  started_at TEXT NOT NULL,
  completed_at TEXT,
  CHECK((status = 'active' AND completed_at IS NULL) OR
        (status = 'completed' AND completed_at IS NOT NULL))
);

CREATE TABLE ticket_form_answers (
  ticket_id INTEGER NOT NULL REFERENCES ticket_workflow_runs(ticket_id) ON DELETE CASCADE,
  step_index INTEGER NOT NULL CHECK(step_index >= 0),
  answers_json TEXT NOT NULL
    CHECK(json_valid(answers_json) AND json_type(answers_json) = 'array'),
  submitted_by_user_id INTEGER NOT NULL REFERENCES users(id),
  submitted_at TEXT NOT NULL,
  PRIMARY KEY(ticket_id, step_index)
);

CREATE TRIGGER trg_workflow_versions_immutable_update
BEFORE UPDATE ON workflow_versions
BEGIN
  SELECT RAISE(ABORT, 'published workflow versions are immutable');
END;
```

Rationale:

- The draft is one canonical document because the builder edits one linear list.
- A version row is the immutable snapshot; tickets reference it rather than copy it.
- The run is only a cursor. There are no task-instance rows or per-step status state machines.
- The completion route's `{position}` is the one-based pinned step position. `WorkflowRunner` converts it to the zero-based cursor before planning; stale or mismatched positions return a typed conflict mapped to HTTP 422 with no writes. No synthetic task ID table is needed.
- One answer row represents one completed form. Its typed JSON array is positional and is interpreted only against the pinned snapshot. There is deliberately no visibility column: every workflow answer is public to authorized ticket readers, which makes assignee-answer requester visibility structural rather than optional.
- The composite `category_workflows(category_id, current_version_id)` foreign key intentionally uses SQLite's default `NO ACTION`. Version deletion has no public API, and pinned tickets also prevent deleting referenced versions. Publishing changes the current pointer only within its single `BEGIN IMMEDIATE` transaction.
- Published rows have no update API and an update trigger. Deleting a category may cascade its unpinned workflow data; existing ticket/category foreign keys continue to protect referenced data.
- `tickets_fts` and its triggers are untouched, so answers cannot enter FTS and no reindex occurs.

Existing categories receive no `category_workflows` row. Existing tickets retain a NULL pin and remain readable without a run. An authorized builder GET reads the optional row and, when absent, renders `[]` in memory without executing an insert or update.

## SQLite migration `0008_audit_event_step_index.sql`

Migration 0008 is additive and performs no data backfill:

```sql
ALTER TABLE audit_events ADD COLUMN step_index INTEGER;
```

The column is nullable. Only semantic category-flow completion and assignment audits persist the sealed zero-based step index of the pinned step that produced them; state-transition audits, non-flow audits, legacy rows, and events produced outside a pinned run remain NULL. `AuditEvent` gains a matching `StepIndex *int`. The view layer joins a semantic event to its pinned step definition and stored form answers only through this exact persisted index — never by timestamp or ordering inference. Corrupt or missing context degrades rendering to the event's safe summary alone; nothing is fabricated. Legacy `workflow_step` rows with NULL context continue to read as `Completed step`.

## SQLite migration `0009_ticket_manual_solutions.sql`

Amendment 2 storage decision — where an optional manual-task solution lives. Alternatives compared:

| Alternative | Verdict |
| --- | --- |
| Dedicated workflow-task record table keyed `(ticket_id, step_index)` | **Chosen** — same record family as `ticket_form_answers`, exact cardinality (at most one solution per completed manual step), scalar TEXT needs no JSON shape, additive forward-only DDL, trivially absent for legacy rows (safe degradation) |
| Reuse `ticket_form_answers.answers_json` | Rejected — its CHECK enforces a JSON array and the read path fail-closes against pinned FORM field definitions (count/kind/options); a free-text solution is semantically not a form answer, and relaxing the CHECK would rewrite an already-applied table, violating forward-only immutability of `0006` |
| `solution` column on `ticket_workflow_runs` | Rejected — wrong cardinality (one run completes several manual steps); breaks the cursor-only run design |
| Solution in `audit_events.note/reason`, comments, or FTS | Rejected — spec-forbidden; attribution stays with the existing completion event |

The migration is additive and performs no data backfill (existing manual completions simply have no solution row):

```sql
CREATE TABLE ticket_manual_solutions (
  ticket_id INTEGER NOT NULL REFERENCES ticket_workflow_runs(ticket_id) ON DELETE CASCADE,
  step_index INTEGER NOT NULL CHECK(step_index >= 0),
  solution TEXT NOT NULL CHECK(length(solution) <= 2000),
  created_by_user_id INTEGER NOT NULL REFERENCES users(id),
  created_at TEXT NOT NULL,
  PRIMARY KEY(ticket_id, step_index)
);
```

- **Write path (same UoW unit).** `CompleteWorkflowCommand.Solution` (trimmed by the HTTP layer; whitespace-only → empty) reaches `WorkflowRunner.PlanComplete`, whose manual branch stamps it on the sealed `WorkflowStepOperation{StepIndex, Audit, Solution}` — the group grammar stays EXACTLY `[WorkflowStep]`. `applyWorkflowOperations` inserts into `ticket_manual_solutions` when `Solution != ""`, reusing the operation's audit actor-user-id/created-at facts so completion, cursor, audit, and solution commit or roll back together; a form-step op carrying a non-empty solution is a plan contradiction rejected before any write.
- **Transport bound.** The handler validates the trimmed value at ≤ 2,000 characters with a typed `ValidationError{Field: "solution"}` (422) BEFORE planning; the SQLite `CHECK(length(solution) <= 2000)` mirrors the bound as defense in depth (`length()` counts characters for TEXT).
- **Read path (pinned-context seam only).** `WorkflowStepContext` gains `Solution string`; `workflowResponseStore.WorkflowStepContext` reads it ONLY in the manual branch by the exact persisted step index, joining the immutable pinned definition it already loads. A missing row (no solution submitted, or a legacy completion from before `0009`) yields an empty `Solution` — never a fabricated placeholder. No other code path queries this table.
- **Never anywhere else.** The solution is excluded from `audit_events.note/reason`, comments, and `tickets_fts` by construction (separate table, no triggers touch them); non-membership assertions are part of the slice's tests.

## Draft, publish, and pin flows

### Safe read and lazy persistence

```text
GET builder        -> authorize -> read optional row
                   -> if absent, render in-memory []
                   -> no INSERT, UPDATE, version, or Draft badge

POST preview       -> authorize -> decode/validate submitted draft in memory
                   -> render read-only ordered summary; no persistence

First mutating POST
(save/add/move/...) -> authorize -> canonicalize resulting submitted draft
                   -> BEGIN IMMEDIATE
                   -> INSERT category_workflows(category_id, draft_json)
                      VALUES(category, '[]') ON CONFLICT DO NOTHING
                   -> UPDATE draft_json to the canonical submitted draft
                   -> COMMIT atomically

First POST publish -> require valid non-empty submitted draft before publication
                   -> one BEGIN IMMEDIATE performs row upsert, draft persistence,
                      immutable version insert, and current-version switch
                   -> any validation/recheck failure rolls back every write
```

Every mutating builder action carries the complete submitted ordered draft, applies its closed action in the application, and persists the resulting canonical document. `preview` is intentionally read-only even though it uses POST to carry unsaved form state. Merely viewing the builder therefore never changes category-list state.

Where the proposal/specification says an empty draft is available when the builder is first opened, this design supplies that draft as an in-memory view model. Durable lazy creation occurs only when configuration is first mutated.

### Draft remains separate from active publication

```text
Read builder      -> optional persisted draft or in-memory []
Edit action       -> upsert/replace draft_json only
Preview           -> render submitted draft in memory; no write
Publish           -> validate submitted non-empty draft
                  -> persist draft + INSERT immutable version N
                  -> UPDATE current_version_id in the same transaction
New ticket        -> reads current_version_id; never reads draft_json
In-flight ticket  -> reads tickets.workflow_version_id; ignores later publishes
```

The category badge is derived, not stored:

| Data state | Managed-list badge | New-ticket availability |
| --- | --- | --- |
| No `category_workflows` row, including after GET-only viewing | none | unavailable |
| Persisted draft exists, no current version | `Draft` | unavailable |
| Persisted draft bytes equal current snapshot | `Published vN` | available |
| Persisted draft bytes differ from current snapshot | `Draft` | available through current vN |

Publish uses exactly one `BEGIN IMMEDIATE`: validate the submitted definition, insert the row when absent with `ON CONFLICT DO NOTHING`, persist canonical `draft_json`, recheck every referenced desk, allocate `MAX(version_no)+1` for that category, insert the immutable version, and switch `current_version_id`. Failure changes neither draft, version history, nor the active pointer. The composite current-version foreign key keeps its default `NO ACTION`; there is no public version-delete operation.

Ticket creation uses one `BEGIN IMMEDIATE`: re-read the persisted category and current version, recheck any optional active agent-plus assignee and every execution-plan precondition, insert the ticket with `workflow_version_id`, append the created audit, insert its run, and apply the application-planned initial automatic advancement. An automatic failure rolls the entire creation back, preserving the create/pin/run all-or-nothing contract.

`GET /tickets/new` uses `WorkflowStore.ListAvailableCategories`; ticket list filters continue using all categories so legacy and historical tickets remain filterable. `POST /tickets` repeats availability validation in the creation transaction and returns the exact 422 category message when no published version exists.

## Linear execution

`WorkflowRunner` owns the finite loop and closed step switch. It first checks the one-based `ExpectedPosition`, converts it to a zero-based cursor, and rejects a stale or mismatched value as `ErrWorkflowPositionConflict`. It then walks the pinned snapshot to construct the complete plan for the submitted human action and all immediately following automatic steps:

```text
application: load persisted execution snapshot
application: require active run and ExpectedPosition-1 == current cursor
application: authorize and validate the submitted current human step
application: advance planned cursor
application: while planned cursor < len(pinned steps):
application:     switch pinned step type:
application:         claim/form/manual_task -> stop with one pending action
application:         least_loaded           -> add fixed assignment mutation; advance
application:         resolve_ticket         -> run domain transition matrix; complete
application:         close_ticket           -> run domain transition matrix; complete
application: cursor == len(steps) -> complete run without changing ticket state
application: submit one concrete plan
sqlite:      BEGIN IMMEDIATE -> reload/recheck -> fixed query/writes/CAS/audits -> COMMIT
```

The switch is application code, never SQLite code. Terminal matrix orchestration is application code and invokes `Ticket.Transition`; the adapter receives already-decided transition results and exact expected before-state. SQLite may reject stale state but may not choose another transition. Likewise, application code decides that a pinned `least_loaded` step requires assignment; SQLite only performs the specified deterministic least-loaded query under the write lock.

A request transaction includes its human completion and all immediately following automatic steps. This prevents automatic steps from being stranded without introducing a scheduler. If an automatic assignment has no eligible persisted member, the plan application fails without answers, cursor, assignment, state, or audit changes; after membership is corrected, the user retries the same current position. The loop is finite because definitions are linear and immutable.

### Exact atomic mutation units

| Trigger | One committed unit |
| --- | --- |
| Ticket create | Ticket row + current version pin + created audit + active run, plus application-planned initial automatic advancement |
| `claim` or `least_loaded` | Persisted eligibility/selection + person assignment + assignment audit when the person changes + `new→in_progress` via an application-planned `Ticket.Transition` and its audit when state is `new` + cursor; an already `in_progress` ticket receives no redundant transition |
| Form or manual step | Persisted snapshot/actor precondition recheck + typed public answer array for a form + `workflow_step` audit + cursor; for a manual step, additionally the optional solution row in `ticket_manual_solutions` when the same completion carried a non-empty trimmed solution (same actor id/timestamp facts as the audit, one transaction, see migration `0009`) |
| `resolve_ticket` | From `new` or `in_progress`, application calls `Ticket.Transition(resolved)` and plans workflow audit + completed run; from `resolved` or `closed`, completed run with no false transition; from `cancelled`, reject with no writes |
| `close_ticket` | From `new` or `in_progress`, application calls `Transition(resolved)` then `Transition(closed)` and plans two ordered workflow audits + completed run; from `resolved`, close + one audit; from `closed`, completed run/no audit; from `cancelled`, reject with no writes |

A pending claim performs no mutation and leaves `new` unchanged. A same-person assignment does not create a false field-change audit; if the ticket is still `new`, the workflow consequence may still create the required state transition audit. Workflow claims are reasonless, including a person-to-person claim; the existing non-empty reason requirement remains exclusive to generic manual reassignment through `POST /tickets/{id}/assign` and `TicketService.Assign`.

### Concurrency and least-loaded determinism

Every runtime mutation starts `BEGIN IMMEDIATE` before the UoW reloads the run, membership, load, ticket, or pinned version. The current modernc DSN serializes writers. Two claimers may render the same action, but the first commit advances the cursor; the second plan fails its expected-position/cursor recheck and receives `ErrWorkflowPositionConflict`, mapped to 422, with no writes.

`least_loaded` executes the fixed query selected by the application's plan inside that same transaction:

```sql
SELECT u.id
FROM desk_members dm
JOIN users u ON u.id = dm.user_id
LEFT JOIN tickets t
  ON t.user_id = u.id AND t.state IN ('new', 'in_progress')
WHERE dm.desk_id = ?
  AND u.active = 1
  AND u.role IN ('agent', 'admin', 'root')
GROUP BY u.id
ORDER BY COUNT(t.id) ASC, u.id ASC
LIMIT 1;
```

There is no category predicate. Resolved, closed, and cancelled tickets do not count. Lower user ID is the stable tie-break. Because selection and assignment share the immediate transaction, a concurrent assignment observes the first committed load before selecting.

## Authorization and read scope

The application makes completion decisions from persisted snapshots, never from submitted actor IDs, roles, assignees, desk IDs, step types, field keys, or owners. Only the authenticated session user ID, expected step position, raw positional form values, and optional manual-task solution enter `CompleteWorkflowCommand`; workflow claims have no reason field. The UoW reloads and compares every persisted fact used by the application before applying the plan; this is a stale-state guard, not adapter-owned policy selection.

| Operation | Application decision and SQLite recheck |
| --- | --- |
| Builder GET | Session actor has `CapManageCategories` before optional draft read; no write occurs |
| Builder mutation/preview/publish | Session actor has `CapManageCategories`; mutation/publish rechecks before writing; preview remains in memory |
| Requester form | `tickets.requester_user_id = actor.id` |
| Assignee form/manual step | `tickets.user_id = actor.id` |
| Claim | Actor is active `agent`, `admin`, or `root` and `desk_members(desk_id, actor.id)` exists; claimant is always that actor |
| Least-loaded | Application selects the closed strategy from the pinned step; SQLite query returns an active agent-plus current desk member |
| Admin/root recovery | Existing audited `TicketService.Assign` self-reassignment first; no completion impersonation path |

Agents need to read tickets currently waiting for one of their desk claims. Add `ScopeAssignedOrClaimable` for list/detail reads: assigned to the actor **or** active run at pinned `assign_to_desk[claim]` whose desk contains the actor. The SQLite scope clause uses the run cursor, `json_extract` on the pinned immutable snapshot, and `desk_members`. Mutation helpers for edit, comment, transition, and generic assignment retain their existing stricter assigned/assignable scopes, so claim visibility does not grant unrelated ticket edits. Templates derive controls from the same persisted checks but never replace them.

Non-terminal completion rejects `resolved`, `closed`, or `cancelled` before a plan is applied. Terminal steps alone use the explicit application-owned matrices. Legacy NULL-pin tickets follow existing scope and lifecycle behavior.

## Audit, answers, and timeline

Keep `domain.ActionWorkflowStep = "workflow_step"` only as a readable legacy fallback. New completions use the closed semantic actions `workflow_requester_form`, `workflow_assignee_form`, and `workflow_manual_task`; no answer, instruction, or solution content is copied into an audit note.

| Event | Persisted actor | Timeline summary |
| --- | --- | --- |
| Claim assignment | Authenticated claimant + actor user ID; sealed `step_index` | Existing `Assigned to <person>` |
| Least-loaded assignment | `workflow`, NULL actor user ID; sealed `step_index` | Existing assignment summary |
| Assignment-triggered state transition | `workflow`, NULL | Existing state summary |
| Requester form completion | Authenticated requester + ID; sealed `step_index` | `Submitted request details` plus the inline pinned label/value list |
| Assignee form completion | Authenticated assignee + ID; sealed `step_index` | `Submitted work details` plus the inline pinned label/value list |
| Manual completion | Current assignee + ID; sealed `step_index` | `Completed task` plus the contextual pinned instruction and, when a non-empty solution was stored at that index, the assignee's solution |
| Resolve/close transition | `workflow`, NULL; no step index | Existing state summary; close from open produces two entries |

Semantic completion and assignment audits persist their sealed zero-based `StepIndex` (`audit_events.step_index`, migration 0008); transition and non-flow audits keep it NULL. The view joins each semantic event to its pinned definition or stored answers only by that exact index — never by timestamp or order inference.

Completed forms remain stored in `ticket_form_answers`, decoded as typed positional arrays, and zipped against the pinned field definitions inside their own audit `TimelineItem` as an escaped inline definition list. A manual completion item renders its contextual pinned instruction plus, when present, the solution joined through the SAME enrichment pass: `WorkflowStepContextStore.WorkflowStepContext` resolves the manual context by the event's persisted sealed step index, now returning the stored `Solution`; the view builder copies it to the new `TimelineItem.StepSolution` field alongside `StepInstruction`. The template renders `StepSolution` inside the event ONLY when non-empty, as escaped plain body text attributed/timestamped by the existing completion event; an empty value renders the instruction alone with no placeholder. Legacy completions without a solution row degrade identically. There is no separate `Workflow responses` card: the single newest-first timeline contains comments, assignments, every completed category-flow step, and state transitions. Corrupt or missing context degrades to the safe summary alone with nothing fabricated; a legacy `workflow_step` row with NULL context remains `Completed step`. The persisted automatic actor value may remain `workflow`, but templates omit actor text for automatic events instead of rendering a `Workflow` label, and no ticket-facing copy mentions `workflow`; human events keep their attributed actor names. Answers never enter audit notes or FTS.

## HTTP and UI

### Category builder

Routes:

- `GET /categories/{id}/workflow`
- `POST /categories/{id}/workflow` with a closed `action` value: `save`, `add_step`, `change_type`, `add_field`, `remove_field`, `move_up`, `move_down`, `remove_step`, `preview`, or `publish`

Both routes require `CapManageCategories` before workflow reads. GET reads only and renders an in-memory empty draft when storage has no row. POST carries the complete ordered submitted draft. Mutating actions persist the canonical resulting draft using `INSERT ... ON CONFLICT DO NOTHING` plus update in one immediate transaction; preview renders submitted state without persistence; publish accepts only a valid non-empty submitted draft and persists the draft, version, and current pointer atomically.

HTMX swaps `#workflow-builder` with `workflow_builder`; ordinary requests render the full `category_workflow` page or return 303 after a successful save/publish. Validation failures render the same inline errors and submitted values with 422 in either mode.

The UI remains the existing tkt visual language. Its only new signature is the operational numbered checklist:

```text
Category / Workflow                         [Preview] [Publish]
Published v3 remains active while editing

1. Request information                      [Down] [Remove]
   Actor: Requester
   Field: Server name / Short text / Required

2. Assign to desk                           [Up] [Down] [Remove]
   Desk: Infrastructure   Strategy: Least loaded

+ Add step
```

- Semantic `<ol>`/`<li>` numbering, never a canvas.
- Type selector reveals only contextual fields.
- Real buttons move up/down; no drag-only interaction. After an HTMX move, the response places focus on the moved step's reorder control and announces the new position through an `aria-live` status.
- Terminal rows explain that they run automatically and must be final.
- Preview is a read-only ordered summary of submitted draft state, not a write, version browser, or publication.
- Plain errors use `role="alert"` and labels such as `Step 2: choose a desk`.
- Category index adds `Configure workflow` and the derived badge. GET-only viewing does not create a `Draft` badge. No top-level navigation item is added.

### Create handler binds no assignee and rejects assignee-carrying requests

- `web/templates/partials/ticket_form.html` renders NO assignee control for ANY role: the `{{if .CanAssign}}` “Assigned user” selector block is removed from the CREATE form, together with the `ticketFormData.CanAssign` flag and its `CapAssignTicket` computation in `newForm`/`renderCreateError` (`internal/adapters/http/handlers_tickets.go`). Detail-page assignment keeps its own separate plumbing — the audited `TicketService.Assign` self-reassign path of rule 6 is preserved untouched.
- `create` treats the mere PRESENCE of a `user_id` or `assignee_id` parameter — any value, including empty, which is exactly what a stale cached form submits — as `&domain.ValidationError{Field: "assignee", Message: "tickets are created unassigned — assignment happens later through the category flow"}`, answered 422 through the existing `renderCreateError` path. No silent drop, no normalization: no ticket, pin, run, or audit results from a rejected creation request.
- `CreateTicketInput.UserID` is REMOVED (`internal/application/ticket_service.go`) so creation stamps an empty assignee structurally — no application path can smuggle a creation-time assignee. Test harnesses that seeded assigned tickets via `Create` seed through the audited `Assign` service call instead.
- Touched files: `internal/adapters/http/handlers_tickets.go`, `internal/application/ticket_service.go`, `web/templates/partials/ticket_form.html`, plus `handlers_tickets_test.go`, `harness_test.go`, `golden_test.go` and the `tickets_new.golden`/`ticket_form.golden` fixtures; handler tests assert 422 + zero writes for assignee-carrying posts from every role.

### Ticket detail

- `POST /tickets/{id}/workflow/steps/{position}/complete` is the only completion route. `{position}` is one-based externally and maps to the zero-based pinned cursor internally; it is not a task-instance ID.
- A stale, missing, non-positive, or mismatched position returns typed `ErrWorkflowPositionConflict`, mapped to 422, and persists nothing.
- The `Pending Actions` card appears above the timeline for the current step. It renders a control only when the persisted actor predicate passes.
- Pending presentation (Amendment 2): the card drops its `<ol>` numbering entirely, and the generic `Mark the current task as complete.` copy is removed everywhere ticket-facing. A pending manual task leads with a short neutral lead-in and the step's immutable pinned INSTRUCTION, read verbatim from the execution snapshot already loaded by `pendingFor` (the same pinned definition the runner plans against — never the live draft) and rendered escaped; below it sits the optional `solution` textarea labeled `Solution (optional)` and the unchanged role-gated submit button. Other step kinds keep their contextual pinned fields/actions; unauthorized viewers still see no actionable control. GET rendering remains strictly read-only — every mutation stays on the POST completion route.
- Claim posts neither assignee ID nor reason; the authenticated actor is the only possible claimant.
- Forms post `answer_<zero-based-position>` raw values. Unknown, duplicate, or extra answer positions and ambiguous repeated values are rejected before any write.
- Manual step completion posts an OPTIONAL plain-text `solution` field through the same completion event (Amendment 2): trimmed by the handler, whitespace-only means none, bounded at 2,000 characters post-trim with a typed 422 above the bound. Forged `answer_*`, `assignee_id`, or `user_id` keys remain rejected before any planning; a completion without a non-empty solution stores nothing and renders no solution block.
- Least-loaded and terminal steps render no button because the application includes them in synchronous advancement when reached. Automatic pending-step explanatory copy uses neutral wording and never mentions `workflow`.
- HTMX success returns `ticket_detail` for `#ticket-detail` `outerHTML`; non-HTMX success returns 303 to ticket detail. Errors re-render the same full page or fragment.
- Completed steps render inside the single merged activity timeline (form answers inline under their own event; manual instructions and non-empty solutions inline); the standalone responses partial include is removed from the detail page. No requester-facing workflow version, pin, historic versions, routing controls, or technical cursor is shown. The visual change follows the APPROVED visual contract `design/category-workflow-refinements.op` (+ PNG preview), not a broad restyle:
  - semantic `dl.workflow-responses`: each label/value pair grouped, separated by hairlines (`#DDE2EA` 1px);
  - fixed-width muted dt (104px desktop, 13px `#7A8391`) whose long dd values wrap (`13px/1.45 #252B34`) inside the entry without overflowing it;
  - single-column stacking below 640px (dt above dd) with no horizontal overflow at 390px;
  - plainly visible keyboard focus (2px `#315EFF` outline, 2px offset) and preserved contrast;
  - every label/value/solution/instruction escaped plain text — never interpreted HTML;
  - a manual solution renders as ordinary event body text, newest-first ordering and comments-before-events ties unchanged.

The specifications' generic “task completion” wording still describes the user-visible pending work. Route and identifier names are a design-level concern and intentionally use `steps/{position}` because no persisted task instance exists.

## Strict TDD and verification

`strict_tdd: true` remains mandatory. Each delivery slice follows RED → GREEN → TRIANGULATE → REFACTOR before moving outward:

1. Domain table tests cover the closed five types, canonical definition JSON, terminal exclusivity/order, field/option rules, and every ticket transition used by the runner. Domain tests do not own advancement orchestration.
2. Application `WorkflowService` tests prove GET-only access does not save or create a row, mutating actions perform the first lazy upsert, preview remains read-only, publish requires a valid non-empty submitted draft, and published availability remains active while editing.
3. Application `WorkflowRunner` fake tests cover the closed loop/switch, actor decisions, terminal matrices through `Ticket.Transition`, `ExpectedPosition` one-to-zero conversion, typed stale-position conflict, no impersonation, and the exact positional answer matrix: checkbox absent/empty/`on`/`true`/invalid, required checkbox, trimmed required text, pinned select options, unknown/duplicate/extra positions, and typed JSON output.
4. Real modernc SQLite tests cover migration/no backfill, safe optional draft reads, first-mutation upsert, immutable versions, default-`NO ACTION` composite current-version FK, one-transaction publish switch, atomic create/run and plan application, rollback on every injected failure, persisted snapshot rechecks, claim races, global least-loaded counts/tie-break, cursor CAS, terminal audit order, typed answer arrays/visibility, and FTS exclusion. Tests assert SQLite applies application plans and never dispatches by step type.
5. `httptest` handler tests precede golden regeneration: safe builder GET and no badge side effect, builder HTMX/full-page parity, keyboard reorder markup/focus target, invalid first publish with no row/version, 422 messages, hidden categories, exact `/workflow/steps/{position}/complete` routing, forged completion denial, stale position with no writes, raw positional answer rejection, and 303/outerHTML behavior.
6. Final gates are `gofmt`, `go vet ./...`, `go test ./... -count=1 -race`, and `go build ./...`.

After lower layers pass, run isolated local Playwright MCP journeys against `go run ./cmd/server` with a unique temporary SQLite database and a free loopback-only port:

- admin GETs an unconfigured builder and verifies no `Draft` badge, then performs the first mutation, uses contextual fields and keyboard reorder, receives invalid-publish feedback, previews without an extra write, and publishes;
- requester creates a ticket, completes requester fields using checkbox/text/select canonical mappings, and remains pinned while a newer draft/version is published;
- eligible desk member claims, current assignee completes manual/assignee form, requester sees typed answers, and resolve is workflow-attributed;
- stale step-position submission makes no change, and least-loaded plus multi-desk offboarding ends directly in close with two ordered state audits.

Check desktop and mobile semantics, keyboard focus, visible errors, URLs, HTMX and ordinary form behavior, console/network failures, and horizontal overflow. Stop only the launched PID and delete the temp DB, sidecars, logs, and screenshots. Browser MCP absence is BLOCKED, never silently skipped or replaced by screenshots.

## Rejected alternatives

| Alternative | Rejection reason |
| --- | --- |
| Generic workflow engine, executor registry, or one interface per step | No current behavior needs extension; a closed application switch is smaller and reviewable |
| Callback or generic transaction API | Lets adapter or caller-selected behavior execute under the transaction and weakens the concrete use-case boundary |
| Adapter-owned step switch or terminal matrix | Moves business orchestration out of the application; SQLite must only recheck and apply a data-only plan |
| Normalized definition/step/config tables | Duplicates a linear JSON snapshot and exists mainly for hypothetical reporting/plugins |
| Task-instance row per step | Cursor plus immutable snapshot already proves pending/completed order; the route therefore names step position honestly |
| Graph/plugin/canvas/branch data | Contradicts the closed linear product model |
| Draft creation on GET | Violates safe-read semantics and creates a `Draft` badge merely by viewing |
| Draft/published blobs directly on category or ticket snapshot copies | Weakens version identity and pin integrity |
| String-only form answer objects | Lose pinned scalar types and make checkbox semantics ambiguous |
| Background scheduler/retry subsystem | No timers or asynchronous automation are required; synchronous bounded advancement is deterministic |
| Admin impersonation | Persisted requester/assignee checks plus audited self-reassignment provide recovery without identity forgery |
| Form answers in comments/audit/FTS | Breaks visibility and search boundaries |
| Reusing `ticket_form_answers` for manual solutions | Array-typed CHECK plus fail-closed positional validation against pinned FORM fields are semantically wrong for a scalar free-text solution; relaxing them rewrites an applied table and corrupts the forms projection |
| A `solution` column on `ticket_workflow_runs` | Wrong cardinality — one run completes several manual steps; breaks the cursor-only run design |
| Persisting solutions in `audit_events.note/reason` or comments | Spec-forbidden (audit-log delta); attribution stays with the existing completion event |
| Drag-only builder or version browser | Not required for the four journeys and increases accessibility/review cost |

## Risks and mitigations

| Risk | Mitigation |
| --- | --- |
| GET accidentally creates persistent draft state | Optional read returns an in-memory empty draft; tests assert no row and no badge until a mutating POST |
| Agent claim visibility accidentally grants edit authority | Separate `ScopeAssignedOrClaimable` read queries from strict mutation queries; test every forged endpoint |
| Application/adapter ownership blurs | Runner tests assert plan output from the closed switch; SQLite tests assert exact recheck/apply behavior with no step dispatch, callbacks, or registry |
| JSON position integrity appears weaker than a step table | Definition and answers use array offsets; command decoding rejects malformed, duplicate, and extra positions; cursor CAS uses the same index |
| Checkbox or select values become ambiguous strings | Apply the exact canonical mapping and persist bool/string scalars in a positional typed JSON array |
| Empty least-loaded desk blocks a preceding action | The immediate transaction rejects and rolls back the entire submitted plan; show a plain desk-membership error and retry after correction |
| Composite current-version reference surprises deletion behavior | Keep SQLite default `NO ACTION`, expose no version-delete API, and switch publication in one immediate transaction |
| Golden churn hides authorization bugs | Require service/runner/SQLite/handler assertions before updating only affected goldens |
| Migration makes all existing categories unavailable for new tickets | This is the approved fail-closed no-backfill behavior; configure and publish explicitly before relying on a category |
| Solution storage drifts outside its record family | The ONLY reader of `ticket_manual_solutions` is `workflowResponseStore.WorkflowStepContext`; runner/UoW tests prove solution+completion+audit+cursor commit or roll back as one unit, and non-membership assertions lock it out of `audit_events.note/reason`, comments, and `tickets_fts` |
| Oversized or pathological solution payloads | Transport validation bounds the trimmed value at 2,000 characters (typed 422 before planning); the SQLite CHECK mirrors the bound; goldens lock escaping |
| Legacy completions without solution rows render placeholders | Missing row degrades to instruction-alone rendering; a detail golden asserts no empty solution block appears |
| `0009` migration drift | Additive forward-only table creation inside the existing immediate migration transaction pattern; rollback drops it first in dependency order; never edits an already-applied migration |
| Create-handler rejection regresses into silent dropping | Presence-based check on `user_id`/`assignee_id` (any value incl. empty) returns typed 422 with zero writes; handler tests assert every role × parameter combination creates nothing |
| Total implementation exceeds 400 authored lines | Superseded: the maintainer accepted `exception-ok` for one direct final PR; keep coherent work-unit commits and honest measurement, never invent new architectural layers to match slices |

## Exact affected files and sequencing boundaries

These nine boundaries are delivery-workload slices, not new architecture, services, extension points, or task-instance layers. Each boundary must compile and test independently and remain below 400 authored changed lines; generated goldens are excluded from the authored-line forecast.

| Boundary | Exact files | Purpose |
| --- | --- | --- |
| S1 — typed model and schema | `internal/domain/workflow.go` (new), `internal/domain/workflow_test.go` (new), `internal/domain/ticket.go`, `internal/domain/ticket_test.go`, `internal/domain/audit.go`, `internal/domain/errors.go`, `internal/adapters/sqlite/migrations/0006_category_workflows.sql` (new), `internal/adapters/sqlite/migration_0006_test.go` (new) | Closed definition types/validation, ticket transitions, pin field, workflow audit action, typed positional answer-array DDL, additive no-backfill DDL, default-`NO ACTION` FK behavior |
| S2 — definition persistence | `internal/application/ports.go`, `internal/adapters/sqlite/workflow_store.go` (new), `internal/adapters/sqlite/workflow_store_test.go` (new), `internal/adapters/sqlite/sqlite.go`, `internal/adapters/sqlite/category_store_test.go` | Optional safe draft reads, first-mutation upsert, atomic publish/switch, summaries, badges, available categories |
| S3 — definition use cases | `internal/application/workflow_service.go` (new), `internal/application/workflow_service_test.go` (new), `internal/application/fakes_test.go`, `internal/application/policy_test.go` | Capability-first GET without persistence, mutating draft actions, read-only preview, valid non-empty first publish, plain issues |
| S4 — application execution planner | `internal/application/workflow_runner.go` (new), `internal/application/workflow_runner_test.go` (new), `internal/application/ports.go`, `internal/application/fakes_test.go`, `internal/application/views.go`, `internal/application/views_test.go` | Closed loop/switch, actor decisions, `ExpectedPosition`, raw positional decoding, typed answer plan, terminal matrices via domain transitions, data-only results |
| S5 — create, pin, and atomic plan application | `internal/application/ticket_service.go`, `internal/application/ticket_service_test.go`, `internal/adapters/sqlite/ticket_store.go`, `internal/adapters/sqlite/ticket_store_test.go`, `internal/adapters/sqlite/workflow_uow.go` (new), `internal/adapters/sqlite/workflow_uow_create_test.go` (new), `internal/adapters/sqlite/workflow_uow_actor_test.go` (new), `internal/application/fakes_test.go` | Atomic current-version observation, create audit, pin/run, persisted snapshot recheck, fixed plan writes/CAS/audits, initial automatic plan, actor/form completion |
| S6 — claim scope and routing | `internal/application/policy.go`, `internal/application/policy_test.go`, `internal/application/ticket_service.go`, `internal/application/comment_service.go`, `internal/adapters/sqlite/filters.go`, `internal/adapters/sqlite/workflow_uow.go`, `internal/adapters/sqlite/workflow_uow_assignment_test.go` (new), `internal/adapters/sqlite/search_store_test.go` | Safe claim visibility, strict mutation scopes, assignment atomicity, deterministic least-loaded query, no adapter step dispatch |
| S7 — terminal persistence and timeline | `internal/adapters/sqlite/workflow_uow.go`, `internal/adapters/sqlite/workflow_uow_terminal_test.go` (new), `internal/application/workflow_runner.go`, `internal/application/workflow_runner_test.go`, `internal/application/views.go`, `internal/application/event_summary_internal_test.go`, `web/templates/partials/timeline.html` | Persist application-decided resolve/close plans, two-audit close, stale-state rejection, workflow actor label, workflow-step summaries |
| S8 — builder HTTP/UI | `internal/adapters/http/handlers_category_workflows.go` (new), `internal/adapters/http/handlers_category_workflows_test.go` (new), `internal/adapters/http/handlers_categories.go`, `web/templates/pages/category_workflow.html` (new), `web/templates/pages/categories_index.html`, `web/templates/partials/workflow_builder.html` (new), `web/templates/partials/styles.html`, `cmd/server/main.go` | Safe GET/in-memory empty draft, first mutating POST persistence, preview without write, publish, derived badges, vertical builder, HTMX/full-page parity, accessible reorder |
| S9 — ticket HTTP/UI and regression | `internal/adapters/http/handlers_tickets.go`, `internal/adapters/http/handlers_tickets_test.go`, `internal/adapters/http/handlers_detail_test.go`, `internal/adapters/http/harness_test.go`, `internal/adapters/http/golden_test.go`, `web/templates/partials/ticket_form.html`, `web/templates/partials/ticket_detail.html`, `web/templates/partials/workflow_pending.html` (new), `web/templates/partials/workflow_answers.html` (new), `web/templates/partials/styles.html`, `internal/adapters/http/testdata/categories_index.golden`, `internal/adapters/http/testdata/ticket_detail.golden`, `internal/adapters/http/testdata/ticket_form.golden`, `internal/adapters/http/testdata/tickets_new.golden`, `internal/adapters/http/testdata/tickets_show.golden`, `internal/adapters/http/testdata/timeline.golden` | Published-only create options, pending controls, typed answers, honest `/workflow/steps/{position}/complete` endpoint, `ExpectedPosition` 422 conflicts, regression fixtures/goldens |

If any boundary forecasts above 400 authored lines during task planning, split tests with their production seam rather than deferring tests or requesting a single oversized review. The original `ask-on-risk` decision was superseded by the maintainer's `exception-ok` waiver for one direct final PR. The nine slices do not justify nine new layers or public abstractions.

The S7/S9 outcome text above reflects the presentation as originally built (separate answers card, `Workflow` actor label). Work unit PR10 in `tasks.md` supersedes that presentation with step-indexed audit correlation, the merged timeline, inline answer/instruction rendering, and neutral automatic copy; its files and gates are authoritative for that follow-up.

### Amendment 2 work-unit slicing suggestion (for sdd-tasks)

Three coherent units, each compiling and testing independently, ordered persistence → presentation/handlers → styling/tests:

1. **WA — solution persistence**: `internal/adapters/sqlite/migrations/0009_ticket_manual_solutions.sql` (new), `internal/application/ports.go` (`CompleteWorkflowCommand.Solution`, `WorkflowStepOperation.Solution`, `WorkflowStepContext.Solution`), `internal/application/workflow_runner.go` (manual branch stamps the sealed op), `internal/adapters/sqlite/workflow_uow.go` (manual-group corroboration + conditional insert), `internal/adapters/sqlite/workflow_response_store.go` (context read by step index), `internal/application/views.go` (`TimelineItem.StepSolution` enrichment) — with runner/UoW/response-store/view tests including atomic-rollback and audit-note/FTS non-membership.
2. **WB — handlers/presentation**: create-contract change (`handlers_tickets.go` presence-based rejection, `ticket_service.go` input removal, `ticket_form.html` selector removal), `completeWorkflow` solution extraction with bound, `pendingFor` pinned-instruction lead, `web/templates/partials/workflow_pending.html` (numbering/generic copy removal, instruction lead, optional textarea) and `timeline.html` (solution-inside-event block) — handler/view tests plus affected goldens (`tickets_new`, `ticket_form`, `tickets_show`, `timeline`).
3. **WC — styling/regression**: `web/templates/partials/styles.html` approved tokens (hairline-separated dl pairs, fixed-width muted dt/wrapping dd, ≤640px single column, focus outline/contrast), full-page detail goldens, isolated Playwright journeys: create-without-assignee (incl. forged-parameter 422), pending manual card leading with pinned instruction, solution round-trip rendered escaped only when written, and completed-form readability at 390px.

## Rollout and rollback

This is a private non-production application. Roll forward in the listed boundaries, then configure/publish categories explicitly. Do not seed or backfill workflows. Opening every builder page is safe and does not configure categories; an explicit mutating POST followed by valid publication is required.

Before shared migration, rollback is a normal code revert. After local migrations are applied, the preferred single-developer rollback is to stop the server, remove the isolated development database, and rerun migrations. If data must be retained, add a forward inverse migration numbered after the current maximum (`0009_ticket_manual_solutions.sql` is newest): drop `ticket_manual_solutions`, then answers, runs, category workflow/version tables in dependency order, recreate `tickets` without `workflow_version_id` using the established SQLite table-copy pattern, and preserve all existing ticket/category rows. Never edit an already-applied migration. An older binary tolerates the additive tables/columns but ignores workflow enforcement, so it is acceptable only as a temporary private recovery step, not as a compatibility mode.

## Phase result contract

- `status`: completed
- `executive_summary`: Amendment 2 incorporated in place: manual-task solutions persist in a dedicated `ticket_manual_solutions` table (migration `0009`, forward-only, no backfill) written atomically in the same UoW unit as completion+cursor+audit and projected only through `WorkflowStepContextStore` → `TimelineItem.StepSolution`; create binds/rejects assignee-carrying requests with typed 422 and removes the selector for all roles; pending cards lead with the pinned instruction without numbering or generic copy; timeline events render solutions escaped only when non-empty with the approved `.op` treatment; D13 publish-time instruction validation documented at the domain `Validate()` gate; stale "no completion metadata" line corrected.
- `artifacts`: `openspec/changes/category-workflows/design.md`
- `next_recommended`: `sdd-tasks`
- `risks`: Solution atomicity/seam-drift requires focused UoW and view tests; presence-based assignee rejection must be asserted for every role; golden churn across pending/timeline/create surfaces; 2,000-char bound needs transport + CHECK mirror coverage.
- `skill_resolution`: none

## Amendment 3 — UI coherence and reasonless pinned-desk claim

### UI boundaries

- **Workflow configurator selects:** retain semantic native `<select>` controls, existing names/values, HTMX triggers, autosave behavior, keyboard operation, and visible focus. Apply only tkt tokens for border, surface, typography, option affordance, high-contrast focus, and narrow-width fit; no custom combobox, scripted replacement, or palette/typography reset.
- **Categories index:** use the supplied screenshot only to organize a semantic `Category`, `Created`, `Status`, `Actions` table. Keep current tkt tokens and simple admin workflow. The destructive action lives in an accessible native disclosure/overflow with an explicit label and keyboard/focus support. At narrow widths rows stack into labeled content without horizontal overflow.
- **Desks index:** use its supplied screenshot only to organize a restrained master/detail view: desk list with member counts, selected-desk detail, disclosed new-desk form, rename/delete, and add/remove member controls. Desktop can present list and detail together; narrow widths stack them. Existing CRUD/member routes and authorization remain unchanged.

### Claim command, projection, and transaction contract

The Assignment sidebar derives Desk and current Assignee from the pinned current claim step. It may render `Assign to me` only after the same persisted eligibility predicate used by the command: active role `agent|admin|root`, current membership in that pinned desk, active run, and matching cursor. Pending Actions renders no claim/reason form and the timeline never renders a claim input.

`POST /tickets/{id}/workflow/steps/{position}/complete` remains the only workflow-claim mutation route. Its workflow-claim branch accepts no `reason`, `assignee_id`, or caller-selected target; the command/operation plan has no reason field for any workflow claim, including true A→B reassignment. The UoW begins its existing immediate transaction and rechecks pinned version, run status/current cursor, ticket state, actor activity/role, and desk membership before writes. A stale cursor, removed membership, deactivated actor, or role loss returns its typed failure with zero writes. Competing valid claims serialize; the first advances the cursor and the later claimant receives the typed position conflict with zero writes.

On success, the projection writes one contextual assignment event exactly once: `Assigned to {person} · {desk}`. If the ticket is `new`, the existing state transition/audit remains part of the same transaction. The generic manual assignment endpoint/service keeps its reason validation and historical reason rendering unchanged.

### Amendment 3 verification boundary

Focused Go tests cover select markup/HTMX preservation, category table/disclosure semantics, desk CRUD/member interactions, eligible/nonmember claim visibility, server-side stale-membership denial, true A→B reasonless workflow claim, generic manual reassignment reason preservation, exact contextual assignment event, and first-wins concurrency. Isolated Playwright covers desktop and 390px categories/desks/selects/claim journeys, keyboard focus, overflow, console/network failures, and cleanup. Delivery selection is deliberately deferred until these implementation units are complete.

## Amendment 4 — Focused presentation corrections

Amendment 4 is a server-rendered presentation correction, not a new workflow capability. It preserves every existing mutation route, server-side authorization decision, and runtime state machine.

### Direct destructive actions

The category and desk index surfaces render their existing destructive POST forms as directly visible submit controls labelled exactly `Delete category` and `Delete desk`. The control remains a native submit button in its existing form: it neither delegates authority to JavaScript nor opens a replacement menu. Existing rejected-delete responses continue to re-render their inline errors in the same local management context. Responsive layout may reposition the form but must keep the button visible, keyboard reachable, and focus-visible at 390px.

### Current-task card

Only the CURRENT pending form/manual-task presentation is restructured as the supplied `Current task` card. The card is a narrow wrapper around the existing server-rendered pending form/manual content; it does not change field names, form action/method, required attributes, native select/checkbox semantics, validation rendering, or HTMX/full-page completion behavior. Its background token is exactly `var(--amber-soft)`, with no derived color and no blue surface. A manual task keeps pinned instructions, `Solution (optional)`, and its existing completion behavior. Completed historical events remain in the single merged timeline and retain ordering, attribution, and content semantics; a wrapper may be added only when needed to keep the surrounding activity visually coherent.

### Builder editor and read-only flow preview

The workflow builder retains its semantic `<ol>` editor, native selects, complete submitted draft, existing closed POST actions, autosave, add/reorder/remove behavior, focus restoration, live announcements, preview, and publish. Its information architecture becomes:

```text
Category / Workflow                         [Preview] [Publish]

[ordered editor: semantic <ol>]   [read-only vertical flow preview]
```

On desktop, the two panels share a responsive layout with the editor on the left and the preview on the right. On mobile, panels stack without horizontal overflow. The preview is derived solely from the submitted/server-rendered linear draft and has no independent state, input, mutation endpoint, drag-and-drop, graph/canvas interaction, or branching behavior. Restrained static connectors may clarify vertical sequence but are decorative only and must not imply editable nodes or alternate workflow paths. HTMX responses and full-page renders must expose equivalent editor and preview state.

### Amendment 4 verification boundary

Strict TDD remains mandatory: focused behavior tests must observe RED before each minimal correction, then GREEN, relevant alternate/error coverage, and refactoring only while focused tests stay green. Golden updates follow handler/template assertions and are rerun for stability. Isolated Playwright must exercise the corrected desktop and 390px journeys on a unique temporary SQLite database and loopback-only server, including keyboard focus, no horizontal overflow, inline rejected-delete errors, SSR/HTMX/full-page parity where applicable, and console/network checks. Amendment 3 D.3 and Amendment 2 WC.4 remain pending until these executable corrections and browser evidence are complete; delivery selection remains deferred.
