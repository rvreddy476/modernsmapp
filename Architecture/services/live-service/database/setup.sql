-- Live streaming service schema
CREATE SCHEMA IF NOT EXISTS live;

-- Live streams
CREATE TABLE IF NOT EXISTS live.streams (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    host_id         UUID NOT NULL,
    title           VARCHAR(200) NOT NULL DEFAULT '',
    description     TEXT DEFAULT '',
    thumbnail_url   TEXT,
    stream_key      VARCHAR(100) NOT NULL UNIQUE,
    status          VARCHAR(20) NOT NULL DEFAULT 'idle'
                    CHECK (status IN ('idle', 'live', 'ended')),
    visibility      VARCHAR(20) NOT NULL DEFAULT 'public'
                    CHECK (visibility IN ('public', 'followers', 'private')),
    peak_viewers    INTEGER NOT NULL DEFAULT 0,
    total_viewers   INTEGER NOT NULL DEFAULT 0,
    like_count      INTEGER NOT NULL DEFAULT 0,
    started_at      TIMESTAMPTZ,
    ended_at        TIMESTAMPTZ,
    duration_secs   INTEGER NOT NULL DEFAULT 0,
    replay_url      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_ls_host ON live.streams (host_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_ls_status ON live.streams (status, started_at DESC) WHERE status = 'live';

-- Live chat messages
CREATE TABLE IF NOT EXISTS live.chat_messages (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    stream_id   UUID NOT NULL REFERENCES live.streams(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL,
    message     TEXT NOT NULL CHECK (char_length(message) BETWEEN 1 AND 500),
    is_pinned   BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_lcm_stream ON live.chat_messages (stream_id, created_at DESC);

-- Viewer sessions (for analytics)
CREATE TABLE IF NOT EXISTS live.viewer_sessions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    stream_id   UUID NOT NULL REFERENCES live.streams(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL,
    joined_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    left_at     TIMESTAMPTZ,
    duration_secs INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_lvs_stream ON live.viewer_sessions (stream_id, joined_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_lvs_active ON live.viewer_sessions (stream_id, user_id) WHERE left_at IS NULL;

-- Scheduled streams
CREATE TABLE IF NOT EXISTS live.scheduled_streams (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    host_id         UUID NOT NULL,
    title           VARCHAR(200) NOT NULL,
    description     TEXT DEFAULT '',
    scheduled_at    TIMESTAMPTZ NOT NULL,
    reminder_sent   BOOLEAN NOT NULL DEFAULT FALSE,
    stream_id       UUID REFERENCES live.streams(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_lss_host ON live.scheduled_streams (host_id, scheduled_at);
CREATE INDEX IF NOT EXISTS idx_lss_upcoming ON live.scheduled_streams (scheduled_at) WHERE stream_id IS NULL;
