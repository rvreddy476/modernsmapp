-- 010_follow_requests.sql — TikTok-style private accounts at the graph layer.
--
-- Reintroduces the follow_requests table that 009 dropped (SR-5): launch was
-- public-accounts-only, so shipping the table without the service/API behind
-- it would have implied a privacy control the product did not have. That
-- machinery exists now — identity user-service persists account_visibility,
-- graph-service resolves it, and POST /v1/graph/follow returns "requested"
-- for a private target — so the table returns WITH its enforcement.
--
-- One row per (requester, target) pair, upserted on re-request: a request
-- declined earlier may be sent again, and the row flips back to 'pending'
-- with a fresh created_at. Terminal rows ('accepted', 'declined',
-- 'cancelled') keep resolved_at for auditability. Idempotent.
CREATE TABLE IF NOT EXISTS follow_requests (
    requester_id UUID NOT NULL,
    target_id    UUID NOT NULL,
    status       TEXT NOT NULL CHECK (status IN ('pending', 'accepted', 'declined', 'cancelled')),
    created_at   TIMESTAMPTZ DEFAULT NOW(),
    resolved_at  TIMESTAMPTZ NULL,
    PRIMARY KEY (requester_id, target_id)
);

-- Incoming inbox: the target's pending requests, newest first.
CREATE INDEX IF NOT EXISTS idx_follow_requests_incoming
    ON follow_requests (target_id, created_at DESC)
    WHERE status = 'pending';

-- Outgoing: the requester's own pending requests (sent list, cancel path).
CREATE INDEX IF NOT EXISTS idx_follow_requests_outgoing
    ON follow_requests (requester_id, created_at DESC)
    WHERE status = 'pending';
