# Module: live-service

_Auto-extracted from source (DDL + route registrations). Verbatim — the source of truth is the code._

## HTTP routes
```
DELETE /streams/:streamId/mutes/:userId
DELETE /streams/:streamId/word-filters
GET /hosts/:hostId/streams
GET /:roomId
GET /:roomId/members
GET /schedule/upcoming
GET /streams
GET /streams/:streamId
GET /streams/:streamId/chat
GET /streams/:streamId/dvr
GET /streams/:streamId/gifts
GET /streams/:streamId/gifts/leaderboard
GET /streams/:streamId/guests
GET /streams/:streamId/mutes
GET /streams/:streamId/playback/*asset
GET /streams/:streamId/polls
GET /streams/:streamId/viewers
GET /streams/:streamId/word-filters
PATCH /streams/:streamId/guests/:userId/status
POST /:roomId/end
POST /:roomId/join
POST /:roomId/leave
POST /:roomId/start
POST /schedule
POST /streams
POST /streams/:streamId/chat
POST /streams/:streamId/chat/:messageId/pin
POST /streams/:streamId/end
POST /streams/:streamId/gifts
POST /streams/:streamId/go-live
POST /streams/:streamId/guests
POST /streams/:streamId/join
POST /streams/:streamId/leave
POST /streams/:streamId/like
POST /streams/:streamId/mutes
POST /streams/:streamId/polls
POST /streams/:streamId/polls/:pollId/vote
POST /streams/:streamId/publish/whip
POST /streams/:streamId/word-filters
GROUP /v1/audio-rooms
GROUP /v1/live
```

## Database schema (CREATE TABLE — full column DDL)
```sql
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

CREATE TABLE IF NOT EXISTS live.chat_messages (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    stream_id   UUID NOT NULL REFERENCES live.streams(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL,
    message     TEXT NOT NULL CHECK (char_length(message) BETWEEN 1 AND 500),
    is_pinned   BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS live.viewer_sessions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    stream_id   UUID NOT NULL REFERENCES live.streams(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL,
    joined_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    left_at     TIMESTAMPTZ,
    duration_secs INTEGER NOT NULL DEFAULT 0
);

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

CREATE TABLE IF NOT EXISTS live_guests (
    stream_id   UUID NOT NULL,
    user_id     UUID NOT NULL,
    role        TEXT NOT NULL DEFAULT 'guest' CHECK (role IN ('co_host','guest','moderator')),
    status      TEXT NOT NULL DEFAULT 'invited' CHECK (status IN ('invited','accepted','declined','removed')),
    invited_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    joined_at   TIMESTAMPTZ,
    PRIMARY KEY (stream_id, user_id)
);

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

CREATE TABLE IF NOT EXISTS live_dvr_segments (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    stream_id   UUID NOT NULL,
    segment_url TEXT NOT NULL,
    start_ts    TIMESTAMPTZ NOT NULL,
    duration_ms INT NOT NULL,
    segment_num INT NOT NULL
);

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

```
