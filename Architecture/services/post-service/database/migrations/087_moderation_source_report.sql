-- Migration 087: an upheld user report is a moderation decision of its own
-- source (lane post-service-2, 2026-11-18).
--
-- trust-safety resolves a report against a post by sending the signed
-- moderation command (POST /v1/posts/internal/moderation) with
-- policy_version "report-v1"; post-service records it in
-- post_moderation_decisions with source 'report' and source_ref_id = the
-- report id. Until now the column allowed only 'admin' and 'appeal'.
--
-- The CHECK only widens: every row the previous image writes ('admin',
-- 'appeal') still passes, and the previous image reads `source` as a plain
-- string (GetModerationSubject passes it to trust-safety, which treats any
-- value but 'copyright' alike), so it survives 'report' rows.
--
-- Rollback: the previous image runs unchanged on this schema; leave the
-- constraint as it is. Restoring the old CHECK would first need every
-- 'report' row rewritten, which loses the audit source, so do not.
--
-- Idempotent: the constraint is dropped by name (033 declared it inline, so
-- Postgres named it post_moderation_decisions_source_check) and re-created.

SET LOCAL lock_timeout = '5s';

ALTER TABLE post_moderation_decisions
    DROP CONSTRAINT IF EXISTS post_moderation_decisions_source_check;

ALTER TABLE post_moderation_decisions
    -- compat: ok widens the 033 CHECK by one value; the previous image writes only 'admin' and 'appeal', both still allowed
    ADD CONSTRAINT post_moderation_decisions_source_check
    CHECK (source IN ('admin','appeal','report'));
