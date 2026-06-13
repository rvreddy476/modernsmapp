-- 001_init.sql — initial schema for live-service-v2 (LiveKit-backed).
-- This is identical to setup.sql; both are kept so a fresh dev DB can
-- bootstrap via setup.sql (idempotent) and prod can apply migrations in
-- the standard versioned order.

CREATE TABLE IF NOT EXISTS live_streams (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_user_id UUID NOT NULL,
    livekit_room    TEXT NOT NULL UNIQUE,
    title           TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    cover_media_id  UUID,
    status          TEXT NOT NULL DEFAULT 'scheduled'
                       CHECK (status IN ('scheduled','live','ended','failed')),
    visibility      TEXT NOT NULL DEFAULT 'public'
                       CHECK (visibility IN ('public','followers','paid')),
    scheduled_at    TIMESTAMPTZ,
    started_at      TIMESTAMPTZ,
    ended_at        TIMESTAMPTZ,
    viewer_peak     INT NOT NULL DEFAULT 0,
    recording_url   TEXT,
    recording_duration_seconds INT,
    egress_id       TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_live_streams_creator ON live_streams(creator_user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_live_streams_status_live ON live_streams(status) WHERE status = 'live';
CREATE INDEX IF NOT EXISTS idx_live_streams_visibility_live ON live_streams(visibility, started_at DESC) WHERE status = 'live';

CREATE TABLE IF NOT EXISTS live_viewer_events (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    stream_id    UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    user_id      UUID NOT NULL,
    event_type   TEXT NOT NULL CHECK (event_type IN ('join','leave')),
    occurred_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_live_viewer_events_stream ON live_viewer_events(stream_id, occurred_at);
