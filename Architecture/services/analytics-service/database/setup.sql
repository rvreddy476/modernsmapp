-- Database setup for Architecture/analytics-service

CREATE SCHEMA IF NOT EXISTS analytics;

CREATE TABLE IF NOT EXISTS analytics.events_raw (
    id UUID,
    user_id UUID,
    session_id UUID,
    type TEXT NOT NULL,
    payload JSONB,
    ts TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
) PARTITION BY RANGE (ts);

-- Create monthly partitions for V1 simulation (e.g., for 2024-2026)
CREATE TABLE IF NOT EXISTS analytics.events_raw_2024_01 PARTITION OF analytics.events_raw
    FOR VALUES FROM ('2024-01-01') TO ('2024-02-01');

CREATE TABLE IF NOT EXISTS analytics.events_raw_default PARTITION OF analytics.events_raw DEFAULT;

CREATE INDEX IF NOT EXISTS idx_events_type_ts ON analytics.events_raw (type, ts DESC);
CREATE INDEX IF NOT EXISTS idx_events_user_ts ON analytics.events_raw (user_id, ts DESC);
