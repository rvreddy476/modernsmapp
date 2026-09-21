-- 001: repair channel_updates.reaction_count — recompute it from
-- update_reactions, the only thing it has ever meant.
--
-- WHY
--
-- The column had two writers that disagreed. syncReactionCount (called in
-- the same transaction as every reaction write) sets it to
-- COUNT(*) FROM update_reactions, while the old SparkUpdate/UnsparkUpdate
-- added and subtracted a spark weight — 1, or 5 for a "supernova". So an
-- update that collected sparks read high by up to 5 per spark, until the
-- next emoji reaction on it clobbered the inflation away; the figure the
-- client displayed depended on which of the two writers went last.
--
-- The service no longer lets sparks touch the column (sparks are not a
-- channel feature: an update has an emoji reaction and a share). This
-- repairs the rows corrupted while they did.
--
-- SAFE TO RE-RUN. It is a recompute, not a delta: running it twice, or
-- running it while the service is up, lands on the same value. A reaction
-- written concurrently is either counted here or written by
-- syncReactionCount afterwards — either way the row ends correct.
--
-- It touches only channel_updates.reaction_count. update_reactions,
-- update_sparks and every other column are read-only to this script.
--
-- NOT APPLIED AUTOMATICALLY. channel-service has no migration runner:
-- BootstrapSchema applies database/setup.sql and nothing else. This file is
-- a deliberate one-off, run by hand against one database at a time.
--
-- HOW TO RUN (one database at a time, dev first, with the service up):
--
--   psql "$CHANNEL_DB_DSN" -v ON_ERROR_STOP=1 \
--     -f database/migrations/001_backfill_reaction_count.sql
--
-- Expect one "UPDATE <n>" line, n = the number of rows that were wrong.
-- A second run on the same database must report UPDATE 0.

BEGIN;

-- updated_at is deliberately NOT bumped: this repairs a derived counter,
-- it is not an edit to the update, and clients sort and cache on that
-- timestamp.
UPDATE channel_updates u
SET reaction_count = c.n
FROM (
    SELECT u2.id, COALESCE(r.n, 0) AS n
    FROM channel_updates u2
    LEFT JOIN (
        SELECT update_id, COUNT(*) AS n
        FROM update_reactions
        GROUP BY update_id
    ) r ON r.update_id = u2.id
) c
WHERE c.id = u.id
  -- Only the wrong rows, so a re-run is a no-op and the "UPDATE n" line
  -- is a real count of the damage.
  AND u.reaction_count IS DISTINCT FROM c.n;

COMMIT;
