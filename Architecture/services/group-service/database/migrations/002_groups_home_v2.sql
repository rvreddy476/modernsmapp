-- 002_groups_home_v2.sql: Group Home v2 — pinned posts, member stats, ban reason, request counter
BEGIN;

-- M1: Pinned posts
ALTER TABLE posts ADD COLUMN IF NOT EXISTS pinned_at TIMESTAMPTZ;

-- M2: Group member stats cache table
CREATE TABLE IF NOT EXISTS group_member_stats (
    group_id        UUID NOT NULL,
    user_id         UUID NOT NULL,
    post_count      INT DEFAULT 0,
    sparks_received INT DEFAULT 0,
    updated_at      TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (group_id, user_id)
);

-- M3: Ban reason on group_members
ALTER TABLE group_members ADD COLUMN IF NOT EXISTS removal_reason TEXT;

-- M4: Pending request count on groups
ALTER TABLE groups ADD COLUMN IF NOT EXISTS pending_request_count INT NOT NULL DEFAULT 0;

COMMIT;
