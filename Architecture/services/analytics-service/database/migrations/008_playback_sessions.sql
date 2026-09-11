-- 008: server-side playback sessions (plan Phase 1A, audit M-08 / M-09).
--
-- Until now play_end was the sole carrier of a view. Heartbeats were
-- persisted and fed nothing; a client that lost its final event — tab
-- closed, app killed, queue flushed after the 24h window — lost the whole
-- view, and the most-watched sessions were the likeliest to lose it.
--
-- This table is the session as the server saw it, one row per
-- (viewer, client session, content), written in the SAME transaction as
-- the ingest receipt so the durability contract behind the 202 covers
-- it. PostgreSQL rather than Scylla for that reason, and because Scylla
-- is unset in every deploy file.
--
-- Update semantics (see internal/store/postgres/sessions.go): every
-- running total is the GREATEST of what has been reported, never a sum
-- of increments. The client queue is at-least-once and a re-sent
-- heartbeat carries a fresh event id that receipts do not collapse,
-- while the tracker's totals are monotonic — so GREATEST is idempotent
-- under replay and reorder and a sum is not. seek_count is the one sum.
--
-- Column list and names are a contract with the aggregation branch that
-- reads this table (plan 1A); change them there and here together.

CREATE TABLE IF NOT EXISTS analytics.playback_sessions (
    actor_id            UUID NOT NULL,
    session_id          UUID NOT NULL,
    content_id          UUID NOT NULL,
    -- Snapshotted from analytics.content_ownership at first sight.
    creator_id          UUID NOT NULL,
    content_type        TEXT NOT NULL,
    -- first_seen is the bucket attribution key for the aggregators.
    first_seen          TIMESTAMPTZ NOT NULL,
    last_seen           TIMESTAMPTZ NOT NULL,
    content_duration_ms BIGINT NOT NULL DEFAULT 0,
    -- watched_ms is clamped to duration x (loop_count + 1);
    -- watched_ms_reported is the client's figure, kept for audit only.
    watched_ms          BIGINT NOT NULL DEFAULT 0,
    watched_ms_reported BIGINT NOT NULL DEFAULT 0,
    max_playhead_ms     BIGINT NOT NULL DEFAULT 0,
    max_continuous_ms   BIGINT NOT NULL DEFAULT 0,
    loop_count          INTEGER NOT NULL DEFAULT 0,
    seek_count          INTEGER NOT NULL DEFAULT 0,
    playback_speed      DOUBLE PRECISION NOT NULL DEFAULT 1,
    -- One bit per second of the content, set when a heartbeat covered
    -- that second. NULL until the first heartbeat lands.
    coverage            BYTEA,
    covered_ms          BIGINT NOT NULL DEFAULT 0,
    -- percent_viewed is the legacy measure (watched / duration); it
    -- counts rewatched seconds. percent_covered is unique coverage and
    -- is what completions and the quality score read after cutover.
    percent_viewed      DOUBLE PRECISION NOT NULL DEFAULT 0,
    percent_covered     DOUBLE PRECISION NOT NULL DEFAULT 0,
    is_self_view        BOOLEAN NOT NULL DEFAULT false,
    end_reason          TEXT,
    finalized_at        TIMESTAMPTZ,
    finalize_reason     TEXT CHECK (finalize_reason IN ('play_end', 'inactivity', 'superseded')),
    is_display_view     BOOLEAN NOT NULL DEFAULT false,
    view_score          DOUBLE PRECISION NOT NULL DEFAULT 0,
    source              TEXT NOT NULL DEFAULT 'live' CHECK (source IN ('live', 'backfill')),
    PRIMARY KEY (actor_id, session_id, content_id)
);

-- The finaliser walks open sessions by inactivity.
CREATE INDEX IF NOT EXISTS idx_playback_sessions_open
    ON analytics.playback_sessions (last_seen)
    WHERE finalized_at IS NULL;

-- Finalised sessions by when they closed, for reconciliation and audit.
CREATE INDEX IF NOT EXISTS idx_playback_sessions_finalized
    ON analytics.playback_sessions (finalized_at)
    WHERE finalized_at IS NOT NULL;

-- The aggregators rebuild whole buckets keyed on first_seen.
CREATE INDEX IF NOT EXISTS idx_playback_sessions_bucket
    ON analytics.playback_sessions (first_seen, content_id)
    WHERE finalized_at IS NOT NULL;

-- A new play_start for the same viewer and content supersedes any open
-- session; that lookup is by (actor, content) over open rows.
CREATE INDEX IF NOT EXISTS idx_playback_sessions_actor_content_open
    ON analytics.playback_sessions (actor_id, content_id)
    WHERE finalized_at IS NULL;
