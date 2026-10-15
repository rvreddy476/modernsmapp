-- 014_report_entity_key.sql: hashtags and sounds become reportable
-- (TikTok parity T-13 / T-03).
--
-- trust.reports.entity_id stays UUID NOT NULL. Every existing reader keeps
-- working unchanged: the admin queue, admin-service's cross-schema mirror
-- (which scans entity_id into a uuid.UUID), the purge, the trust-state and
-- connection-filter reads, and the one-active-report-per-reporter index
-- uq_reports_one_active_per_reporter_entity (008).
--
--   sound    a media-service audio id, which is already a UUID: stored in
--            entity_id as is; entity_key stays NULL.
--   hashtag  addressed by its text, not a UUID. entity_key holds the
--            normalised tag (no '#', lower case, 1-50 characters: the
--            post-service MaxHashtagLength), and entity_id holds a UUIDv5
--            derived from it in Go (service.ReportEntityID). The same tag
--            therefore always lands on the same entity_id, so the existing
--            duplicate-report index and every entity_id read apply to tags
--            exactly as they apply to posts.
--
-- Only hashtag rows carry a key; the CHECK keeps the two columns in step.
-- (Case folding is Go's job: PostgreSQL lower() depends on the database
-- ctype, so it is not asserted here.)

ALTER TABLE trust.reports ADD COLUMN IF NOT EXISTS entity_key TEXT;

ALTER TABLE trust.reports DROP CONSTRAINT IF EXISTS reports_entity_key_check;
ALTER TABLE trust.reports ADD CONSTRAINT reports_entity_key_check CHECK (
    (entity_type = 'hashtag' AND entity_key IS NOT NULL
        AND char_length(entity_key) BETWEEN 1 AND 50
        AND left(entity_key, 1) <> '#')
    OR (entity_type <> 'hashtag' AND entity_key IS NULL)
);

-- The admin "reports about this tag" lookup.
CREATE INDEX IF NOT EXISTS idx_reports_entity_key
    ON trust.reports (entity_type, entity_key)
    WHERE entity_key IS NOT NULL;
