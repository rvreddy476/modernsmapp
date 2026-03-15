-- Migration 002: Feed hide and mute tables
-- Applied at: service startup

-- Feed hide: user hides a specific post from their feed
CREATE TABLE IF NOT EXISTS feed_hides (
    user_id   UUID NOT NULL,
    post_id   UUID NOT NULL,
    hidden_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, post_id)
);

-- Feed mutes: mute a user, topic (hashtag), or keyword
CREATE TABLE IF NOT EXISTS feed_mutes (
    user_id     UUID NOT NULL,
    target_type TEXT NOT NULL CHECK (target_type IN ('user','topic','hashtag')),
    target_id   TEXT NOT NULL,
    muted_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at  TIMESTAMPTZ,
    PRIMARY KEY (user_id, target_type, target_id)
);
CREATE INDEX IF NOT EXISTS idx_feed_mutes_user ON feed_mutes(user_id, target_type);
