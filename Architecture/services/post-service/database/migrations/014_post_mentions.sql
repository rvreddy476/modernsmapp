-- Migration 014: Post mentions table for tracking @username mentions in posts
CREATE TABLE IF NOT EXISTS post_mentions (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id             UUID NOT NULL,
    post_type           VARCHAR(20) NOT NULL DEFAULT 'post',
    mentioned_user_id   TEXT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(post_id, mentioned_user_id)
);
CREATE INDEX IF NOT EXISTS idx_mentions_user ON post_mentions(mentioned_user_id, created_at DESC);
