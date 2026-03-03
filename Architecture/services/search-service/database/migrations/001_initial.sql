-- Database setup for Architecture/search-service

CREATE SCHEMA IF NOT EXISTS search;

CREATE TABLE IF NOT EXISTS search.event_dedup (
    event_id UUID PRIMARY KEY,
    processed_at TIMESTAMPTZ NOT NULL
);
