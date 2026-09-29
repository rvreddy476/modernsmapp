-- Transcode lease (2026-09-29).
--
-- When the media worker died mid-transcode the asset stayed
-- processing_status='processing' with nothing to tell a live job from a dead
-- one: updated_at does not move while ffmpeg runs, and a long upload
-- legitimately takes hours. The worker now stamps a heartbeat on the asset
-- when it starts a transcode and every 30 s while the job runs
-- (cmd/worker/transcode_lease.go); the stall sweeper in the same process
-- re-queues an asset whose heartbeat has gone stale and gives up after
-- transcode_attempts re-queues (internal/store/postgres/transcode_lease.go).
--
--   transcode_heartbeat_at  last proof of life from the job working on this
--                           asset. NULL = not started since it was last
--                           queued (a re-queue clears it). Never touches
--                           updated_at, so it cannot disturb any ordering.
--   transcode_attempts      re-queues the stall sweeper has made since the
--                           asset was last queued by an upload or by an
--                           operator reprocess (both reset it to 0).
--
-- Neither column references media, so reclaim_policy.go (which classifies
-- media-referencing columns in OTHER tables) has nothing to classify.
ALTER TABLE media_assets
    ADD COLUMN IF NOT EXISTS transcode_heartbeat_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS transcode_attempts     INT NOT NULL DEFAULT 0;

-- "Is any transcode alive right now?" — the sweeper's pipeline-busy probe
-- reads the newest heartbeat. Only rows that ever had a job are indexed.
CREATE INDEX IF NOT EXISTS idx_media_assets_transcode_heartbeat
    ON media_assets (transcode_heartbeat_at)
    WHERE transcode_heartbeat_at IS NOT NULL;
