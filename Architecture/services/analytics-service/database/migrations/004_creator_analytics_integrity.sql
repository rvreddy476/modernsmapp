-- Module 6: canonical content ownership and durable client-ingest receipts.
-- Money must never depend on these tables; they are analytics authority only.

CREATE TABLE IF NOT EXISTS analytics.content_ownership (
    content_id   UUID PRIMARY KEY,
    creator_id   UUID NOT NULL,
    content_type TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL,
    projected_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_content_ownership_creator
    ON analytics.content_ownership (creator_id, created_at DESC);

CREATE TABLE IF NOT EXISTS analytics.ingest_receipts (
    event_id     TEXT PRIMARY KEY CHECK (length(event_id) BETWEEN 16 AND 128),
    actor_id     UUID NOT NULL,
    session_id   UUID NOT NULL,
    content_id   UUID NOT NULL REFERENCES analytics.content_ownership(content_id) ON DELETE RESTRICT,
    event_type   TEXT NOT NULL,
    accepted_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (actor_id, session_id, content_id, event_type)
);

