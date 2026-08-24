-- Step-indexed audit correlation: audit_events.step_index records the sealed
-- zero-based pinned step index of a semantic workflow event (requester/assignee
-- form, manual task, contextual assignment). NULLABLE with no default: state
-- transitions, non-flow audits, and all pre-0008 rows stay NULL — there is no
-- backfill, because legacy rows have no trustworthy index and correlation must
-- never be inferred from timestamps or occurrence order.
ALTER TABLE audit_events ADD COLUMN step_index INTEGER;
