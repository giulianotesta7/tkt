# Delta for Audit Log

Scope note: this is the spec-phase delta for change `agent-downgrade-ticket-handoff` (issue #47). It adds the audit contract for the automatic handoff performed by the atomic downgrade. No canonical spec file is edited here.

## ADDED Requirements

### Requirement: Downgrade Handoff Audit Events

Every automatic reassignment or unassignment performed by the atomic downgrade handoff MUST record exactly one assignment audit event per affected open ticket, following the existing `Ticket.ApplyUpdate` event convention: action `update` on field `user` with the actual from/to assignee values (to empty when the ticket becomes unassigned). The actor MUST be the initiating admin with the actor user ID set. The reason MUST identify the role downgrade. When a desk was resolved for the ticket, the event MUST carry that `desk_id`; when no desk resolved, `desk_id` MUST be NULL. The step index MUST remain NULL for every handoff event because the handoff occurs outside any pinned workflow run. The role change itself MUST continue to be recorded in `role_changes` with the acting user as today. A failed downgrade MUST persist no handoff audit events.

#### Scenario: Reassignment event fields

- GIVEN a downgrade handoff reassigns an open ticket to an eligible pool member
- WHEN the event is persisted
- THEN it records action `update`, field `user`, the downgraded account as from-value, the replacement as to-value, the initiating admin as actor with actor user ID set, a reason identifying the role downgrade, and the resolved desk id
- AND the event commits inside the same transaction as the reassignment

#### Scenario: Unassignment event fields

- GIVEN a downgrade handoff leaves an open ticket unassigned because no desk resolves or no eligible member exists
- WHEN the event is persisted
- THEN it records action `update`, field `user`, the downgraded account as from-value, an empty to-value, the initiating admin as actor with actor user ID set, a reason identifying the role downgrade, and a NULL desk id when no desk resolved

#### Scenario: Step index NULL outside pinned runs

- GIVEN one or more handoff audit events are persisted during a downgrade
- WHEN the `audit_events` rows are inspected
- THEN every handoff event's step index is NULL
- AND no handoff event is treated as a pinned semantic workflow event by the timeline

## Notes

Traceability for `Downgrade Handoff Audit Events` (evidence paths show today's seams; T2–T4 add the implementation and tests):

| Evidence | Path | What it proves |
|---|---|---|
| Update event convention | `internal/domain/ticket.go:107` (`Ticket.ApplyUpdate`) + `internal/domain/audit.go:12` (`ActionUpdate`) | Action/field/from/to shape the handoff events follow |
| Reassignment reason convention | `internal/domain/audit.go:23` (`Reason` mandatory for reassignment) | Reason field carrying the role-downgrade context |
| Desk context field | `internal/domain/audit.go:45` (`DeskID`) | Desk snapshot carried when a desk resolves |
| Step-index rule outside pinned runs | `openspec/specs/audit-log/spec.md` (`Step-Indexed Semantic Audit Events`) | Step index NULL is the mandated value outside pinned runs |
| role_changes persistence today | `internal/adapters/sqlite/user_store.go:110` | The role change row the operation preserves |
| Event field assertions | `internal/adapters/sqlite/user_store_test.go` (T2) | Actor, reason, desk id, step index, from/to per event |
