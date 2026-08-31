# Proposal: resolved = awaiting requester confirmation

- **Change:** `resolved-requester-confirmation`
- **Governing issue:** GitHub #55 (status:approved, type:feature, area:tickets)
- **Status:** proposed
- **Inputs:** `explore.md` (same change directory), confirmed product decisions from the pre-proposal handoff.

## Why

Today `resolved` and `closed` are behaviorally interchangeable terminal-ish states, and nothing records how a ticket got closed:

- The domain transition matrix allows any authorized agent to manually move `resolved → closed` (`internal/domain/state.go:39`). There is no notion of who or what closed the ticket: `Transition` emits a single audit event with `Action: transition, Field: "state"` and no attribution (`internal/domain/ticket.go:76-87`), and no closed-via concept exists anywhere — a grep for `ClosedVia|closed_via|ResolvedVia|resolved_via` returns zero hits across schema, domain, and docs (see explore.md §7).
- `IsClosed()` lumps `resolved + closed + cancelled` into one read-only predicate (`internal/domain/state.go:18-25`), so a requester whose ticket is "resolved" is fully locked out — they cannot comment (`internal/application/comment_service.go:50-55`), confirm, or reject. The requester has no way to signal whether the resolution actually solved their problem.
- The workflow `close_ticket` terminal step resolves-then-closes in one atomic terminal action (`internal/application/workflow_runner.go`, `applyTerminal` ~:250-296), so "the workflow finished" and "the requester agreed" are indistinguishable in history.

The product gap: `resolved` currently means nothing to the person who opened the ticket. Issue #55 redefines it as **awaiting requester confirmation**, gives the requester an in-app say, and makes closure attribution explicit — except where no requester exists to confirm.

## What Changes

Confirmed product decisions (handoff, not re-negotiable here):

1. **Confirmation surface is in-app for the logged-in requester.** When the session user equals the ticket's `RequesterUserID`, they get a confirm/reject action on the ticket. No email, no unauthenticated links.
2. **`resolved` means "awaiting requester confirmation"** for every ticket that has an identifiable requester.
3. **Requester confirms → ticket becomes `closed`.** The audit trail must distinguish closure-by-requester-confirmation from closure-by-workflow-terminal-step. (Attribution mechanism is a design decision.)
4. **Requester rejects → ticket returns to `in_progress` as a manual ticket:** the workflow link is detached (`workflow_version_id` set to NULL). Tickets that were already manual simply return to `in_progress`.
5. **Closure without confirmation is allowed only for tickets with `RequesterUserID == NULL`** (legacy tickets with no provable creator): an agent may manually transition `resolved → closed`, audited as a manual agent closure. Every ticket that has a requester — including manual (non-workflow) tickets — requires requester confirmation to close.
6. **While in `resolved`, the requester is active:** they can confirm/reject and comment. All other surfaces (field edits, assignment, agent closure) stay read-only for everyone, subject to existing read/comment visibility rules.
7. **Workflow final `close_ticket` step keeps its current behavior:** a workflow run may still close directly without requester confirmation. That remains the sanctioned workflow path into closed.

Behavioral delta per layer (WHAT, not HOW):

- **Domain rules** — the legal transition `resolved → closed` (`state.go:39`) becomes conditional: permitted for the requester-confirmation path, permitted as a manual agent closure only when `RequesterUserID` is NULL, and still permitted inside the workflow `close_ticket` terminal matrix. The requester gains a new transition path `resolved → in_progress` (rejection) that also detaches the workflow (`ticket.go:28` discriminator; detachment persistence is a design decision). The existing reopen transition stays for agents.
- **Application policy** — `TicketService.Transition`'s single `CapEditTicket` gate (`internal/application/ticket_service.go:287-310`) no longer covers every closure: a requester-keyed authorization (identity: session user == `RequesterUserID`, precedent `requireFormActor` at `workflow_runner.go:~198-214`) is needed for the confirm/reject path, and a requester-NULL predicate gates the manual agent closure. The read-only carve-out while in `resolved` applies to comments for the requester only (`comment_service.go:50-55`); edits/assignment stay locked for everyone.
- **Persistence** — a schema migration (next in sequence, after `0009`) carries whatever attribution and workflow-detachment persistence the design chooses; no new state value is introduced (the `state` CHECK in `0001_init.sql:26-40` stays untouched).
- **Audit** — closure events become attributable: requester-confirmation closure, manual agent closure of requester-less tickets, and workflow-terminal closure must each be distinguishable in the audit trail (`internal/domain/audit.go`; actor conventions and the exact mechanism are design decisions, constrained by the workflow UoW validators that pin workflow audit shapes).
- **HTTP/UI** — the requester sees a confirm/reject control on their resolved ticket (in-app, identity-gated, HTMX per project convention D6); `allowedNext` (`handlers_tickets.go:~160-175`) and `detailData.Closed` (`~:395-400`) change so the agent-facing Close control appears only for requester-less tickets, the requester sees their confirmation controls on resolved tickets, and the requester keeps a comment form while others see resolved as read-only. Lifecycle meta (`partials/ticket_detail.html:112-119`) and badges already distinguish resolved/closed visually (`partials/styles.html:94-95`).

## Impact / Capabilities affected

Canonical specs receiving deltas (identified by exploration; deltas are written in the spec phase, not here):

| Capability | Delta |
|---|---|
| `ticket-state-machine` | Primary. Conditional `resolved → closed`; new requester rejection path `resolved → in_progress` with workflow detachment; requester-NULL exception for manual closure. |
| `ticket-workflow-execution` | `resolved` after a run becomes a waiting state; rejection detaches the workflow; `close_ticket` terminal behavior explicitly preserved unchanged. |
| `audit-log` | New closure attribution requirement: requester-confirmation vs workflow-terminal vs manual-agent closure of requester-less tickets must be distinguishable; `No Silent Mutations` still holds. |
| `ticket-management` | Lifecycle timestamps semantics with confirmation; detail presentation gains the requester confirmation control; read-only presentation carve-out for the requester on resolved tickets. |
| `role-authorization` | First role-`user` mutation path: an explicit carve-out for requester-keyed confirmation on their own tickets (identity predicate, precedent `requireFormActor`), while the existing role hierarchy stays otherwise unchanged. |
| `comment-timeline` | The `resolved` state is carved out of the closed-state comment rejection for the requester only: while a ticket is `resolved`, only its requester may comment; `closed`/`cancelled` reject everyone. Required because confirmed decision 6 (active requester) contradicts the merged closed-state rejection contract. |

Spec-collision note (resolved 2026-08-30): both previously-unarchived changes (`sync-workflow-polish-contracts` via PR #83 / issue #82, `sync-frontend-contracts` via PR #84 / issue #74) are now merged and archived, so this change's deltas apply against clean post-merge canonical text (main `03da3e2`).

## Non-goals

- **Email or public-token confirmation.** No email infrastructure, no unauthenticated routes, no token tables. In-app confirmation for the logged-in requester only; email-token flow is possible future work.
- **Auto-close timers.** No automatic `resolved → closed` after N days awaiting confirmation; a ticket stays in `resolved` until the requester or an authorized path (workflow terminal step, requester-less manual closure) acts.
- **Per-desk configuration of confirmation policy.** The confirmation rule is global, not configurable per desk.
- **No new lifecycle states.** The five existing states and the `state` CHECK constraint stay as-is; this change reinterprets `resolved` semantics.
- **No change to the workflow `close_ticket` terminal step** (decision 7) and no change to how workflow runs complete otherwise.

## Rollback plan

This change touches domain semantics and schema, so rollback is planned explicitly:

- **Schema:** the migration is expected to be additive only (new attribution/detachment persistence; no column drops, no state-value changes). An additive migration is safe to leave in place on rollback — unused columns are dormant, and existing rows stay valid. If the design instead extends existing columns/conventions, the revert migration must restore the pre-`0010` shape without data loss; design decides which applies and documents reversibility there.
- **Data:** audit events written under the new attribution remain historically accurate and readable after a code rollback; nothing needs purging. Tickets detached from workflows by rejection keep their state; re-enabling the feature later does not retroactively re-attach them.
- **Behavior:** rolling back the application/domain code restores today's matrix (`resolved → closed` for any authorized agent, `IsClosed` read-only everywhere, `close_ticket` unchanged). Because no new state values and no destructive schema changes exist, a pure code revert is a complete functional rollback.
- **Verification of rollback:** after revert, the pre-change test suites (domain matrix table, terminal matrices, e2e ticket-detail journey) pass unchanged; the only residue is the dormant migration.

## Rough scope hint

For the tasks-phase Review Workload Forecast only — no line-count commitments:

- **domain/** — transition matrix conditions, requester-keyed/rejection transition, audit event attribution fields, lifecycle timestamp handling on the confirmation paths; `state_test.go` / `ticket_test.go` matrix updates.
- **application/** — policy gates in `ticket_service.go` (requester confirmation path, requester-NULL manual closure), comment read-only carve-out, rejection workflow-detachment; `workflow_runner.go` terminal matrices unchanged; service tests.
- **adapters/sqlite/** — migration `0010` (+ backfill pattern per `migration_0003/0008/0009` tests), store columns, UoW handling for the new transition paths; migration + UoW tests.
- **adapters/http/** — requester confirm/reject handling, `allowedNext` / `detailData.Closed` adjustments, requester-NULL gating of the transition endpoint; handler tests.
- **web/templates/** — detail partial confirmation control, state copy, golden regeneration.
- **e2e/** — the resolved→closed journey in `ticket-detail.spec.ts` changes; new requester confirm/reject journeys; README coverage rows.
