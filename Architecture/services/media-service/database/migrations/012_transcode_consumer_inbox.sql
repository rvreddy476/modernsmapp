-- Module 4 M4-P0-3 — durable consumer inbox for the transcode worker.
--
-- WHY AN INBOX AND NOT REDIS
--
-- The worker's terminal effect is a set of PostgreSQL writes (status, HLS key,
-- moderation status, variants). The only way "this event was applied" and "the
-- effect exists" cannot disagree is for both to be written in the same
-- transaction. A Redis claim is a separate system that can succeed while the
-- effect fails, which is precisely the loss path Module 3 CLB-1 closed for the
-- suggestion consumer.
--
-- WHY IT MATTERS MORE HERE THAN ELSEWHERE
--
-- Transcoding is expensive and slow. Without idempotency, a redelivery after a
-- rebalance re-runs ffmpeg over the same asset: two workers burn CPU on
-- identical work, race to write variants, and can interleave a stale result
-- over a fresh one. With two replicas — which the prod values now set — that
-- is the normal case, not an edge case.

CREATE TABLE IF NOT EXISTS media_transcode_inbox (
    -- The event id from the envelope. The producer sets it per request, so a
    -- redelivery of the SAME request carries the same id.
    event_id        TEXT PRIMARY KEY,
    media_asset_id  UUID NOT NULL,
    -- Terminal outcome recorded with the effect: 'ready' or 'failed'.
    outcome         TEXT NOT NULL,
    attempts        INT  NOT NULL DEFAULT 1,
    applied_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_media_transcode_inbox_asset
    ON media_transcode_inbox (media_asset_id, applied_at DESC);

-- Operational visibility: how long has the oldest un-transcoded asset been
-- waiting. Without this, a stalled worker looks identical to "no uploads",
-- which is the failure mode that went unnoticed when the worker was not
-- deployed at all.
CREATE INDEX IF NOT EXISTS idx_media_assets_processing_age
    ON media_assets (created_at)
    WHERE processing_status IN ('uploaded', 'processing');
