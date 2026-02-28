-- Unified init script for the shared Postgres instance.
-- Creates both databases and applies identity-platform schema.

-- 1. The "app" database is auto-created by POSTGRES_DB env var.
-- 2. Create the identity_db used by identity-platform services.
CREATE DATABASE identity_db;

-- Apply identity-platform schema
\connect identity_db;

CREATE SCHEMA IF NOT EXISTS auth;
CREATE SCHEMA IF NOT EXISTS usr;
CREATE SCHEMA IF NOT EXISTS profile;

-- auth schema
CREATE TABLE IF NOT EXISTS auth.users (
    user_id UUID PRIMARY KEY,
    phone TEXT UNIQUE,
    email TEXT UNIQUE,
    password_hash TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT users_identity_check CHECK (phone IS NOT NULL OR email IS NOT NULL)
);

CREATE TABLE IF NOT EXISTS auth.otp_codes (
    id UUID PRIMARY KEY,
    phone TEXT NOT NULL,
    otp_hash TEXT NOT NULL,
    purpose TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    attempts INT DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_otp_phone_purpose ON auth.otp_codes(phone, purpose);

CREATE TABLE IF NOT EXISTS auth.sessions (
    session_id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES auth.users(user_id),
    refresh_token_hash TEXT NOT NULL,
    device_id TEXT,
    platform TEXT,
    ip TEXT,
    user_agent TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_sessions_user_created ON auth.sessions(user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS auth.outbox_events (
    id BIGSERIAL PRIMARY KEY,
    event_type TEXT NOT NULL,
    partition_key TEXT NOT NULL DEFAULT '',
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_outbox_unpublished ON auth.outbox_events(id) WHERE published_at IS NULL;

-- usr schema
CREATE TABLE IF NOT EXISTS usr.users (
    id UUID PRIMARY KEY REFERENCES auth.users(user_id),
    status TEXT NOT NULL DEFAULT 'active',
    is_verified BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS usr.user_settings (
    user_id UUID PRIMARY KEY REFERENCES usr.users(id),
    account_visibility TEXT NOT NULL DEFAULT 'public',
    allow_messages_from TEXT NOT NULL DEFAULT 'everyone',
    allow_comments_from TEXT NOT NULL DEFAULT 'everyone',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- profile schema
CREATE TABLE IF NOT EXISTS profile.profiles (
    user_id UUID PRIMARY KEY REFERENCES auth.users(user_id),
    username TEXT UNIQUE,
    display_name TEXT NOT NULL,
    first_name TEXT DEFAULT '',
    last_name TEXT DEFAULT '',
    bio TEXT DEFAULT '',
    dob DATE,
    gender TEXT DEFAULT '',
    avatar_media_id UUID,
    cover_media_id UUID,
    category TEXT DEFAULT 'personal',
    profession TEXT DEFAULT '',
    website TEXT DEFAULT '',
    location TEXT DEFAULT '',
    badge_flags INT DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_profiles_display_name ON profile.profiles(display_name);
CREATE UNIQUE INDEX IF NOT EXISTS idx_profiles_username ON profile.profiles(username) WHERE username IS NOT NULL;

CREATE TABLE IF NOT EXISTS profile.user_links (
    user_id UUID NOT NULL REFERENCES profile.profiles(user_id),
    platform TEXT NOT NULL,
    url TEXT NOT NULL,
    display_label TEXT DEFAULT '',
    sort_order INT DEFAULT 0,
    PRIMARY KEY (user_id, platform)
);

CREATE TABLE IF NOT EXISTS profile.user_about (
    user_id UUID NOT NULL REFERENCES profile.profiles(user_id),
    section TEXT NOT NULL,
    item_id UUID NOT NULL DEFAULT gen_random_uuid(),
    data JSONB NOT NULL,
    visibility TEXT NOT NULL DEFAULT 'public',
    sort_order INT DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, section, item_id)
);
CREATE INDEX IF NOT EXISTS idx_user_about_section ON profile.user_about(user_id, section);
