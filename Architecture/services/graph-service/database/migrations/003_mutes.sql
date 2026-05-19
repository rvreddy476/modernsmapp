-- 003_mutes.sql: Add mute table for soft-block (mute without notification)
-- The mutes table lives in the dedicated `graph` schema, which must exist
-- before the table is created.
CREATE SCHEMA IF NOT EXISTS graph;

CREATE TABLE IF NOT EXISTS graph.mutes (
    muter_id   UUID NOT NULL,
    muted_id   UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (muter_id, muted_id)
);

CREATE INDEX IF NOT EXISTS idx_mutes_muter ON graph.mutes(muter_id);
