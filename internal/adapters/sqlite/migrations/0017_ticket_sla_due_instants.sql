-- 0017_ticket_sla_due_instants.sql — frozen SLA due/warning instants (issue #211).
--
-- ticket_sla gains FOUR frozen INSTANTS alongside the two frozen target
-- DURATIONS: warn_first_response_at, due_first_response_at, warn_resolve_at,
-- due_resolve_at. FOUR and not two, and why:
--
--   at_risk means "the warning share of the target has elapsed", and with a
--   working calendar "elapsed" is WORKING time. Resolving the warning point
--   at projection time would force the calendar into ProjectSLA and destroy
--   the property that the projection is pure — clock-free and I/O-free.
--   Freezing the warning instants (like the due instants) at creation keeps
--   the projection pure: it reads instants, never the calendar, never a
--   setting.
--
-- The consequence, by design: sla_warning_percent applies to tickets
-- created from then on, because the warning point is frozen with the
-- targets. That is the same freeze rule the targets already follow (the
-- WorkflowVersionID precedent): a later calendar or warning-percent edit
-- never rewrites the verdict of a ticket already created.
--
-- SQLite cannot ADD a NOT NULL column without a DEFAULT, so DEFAULT '' is a
-- migration-mechanics constraint, not a meaningful value. Every row written
-- by the application carries real instants (formatSLAInstant stores '' only
-- for a zero instant); the only rows carrying '' are ones created before
-- this migration, and the SLA projection reads '' back as the zero time —
-- "no frozen SLA" — never as a date in year zero.

ALTER TABLE ticket_sla ADD COLUMN warn_first_response_at TEXT NOT NULL DEFAULT '';
ALTER TABLE ticket_sla ADD COLUMN due_first_response_at TEXT NOT NULL DEFAULT '';
ALTER TABLE ticket_sla ADD COLUMN warn_resolve_at TEXT NOT NULL DEFAULT '';
ALTER TABLE ticket_sla ADD COLUMN due_resolve_at TEXT NOT NULL DEFAULT '';
