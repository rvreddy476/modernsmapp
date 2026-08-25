-- Database setup for Architecture/feed-service

CREATE TABLE IF NOT EXISTS celeb_authors (
    author_id UUID PRIMARY KEY,
    is_celeb BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS event_dedup (
    event_id UUID PRIMARY KEY,
    processed_at TIMESTAMPTZ NOT NULL
);

-- v2.0: Ranking signal tables

CREATE TABLE IF NOT EXISTS user_interactions (
    viewer_id       UUID NOT NULL,
    author_id       UUID NOT NULL,
    like_rate       FLOAT NOT NULL DEFAULT 0.0,
    comment_rate    FLOAT NOT NULL DEFAULT 0.0,
    share_rate      FLOAT NOT NULL DEFAULT 0.0,
    total_score     FLOAT NOT NULL DEFAULT 0.0,
    author_penalty  FLOAT NOT NULL DEFAULT 0.0,
    author_boost    FLOAT NOT NULL DEFAULT 0.0,
    interaction_count INTEGER NOT NULL DEFAULT 0,
    last_interaction TIMESTAMPTZ,
    computed_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
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
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    post_id         UUID NOT NULL,
    media_type      TEXT,
    dwell_seconds   FLOAT NOT NULL DEFAULT 0,
    action          TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_impressions_user_created ON post_impressions (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_impressions_post ON post_impressions (post_id);

CREATE TABLE IF NOT EXISTS user_preferences (
    user_id    UUID PRIMARY KEY,
    feed_mode  TEXT NOT NULL DEFAULT 'chronological',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- migration 003 (Module 1 P0-4): explicit viewer long-video frequency for
-- the social home feed. hidden = hard exclusion; the others are
-- multiplier + composition-target tiers. Does not affect PostTube
-- surfaces, subscriptions, or direct links.
ALTER TABLE user_preferences
    ADD COLUMN IF NOT EXISTS long_video_frequency TEXT NOT NULL DEFAULT 'balanced'
        CHECK (long_video_frequency IN ('hidden','reduced','balanced','preferred'));

-- migration 003 (Module 1 P0-1): normalized per-post main-feed distribution
-- state, written from PostCreated/PostDistributionUpdated events. rev is
-- the post-service monotonic distribution_rev — the upsert only applies
-- when the incoming rev is newer, so replayed/reordered events are safe.
-- Absence of a row = legacy post = eligible for social home.
CREATE TABLE IF NOT EXISTS feed_distribution (
    post_id    UUID PRIMARY KEY,
    main_feed  BOOLEAN NOT NULL DEFAULT TRUE,
    rev        BIGINT  NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_feed_distribution_excluded
    ON feed_distribution (post_id) WHERE main_feed = FALSE;
