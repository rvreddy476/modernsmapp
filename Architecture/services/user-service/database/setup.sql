-- Database setup for Architecture/user-service
-- Consolidates initial schema and extended profile fields.

CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY,
    username TEXT UNIQUE,
    display_name TEXT NOT NULL,
    first_name TEXT,
    last_name TEXT,
    bio TEXT DEFAULT '',
    dob DATE,
    gender TEXT,
    avatar_media_id UUID,
    cover_media_id UUID,
    category TEXT DEFAULT 'personal',
    profession TEXT DEFAULT '',
    website TEXT DEFAULT '',
    location TEXT DEFAULT '',
    badge_flags INT DEFAULT 0,
    is_verified BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username ON users(username) WHERE username IS NOT NULL;

CREATE TABLE IF NOT EXISTS user_links (
    user_id UUID NOT NULL REFERENCES users(id),
    platform TEXT NOT NULL,
    url TEXT NOT NULL,
    display_label TEXT DEFAULT '',
    sort_order INT DEFAULT 0,
    PRIMARY KEY (user_id, platform)
);

CREATE TABLE IF NOT EXISTS user_about (
    user_id UUID NOT NULL REFERENCES users(id),
    section TEXT NOT NULL,
    item_id UUID NOT NULL DEFAULT gen_random_uuid(),
    data JSONB NOT NULL,
    visibility TEXT NOT NULL DEFAULT 'public',
    sort_order INT DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, section, item_id)
);

CREATE INDEX IF NOT EXISTS idx_user_about_section ON user_about(user_id, section);

CREATE TABLE IF NOT EXISTS user_settings (
    user_id UUID PRIMARY KEY REFERENCES users(id),
    account_visibility TEXT DEFAULT 'public',
    allow_messages_from TEXT DEFAULT 'everyone',
    allow_comments_from TEXT DEFAULT 'everyone',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
