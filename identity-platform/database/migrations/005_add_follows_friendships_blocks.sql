-- Migration 005: Add follows, friendships, and blocks tables to profile schema
-- Depends on: 001_enum_types.sql
-- Run against: identity_db

\connect identity_db;

-- Follows table
CREATE TABLE IF NOT EXISTS profile.follows (
    id              UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    follower_id     UUID            NOT NULL REFERENCES profile.profiles(user_id) ON DELETE CASCADE,
    following_id    UUID            NOT NULL REFERENCES profile.profiles(user_id) ON DELETE CASCADE,
    status          follow_status   NOT NULL DEFAULT 'active',
    created_at      TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    UNIQUE(follower_id, following_id),
    CHECK (follower_id != following_id)
);

CREATE INDEX IF NOT EXISTS idx_follows_follower  ON profile.follows(follower_id);
CREATE INDEX IF NOT EXISTS idx_follows_following ON profile.follows(following_id);

-- Friendships table
CREATE TABLE IF NOT EXISTS profile.friendships (
    id              UUID                PRIMARY KEY DEFAULT gen_random_uuid(),
    requester_id    UUID                NOT NULL REFERENCES profile.profiles(user_id) ON DELETE CASCADE,
    addressee_id    UUID                NOT NULL REFERENCES profile.profiles(user_id) ON DELETE CASCADE,
    status          friendship_status   NOT NULL DEFAULT 'pending',
    created_at      TIMESTAMPTZ         NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ         NOT NULL DEFAULT NOW(),
    UNIQUE(requester_id, addressee_id),
    CHECK (requester_id != addressee_id)
);

CREATE INDEX IF NOT EXISTS idx_friendships_requester ON profile.friendships(requester_id, status);
CREATE INDEX IF NOT EXISTS idx_friendships_addressee ON profile.friendships(addressee_id, status);

-- Blocks table
CREATE TABLE IF NOT EXISTS profile.blocks (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    blocker_id  UUID        NOT NULL REFERENCES profile.profiles(user_id) ON DELETE CASCADE,
    blocked_id  UUID        NOT NULL REFERENCES profile.profiles(user_id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(blocker_id, blocked_id),
    CHECK (blocker_id != blocked_id)
);

CREATE INDEX IF NOT EXISTS idx_blocks_blocker ON profile.blocks(blocker_id);
CREATE INDEX IF NOT EXISTS idx_blocks_blocked ON profile.blocks(blocked_id);
