---
change: category-workflows
phase: design
status: completed
artifact_store: openspec
execution_mode: interactive
delivery_strategy: ask-on-risk
review_budget: 400
---

# Design: Linear Category Workflows

## Decision summary

Implement one category-owned editable JSON draft, immutable published JSON snapshots, one ticket pin, and one cursor per ticket. Builder reads are safe: `GET /categories/{id}/workflow` never writes, and a missing draft row is represented by an in-memory empty draft until the first mutating POST. Execution is planned by the application through one closed loop and one closed `switch` over five step types; there is no workflow engine, executor registry, graph, plugin API, callback transaction API, or per-step interface hierarchy.

The current SQLite adapter already uses `modernc.org/sqlite` (`internal/adapters/sqlite/sqlite.go` and `go.mod`). This design keeps it because the pure-Go driver already supplies the project's foreign-key, WAL, busy-timeout, immediate-transaction, FTS5, and JSON support. The driver choice in `openspec/config.yaml` is stale text, not an open decision.

Two design-level clarifications resolve ambiguous wording in the proposal and specifications without changing approved product behavior:

- “Begins configuration” means the first mutating builder POST, not a GET. An authorized GET may display an empty editable draft but creates no `category_workflows` row and no `Draft` badge merely by viewing.
- Specifications may use “task” generically for pending workflow work. Because the model has no task-instance identity, the completion route honestly identifies the pinned step position: `POST /tickets/{id}/workflow/steps/{position}/complete`.

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
    TicketID        int64
    ActorUserID     int64
    ExpectedPosition int // one-based route position
    Reason          string
    RawAnswers      RawPositionalValues
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
9. Require non-empty manual-task instructions.
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
| Form or manual step | Persisted snapshot/actor precondition recheck + typed public answer array for a form + `workflow_step` audit + cursor |
| `resolve_ticket` | From `new` or `in_progress`, application calls `Ticket.Transition(resolved)` and plans workflow audit + completed run; from `resolved` or `closed`, completed run with no false transition; from `cancelled`, reject with no writes |
| `close_ticket` | From `new` or `in_progress`, application calls `Transition(resolved)` then `Transition(closed)` and plans two ordered workflow audits + completed run; from `resolved`, close + one audit; from `closed`, completed run/no audit; from `cancelled`, reject with no writes |

A pending claim performs no mutation and leaves `new` unchanged. A same-person assignment does not create a false field-change audit; if the ticket is still `new`, the workflow consequence may still create the required state transition audit. Person-to-person reassignment retains the existing non-empty reason requirement before any mutation.

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

The application makes completion decisions from persisted snapshots, never from submitted actor IDs, roles, assignees, desk IDs, step types, field keys, or owners. Only the authenticated session user ID, expected step position, reason, and raw positional form values enter `CompleteWorkflowCommand`. The UoW reloads and compares every persisted fact used by the application before applying the plan; this is a stale-state guard, not adapter-owned policy selection.

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

Add `domain.ActionWorkflowStep = "workflow_step"` for form/manual completion. It records no answer content:

| Event | Actor | Timeline summary |
| --- | --- | --- |
| Claim assignment | Authenticated claimant + actor user ID | Existing `Assigned to <person>` |
| Least-loaded assignment | `workflow`, NULL actor user ID | Existing assignment summary |
| Assignment-triggered state transition | `workflow`, NULL | Existing state summary |
| Form completion | Authenticated requester/assignee + ID | `Submitted workflow form` |
| Manual completion | Current assignee + ID | `Completed workflow task` |
| Resolve/close transition | `workflow`, NULL | Existing state summary; close from open produces two entries |

`TimelineItem.ActorLabel` renders persisted actor `workflow` as `Workflow` without a user lookup or synthetic user. Completed forms are read from `ticket_form_answers`, decoded as typed positional arrays, and zipped against the pinned field definitions. They appear in pinned order in a read-only `Workflow responses` card for every actor who can read the ticket, including the requester for assignee submissions. They are not comments, audit notes, or search documents.

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

### Ticket detail

- `POST /tickets/{id}/workflow/steps/{position}/complete` is the only completion route. `{position}` is one-based externally and maps to the zero-based pinned cursor internally; it is not a task-instance ID.
- A stale, missing, non-positive, or mismatched position returns typed `ErrWorkflowPositionConflict`, mapped to 422, and persists nothing.
- The `Pending Actions` card appears above the timeline for the current step. It renders a control only when the persisted actor predicate passes.
- Claim posts no assignee ID; it may post only a reassignment reason.
- Forms post `answer_<zero-based-position>` raw values. Unknown, duplicate, or extra answer positions and ambiguous repeated values are rejected before any write.
- Manual step completion posts no completion metadata.
- Least-loaded and terminal steps render no button because the application includes them in synchronous advancement when reached.
- HTMX success returns `ticket_detail` for `#ticket-detail` `outerHTML`; non-HTMX success returns 303 to ticket detail. Errors re-render the same full page or fragment.
- Completed forms render read-only below Pending Actions. No requester-facing workflow version, pin, historic versions, routing controls, or technical cursor is shown.

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
| Total implementation exceeds 400 authored lines | Treat the nine boundaries below only as workload slices and apply `ask-on-risk` before implementation; never invent new architectural layers to match slices |

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

If any boundary forecasts above 400 authored lines during task planning, split tests with their production seam rather than deferring tests or requesting a single oversized review. `ask-on-risk` requires an explicit delivery decision before apply. The nine slices do not justify nine new layers or public abstractions.

## Rollout and rollback

This is a private non-production application. Roll forward in the listed boundaries, then configure/publish categories explicitly. Do not seed or backfill workflows. Opening every builder page is safe and does not configure categories; an explicit mutating POST followed by valid publication is required.

Before shared migration, rollback is a normal code revert. After local `0006`, the preferred single-developer rollback is to stop the server, remove the isolated development database, and rerun migrations. If data must be retained, add a forward `0007` inverse migration: drop answers, runs, category workflow/version tables in dependency order, recreate `tickets` without `workflow_version_id` using the established SQLite table-copy pattern, and preserve all existing ticket/category rows. Never edit an already-applied `0006`. An older binary tolerates the additive tables/column but ignores workflow enforcement, so it is acceptable only as a temporary private recovery step, not as a compatibility mode.

## Phase result contract

- `status`: completed
- `executive_summary`: Corrected design keeps builder GETs write-free with lazy persistence on the first mutation, places the closed advancement loop and business decisions in `WorkflowRunner`, limits SQLite to atomic recheck/query/write/CAS duties, defines exact positional typed answer decoding, and names completion by one-based step position.
- `artifacts`: `openspec/changes/category-workflows/design.md`
- `next_recommended`: `sdd-tasks`
- `risks`: Implementation exceeds one 400-line review budget; plan/UoW ownership, first-mutation draft persistence, positional answer decoding, and cursor-conflict handling require focused tests.
- `skill_resolution`: paths-injected
