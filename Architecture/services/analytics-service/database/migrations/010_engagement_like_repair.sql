-- 010: collapse the double-counted likes (plan Phase 1B, audit M-05).
--
-- Two writers put likes into events_raw with disjoint identities: the
-- HTTP ingest path (one receipt per client event, keyed per session)
-- and the Kafka engagement consumer (a bare INSERT with a fresh uuid
-- and no receipt at all). Every like from a web client that also
-- reached Kafka as PostReacted was counted twice, and that count feeds
-- the quality score.
--
-- From this migration on both paths write through InsertAcceptedBatch
-- with a receipt keyed on (actor, nil session, content, 'like',
-- 'content'), which the partial unique index from 006 collapses. This
-- repairs what is already there:
--
--   1. keep the earliest like per (viewer, content), delete the rest;
--   2. replace every like receipt with one per survivor, in the new
--      shape, so a Kafka redelivery or a client replay of an old event
--      id lands on the receipt and is a duplicate, not a third row.
--
-- The hourly aggregates for affected hours are not rewritten here — SQL
-- cannot run the aggregator. The hourly worker rebuilds the last two
-- hours every five minutes on its own; older hours inside the rollup
-- window are rebuilt by POST /v1/analytics/internal/aggregate, and the
-- likes column is not money-bearing while the quality multiplier is
-- frozen at 1.0 (plan, decisions).

-- 1. Duplicate like rows. Deleted by id: every writer set one, and a
--    NULL id (none is expected) is left alone rather than guessed at.
DELETE FROM analytics.events_raw
WHERE type = 'like'
  AND id IN (
      SELECT id FROM (
          SELECT id,
                 ROW_NUMBER() OVER (
                     PARTITION BY user_id, payload->>'content_id'
                     ORDER BY ts, received_at, id
                 ) AS rn
          FROM analytics.events_raw
          WHERE type = 'like'
            AND id IS NOT NULL
            AND user_id IS NOT NULL
            AND payload->>'content_id' IS NOT NULL
      ) ranked
      WHERE rn > 1
  );

-- 2. Receipts. The old per-session like receipts would collide with the
--    new per-content shape (two sessions, one viewer, one content), so
--    they are replaced wholesale rather than updated in place.
DELETE FROM analytics.ingest_receipts WHERE event_type = 'like';

INSERT INTO analytics.ingest_receipts
    (event_id, actor_id, session_id, content_id, event_type, dedupe_key, accepted_at)
SELECT
    'repair:like:' || e.id::text,
    e.user_id,
    '00000000-0000-0000-0000-000000000000'::uuid,
    (e.payload->>'content_id')::uuid,
    'like',
    'content',
    e.received_at
FROM analytics.events_raw e
WHERE e.type = 'like'
  AND e.id IS NOT NULL
  AND e.user_id IS NOT NULL
  AND e.payload->>'content_id' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
  -- ingest_receipts.content_id references content_ownership; a like on
  -- content that was never projected cannot carry a receipt, and it
  -- could not have been ingested over HTTP either.
  AND EXISTS (
      SELECT 1 FROM analytics.content_ownership o
      WHERE o.content_id = (e.payload->>'content_id')::uuid
  )
ON CONFLICT DO NOTHING;
