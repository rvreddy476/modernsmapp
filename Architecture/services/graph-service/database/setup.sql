-- Database setup for Architecture/graph-service

CREATE TABLE IF NOT EXISTS follows (
    follower_id UUID NOT NULL,
    followee_id UUID NOT NULL,
    source TEXT NOT NULL DEFAULT 'profile',
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (follower_id, followee_id)
);

CREATE INDEX IF NOT EXISTS idx_follows_followee_desc ON follows(followee_id, created_at DESC);
-- HG2: GetFollowing cursor pagination requires the symmetric index so a
-- (follower_id, created_at, followee_id) keyset query is O(log n).
CREATE INDEX IF NOT EXISTS idx_follows_follower_desc ON follows(follower_id, created_at DESC);

CREATE TABLE IF NOT EXISTS blocks (
    blocker_id UUID NOT NULL,
    blocked_id UUID NOT NULL,
    reason VARCHAR(32),
    context VARCHAR(32),
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

-- Connection requests (explicit two-way trust request, not mutual-follow).
CREATE TABLE IF NOT EXISTS connection_requests (
    sender_id UUID NOT NULL,
    receiver_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    source TEXT NOT NULL DEFAULT 'profile',
    message VARCHAR(280),
    risk_score SMALLINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    responded_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '30 days'),
    PRIMARY KEY (sender_id, receiver_id)
);

CREATE INDEX IF NOT EXISTS idx_connection_req_receiver ON connection_requests(receiver_id, status);
CREATE INDEX IF NOT EXISTS idx_connection_requests_expiry
    ON connection_requests (expires_at) WHERE status = 'pending';

-- Connections (bidirectional, normalized: user_a < user_b).
CREATE TABLE IF NOT EXISTS connections (
    user_a UUID NOT NULL,
    user_b UUID NOT NULL,
    source_request_id BIGINT,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_a, user_b)
);

CREATE INDEX IF NOT EXISTS idx_connections_b ON connections(user_b);
