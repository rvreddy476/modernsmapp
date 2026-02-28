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
