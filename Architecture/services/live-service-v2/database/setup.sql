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

-- Phase 2: minimal chat overlay. Persistent buffer so a late viewer
-- can replay the last N messages on page load; live fanout is via
-- Redis pub/sub on channel `livestream:chat:{streamID}` consumed by
-- the ws-gateway via its dynamic subscribe_* pattern. Mute /
-- word-filter / pin features (v1 live-service equivalents) are out
-- of scope for this Phase A.
CREATE TABLE IF NOT EXISTS live_chat_messages (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    stream_id   UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL,
    text        TEXT NOT NULL CHECK (char_length(text) BETWEEN 1 AND 500),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_live_chat_messages_stream
    ON live_chat_messages(stream_id, created_at DESC);

-- Phase B chat moderation: per-stream mutes, word filters, and a single
-- "is_pinned" pointer hung off live_chat_messages. Surfaced via the host
-- moderation endpoints on /v1/livestream/streams/:id/chat/{mute,word-filters,pin}.

-- Per-stream mutes. Creator can mute a viewer for the rest of the
-- stream; auto-cleared on stream end via ON DELETE CASCADE if/when
-- the stream row is dropped.
CREATE TABLE IF NOT EXISTS live_chat_mutes (
    stream_id   UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL,
    muted_by    UUID NOT NULL,
    muted_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (stream_id, user_id)
);

-- Per-stream word filters. Free-text substring match; case-insensitive
-- enforced in Go (lower(text) LIKE '%lower(word)%').
CREATE TABLE IF NOT EXISTS live_chat_word_filters (
    stream_id   UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    word        TEXT NOT NULL CHECK (char_length(word) BETWEEN 1 AND 100),
    added_by    UUID NOT NULL,
    added_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (stream_id, word)
);

-- Pinned messages. One pin per stream at a time — the latest pin
-- replaces any prior. Stored as a column on live_chat_messages
-- rather than a side table so the pin travels with the message row.
ALTER TABLE live_chat_messages
    ADD COLUMN IF NOT EXISTS is_pinned BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS pinned_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_live_chat_pinned
    ON live_chat_messages(stream_id) WHERE is_pinned = TRUE;
