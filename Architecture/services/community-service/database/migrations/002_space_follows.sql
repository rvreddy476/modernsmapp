-- Migration 002: Community space follows for selective space notifications
CREATE TABLE IF NOT EXISTS community_space_follows (
    community_id    UUID NOT NULL,
    space_id        UUID NOT NULL,
    user_id         TEXT NOT NULL,
    followed_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (community_id, space_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_space_follows_user ON community_space_follows(user_id, community_id);
CREATE INDEX IF NOT EXISTS idx_space_follows_space ON community_space_follows(space_id);
