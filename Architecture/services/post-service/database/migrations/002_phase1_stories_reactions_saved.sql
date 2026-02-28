-- Phase 1 migration: Stories, Multi-Reactions, Saved Collections, Hashtags/Mentions
-- Applied by post-service ensurePhaseSchema() at startup, this file is the canonical reference.

-- Extend posts table with hashtags, mentions, location, post_type, app_origin
ALTER TABLE posts ADD COLUMN IF NOT EXISTS hashtags TEXT[];
ALTER TABLE posts ADD COLUMN IF NOT EXISTS mentions UUID[];
ALTER TABLE posts ADD COLUMN IF NOT EXISTS location_name TEXT;
ALTER TABLE posts ADD COLUMN IF NOT EXISTS location_lat DOUBLE PRECISION;
ALTER TABLE posts ADD COLUMN IF NOT EXISTS location_lng DOUBLE PRECISION;
ALTER TABLE posts ADD COLUMN IF NOT EXISTS post_type TEXT NOT NULL DEFAULT 'standard';
ALTER TABLE posts ADD COLUMN IF NOT EXISTS app_origin TEXT;

CREATE INDEX IF NOT EXISTS idx_posts_hashtags ON posts USING GIN (hashtags);

-- Stories
CREATE TABLE IF NOT EXISTS stories (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    author_id       UUID NOT NULL,
    media_url       TEXT NOT NULL,
    media_type      TEXT NOT NULL,
    caption         TEXT NOT NULL DEFAULT '',
    stickers        JSONB,
    music_track     JSONB,
    visibility      TEXT NOT NULL DEFAULT 'public',
    view_count      INTEGER NOT NULL DEFAULT 0,
    expires_at      TIMESTAMPTZ NOT NULL,
    is_highlight    BOOLEAN NOT NULL DEFAULT FALSE,
    highlight_group TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_stories_author ON stories (author_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_stories_expiry ON stories (expires_at) WHERE is_highlight = FALSE;

-- Multi-reactions (replaces simple likes)
CREATE TABLE IF NOT EXISTS reactions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    target_type   TEXT NOT NULL,
    target_id     UUID NOT NULL,
    user_id       UUID NOT NULL,
    reaction_type TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(target_type, target_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_reactions_target ON reactions (target_type, target_id);
CREATE INDEX IF NOT EXISTS idx_reactions_user ON reactions (user_id, target_type, created_at DESC);

-- Saved items with collections
CREATE TABLE IF NOT EXISTS saved_items (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    target_type     TEXT NOT NULL,
    target_id       UUID NOT NULL,
    collection_name TEXT NOT NULL DEFAULT 'default',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(user_id, target_type, target_id)
);

CREATE INDEX IF NOT EXISTS idx_saved_items_user ON saved_items (user_id, collection_name, created_at DESC);
