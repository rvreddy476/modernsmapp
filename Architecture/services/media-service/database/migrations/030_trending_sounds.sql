-- Trending sounds (2026-10-24).
--
-- The ranking GET /v1/audio/trending serves, computed every 15 minutes by
-- the trending worker (internal/service/trending_sounds.go) from the uses
-- post-service counts (GET /v1/internal/sound-uses) and stored here, whole:
-- each run replaces every row in one transaction, so a reader sees one
-- run's list or the next one's, never a mixture.
--
--   rank          1 = most trending. Unique: the list's order and its cursor.
--   score         uses_7d weighted by recency (last 24 h x3, days 2-3 x2,
--                 days 4-7 x1).
--   uses_7d       distinct (author, day) uses in the last 7 days.
--   uses_prev_7d  the same for the 7 days before.
--   rising        uses_7d >= 3 and (uses_prev_7d = 0 or uses_7d >= 2 x uses_prev_7d).
--   computed_at   the run that wrote the row.
--
-- A row exists only for a sound that had at least 3 uses and was playable
-- to everyone when the run checked it; every read re-checks the sound for
-- its viewer as well.
CREATE TABLE IF NOT EXISTS audio_trending_sounds (
    audio_track_id UUID PRIMARY KEY REFERENCES audio_tracks(id) ON DELETE CASCADE,
    rank           INT NOT NULL CHECK (rank > 0),
    score          INT NOT NULL CHECK (score >= 0),
    uses_7d        INT NOT NULL CHECK (uses_7d >= 0),
    uses_prev_7d   INT NOT NULL CHECK (uses_prev_7d >= 0),
    rising         BOOLEAN NOT NULL,
    computed_at    TIMESTAMPTZ NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_audio_trending_sounds_rank
    ON audio_trending_sounds (rank);

CREATE INDEX IF NOT EXISTS idx_audio_trending_sounds_rising
    ON audio_trending_sounds (rank)
    WHERE rising;

-- The last run: one row. updated_at on the wire, and what tells a replica
-- that another one already computed this window.
CREATE TABLE IF NOT EXISTS audio_trending_runs (
    singleton   BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    computed_at TIMESTAMPTZ NOT NULL,
    sound_count INT NOT NULL CHECK (sound_count >= 0)
);
