-- Post Reposts (Echo) table
CREATE TABLE IF NOT EXISTS post_reposts (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL,
    original_post_id    UUID NOT NULL,
    repost_type         TEXT NOT NULL CHECK (repost_type IN ('plain', 'quote')),
    quote_text          TEXT,
    visibility          TEXT NOT NULL DEFAULT 'public',
    status              TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'undone')),
    source_context_type TEXT CHECK (source_context_type IS NULL OR source_context_type IN ('feed', 'post_detail', 'profile', 'search', 'stash')),
    source_context_id   UUID,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at          TIMESTAMPTZ
);

-- Only one active repost per user per original post
CREATE UNIQUE INDEX IF NOT EXISTS idx_post_reposts_active_unique
    ON post_reposts (user_id, original_post_id)
    WHERE status = 'active';

-- For user's reposts profile feed
CREATE INDEX IF NOT EXISTS idx_post_reposts_user
    ON post_reposts (user_id, created_at DESC)
    WHERE status = 'active';

-- For "who reposted this" list
CREATE INDEX IF NOT EXISTS idx_post_reposts_original
    ON post_reposts (original_post_id, created_at DESC)
    WHERE status = 'active';

-- Add repost_count to engagement counts
ALTER TABLE post_engagement_counts
    ADD COLUMN IF NOT EXISTS repost_count INT NOT NULL DEFAULT 0;
