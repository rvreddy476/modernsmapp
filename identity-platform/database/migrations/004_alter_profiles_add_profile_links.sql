-- Migration 004: Add missing columns to profiles + create profile_links table
-- Depends on: 001_enum_types.sql
-- Run against: identity_db

\connect identity_db;

-- Enable trigram extension for search indexes
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- Add missing columns to profile.profiles
ALTER TABLE profile.profiles
    ADD COLUMN IF NOT EXISTS preferred_name     VARCHAR(100),
    ADD COLUMN IF NOT EXISTS pronouns           VARCHAR(30),
    ADD COLUMN IF NOT EXISTS is_verified        BOOLEAN             NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS verification_level verification_level  NOT NULL DEFAULT 'email',
    ADD COLUMN IF NOT EXISTS status_text        VARCHAR(100),
    ADD COLUMN IF NOT EXISTS status_emoji       VARCHAR(10),
    ADD COLUMN IF NOT EXISTS status_expires_at  TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS profile_theme_color VARCHAR(7)         NOT NULL DEFAULT '#1A73E8',
    ADD COLUMN IF NOT EXISTS intro_media_url    VARCHAR(500),
    ADD COLUMN IF NOT EXISTS intro_media_type   intro_media_type,
    ADD COLUMN IF NOT EXISTS cta_label          VARCHAR(30),
    ADD COLUMN IF NOT EXISTS cta_url            VARCHAR(500),
    ADD COLUMN IF NOT EXISTS member_since_badge BOOLEAN             NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS timezone           VARCHAR(50),
    ADD COLUMN IF NOT EXISTS follower_count     BIGINT              NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS following_count    BIGINT              NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS friend_count       BIGINT              NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS post_count         BIGINT              NOT NULL DEFAULT 0;

-- Trigram indexes for profile search
CREATE INDEX IF NOT EXISTS idx_profiles_display_name_trgm
    ON profile.profiles USING gin(display_name gin_trgm_ops);

CREATE INDEX IF NOT EXISTS idx_profiles_username_trgm
    ON profile.profiles USING gin(username gin_trgm_ops);

-- New profile_links table — replaces user_links with UUID PK and analytics
CREATE TABLE IF NOT EXISTS profile.profile_links (
    id          UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    profile_id  UUID            NOT NULL REFERENCES profile.profiles(user_id) ON DELETE CASCADE,
    title       VARCHAR(100)    NOT NULL,
    url         VARCHAR(500)    NOT NULL,
    icon        VARCHAR(50),
    category    VARCHAR(50),
    sort_order  INT             NOT NULL DEFAULT 0,
    click_count BIGINT          NOT NULL DEFAULT 0,
    is_pinned   BOOLEAN         NOT NULL DEFAULT FALSE,
    visibility  visibility_level NOT NULL DEFAULT 'public',
    created_at  TIMESTAMPTZ     NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_profile_links_profile
    ON profile.profile_links(profile_id, sort_order);

-- Migrate existing data from user_links → profile_links
INSERT INTO profile.profile_links (profile_id, title, url, icon, sort_order)
SELECT user_id, COALESCE(display_label, platform), url, platform, sort_order
FROM profile.user_links
ON CONFLICT DO NOTHING;

-- Keep old table for backward compatibility during migration period
-- DROP TABLE profile.user_links; -- uncomment after services are updated
