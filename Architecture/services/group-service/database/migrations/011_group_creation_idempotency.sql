-- A creator retry must resolve to the original group even after a process or
-- Redis restart. PostgreSQL is the canonical idempotency store.
CREATE TABLE IF NOT EXISTS group_creation_requests (
    creator_id UUID NOT NULL,
    idempotency_key TEXT NOT NULL,
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (creator_id, idempotency_key)
);
