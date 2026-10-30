-- 0038_audit_api_key.sql — attribute ticket audit events to API keys
-- (issue #489, part 2).
--
-- The actor remains the service user. The nullable key identifies which of
-- that user's credentials initiated the request. Browser and historical events
-- keep NULL; history is never backfilled or removed when a key is revoked.
ALTER TABLE audit_events ADD COLUMN api_key_id INTEGER REFERENCES api_keys(id);
