-- Database setup for Architecture/graph-service

CREATE TABLE IF NOT EXISTS follows (
    follower_id UUID NOT NULL,
    followee_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (follower_id, followee_id)
);

CREATE INDEX IF NOT EXISTS idx_follows_followee_desc ON follows(followee_id, created_at DESC);

CREATE TABLE IF NOT EXISTS blocks (
    blocker_id UUID NOT NULL,
    blocked_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (blocker_id, blocked_id)
);

CREATE TABLE IF NOT EXISTS counts (
    user_id UUID PRIMARY KEY,
    follower_count BIGINT DEFAULT 0,
    following_count BIGINT DEFAULT 0,
    friend_count BIGINT DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL
);

-- Friend requests (explicit, not mutual-follow)
CREATE TABLE IF NOT EXISTS friend_requests (
    sender_id UUID NOT NULL,
    receiver_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (sender_id, receiver_id)
);

CREATE INDEX IF NOT EXISTS idx_friend_req_receiver ON friend_requests(receiver_id, status);

-- Friends (bidirectional, normalized: user_a < user_b)
CREATE TABLE IF NOT EXISTS friends (
    user_a UUID NOT NULL,
    user_b UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_a, user_b)
);

CREATE INDEX IF NOT EXISTS idx_friends_b ON friends(user_b);
