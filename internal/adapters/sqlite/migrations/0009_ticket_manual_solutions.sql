-- Manual-task completion solutions (Amendment 2): one workflow-task record per
-- completed manual step, keyed PRIMARY KEY(ticket_id, step_index) in the same
-- record family as ticket_form_answers. Additive forward-only DDL with NO data
-- backfill — pre-amendment manual completions simply have no solution row and
-- read back an empty solution. The 2,000-character CHECK mirrors the HTTP
-- transport bound as defense in depth; the solution lives ONLY here, never in
-- audit note/reason fields, comments, or full-text search.
CREATE TABLE ticket_manual_solutions (
    ticket_id INTEGER NOT NULL REFERENCES ticket_workflow_runs (
        ticket_id
    ) ON DELETE CASCADE,
    step_index INTEGER NOT NULL CHECK (step_index >= 0),
    solution TEXT NOT NULL CHECK (length(solution) <= 2000),
    created_by_user_id INTEGER NOT NULL REFERENCES users (id),
    created_at TEXT NOT NULL,
    PRIMARY KEY (ticket_id, step_index)
);
