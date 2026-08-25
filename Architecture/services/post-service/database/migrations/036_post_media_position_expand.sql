-- Ordered carousel, phase A (expand). Creator Studio P0-A, errata E-1.
--
-- ADDITIVE AND BACKWARD COMPATIBLE ON PURPOSE.
--
-- `position` is nullable here and only here. An old post-service pod that is
-- still inserting `(post_id, media_id, kind)` keeps working for the whole
-- rollout window; taking the column straight to NOT NULL would break every
-- old writer the instant this file committed. The contract step is a SEPARATE
-- release (037) and must not ship in the same artifact as this one.
--
-- The concurrent indices are NOT here. `migrationrunner` wraps every migration
-- file in a transaction and `CREATE INDEX CONCURRENTLY` cannot run inside one,
-- so they are built by the `post-media-position-index` operational job.

ALTER TABLE post_media ADD COLUMN IF NOT EXISTS position INT;

-- Deterministic backfill. `ORDER BY media_id`, never physical row order:
-- row order is not a contract and a second run must agree with the first.
-- Legacy multi-media posts get a stable arbitrary order, which is honest —
-- their authoring order was never recorded and cannot be recovered.
WITH ordered AS (
    SELECT post_id, media_id,
           ROW_NUMBER() OVER (PARTITION BY post_id ORDER BY media_id) - 1 AS pos
    FROM post_media
    WHERE position IS NULL
)
UPDATE post_media pm
SET position = o.pos
FROM ordered o
WHERE pm.post_id = o.post_id AND pm.media_id = o.media_id;

-- Durable ledger for out-of-band operational jobs.
--
-- The concurrent-index job records completion here. Without this table that
-- job would build both indices and then fail on its last statement, leaving
-- the release gate with nothing to read.
CREATE TABLE IF NOT EXISTS ops_job_runs (
    job          TEXT        NOT NULL PRIMARY KEY,
    completed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
