-- Database setup for Architecture/trust-safety-service

CREATE SCHEMA IF NOT EXISTS trust;

CREATE TABLE IF NOT EXISTS trust.reports (
    id UUID PRIMARY KEY,
    reporter_id UUID NOT NULL,
    entity_type TEXT NOT NULL, -- 'user', 'post', 'comment'
    entity_id UUID NOT NULL,
    reason TEXT NOT NULL,
    details TEXT,
    status TEXT NOT NULL DEFAULT 'open', -- 'open', 'reviewing', 'closed'
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_reports_status_created
    ON trust.reports (status, created_at DESC);
