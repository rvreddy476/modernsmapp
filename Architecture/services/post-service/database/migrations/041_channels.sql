-- Migration 041: Tube channels (2026-09-05).
--
-- Founder rule: "Before submitting a video, a channel name should be created."
-- One channel per account, owned by post-service (it owns the videos).
--
-- The `channels` table already exists in the shared app database: user-service's
-- phase-6 DDL created it (creator video channels, `id` PK, several channels per
-- user, never populated — zero rows on every environment), and post-service's
-- own video_series.channel_id / playlists.channel_id FKs point at it. Rather
-- than a second table this migration ADAPTS that one to the channel contract:
--
--   * one channel per account: UNIQUE (user_id) — user_id is the logical key
--     the API exposes; the surrogate `id` stays so the existing FKs keep
--     working;
--   * `avatar_media_id` (already added by user-service migration 002 on live
--     DBs; added here for a DB where post-service boots first);
--   * `about` is served from the existing `description` column.
--
-- The CREATE TABLE below is the full user-service column set so that a fresh
-- database that runs post-service before user-service ends up with a table
-- user-service's own code can still read (its CREATE TABLE IF NOT EXISTS then
-- no-ops).
CREATE TABLE IF NOT EXISTS channels (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID NOT NULL,
    handle           TEXT NOT NULL UNIQUE,
    name             TEXT NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    category         TEXT NOT NULL DEFAULT '',
    country          TEXT NOT NULL DEFAULT '',
    language         TEXT NOT NULL DEFAULT '',
    contact_email    TEXT NOT NULL DEFAULT '',
    collab_status    TEXT NOT NULL DEFAULT 'closed',
    content_schedule TEXT NOT NULL DEFAULT '',
    subscriber_count INTEGER NOT NULL DEFAULT 0,
    is_verified      BOOLEAN NOT NULL DEFAULT FALSE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE channels ADD COLUMN IF NOT EXISTS avatar_media_id UUID;
ALTER TABLE channels ADD COLUMN IF NOT EXISTS banner_media_id UUID;
ALTER TABLE channels ADD COLUMN IF NOT EXISTS is_default BOOLEAN NOT NULL DEFAULT FALSE;

-- One channel per account.
CREATE UNIQUE INDEX IF NOT EXISTS uq_channels_user ON channels (user_id);
CREATE INDEX IF NOT EXISTS idx_channels_user ON channels (user_id);
CREATE INDEX IF NOT EXISTS idx_channels_handle ON channels (handle);

-- Media-access authority: a channel avatar is public, so the delivery gate
-- needs "which channel owns this media" in one indexed lookup.
CREATE INDEX IF NOT EXISTS idx_channels_avatar_media
    ON channels (avatar_media_id) WHERE avatar_media_id IS NOT NULL;
