-- live-service-v2 — LiveKit-backed live streaming schema.
--
-- This service is the v2 replacement for the legacy RTMP/OBS live-service:
-- the broadcaster talks to a LiveKit SFU directly from the browser via WebRTC,
-- recording is performed by LiveKit Egress to an S3-compatible bucket
-- (MinIO in dev), and viewers receive subscriber-only LiveKit tokens after
-- a visibility check.
--
-- Schema lives in the default search_path (public) — every other service
-- here uses gen_random_uuid which is already enabled cluster-wide, so we
-- do NOT create the pgcrypto extension explicitly.

CREATE TABLE IF NOT EXISTS live_streams (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_user_id UUID NOT NULL,
    livekit_room    TEXT NOT NULL UNIQUE,
    title           TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    cover_media_id  UUID,
    status          TEXT NOT NULL DEFAULT 'scheduled'
                       CHECK (status IN ('scheduled','live','ended','failed')),
    -- privacy: public, followers-only, paid (paid hooks into commerce later)
    visibility      TEXT NOT NULL DEFAULT 'public'
                       CHECK (visibility IN ('public','followers','paid')),
    scheduled_at    TIMESTAMPTZ,
    started_at      TIMESTAMPTZ,
    ended_at        TIMESTAMPTZ,
    viewer_peak     INT NOT NULL DEFAULT 0,
    -- VOD pointer once Egress finishes
    recording_url   TEXT,
    recording_duration_seconds INT,
    -- LiveKit Egress job ID we get back when StartEgress fires.
    -- Stored so EndStream can call StopEgress idempotently.
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
