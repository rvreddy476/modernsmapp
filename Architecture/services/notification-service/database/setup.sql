-- Database setup for Architecture/notification-service

CREATE SCHEMA IF NOT EXISTS notify_meta;

CREATE TABLE IF NOT EXISTS notify_meta.event_dedup (
    event_id UUID PRIMARY KEY,
    processed_at TIMESTAMPTZ NOT NULL
);
