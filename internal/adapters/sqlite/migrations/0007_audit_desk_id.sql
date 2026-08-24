-- Contextual workflow timeline (audit half): audit_events.desk_id records the
-- pinned assign_to_desk desk context of a structured workflow_assignment event,
-- so the timeline can render "Assigned to person · desk" from persisted facts.
-- NULLABLE: every other action carries no desk context, and all pre-0007 rows
-- stay NULL. The FK is ON DELETE SET NULL so deleting a desk never deletes
-- history — the row survives and degrades to "Unknown desk".
ALTER TABLE audit_events ADD COLUMN desk_id INTEGER
REFERENCES desks (id) ON DELETE SET NULL;
