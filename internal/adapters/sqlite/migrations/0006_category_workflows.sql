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
