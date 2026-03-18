-- 012_profile_stats.sql: Dedicated profile stats cache table
BEGIN;

CREATE TABLE IF NOT EXISTS profile.profile_stats (
    user_id        UUID PRIMARY KEY REFERENCES profile.profiles(user_id) ON DELETE CASCADE,
    post_count     INT NOT NULL DEFAULT 0,
    follower_count INT NOT NULL DEFAULT 0,
    following_count INT NOT NULL DEFAULT 0,
    friend_count   INT NOT NULL DEFAULT 0,
    total_sparks   INT NOT NULL DEFAULT 0,
    is_creator     BOOLEAN NOT NULL DEFAULT false,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_profile_stats_creator ON profile.profile_stats(is_creator) WHERE is_creator = true;

COMMIT;
