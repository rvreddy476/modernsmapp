-- =============================================================================
-- IDENTITY_DB — Full schema + seed data
-- Run against: identity_db
-- =============================================================================

\connect identity_db;

-- Schemas
CREATE SCHEMA IF NOT EXISTS auth;
CREATE SCHEMA IF NOT EXISTS usr;
CREATE SCHEMA IF NOT EXISTS profile;

-- Extensions
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- ============================================================
-- ENUM types
-- ============================================================
DO $$ BEGIN CREATE TYPE account_type       AS ENUM ('personal', 'creator', 'business'); EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN CREATE TYPE account_status     AS ENUM ('active', 'suspended', 'deleted', 'pending_deletion'); EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN CREATE TYPE age_verification   AS ENUM ('unverified', 'under_16', 'minor', 'adult'); EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN CREATE TYPE profile_category   AS ENUM ('personal', 'creator', 'business'); EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN CREATE TYPE verification_level AS ENUM ('email', 'phone', 'id', 'org'); EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN CREATE TYPE intro_media_type   AS ENUM ('audio', 'video'); EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN CREATE TYPE profile_section_type AS ENUM ('basic_info', 'contact', 'location', 'life_entry', 'interests', 'services'); EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN CREATE TYPE visibility_level   AS ENUM ('public', 'followers', 'friends', 'only_me'); EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN CREATE TYPE follow_status      AS ENUM ('active', 'pending'); EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN CREATE TYPE friendship_status  AS ENUM ('pending', 'accepted', 'rejected', 'blocked'); EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- ============================================================
-- auth schema
-- ============================================================
CREATE TABLE IF NOT EXISTS auth.users (
    user_id                UUID            PRIMARY KEY,
    phone                  TEXT            UNIQUE,
    email                  TEXT            UNIQUE,
    password_hash          TEXT,
    email_verified         BOOLEAN         NOT NULL DEFAULT FALSE,
    phone_verified         BOOLEAN         NOT NULL DEFAULT FALSE,
    two_factor_enabled     BOOLEAN         NOT NULL DEFAULT FALSE,
    two_factor_secret      VARCHAR(255),
    account_type           account_type    NOT NULL DEFAULT 'personal',
    account_status         account_status  NOT NULL DEFAULT 'active',
    login_provider         VARCHAR(50),
    recovery_email         VARCHAR(255),
    recovery_phone         VARCHAR(20),
    age_verification       age_verification NOT NULL DEFAULT 'unverified',
    consent_terms          BOOLEAN         NOT NULL DEFAULT FALSE,
    consent_privacy        BOOLEAN         NOT NULL DEFAULT FALSE,
    consent_age            BOOLEAN         NOT NULL DEFAULT FALSE,
    deletion_requested_at  TIMESTAMPTZ,
    scheduled_purge_date   TIMESTAMPTZ,
    last_login_at          TIMESTAMPTZ,
    created_at             TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    CONSTRAINT users_identity_check CHECK (phone IS NOT NULL OR email IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS idx_users_pending_deletion ON auth.users(scheduled_purge_date) WHERE account_status = 'pending_deletion';
CREATE INDEX IF NOT EXISTS idx_users_login_provider ON auth.users(login_provider) WHERE login_provider IS NOT NULL;

CREATE TABLE IF NOT EXISTS auth.otp_codes (
    id          UUID        PRIMARY KEY,
    phone       TEXT        NOT NULL,
    otp_hash    TEXT        NOT NULL,
    purpose     TEXT        NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    attempts    INT         DEFAULT 0,
    created_at  TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_otp_phone_purpose ON auth.otp_codes(phone, purpose);

CREATE TABLE IF NOT EXISTS auth.sessions (
    session_id          UUID        PRIMARY KEY,
    user_id             UUID        NOT NULL REFERENCES auth.users(user_id),
    refresh_token_hash  TEXT        NOT NULL,
    device_id           TEXT,
    platform            TEXT,
    ip                  TEXT,
    user_agent          TEXT,
    is_active           BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at          TIMESTAMPTZ NOT NULL,
    revoked_at          TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_sessions_user_created ON auth.sessions(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_sessions_user_active  ON auth.sessions(user_id, is_active) WHERE is_active = TRUE;

CREATE TABLE IF NOT EXISTS auth.trusted_devices (
    id                  UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID            NOT NULL REFERENCES auth.users(user_id) ON DELETE CASCADE,
    device_fingerprint  VARCHAR(255)    NOT NULL,
    device_name         VARCHAR(100),
    last_used_at        TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    trusted_at          TIMESTAMPTZ     NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_trusted_devices_user ON auth.trusted_devices(user_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_trusted_devices_user_fingerprint ON auth.trusted_devices(user_id, device_fingerprint);

CREATE TABLE IF NOT EXISTS auth.outbox_events (
    id              BIGSERIAL       PRIMARY KEY,
    event_type      TEXT            NOT NULL,
    partition_key   TEXT            NOT NULL DEFAULT '',
    payload         JSONB           NOT NULL,
    created_at      TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    published_at    TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_outbox_unpublished ON auth.outbox_events(id) WHERE published_at IS NULL;

-- ============================================================
-- usr schema
-- ============================================================
CREATE TABLE IF NOT EXISTS usr.users (
    id          UUID        PRIMARY KEY REFERENCES auth.users(user_id),
    status      TEXT        NOT NULL DEFAULT 'active',
    is_verified BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS usr.user_settings (
    user_id              UUID        PRIMARY KEY REFERENCES usr.users(id),
    account_visibility   TEXT        NOT NULL DEFAULT 'public',
    allow_messages_from  TEXT        NOT NULL DEFAULT 'everyone',
    allow_comments_from  TEXT        NOT NULL DEFAULT 'everyone',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ============================================================
-- profile schema
-- ============================================================
CREATE TABLE IF NOT EXISTS profile.profiles (
    user_id             UUID                PRIMARY KEY REFERENCES auth.users(user_id),
    username            TEXT                UNIQUE,
    display_name        TEXT                NOT NULL,
    first_name          TEXT                DEFAULT '',
    last_name           TEXT                DEFAULT '',
    preferred_name      VARCHAR(100),
    pronouns            VARCHAR(30),
    bio                 TEXT                DEFAULT '',
    dob                 DATE,
    gender              TEXT                DEFAULT '',
    avatar_media_id     UUID,
    cover_media_id      UUID,
    category            TEXT                DEFAULT 'personal',
    profession          TEXT                DEFAULT '',
    website             TEXT                DEFAULT '',
    location            TEXT                DEFAULT '',
    badge_flags         INT                 DEFAULT 0,
    is_verified         BOOLEAN             NOT NULL DEFAULT FALSE,
    verification_level  verification_level  NOT NULL DEFAULT 'email',
    status_text         VARCHAR(100),
    status_emoji        VARCHAR(10),
    status_expires_at   TIMESTAMPTZ,
    profile_theme_color VARCHAR(7)          NOT NULL DEFAULT '#1A73E8',
    intro_media_url     VARCHAR(500),
    intro_media_type    intro_media_type,
    cta_label           VARCHAR(30),
    cta_url             VARCHAR(500),
    member_since_badge  BOOLEAN             NOT NULL DEFAULT TRUE,
    timezone            VARCHAR(50),
    follower_count      BIGINT              NOT NULL DEFAULT 0,
    following_count     BIGINT              NOT NULL DEFAULT 0,
    friend_count        BIGINT              NOT NULL DEFAULT 0,
    post_count          BIGINT              NOT NULL DEFAULT 0,
    created_at          TIMESTAMPTZ         NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ         NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_profiles_display_name ON profile.profiles(display_name);
CREATE UNIQUE INDEX IF NOT EXISTS idx_profiles_username ON profile.profiles(username) WHERE username IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_profiles_display_name_trgm ON profile.profiles USING gin(display_name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_profiles_username_trgm ON profile.profiles USING gin(username gin_trgm_ops);

CREATE TABLE IF NOT EXISTS profile.profile_links (
    id          UUID                PRIMARY KEY DEFAULT gen_random_uuid(),
    profile_id  UUID                NOT NULL REFERENCES profile.profiles(user_id) ON DELETE CASCADE,
    title       VARCHAR(100)        NOT NULL,
    url         VARCHAR(500)        NOT NULL,
    icon        VARCHAR(50),
    category    VARCHAR(50),
    sort_order  INT                 NOT NULL DEFAULT 0,
    click_count BIGINT              NOT NULL DEFAULT 0,
    is_pinned   BOOLEAN             NOT NULL DEFAULT FALSE,
    visibility  visibility_level    NOT NULL DEFAULT 'public',
    created_at  TIMESTAMPTZ         NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_profile_links_profile ON profile.profile_links(profile_id, sort_order);

CREATE TABLE IF NOT EXISTS profile.user_links (
    user_id       UUID    NOT NULL REFERENCES profile.profiles(user_id),
    platform      TEXT    NOT NULL,
    url           TEXT    NOT NULL,
    display_label TEXT    DEFAULT '',
    sort_order    INT     DEFAULT 0,
    PRIMARY KEY (user_id, platform)
);

CREATE TABLE IF NOT EXISTS profile.user_about (
    user_id     UUID        NOT NULL REFERENCES profile.profiles(user_id),
    section     TEXT        NOT NULL,
    item_id     UUID        NOT NULL DEFAULT gen_random_uuid(),
    data        JSONB       NOT NULL,
    visibility  TEXT        NOT NULL DEFAULT 'public',
    sort_order  INT         DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, section, item_id)
);
CREATE INDEX IF NOT EXISTS idx_user_about_section ON profile.user_about(user_id, section);

CREATE TABLE IF NOT EXISTS profile.follows (
    id              UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    follower_id     UUID            NOT NULL REFERENCES profile.profiles(user_id) ON DELETE CASCADE,
    following_id    UUID            NOT NULL REFERENCES profile.profiles(user_id) ON DELETE CASCADE,
    status          follow_status   NOT NULL DEFAULT 'active',
    created_at      TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    UNIQUE(follower_id, following_id),
    CHECK (follower_id != following_id)
);
CREATE INDEX IF NOT EXISTS idx_follows_follower  ON profile.follows(follower_id);
CREATE INDEX IF NOT EXISTS idx_follows_following ON profile.follows(following_id);

CREATE TABLE IF NOT EXISTS profile.friendships (
    id              UUID                PRIMARY KEY DEFAULT gen_random_uuid(),
    requester_id    UUID                NOT NULL REFERENCES profile.profiles(user_id) ON DELETE CASCADE,
    addressee_id    UUID                NOT NULL REFERENCES profile.profiles(user_id) ON DELETE CASCADE,
    status          friendship_status   NOT NULL DEFAULT 'pending',
    created_at      TIMESTAMPTZ         NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ         NOT NULL DEFAULT NOW(),
    UNIQUE(requester_id, addressee_id),
    CHECK (requester_id != addressee_id)
);
CREATE INDEX IF NOT EXISTS idx_friendships_requester ON profile.friendships(requester_id, status);
CREATE INDEX IF NOT EXISTS idx_friendships_addressee ON profile.friendships(addressee_id, status);

CREATE TABLE IF NOT EXISTS profile.blocks (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    blocker_id  UUID        NOT NULL REFERENCES profile.profiles(user_id) ON DELETE CASCADE,
    blocked_id  UUID        NOT NULL REFERENCES profile.profiles(user_id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(blocker_id, blocked_id),
    CHECK (blocker_id != blocked_id)
);
CREATE INDEX IF NOT EXISTS idx_blocks_blocker ON profile.blocks(blocker_id);
CREATE INDEX IF NOT EXISTS idx_blocks_blocked ON profile.blocks(blocked_id);

-- ============================================================
-- SEED DATA
-- ============================================================
-- Users: user1@example.com / user2@example.com / user3@example.com
-- All passwords: password123

INSERT INTO auth.users (user_id, email, phone, password_hash, created_at) VALUES
    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'user1@example.com', NULL, '$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy', '2026-01-15T10:00:00Z'),
    ('a1b2c3d4-e5f6-7890-abcd-ef1234567890', 'user2@example.com', NULL, '$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy', '2026-01-15T10:01:00Z'),
    ('c3d4e5f6-a1b2-3456-7890-abcdef123456', 'user3@example.com', '+15551234567', '$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy', '2026-01-15T10:02:00Z')
ON CONFLICT DO NOTHING;

INSERT INTO usr.users (id, status, is_verified, created_at, updated_at) VALUES
    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'active', TRUE,  '2026-01-15T10:00:00Z', '2026-02-01T12:00:00Z'),
    ('a1b2c3d4-e5f6-7890-abcd-ef1234567890', 'active', TRUE,  '2026-01-15T10:01:00Z', '2026-01-20T09:00:00Z'),
    ('c3d4e5f6-a1b2-3456-7890-abcdef123456', 'active', FALSE, '2026-01-15T10:02:00Z', '2026-01-15T10:02:00Z')
ON CONFLICT DO NOTHING;

INSERT INTO usr.user_settings (user_id, account_visibility, allow_messages_from, allow_comments_from, created_at, updated_at) VALUES
    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'public', 'everyone', 'everyone', '2026-01-15T10:00:00Z', '2026-01-15T10:00:00Z'),
    ('a1b2c3d4-e5f6-7890-abcd-ef1234567890', 'public', 'everyone', 'everyone', '2026-01-15T10:01:00Z', '2026-01-15T10:01:00Z'),
    ('c3d4e5f6-a1b2-3456-7890-abcdef123456', 'public', 'followers', 'everyone', '2026-01-15T10:02:00Z', '2026-01-15T10:02:00Z')
ON CONFLICT DO NOTHING;

INSERT INTO profile.profiles (user_id, username, display_name, first_name, last_name, bio, dob, gender, avatar_media_id, cover_media_id, category, profession, website, location, badge_flags, created_at, updated_at) VALUES
    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'johndoe', 'John Doe', 'John', 'Doe', 'Full-stack developer & open-source enthusiast.', '1995-06-15', 'male', '00000000-0000-4000-a000-000000000001', '00000000-0000-4000-a000-000000000002', 'personal', 'Software Engineer', 'https://johndoe.dev', 'San Francisco, CA', 3, '2026-01-15T10:00:00Z', '2026-02-01T12:00:00Z'),
    ('a1b2c3d4-e5f6-7890-abcd-ef1234567890', 'janedoe', 'Jane Doe', 'Jane', 'Doe', 'Designer & photographer.', '1998-03-22', 'female', '00000000-0000-4000-a000-000000000003', NULL, 'personal', 'UX Designer', 'https://janedoe.design', 'New York, NY', 1, '2026-01-15T10:01:00Z', '2026-01-20T09:00:00Z'),
    ('c3d4e5f6-a1b2-3456-7890-abcdef123456', 'bobsmith', 'Bob Smith', 'Bob', 'Smith', 'Music producer and coffee addict.', '1992-11-08', 'male', NULL, NULL, 'personal', 'Music Producer', '', 'Austin, TX', 0, '2026-01-15T10:02:00Z', '2026-01-15T10:02:00Z')
ON CONFLICT DO NOTHING;

INSERT INTO profile.user_links (user_id, platform, url, display_label, sort_order) VALUES
    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'github',   'https://github.com/johndoe',     'GitHub',    0),
    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'twitter',  'https://x.com/johndoe',          '@johndoe',  1),
    ('a1b2c3d4-e5f6-7890-abcd-ef1234567890', 'dribbble', 'https://dribbble.com/janedoe',    'Dribbble',  0)
ON CONFLICT DO NOTHING;
