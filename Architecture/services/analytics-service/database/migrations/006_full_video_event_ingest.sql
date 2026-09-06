-- 006: open the ingest path to the full video event model.
--
-- Migration 004 gave analytics.ingest_receipts a blanket
--   UNIQUE (actor_id, session_id, content_id, event_type)
-- which was correct while the endpoint accepted exactly one event type
-- (play_end), where one row per (viewer, session, content) is the whole
-- truth. It is wrong for the other twelve event types: a single watch
-- session legitimately emits many watch_heartbeat rows and several
-- milestone rows, and that constraint would silently report every one
-- after the first as a duplicate.
--
-- The dedupe key that actually matters is the client's event_id, which
-- is already the primary key. So: keep the one-per-session rule for
-- play_end (the row money and view counts are derived from) as a partial
-- unique index, and let every other type dedupe on event_id alone.

ALTER TABLE analytics.ingest_receipts
    DROP CONSTRAINT IF EXISTS ingest_receipts_actor_id_session_id_content_id_event_type_key;

CREATE UNIQUE INDEX IF NOT EXISTS uq_ingest_receipts_play_end_session
    ON analytics.ingest_receipts (actor_id, session_id, content_id)
    WHERE event_type = 'play_end';

-- Milestones are also once-per-(session, milestone_type) by definition:
-- crossing PCT_50 twice in one session is a replay artefact, not two
-- milestones. The milestone kind lives in the receipt so the constraint
-- can see it.
ALTER TABLE analytics.ingest_receipts
    ADD COLUMN IF NOT EXISTS dedupe_key TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS uq_ingest_receipts_dedupe_key
    ON analytics.ingest_receipts (actor_id, session_id, content_id, event_type, dedupe_key)
    WHERE dedupe_key IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_ingest_receipts_content
    ON analytics.ingest_receipts (content_id, accepted_at DESC);

-- The content index from 001 only covered the playback event types.
-- Aggregation now groups every ingested type by content_id, so widen it.
CREATE INDEX IF NOT EXISTS idx_events_raw_content_all
    ON analytics.events_raw ((payload->>'content_id'), ts DESC);

-- Aggregation reads (creator_id, ts) directly off events_raw when
-- rebuilding a creator's hour; without this it is a partition scan.
CREATE INDEX IF NOT EXISTS idx_events_raw_creator_ts
    ON analytics.events_raw ((payload->>'creator_id'), ts DESC);
