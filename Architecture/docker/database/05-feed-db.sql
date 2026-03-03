-- =============================================================================
-- FEED_DB — Dedicated feed service database (v2.1)
-- Run against: feed_db
-- =============================================================================

\connect feed_db;

-- ============================================================
-- Feed service tables (moved from app-db)
-- ============================================================
CREATE TABLE IF NOT EXISTS celeb_authors (
    author_id  UUID PRIMARY KEY,
    is_celeb   BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS user_interactions (
    viewer_id         UUID NOT NULL,
    author_id         UUID NOT NULL,
    like_rate         FLOAT NOT NULL DEFAULT 0.0,
    comment_rate      FLOAT NOT NULL DEFAULT 0.0,
    share_rate        FLOAT NOT NULL DEFAULT 0.0,
    total_score       FLOAT NOT NULL DEFAULT 0.0,
    author_penalty    FLOAT NOT NULL DEFAULT 0.0,
    author_boost      FLOAT NOT NULL DEFAULT 0.0,
    interaction_count INTEGER NOT NULL DEFAULT 0,
    last_interaction  TIMESTAMPTZ,
    computed_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (viewer_id, author_id)
);
CREATE INDEX IF NOT EXISTS idx_interactions_viewer ON user_interactions (viewer_id);

CREATE TABLE IF NOT EXISTS viewer_media_prefs (
    user_id         UUID PRIMARY KEY,
    video_p95_dwell FLOAT DEFAULT 0,
    image_p95_dwell FLOAT DEFAULT 0,
    text_p95_dwell  FLOAT DEFAULT 0,
    preferred_type  TEXT DEFAULT 'text',
    computed_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS post_impressions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL,
    post_id       UUID NOT NULL,
    media_type    TEXT,
    dwell_seconds FLOAT NOT NULL DEFAULT 0,
    action        TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_impressions_user_created ON post_impressions (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_impressions_post ON post_impressions (post_id);

CREATE TABLE IF NOT EXISTS user_preferences (
    user_id    UUID PRIMARY KEY,
    feed_mode  TEXT NOT NULL DEFAULT 'chronological',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============================================================
-- feed_db outbox + inbox
-- ============================================================
CREATE TABLE IF NOT EXISTS outbox_events (
    id            BIGSERIAL PRIMARY KEY,
    event_type    TEXT NOT NULL,
    partition_key TEXT NOT NULL DEFAULT '',
    payload       JSONB NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at  TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_feed_outbox_unpublished ON outbox_events(id) WHERE published_at IS NULL;

CREATE TABLE IF NOT EXISTS inbox_events (
    consumer_name TEXT NOT NULL,
    event_id      UUID NOT NULL,
    processed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (consumer_name, event_id)
);
CREATE INDEX IF NOT EXISTS idx_feed_inbox_cleanup ON inbox_events (processed_at);
