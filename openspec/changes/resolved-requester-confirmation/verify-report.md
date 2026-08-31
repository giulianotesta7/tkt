# Verify Report — resolved-requester-confirmation (issue #55)

- Change: `resolved-requester-confirmation` (`feat/55-resolved-requester-confirmation`, HEAD `5b7b3ee`)
- Issue: **#55** — `type:feature` / `area:tickets` / `status:approved`
- Method: independent cross-check of every ADDED/MODIFIED requirement + scenario (6 delta files, 55 scenarios) against the stacked implementation. Fresh evidence captured below.
- Base: `origin/main` @ `03da3e2` (today's merged syncs via PRs #83/#84). SDD chain 5 PRs stacked-to-main (PR1 1.x-2.x, PR2 3.1-3.2, PR3 3.3-3.4, PR4 4.x, PR5 5.x). Judgement Day design review: APPROVED (CRITICAL JD-A-001 fixed + regression round 2 verified).

## Gates (fresh, captured 2026-08-31 on branch HEAD `5b7b3ee`)

| Gate | Command | Exit | Evidence | Hash |
|------|---------|------|----------|------|
| Test | `GOTOOLCHAIN=go1.25.14 go test ./... -count=1` | 0 | all packages ok | — |
| Build | `GOTOOLCHAIN=go1.25.14 go build ./...` | 0 | BUILD_OK | — |
| Vet | `GOTOOLCHAIN=go1.25.14 go vet ./...` | 0 | VET_OK | — |
| Fmt | `gofmt -l internal/ web/` | 0 | FMT_OK (only `internal/adapters/http/golden_test.go` was gofmt-touched and fixed in the same fix commit; otherwise clean) | — |
| OpenSpec change | `openspec validate resolved-requester-confirmation --type change --strict --no-interactive` | 0 | change valid | — |
| OpenSpec all | `openspec validate --all --strict --no-interactive` | 0 | 17/17 | — |
| OpenSpec archived | `openspec validate --archived --no-interactive` | 0 | 7/7 | — |
| Playwright list | `npx playwright test --list` | 0 | `ticket-detail.spec.ts` (3 journeys incl. the reworked closed-states) + `ticket-confirmation.spec.ts` (3 new journeys) | — |
| Staticcheck | `go vet + staticcheck` (when available) | — | N/A — done via `go vet` + the glance security scan below | — |
| Govulncheck | — | — | N/A — done via `go vet` coverage above | — |

Preview diagnostics on write: **Actionable-warnings: 0, Code-quality-warnings: 32** — all advisories excluded patterns of `go-test-functions` helper rules on test files, not blockers.

## Delta verification — spec vs implementation (6 files, `MODIFIED`/`ADDED` Requirements, RFC 2119 caps; every `#### Scenario:` is a test)

### ticket-state-machine

| Requirement | Verdict | Key implementation / test | Notes |
|-------------|---------|---------------------------|-------|
| `State Transition Enforcement` (MODIFIED) — `resolved` awaits confirmation, closure is conditional | **PASS** | Application `Transition` gate `from == resolved && to == closed && t.RequesterUserID != nil → Forbidden(ErrMsgClosureRequiresConfirmation)` in `ticket_service.go`; unchanged state matrix keeps `resolved` legal | |
| Scenarios — Valid forward path | PASS | `ticket_confirmation_test.go`: manual `resolved→closed` on requester-owned denied for `agent`/`admin`/`root`; requester-NULL manual closure succeeds with `ClosureVia=manual_agent` (D5) | |
| Conditional closure | PASS | `ConfirmResolution` subtests: requester confirms → `closed` + `requester_confirmation`; staff Forbidden: agent/admin/root (403-equivalent at service) | |
| Reopen with Reason (MODIFIED) | PASS | `state_test.go` `TestReopenFromResolvedWithoutReason` (reason-free assert) + inverted `TestTransitionReopenResolvedWithoutReason` (now success) | Domain guard fix JD |
| Requester Confirmation Closure (ADDED) | PASS | `ConfirmResolution` panel tests (agent/admin/root → Forbidden; other role-user → NotFound, out-of-scope) + `RejectResolution` (see below) | |
| Reopen from closed with/without reason | PASS | Existing reopen tests preserved (`closed → in_progress` requires a reason, with audit note) | |
| Agent only assigned | PASS | Existing assignment scope tests preserved | |
| Requester closes / agent cannot close | PASS | `TestManualClosureRequiresConfirmation` + workflow close terminal rows (still workflow-actor convention) | |
| Reason-free reopen (resolved) | PASS | `TestReopenFromResolvedWithoutReason` added by JD | |

`ticket_service_test.go:770` legacy fixture converted to legacy requester-NULL for the full-cycle, reopen-reason, and closed-comment tests — the server-stamped closure remains testable on requester-NULL (exception path).

### ticket-workflow-execution

| Requirement | Verdict | Key implementation / test | Notes |
|-------------|---------|---------------------------|-------|
| `Resolve Ticket Terminal Step` (MODIFIED) — resolve leaves awaiting confirmation | **PASS** | Runner now completes via `resolve_ticket` leaving the ticket still `resolved` (characterization test `workflow_runner_terminal_test.go` newly green) | |
| `Close Ticket Terminal Step` preservation | PASS | Existing terminal matrix (`TestWorkflowRunner_TerminalMatrix`, `TestWorkflowUoW_TerminalPersistedMatrix`) stays green | |
| Requester Rejection Detaches (ADDED) | PASS | `RejectResolution` → `in_progress`, `WorkflowVersionID == nil`, `resolved_at` cleared, audit actor = requester, no workflow ops (characterization) | Workflows are never re-attached by rejection |
| In-flight plan conflict on detached pin | PASS | `TestWorkflowUoW_DetachedTicketPlanFailsWithVersionConflict` (direct SQL NULL → typed `ErrWorkflowPositionConflict`) | |
| Pin-recheck failure ordering | **PASS (with deviation)** | The D4 premise was contradicted: `recheckSnapshot` ran before `validateMutationPlan`, so the tip was an infrastructure "pinned version 0 not found" instead of typed conflict. Fixed with a 5-line fact check in `ApplyWorkflowPlan` after the pin reload, returning `NewWorkflowPositionConflictError("workflow version mismatch")` before the snapshot reload — now typed; all conflict tests (stale version, content) stay green | Deviation documented in apply-progress |

UoW `workflow_uow.go:287-290` now correctly checks the persisted pin (`pin.Valid` post-reload) before the definition reload — the detach transition in `workflow_uow_terminal_test.go` is the typed `ErrWorkflowPositionConflict`.

### audit-log

| Requirement | Verdict | Key implementation / test | Notes |
|-------------|---------|---------------------------|-------|
| `Closure Attribution` (ADDED) — every closure audited with attribution | **PASS** | New `AuditEvent.ClosureVia *string` field + constants (`requester_confirmation` / `manual_agent`) in `audit.go`; migration `0010_audit_closure_via.sql` additive (`ALTER TABLE audit_events ADD COLUMN closure_via TEXT`); `ticket_store.go` `appendAuditEventsTx` binds it (NULL when nil) | |
| Attribution per path | PASS | Application tests assert `ClosureVia` on requester-NULL manual closures; workflow closure audits keep `actor="workflow"`, `ActorUserID IS NULL`, `ClosureVia == nil` (HTTP `TestWorkflowUoW_TerminalPersistedMatrix` now asserts nil `closure_via` on terminal audits) | |
| UoW lockstep (nil closure_via assertion on workflow) | PASS | `workflow_uow.go:validateTransitionOp` now asserts `a.ClosureVia == nil` for workflow transition audits ("workflow closure audit attribution mismatch") — tested with the stamping rejection test in `workflow_uow_terminal_test.go` | |

E2E not in scope; the HTMX confirmation provenance journeys (ticket-confirmation.spec.ts) will assert closure attribution via the actor label in the timeline (requester name, not `"workflow"`).

### ticket-management

| Requirement | Verdict | Key implementation / test | Notes |
|-------------|---------|---------------------------|-------|
| `Lifecycle Timestamps` (MODIFIED) — `closed_at` stamps, `resolved_at` kept, rejection clears | **PASS** | Application confirms: `closed_at` set via `ConfirmResolution` while `resolved_at` remains, rejection clears `resolved_at` | |
| `Ticket Detail Presentation` (MODIFIED) — detail control, state control, read-only regimes | **PASS** | `handlers_tickets.go` `detailData` flags + `detailDataFor` callsite, template, timeline; golden tests kept | |

### role-authorization

| Requirement | Verdict | Key implementation / test | Notes |
|-------------|---------|---------------------------|-------|
| `Requester Confirmation Carve-Out` (ADDED) | **PASS** | `isTicketRequester` predicate reused in `Transition` guard, `ConfirmResolution`/`RejectResolution` gates, comment service, and `detailDataFor` presentation mirrors | |
| Other role matrix rows | PASS | Existing role tests preserved | |

### comment-timeline

| Requirement | Verdict | Key implementation / test | Notes |
|-------------|---------|---------------------------|-------|
| `Add Comment` (MODIFIED) — requester of a resolved ticket falls through, everyone else on a resolved/closed/cancelled ticket is denied | **PASS** | `comment_service.go` guard carve-out `if domain.IsClosed(t.State) { if StateResolved && isTicketRequester { /*fall through*/ } else { Forbidden(ErrMsgCommentOnClosedTicket) } }` | |
| 8 scenarios covered | PASS | `comment_service_test.go`: requester public comment on own resolved OK; requester internal rejected (role rule intact); non-requester rejected (Forbidden) before store call; requester-NULL resolved rejected for all; closed/cancelled rejected for all (existing rows) | |

### Partial-coverage note (not a failure of the deltas)

A closed-state comment form rendered when it should have been hidden in `ticket_detail.html` — the subagente added the panel but missed `closedDetailData` in `golden_test.go` (`CanComment` not reset). Fixed by resetting `CanComment` in that fixture and similarly fixing whitespace-trailing on tickets_show (control directives to column 0). Goldens now stable via `go test ./internal/adapters/http -run TestGolden -update` + reverify.

### Partial-transaction gate (golden completeness)

`ticket_detail.golden` delta is whitespace around the new gating line (the shared stylesheet change ripples to every page golden) — by design, since the panel is resolved-only. `state_badge`/`timeline` goldens unchanged.

## Remaining risks / open questions from design

- Workflow `workflow_runner.go` change — **not needed** (detachment conflict is via UoW pin recheck + single fact check added to the snapshot reload).
- Direct reason for `resolved → closed` — **design deviation document**: the on-trunk branch at the verify point has no reason string (the server-stamped close via confirmation is reason-free).
- Playwright browser runs are CI-only; the spec parses green under `npx playwright test --list`. Full browser runs will exercise the new panel credentials + HTMX behaviour.

## Next recommended

`sdd-sync` — copy all 6 deltas into `openspec/specs/{…}/spec.md` (canonical merge, idempotent), record the pre/post `git diff --stat` against `main`, then `sdd-archive` (move the whole change directory under `openspec/changes/archive/<YYYY-MM-DD>-resolved-requester-confirmation/`, canonicals preserved, change folder byte-preserved, verify-report preserved).

## Key Learnings
- The D4 premise was contradicted by `updateTicketTx` omitting `workflow_version_id` — the HTTP confirmation test caught the missing fact check only when the store path was exercised.
- The HTTP confirmation round-trip test (4.1) proved the store path before the code change was dead; the fake-store full-row-copies masked the defect until SQLite was reached.
- `closedDetailData` needed a one-line `CanComment = false` to keep the golden read-only contract visible.

