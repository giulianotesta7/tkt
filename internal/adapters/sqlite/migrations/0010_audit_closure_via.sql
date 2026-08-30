-- Closure attribution (issue #55): distinguishes requester-confirmation closure
-- from manual agent closure on closure transition events. Additive forward-only
-- column; workflow-terminal closures intentionally leave it NULL and remain
-- attributed by the existing workflow actor convention. No backfill: closures
-- recorded before this change were written under a policy with no attribution
-- concept, and fabricating closure_via values for them would corrupt history.
ALTER TABLE audit_events ADD COLUMN closure_via TEXT;
