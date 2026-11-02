-- Preserve legacy values byte-for-byte. Email wins a disagreement, but the
-- previous company address remains available to root as a migration notice.
INSERT INTO settings (key, value)
SELECT 'instance_address_company_conflict', company.value
FROM settings AS company JOIN settings AS email ON email.key = 'email_base_url'
WHERE company.key = 'company_signin_address'
  AND company.value <> '' AND email.value <> '' AND company.value <> email.value;

INSERT INTO settings (key, value)
VALUES ('instance_address', COALESCE(
  (SELECT NULLIF(value, '') FROM settings WHERE key = 'email_base_url'),
  (SELECT NULLIF(value, '') FROM settings WHERE key = 'company_signin_address'), ''));

DELETE FROM settings WHERE key IN ('email_base_url', 'company_signin_address');

CREATE TABLE instance_address_events (
  id INTEGER PRIMARY KEY,
  actor_user_id INTEGER NOT NULL REFERENCES users(id),
  previous_address TEXT NOT NULL,
  address TEXT NOT NULL,
  created_at TEXT NOT NULL
);
