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

-- Live Guests / Co-hosts
CREATE TABLE IF NOT EXISTS live_guests (
    stream_id   UUID NOT NULL,
    user_id     UUID NOT NULL,
    role        TEXT NOT NULL DEFAULT 'guest' CHECK (role IN ('co_host','guest','moderator')),
    status      TEXT NOT NULL DEFAULT 'invited' CHECK (status IN ('invited','accepted','declined','removed')),
    invited_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    joined_at   TIMESTAMPTZ,
    PRIMARY KEY (stream_id, user_id)
);

-- Live Polls
CREATE TABLE IF NOT EXISTS live_polls (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    stream_id   UUID NOT NULL,
    question    TEXT NOT NULL,
    options     JSONB NOT NULL,
    status      TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','closed')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ends_at     TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS live_poll_votes (
    poll_id     UUID NOT NULL REFERENCES live_polls(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL,
    option_id   TEXT NOT NULL,
    voted_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (poll_id, user_id)
);

-- Live Gifts
CREATE TABLE IF NOT EXISTS live_gifts (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    stream_id   UUID NOT NULL,
    sender_id   UUID NOT NULL,
    gift_type   TEXT NOT NULL CHECK (gift_type IN ('star','rocket','crown','diamond','heart')),
    gift_count  INT NOT NULL DEFAULT 1,
    value_inr   NUMERIC(8,2) NOT NULL,
    message     TEXT,
    sent_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_live_gifts_stream ON live_gifts(stream_id, sent_at DESC);

-- Live Moderation
CREATE TABLE IF NOT EXISTS live_mutes (
    stream_id   UUID NOT NULL,
    user_id     UUID NOT NULL,
    muted_by    UUID NOT NULL,
    muted_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (stream_id, user_id)
);

CREATE TABLE IF NOT EXISTS live_word_filters (
    stream_id   UUID NOT NULL,
    word        TEXT NOT NULL,
    added_by    UUID NOT NULL,
    PRIMARY KEY (stream_id, word)
);

-- DVR Segments
CREATE TABLE IF NOT EXISTS live_dvr_segments (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    stream_id   UUID NOT NULL,
    segment_url TEXT NOT NULL,
    start_ts    TIMESTAMPTZ NOT NULL,
    duration_ms INT NOT NULL,
    segment_num INT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_dvr_segments_stream ON live_dvr_segments(stream_id, segment_num);

-- Audio Rooms
CREATE TABLE IF NOT EXISTS audio_rooms (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    host_id            UUID NOT NULL,
    topic              TEXT NOT NULL,
    description        TEXT NOT NULL DEFAULT '',
    type               TEXT NOT NULL DEFAULT 'open' CHECK (type IN ('open','invite_only','community')),
    community_id       UUID,
    status             TEXT NOT NULL DEFAULT 'scheduled' CHECK (status IN ('scheduled','live','ended')),
    scheduled_at       TIMESTAMPTZ,
    started_at         TIMESTAMPTZ,
    ended_at           TIMESTAMPTZ,
    listener_count     INT NOT NULL DEFAULT 0,
    recording_enabled  BOOLEAN NOT NULL DEFAULT FALSE,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_audio_rooms_status ON audio_rooms(status, started_at DESC);

CREATE TABLE IF NOT EXISTS audio_room_members (
    room_id     UUID NOT NULL REFERENCES audio_rooms(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL,
    role        TEXT NOT NULL DEFAULT 'listener' CHECK (role IN ('host','co_host','speaker','listener')),
    hand_raised BOOLEAN NOT NULL DEFAULT FALSE,
    is_muted    BOOLEAN NOT NULL DEFAULT FALSE,
    joined_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    left_at     TIMESTAMPTZ,
    PRIMARY KEY (room_id, user_id)
);

CREATE TABLE IF NOT EXISTS audio_room_recordings (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id              UUID NOT NULL REFERENCES audio_rooms(id),
    recording_url        TEXT,
    consent_acknowledged BOOLEAN NOT NULL DEFAULT FALSE,
    duration_ms          INT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
