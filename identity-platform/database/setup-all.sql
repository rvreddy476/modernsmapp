-- =============================================================================
-- MASTER SETUP — creates ALL tables in identity_db
-- =============================================================================
-- STEP 1: Run init-databases.sql first (creates identity_db)
-- STEP 2: Connect to identity_db in pgAdmin, then run THIS file
-- STEP 3: Run seed.sql to populate test data
-- =============================================================================

-- ============================================================
-- SCHEMAS
-- ============================================================
CREATE SCHEMA IF NOT EXISTS auth;
CREATE SCHEMA IF NOT EXISTS usr;
CREATE SCHEMA IF NOT EXISTS profile;
CREATE SCHEMA IF NOT EXISTS chat;
CREATE SCHEMA IF NOT EXISTS analytics;

-- ============================================================
-- auth schema — auth-service
-- ============================================================

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

-- ============================================================
-- usr schema — user-service
-- ============================================================

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

-- ============================================================
-- profile schema — profile-service
-- ============================================================

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

-- ============================================================
-- graph-service (public schema)
-- ============================================================

CREATE TABLE IF NOT EXISTS follows (
    follower_id UUID NOT NULL,
    followee_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (follower_id, followee_id)
);
CREATE INDEX IF NOT EXISTS idx_follows_followee_desc ON follows(followee_id, created_at DESC);

CREATE TABLE IF NOT EXISTS blocks (
    blocker_id UUID NOT NULL,
    blocked_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (blocker_id, blocked_id)
);

CREATE TABLE IF NOT EXISTS counts (
    user_id UUID PRIMARY KEY,
    follower_count BIGINT DEFAULT 0,
    following_count BIGINT DEFAULT 0,
    friend_count BIGINT DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS friend_requests (
    sender_id UUID NOT NULL,
    receiver_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (sender_id, receiver_id)
);
CREATE INDEX IF NOT EXISTS idx_friend_req_receiver ON friend_requests(receiver_id, status);

CREATE TABLE IF NOT EXISTS friends (
    user_a UUID NOT NULL,
    user_b UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_a, user_b)
);
CREATE INDEX IF NOT EXISTS idx_friends_b ON friends(user_b);

-- ============================================================
-- media-service (public schema)
-- ============================================================

CREATE TABLE IF NOT EXISTS media (
    id UUID PRIMARY KEY,
    owner_user_id UUID NOT NULL,
    kind TEXT NOT NULL,              -- image, video, gif, avatar, cover
    mime TEXT NOT NULL,
    size_bytes BIGINT NOT NULL,
    bucket TEXT NOT NULL,
    object_key TEXT NOT NULL,
    status TEXT NOT NULL,            -- init, uploaded, processing, ready, failed
    width INT,
    height INT,
    duration_ms INT,
    blurhash TEXT,
    alt_text TEXT DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_media_owner_user_id ON media(owner_user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS media_variants (
    media_id   UUID NOT NULL REFERENCES media(id),
    variant    TEXT NOT NULL,
    width      INT,
    height     INT,
    size_bytes BIGINT,
    mime       TEXT NOT NULL,
    object_key TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (media_id, variant)
);

-- ============================================================
-- post-service (public schema)
-- ============================================================

CREATE TABLE IF NOT EXISTS posts (
    id UUID PRIMARY KEY,
    author_id UUID NOT NULL,
    text TEXT NOT NULL,
    visibility TEXT NOT NULL,
    content_type TEXT NOT NULL DEFAULT 'post',
    is_pinned BOOLEAN DEFAULT FALSE,
    feeling TEXT,
    activity TEXT,
    activity_detail TEXT,
    rich_text JSONB,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_posts_author_desc ON posts(author_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_posts_author_type ON posts(author_id, content_type, created_at DESC);

CREATE TABLE IF NOT EXISTS post_media (
    post_id UUID NOT NULL,
    media_id UUID NOT NULL,
    kind TEXT NOT NULL,
    PRIMARY KEY (post_id, media_id)
);

CREATE TABLE IF NOT EXISTS polls (
    post_id UUID PRIMARY KEY REFERENCES posts(id),
    question TEXT NOT NULL,
    allows_multiple BOOLEAN DEFAULT FALSE,
    ends_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS poll_options (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id UUID NOT NULL REFERENCES polls(post_id),
    label TEXT NOT NULL,
    sort_order INT DEFAULT 0
);

CREATE TABLE IF NOT EXISTS poll_votes (
    post_id UUID NOT NULL,
    option_id UUID NOT NULL,
    user_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (post_id, user_id, option_id)
);
CREATE INDEX IF NOT EXISTS idx_poll_votes_option ON poll_votes(option_id);

-- ============================================================
-- message-service (chat schema)
-- ============================================================

CREATE TABLE IF NOT EXISTS chat.conversations (
    id UUID PRIMARY KEY,
    type TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS chat.conversation_members (
    conversation_id UUID NOT NULL REFERENCES chat.conversations(id),
    user_id UUID NOT NULL,
    joined_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (conversation_id, user_id)
);

CREATE TABLE IF NOT EXISTS chat.direct_conversation_keys (
    user_a UUID NOT NULL,
    user_b UUID NOT NULL,
    conversation_id UUID NOT NULL REFERENCES chat.conversations(id),
    PRIMARY KEY (user_a, user_b),
    CHECK (user_a < user_b)
);

-- ============================================================
-- feed-service (public schema)
-- ============================================================

CREATE TABLE IF NOT EXISTS celeb_authors (
    author_id UUID PRIMARY KEY,
    is_celeb BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS event_dedup (
    event_id UUID PRIMARY KEY,
    processed_at TIMESTAMPTZ NOT NULL
);

-- ============================================================
-- analytics-service (analytics schema)
-- ============================================================

CREATE TABLE IF NOT EXISTS analytics.events_raw (
    id UUID,
    user_id UUID,
    session_id UUID,
    type TEXT NOT NULL,
    payload JSONB,
    ts TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
) PARTITION BY RANGE (ts);

CREATE TABLE IF NOT EXISTS analytics.events_raw_2024_01 PARTITION OF analytics.events_raw
    FOR VALUES FROM ('2024-01-01') TO ('2024-02-01');

CREATE TABLE IF NOT EXISTS analytics.events_raw_default PARTITION OF analytics.events_raw DEFAULT;

CREATE INDEX IF NOT EXISTS idx_events_type_ts ON analytics.events_raw (type, ts DESC);
CREATE INDEX IF NOT EXISTS idx_events_user_ts ON analytics.events_raw (user_id, ts DESC);
