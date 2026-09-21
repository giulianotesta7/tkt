-- 0015_ticket_sla.sql — SLA commitment frozen onto one ticket (issue #211).
--
-- A row is written ONCE at ticket creation (the WorkflowVersionID precedent):
-- a later policy edit must not rewrite history, so the row is never updated
-- afterwards. A missing row means "this ticket has no SLA" — a legitimate state
-- for legacy tickets and for tickets created while SLA was disabled; there is
-- no DEFAULT row and nothing backfills existing tickets. Targets are INTEGER
-- SECONDS. started_at anchors both milestone clocks; policy_snapshot_at records
-- when the targets were frozen (informational only: because the targets are
-- frozen, the instance's sla_enabled_at is never load-bearing).

CREATE TABLE ticket_sla (
  ticket_id              INTEGER PRIMARY KEY REFERENCES tickets(id) ON DELETE CASCADE,
  first_response_seconds INTEGER NOT NULL,
  resolve_seconds        INTEGER NOT NULL,
  started_at             TEXT NOT NULL,
  policy_snapshot_at     TEXT NOT NULL
);
