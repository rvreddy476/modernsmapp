-- 005_live_surfaces.sql — live in PostTube and Reels, free hearts, top
-- supporters, the founding creator badge (2 Oct 2026).
--
-- Streams gain
--   orientation        'landscape' (PostTube, every row before this
--                      migration) or 'portrait' (the Reels Live tab);
--   category           a slug of post-service's video taxonomy, '' = none
--                      (validated in Go against GET /v1/posts/categories);
--   heart_count        the total of free hearts sent on the stream;
--   recording_post_id  the video post the recording became. Nothing writes
--                      it yet: post-service makes that post from
--                      live.stream.vod_ready and does not report the id
--                      back, so the column is NULL until it does.
--
-- live_stream_reminders: "Notify me" on a scheduled stream. The rows are
-- kept after the stream starts: notification-service pages them through
-- GET /v1/livestream/internal/streams/:id/reminders when it consumes
-- live.stream.started.
--
-- live_stream_hearts: hearts per viewer per stream (capped in Go at 10,000;
-- further hearts are accepted and ignored). created_at is the viewer's first
-- heart, one half of "earliest first activity" in the supporters ranking.
--
-- live_creator_badges: permanent creator badges. A revoked badge keeps its
-- row with revoked_at set, so the PRIMARY KEY stops it being granted again.
--
-- Additive and idempotent.

ALTER TABLE live_streams
    ADD COLUMN IF NOT EXISTS orientation       TEXT   NOT NULL DEFAULT 'landscape',
    ADD COLUMN IF NOT EXISTS category          TEXT   NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS heart_count       BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS recording_post_id UUID;

ALTER TABLE live_streams DROP CONSTRAINT IF EXISTS live_streams_orientation_check;
ALTER TABLE live_streams ADD CONSTRAINT live_streams_orientation_check
    CHECK (orientation IN ('landscape','portrait'));

-- Discovery: what is on air (by viewers), what is coming, and one creator's
-- streams (the channel Live tab).
CREATE INDEX IF NOT EXISTS idx_live_streams_on_air
    ON live_streams (viewer_count DESC, started_at DESC, id DESC)
    WHERE status IN ('live','reconnecting');
CREATE INDEX IF NOT EXISTS idx_live_streams_upcoming
    ON live_streams (scheduled_at, id)
    WHERE status = 'scheduled' AND scheduled_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_live_streams_creator_status
    ON live_streams (creator_user_id, status);

CREATE TABLE IF NOT EXISTS live_stream_reminders (
    stream_id  UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (stream_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_live_stream_reminders_user
    ON live_stream_reminders (user_id);

CREATE TABLE IF NOT EXISTS live_stream_hearts (
    stream_id  UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL,
    hearts     INT  NOT NULL DEFAULT 0 CHECK (hearts >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (stream_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_live_stream_hearts_user
    ON live_stream_hearts (user_id);

-- stream_id is the stream that earned the badge. No foreign key: the badge
-- is the creator's, not the stream's.
CREATE TABLE IF NOT EXISTS live_creator_badges (
    user_id        UUID NOT NULL,
    badge          TEXT NOT NULL CHECK (badge IN ('founding_creator')),
    granted_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    stream_id      UUID,
    revoked_at     TIMESTAMPTZ,
    revoked_by     UUID,
    revoked_reason TEXT,
    PRIMARY KEY (user_id, badge)
);

-- Backfill: every creator with a stream that already ended after at least
-- five minutes on air (the default LIVE_FOUNDING_MIN_LIVE; the founding
-- window is open when this runs). Granted at the end of the first such
-- stream.
INSERT INTO live_creator_badges (user_id, badge, granted_at, stream_id)
SELECT DISTINCT ON (creator_user_id) creator_user_id, 'founding_creator', ended_at, id
FROM live_streams
WHERE status = 'ended'
  AND started_at IS NOT NULL
  AND ended_at IS NOT NULL
  AND ended_at - started_at >= INTERVAL '5 minutes'
ORDER BY creator_user_id, ended_at ASC, id ASC
ON CONFLICT (user_id, badge) DO NOTHING;
