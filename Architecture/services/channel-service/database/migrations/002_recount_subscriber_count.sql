-- 002: repair broadcast_channels.subscriber_count — recount it from
-- channel_members, the only thing it has ever meant (non-banned rows).
--
-- WHY
--
-- The column was the sum of Redis shard deltas, written over the row every
-- 10 seconds by the flush worker. Nothing seeded the shards from Postgres, so:
-- a new channel (created at 1, the owner) dropped to 1 on its first join, a
-- channel with members from before the counter showed only the joins since,
-- a Redis flush reset counts to zero, and an increment taken by the Postgres
-- fallback was overwritten by the next flush. The account-deletion consumer
-- also removed member rows without touching the count.
--
-- The service now recounts from the roster on every change
-- (service.refreshSubscriberCount, store.RecountSubscriberCounts). This
-- repairs the rows that are wrong now; a channel nobody joins or leaves
-- again would otherwise keep its wrong count.
--
-- SAFE TO RE-RUN. It is a recompute, not a delta: running it twice, or while
-- the service is up, lands on the same value. A join written concurrently is
-- either counted here or recounted by the flush worker afterwards.
--
-- It touches only broadcast_channels.subscriber_count. updated_at is NOT
-- bumped: a derived counter changing is not an edit to the channel.
--
-- NOT APPLIED AUTOMATICALLY. channel-service has no migration runner:
-- BootstrapSchema applies database/setup.sql and nothing else. Run by hand,
-- one database at a time, dev first, with the service up:
--
--   psql "$CHANNEL_DB_DSN" -v ON_ERROR_STOP=1 \
--     -f database/migrations/002_recount_subscriber_count.sql
--
-- Expect one "UPDATE <n>" line, n = the number of channels that were wrong.
-- A second run on the same database must report UPDATE 0.

BEGIN;

UPDATE broadcast_channels b
SET subscriber_count = c.n
FROM (
    SELECT ch.id, COALESCE(m.n, 0) AS n
    FROM broadcast_channels ch
    LEFT JOIN (
        SELECT channel_id, COUNT(*) AS n
        FROM channel_members
        WHERE role <> 'banned'
        GROUP BY channel_id
    ) m ON m.channel_id = ch.id
) c
WHERE c.id = b.id
  -- Only the wrong rows, so a re-run is a no-op and the "UPDATE n" line is a
  -- real count of the damage.
  AND b.subscriber_count IS DISTINCT FROM c.n;

COMMIT;
