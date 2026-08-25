-- Ordered carousel, phase B (contract). Creator Studio P0-A, errata E-1 + R-1.
--
-- SHIPS IN A LATER RELEASE THAN 036. Never the same artifact: the gap between
-- the two releases is where old writers drain. Release B retains 036 as
-- immutable history (R-1) so a fresh database can boot the latest artifact and
-- reach this file with the column already present.
--
-- Preconditions on an UPGRADED database, all verified before this release ships:
--   1. the `post-media-position-index` job recorded completion in ops_job_runs;
--   2. SELECT count(*) FROM post_media WHERE position IS NULL            => 0
--   3. duplicate (post_id, position) count                               => 0
--   4. invalid-index count for idx_post_media_post_position_unique       => 0
--
-- On a FRESH database there are no old pods to drain and no concurrent job has
-- run, so the unique index is built transactionally by the first statement.
-- On an upgraded database that index already exists and the statement is a
-- no-op, which is what makes one file correct for both paths.

CREATE UNIQUE INDEX IF NOT EXISTS idx_post_media_post_position_unique
    ON post_media (post_id, position);

ALTER TABLE post_media ALTER COLUMN position SET NOT NULL;

ALTER TABLE post_media ADD CONSTRAINT post_media_position_unique
    UNIQUE USING INDEX idx_post_media_post_position_unique;
