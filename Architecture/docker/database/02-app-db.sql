-- =============================================================================
-- APP DB — All Architecture service schemas + seed data
-- Run against: app
-- =============================================================================

\connect app;

-- ============================================================
-- user-service
-- ============================================================
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
    click_count INTEGER NOT NULL DEFAULT 0,
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

-- user-service Phase 6: channels, pages, reputation
ALTER TABLE users ADD COLUMN IF NOT EXISTS pronouns TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS status_text TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS status_emoji TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS status_expires_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS channels (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID NOT NULL REFERENCES users(id),
    handle           TEXT NOT NULL UNIQUE,
    name             TEXT NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    icon_url         TEXT NOT NULL DEFAULT '',
    banner_url       TEXT NOT NULL DEFAULT '',
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
CREATE INDEX IF NOT EXISTS idx_channels_user ON channels (user_id);
CREATE INDEX IF NOT EXISTS idx_channels_handle ON channels (handle);

CREATE TABLE IF NOT EXISTS channel_links (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    title      TEXT NOT NULL,
    url        TEXT NOT NULL,
    sort_order INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_channel_links_channel ON channel_links (channel_id);

CREATE TABLE IF NOT EXISTS channel_milestones (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id     UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    milestone_type TEXT NOT NULL,
    title          TEXT NOT NULL,
    achieved_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    is_public      BOOLEAN NOT NULL DEFAULT TRUE
);
CREATE INDEX IF NOT EXISTS idx_channel_milestones_channel ON channel_milestones (channel_id);

CREATE TABLE IF NOT EXISTS business_pages (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        UUID NOT NULL REFERENCES users(id),
    page_handle    TEXT NOT NULL UNIQUE,
    page_name      TEXT NOT NULL,
    category       TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    address        TEXT NOT NULL DEFAULT '',
    lat            DOUBLE PRECISION,
    lng            DOUBLE PRECISION,
    business_hours JSONB,
    phone          TEXT NOT NULL DEFAULT '',
    whatsapp       TEXT NOT NULL DEFAULT '',
    business_email TEXT NOT NULL DEFAULT '',
    services       JSONB,
    price_range    TEXT NOT NULL DEFAULT '',
    booking_url    TEXT NOT NULL DEFAULT '',
    menu_urls      JSONB,
    is_verified    BOOLEAN NOT NULL DEFAULT FALSE,
    avg_rating     DOUBLE PRECISION NOT NULL DEFAULT 0,
    review_count   INTEGER NOT NULL DEFAULT 0,
    faq            JSONB,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_business_pages_user ON business_pages (user_id);
CREATE INDEX IF NOT EXISTS idx_business_pages_handle ON business_pages (page_handle);

CREATE TABLE IF NOT EXISTS business_reviews (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    page_id     UUID NOT NULL REFERENCES business_pages(id) ON DELETE CASCADE,
    reviewer_id UUID NOT NULL,
    rating      INTEGER NOT NULL CHECK (rating BETWEEN 1 AND 5),
    review_text TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(page_id, reviewer_id)
);
CREATE INDEX IF NOT EXISTS idx_business_reviews_page ON business_reviews (page_id, created_at DESC);

CREATE TABLE IF NOT EXISTS user_reputation (
    user_id              UUID PRIMARY KEY REFERENCES users(id),
    trust_score          DECIMAL(3,2) NOT NULL DEFAULT 0.50,
    endorsement_count    INTEGER NOT NULL DEFAULT 0,
    cross_platform_proofs JSONB NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS endorsements (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    from_user_id UUID NOT NULL REFERENCES users(id),
    to_user_id   UUID NOT NULL REFERENCES users(id),
    skill_tag    TEXT NOT NULL,
    message      TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(from_user_id, to_user_id, skill_tag)
);
CREATE INDEX IF NOT EXISTS idx_endorsements_to ON endorsements (to_user_id);
CREATE INDEX IF NOT EXISTS idx_endorsements_from ON endorsements (from_user_id);

-- ============================================================
-- graph-service
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
-- media-service
-- ============================================================
CREATE TABLE IF NOT EXISTS media_assets (
    id UUID PRIMARY KEY,
    uploader_id UUID NOT NULL,
    file_type TEXT NOT NULL,
    media_subtype TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    file_size_bytes BIGINT NOT NULL,
    storage_bucket TEXT NOT NULL,
    storage_key TEXT NOT NULL,
    processing_status TEXT NOT NULL,
    width INT,
    height INT,
    duration_seconds INT,
    blurhash TEXT,
    alt_text TEXT DEFAULT '',
    original_url VARCHAR(500),
    cdn_url VARCHAR(500),
    thumbnail_url VARCHAR(500),
    is_vertical BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_media_assets_uploader_id ON media_assets(uploader_id, created_at DESC);

CREATE TABLE IF NOT EXISTS media_variants (
    media_asset_id UUID NOT NULL REFERENCES media_assets(id),
    variant        TEXT NOT NULL,
    width          INT,
    height         INT,
    size_bytes     BIGINT,
    mime           TEXT NOT NULL,
    object_key     TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (media_asset_id, variant)
);

CREATE TABLE IF NOT EXISTS transcoding_jobs (
    id UUID PRIMARY KEY,
    media_asset_id UUID NOT NULL REFERENCES media_assets(id),
    target_quality VARCHAR(20) NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued',
    output_url VARCHAR(500),
    output_size_bytes BIGINT,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_transcoding_jobs_media ON transcoding_jobs(media_asset_id);

-- ============================================================
-- post-service
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
    hashtags TEXT[],
    mentions UUID[],
    location_name TEXT,
    location_lat DOUBLE PRECISION,
    location_lng DOUBLE PRECISION,
    post_type TEXT NOT NULL DEFAULT 'standard',
    app_origin TEXT,
    no_comments BOOLEAN NOT NULL DEFAULT FALSE,
    no_likes BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_posts_author_desc ON posts(author_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_posts_author_type ON posts(author_id, content_type, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_posts_hashtags ON posts USING GIN (hashtags);
CREATE INDEX IF NOT EXISTS idx_posts_author_reel ON posts (author_id, created_at DESC) WHERE content_type = 'reel' AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_posts_author_video ON posts (author_id, created_at DESC) WHERE content_type = 'video' AND deleted_at IS NULL;

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

CREATE TABLE IF NOT EXISTS comments (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id        UUID NOT NULL REFERENCES posts(id),
    author_id      UUID NOT NULL,
    parent_id      UUID REFERENCES comments(id),
    body           TEXT NOT NULL CHECK (char_length(body) BETWEEN 1 AND 2000),
    like_count     INTEGER NOT NULL DEFAULT 0,
    reply_count    INTEGER NOT NULL DEFAULT 0,
    is_reply       BOOLEAN NOT NULL DEFAULT FALSE,
    is_deleted     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_comments_post ON comments (post_id, created_at DESC) WHERE is_deleted = FALSE;
CREATE INDEX IF NOT EXISTS idx_comments_parent ON comments (parent_id, created_at ASC) WHERE parent_id IS NOT NULL AND is_deleted = FALSE;
CREATE INDEX IF NOT EXISTS idx_comments_author ON comments (author_id, created_at DESC) WHERE is_deleted = FALSE;

CREATE TABLE IF NOT EXISTS post_engagement_counts (
    post_id         UUID PRIMARY KEY REFERENCES posts(id),
    like_count      INTEGER NOT NULL DEFAULT 0,
    comment_count   INTEGER NOT NULL DEFAULT 0,
    share_count     INTEGER NOT NULL DEFAULT 0,
    bookmark_count  INTEGER NOT NULL DEFAULT 0,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE OR REPLACE FUNCTION create_engagement_counts() RETURNS TRIGGER AS $$
BEGIN INSERT INTO post_engagement_counts (post_id) VALUES (NEW.id); RETURN NEW; END; $$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_create_engagement_counts ON posts;
CREATE TRIGGER trg_create_engagement_counts AFTER INSERT ON posts FOR EACH ROW EXECUTE FUNCTION create_engagement_counts();

CREATE TABLE IF NOT EXISTS engagement_event_log (
    event_id      TEXT PRIMARY KEY,
    event_type    TEXT NOT NULL,
    target_id     UUID NOT NULL,
    processed_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_event_log_age ON engagement_event_log (processed_at);

-- Stories
CREATE TABLE IF NOT EXISTS stories (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    author_id       UUID NOT NULL,
    media_url       TEXT NOT NULL,
    media_type      TEXT NOT NULL,
    caption         TEXT NOT NULL DEFAULT '',
    stickers        JSONB,
    music_track     JSONB,
    visibility      TEXT NOT NULL DEFAULT 'public',
    view_count      INTEGER NOT NULL DEFAULT 0,
    expires_at      TIMESTAMPTZ NOT NULL,
    is_highlight    BOOLEAN NOT NULL DEFAULT FALSE,
    highlight_group TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_stories_author ON stories (author_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_stories_expiry ON stories (expires_at) WHERE is_highlight = FALSE;

-- Reactions
CREATE TABLE IF NOT EXISTS reactions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    target_type   TEXT NOT NULL,
    target_id     UUID NOT NULL,
    user_id       UUID NOT NULL,
    reaction_type TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(target_type, target_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_reactions_target ON reactions (target_type, target_id);
CREATE INDEX IF NOT EXISTS idx_reactions_user ON reactions (user_id, target_type, created_at DESC);

-- Saved items
CREATE TABLE IF NOT EXISTS saved_items (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    target_type     TEXT NOT NULL,
    target_id       UUID NOT NULL,
    collection_name TEXT NOT NULL DEFAULT 'default',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(user_id, target_type, target_id)
);
CREATE INDEX IF NOT EXISTS idx_saved_items_user ON saved_items (user_id, collection_name, created_at DESC);

-- ============================================================
-- feed-service
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

CREATE TABLE IF NOT EXISTS user_interactions (
    viewer_id       UUID NOT NULL,
    author_id       UUID NOT NULL,
    like_rate       FLOAT NOT NULL DEFAULT 0.0,
    comment_rate    FLOAT NOT NULL DEFAULT 0.0,
    share_rate      FLOAT NOT NULL DEFAULT 0.0,
    total_score     FLOAT NOT NULL DEFAULT 0.0,
    author_penalty  FLOAT NOT NULL DEFAULT 0.0,
    author_boost    FLOAT NOT NULL DEFAULT 0.0,
    interaction_count INTEGER NOT NULL DEFAULT 0,
    last_interaction TIMESTAMPTZ,
    computed_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (viewer_id, author_id)
);
CREATE INDEX IF NOT EXISTS idx_interactions_viewer ON user_interactions (viewer_id);

CREATE TABLE IF NOT EXISTS viewer_media_prefs (
    user_id         UUID PRIMARY KEY,
    video_p95_dwell FLOAT DEFAULT 0,
    image_p95_dwell FLOAT DEFAULT 0,
    text_p95_dwell  FLOAT DEFAULT 0,
    preferred_type  TEXT DEFAULT 'text',
    computed_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS post_impressions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    post_id         UUID NOT NULL,
    media_type      TEXT,
    dwell_seconds   FLOAT NOT NULL DEFAULT 0,
    action          TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_impressions_user_created ON post_impressions (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_impressions_post ON post_impressions (post_id);

CREATE TABLE IF NOT EXISTS user_preferences (
    user_id    UUID PRIMARY KEY,
    feed_mode  TEXT NOT NULL DEFAULT 'chronological',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============================================================
-- notification-service
-- ============================================================
CREATE SCHEMA IF NOT EXISTS notify_meta;
CREATE TABLE IF NOT EXISTS notify_meta.event_dedup (
    event_id UUID PRIMARY KEY,
    processed_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS notification_preferences (
    user_id          UUID PRIMARY KEY,
    email_enabled    BOOLEAN NOT NULL DEFAULT TRUE,
    push_enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    sms_enabled      BOOLEAN NOT NULL DEFAULT FALSE,
    quiet_hours_start TIME,
    quiet_hours_end   TIME,
    muted_types      JSONB NOT NULL DEFAULT '[]',
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS user_devices (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL,
    platform   TEXT NOT NULL CHECK (platform IN ('ios', 'android', 'web')),
    push_token TEXT NOT NULL,
    is_active  BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(user_id, platform, push_token)
);
CREATE INDEX IF NOT EXISTS idx_user_devices_user ON user_devices (user_id) WHERE is_active = TRUE;

-- ============================================================
-- search-service
-- ============================================================
CREATE SCHEMA IF NOT EXISTS search;
CREATE TABLE IF NOT EXISTS search.event_dedup (
    event_id UUID PRIMARY KEY,
    processed_at TIMESTAMPTZ NOT NULL
);

-- ============================================================
-- trust-safety-service
-- ============================================================
CREATE SCHEMA IF NOT EXISTS trust;
CREATE TABLE IF NOT EXISTS trust.reports (
    id UUID PRIMARY KEY,
    reporter_id UUID NOT NULL,
    entity_type TEXT NOT NULL,
    entity_id UUID NOT NULL,
    reason TEXT NOT NULL,
    details TEXT,
    status TEXT NOT NULL DEFAULT 'open',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_reports_status_created ON trust.reports (status, created_at DESC);

-- ============================================================
-- monetization-service
-- ============================================================
CREATE TABLE IF NOT EXISTS wallets (
    user_id       UUID PRIMARY KEY,
    balance       DECIMAL(12,2) NOT NULL DEFAULT 0.00,
    lifetime_earnings DECIMAL(12,2) NOT NULL DEFAULT 0.00,
    pending_payout DECIMAL(12,2) NOT NULL DEFAULT 0.00,
    currency      TEXT NOT NULL DEFAULT 'INR',
    is_frozen     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS creator_tiers (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_id       UUID NOT NULL,
    name             TEXT NOT NULL,
    price            DECIMAL(8,2) NOT NULL,
    currency         TEXT NOT NULL DEFAULT 'INR',
    perks            JSONB NOT NULL DEFAULT '[]',
    subscriber_count INTEGER NOT NULL DEFAULT 0,
    is_active        BOOLEAN NOT NULL DEFAULT TRUE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_creator_tiers_creator ON creator_tiers (creator_id);

CREATE TABLE IF NOT EXISTS transactions (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wallet_id      UUID NOT NULL REFERENCES wallets(user_id),
    type           TEXT NOT NULL CHECK (type IN ('earning', 'payout', 'refund', 'adjustment', 'subscription_payment')),
    amount         DECIMAL(12,2) NOT NULL,
    currency       TEXT NOT NULL DEFAULT 'INR',
    status         TEXT NOT NULL DEFAULT 'completed' CHECK (status IN ('pending', 'completed', 'failed', 'reversed')),
    reference_type TEXT NOT NULL DEFAULT '',
    reference_id   TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_transactions_wallet ON transactions (wallet_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_transactions_type ON transactions (wallet_id, type, created_at DESC);

CREATE TABLE IF NOT EXISTS payout_methods (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID NOT NULL,
    method_type      TEXT NOT NULL CHECK (method_type IN ('upi', 'bank_transfer', 'paypal')),
    details_encrypted TEXT NOT NULL,
    is_verified      BOOLEAN NOT NULL DEFAULT FALSE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_payout_methods_user ON payout_methods (user_id);

CREATE TABLE IF NOT EXISTS subscriptions (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    subscriber_id        UUID NOT NULL,
    creator_id           UUID NOT NULL,
    tier_id              UUID NOT NULL REFERENCES creator_tiers(id),
    tier_name            TEXT NOT NULL,
    price                DECIMAL(8,2) NOT NULL,
    currency             TEXT NOT NULL DEFAULT 'INR',
    status               TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'cancelled', 'expired', 'paused')),
    current_period_start TIMESTAMPTZ NOT NULL DEFAULT now(),
    current_period_end   TIMESTAMPTZ NOT NULL DEFAULT now() + INTERVAL '30 days',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_subscriptions_subscriber ON subscriptions (subscriber_id, status);
CREATE INDEX IF NOT EXISTS idx_subscriptions_creator ON subscriptions (creator_id, status);

CREATE TABLE IF NOT EXISTS tax_info (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL UNIQUE,
    country             TEXT NOT NULL,
    tax_data_encrypted  TEXT NOT NULL,
    verification_status TEXT NOT NULL DEFAULT 'pending' CHECK (verification_status IN ('pending', 'verified', 'rejected')),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS monetization_audit_log (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    table_name   TEXT NOT NULL,
    operation    TEXT NOT NULL,
    old_data     JSONB,
    new_data     JSONB,
    performer_id UUID,
    ip_address   TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_audit_log_table ON monetization_audit_log (table_name, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_log_performer ON monetization_audit_log (performer_id, created_at DESC);

-- ============================================================
-- group-service
-- ============================================================
CREATE TABLE IF NOT EXISTS groups (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    description TEXT DEFAULT '',
    avatar_media_id UUID,
    cover_media_id UUID,
    creator_id UUID NOT NULL,
    visibility TEXT NOT NULL DEFAULT 'public' CHECK (visibility IN ('public', 'private')),
    is_archived BOOLEAN NOT NULL DEFAULT FALSE,
    chat_conversation_id UUID,
    member_count BIGINT DEFAULT 0,
    post_count BIGINT DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_groups_creator ON groups(creator_id);
CREATE INDEX IF NOT EXISTS idx_groups_visibility ON groups(visibility) WHERE is_archived = FALSE;
CREATE INDEX IF NOT EXISTS idx_groups_name_search ON groups USING gin(to_tsvector('english', name));

CREATE TABLE IF NOT EXISTS group_members (
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    role TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('admin', 'moderator', 'member')),
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (group_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_group_members_user ON group_members(user_id);

CREATE TABLE IF NOT EXISTS group_invites (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    inviter_id UUID NOT NULL,
    invitee_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'rejected')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(group_id, invitee_id)
);
CREATE INDEX IF NOT EXISTS idx_group_invites_invitee ON group_invites(invitee_id, status);

CREATE TABLE IF NOT EXISTS group_posts (
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    post_id UUID NOT NULL,
    author_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (group_id, post_id)
);
CREATE INDEX IF NOT EXISTS idx_group_posts_group_time ON group_posts(group_id, created_at DESC);

-- ============================================================
-- admin-service
-- ============================================================
CREATE SCHEMA IF NOT EXISTS admin;
CREATE TABLE IF NOT EXISTS admin.audit_log (
    id UUID PRIMARY KEY,
    admin_actor TEXT NOT NULL,
    action TEXT NOT NULL,
    entity_type TEXT NOT NULL,
    entity_id TEXT NOT NULL,
    payload JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS admin.suspensions (
    user_id UUID PRIMARY KEY,
    until TIMESTAMPTZ NOT NULL,
    reason TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ============================================================
-- feature-flag-service
-- ============================================================
CREATE SCHEMA IF NOT EXISTS flags;
CREATE TABLE IF NOT EXISTS flags.flags (
    key TEXT PRIMARY KEY,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    rollout_pct INT NOT NULL DEFAULT 0,
    target_user_ids TEXT[],
    payload JSONB,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ============================================================
-- analytics-service
-- ============================================================
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

CREATE TABLE IF NOT EXISTS analytics.events_raw_2024_01 PARTITION OF analytics.events_raw FOR VALUES FROM ('2024-01-01') TO ('2024-02-01');
CREATE TABLE IF NOT EXISTS analytics.events_raw_default PARTITION OF analytics.events_raw DEFAULT;
CREATE INDEX IF NOT EXISTS idx_events_type_ts ON analytics.events_raw (type, ts DESC);
CREATE INDEX IF NOT EXISTS idx_events_user_ts ON analytics.events_raw (user_id, ts DESC);

-- analytics video indexes
CREATE INDEX IF NOT EXISTS idx_events_raw_content ON analytics.events_raw ((payload->>'content_id'), ts DESC) WHERE type IN ('play_start','milestone','play_end','watch_heartbeat','impression');
CREATE INDEX IF NOT EXISTS idx_events_raw_session ON analytics.events_raw ((payload->>'session_id'), ts DESC) WHERE type IN ('play_start','milestone','play_end','watch_heartbeat');

CREATE TABLE IF NOT EXISTS analytics.content_hourly_agg (
    content_id UUID NOT NULL, hour_bucket TIMESTAMPTZ NOT NULL, creator_id UUID NOT NULL, content_type TEXT NOT NULL,
    impressions BIGINT DEFAULT 0, plays BIGINT DEFAULT 0, views_display BIGINT DEFAULT 0,
    views_1s BIGINT DEFAULT 0, views_3s BIGINT DEFAULT 0, views_10s BIGINT DEFAULT 0,
    views_30s BIGINT DEFAULT 0, views_60s BIGINT DEFAULT 0,
    unique_viewers BIGINT DEFAULT 0, repeat_viewers BIGINT DEFAULT 0,
    watch_time_total_ms BIGINT DEFAULT 0, avg_watch_time_ms DOUBLE PRECISION DEFAULT 0,
    avg_percent_viewed DOUBLE PRECISION DEFAULT 0, completion_rate DOUBLE PRECISION DEFAULT 0,
    rewatch_rate DOUBLE PRECISION DEFAULT 0, skip_rate DOUBLE PRECISION DEFAULT 0, early_swipe_rate DOUBLE PRECISION DEFAULT 0,
    likes BIGINT DEFAULT 0, comments BIGINT DEFAULT 0, shares BIGINT DEFAULT 0, saves BIGINT DEFAULT 0,
    follows_from_content BIGINT DEFAULT 0, not_interested BIGINT DEFAULT 0, reports BIGINT DEFAULT 0, blocks BIGINT DEFAULT 0,
    view_score_total DOUBLE PRECISION DEFAULT 0, vqs_avg DOUBLE PRECISION DEFAULT 0, content_quality_score DOUBLE PRECISION DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (content_id, hour_bucket)
) PARTITION BY RANGE (hour_bucket);

CREATE TABLE IF NOT EXISTS analytics.content_hourly_agg_default PARTITION OF analytics.content_hourly_agg DEFAULT;
CREATE INDEX IF NOT EXISTS idx_hourly_agg_creator ON analytics.content_hourly_agg (creator_id, hour_bucket DESC);
CREATE INDEX IF NOT EXISTS idx_hourly_agg_cqs ON analytics.content_hourly_agg (content_quality_score DESC, hour_bucket DESC);

CREATE TABLE IF NOT EXISTS analytics.content_daily_summary (
    content_id UUID NOT NULL, day_bucket DATE NOT NULL, creator_id UUID NOT NULL, content_type TEXT NOT NULL,
    impressions BIGINT DEFAULT 0, plays BIGINT DEFAULT 0, views_display BIGINT DEFAULT 0,
    unique_viewers BIGINT DEFAULT 0, watch_time_total_ms BIGINT DEFAULT 0,
    avg_watch_time_ms DOUBLE PRECISION DEFAULT 0, avg_percent_viewed DOUBLE PRECISION DEFAULT 0,
    completion_rate DOUBLE PRECISION DEFAULT 0,
    likes BIGINT DEFAULT 0, comments BIGINT DEFAULT 0, shares BIGINT DEFAULT 0, saves BIGINT DEFAULT 0,
    view_score_total DOUBLE PRECISION DEFAULT 0, content_quality_score DOUBLE PRECISION DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (content_id, day_bucket)
);
CREATE INDEX IF NOT EXISTS idx_daily_summary_creator ON analytics.content_daily_summary (creator_id, day_bucket DESC);

-- ============================================================
-- SEED DATA for app db
-- ============================================================
INSERT INTO users (id, username, display_name, first_name, last_name, bio, dob, gender, avatar_media_id, cover_media_id, category, profession, website, location, badge_flags, is_verified, created_at, updated_at) VALUES
    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'johndoe', 'John Doe', 'John', 'Doe', 'Full-stack developer & open-source enthusiast.', '1995-06-15', 'male', '00000000-0000-4000-a000-000000000001', '00000000-0000-4000-a000-000000000002', 'personal', 'Software Engineer', 'https://johndoe.dev', 'San Francisco, CA', 3, TRUE, '2026-01-15T10:00:00Z', '2026-02-01T12:00:00Z'),
    ('a1b2c3d4-e5f6-7890-abcd-ef1234567890', 'janedoe', 'Jane Doe', 'Jane', 'Doe', 'Designer & photographer.', '1998-03-22', 'female', '00000000-0000-4000-a000-000000000003', NULL, 'personal', 'UX Designer', 'https://janedoe.design', 'New York, NY', 1, TRUE, '2026-01-15T10:01:00Z', '2026-01-20T09:00:00Z'),
    ('c3d4e5f6-a1b2-3456-7890-abcdef123456', 'bobsmith', 'Bob Smith', 'Bob', 'Smith', 'Music producer and coffee addict.', '1992-11-08', 'male', NULL, NULL, 'personal', 'Music Producer', NULL, 'Austin, TX', 0, FALSE, '2026-01-15T10:02:00Z', '2026-01-15T10:02:00Z')
ON CONFLICT DO NOTHING;

INSERT INTO user_settings (user_id, account_visibility, allow_messages_from, allow_comments_from, created_at, updated_at) VALUES
    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'public', 'everyone', 'everyone', '2026-01-15T10:00:00Z', '2026-01-15T10:00:00Z'),
    ('a1b2c3d4-e5f6-7890-abcd-ef1234567890', 'public', 'everyone', 'everyone', '2026-01-15T10:01:00Z', '2026-01-15T10:01:00Z'),
    ('c3d4e5f6-a1b2-3456-7890-abcdef123456', 'public', 'followers', 'everyone', '2026-01-15T10:02:00Z', '2026-01-15T10:02:00Z')
ON CONFLICT DO NOTHING;

INSERT INTO follows (follower_id, followee_id, created_at) VALUES
    ('a1b2c3d4-e5f6-7890-abcd-ef1234567890', 'b2e06bd7-fa13-4f05-94cc-8973bcafe892', '2026-01-16T12:00:00Z'),
    ('c3d4e5f6-a1b2-3456-7890-abcdef123456', 'b2e06bd7-fa13-4f05-94cc-8973bcafe892', '2026-01-17T08:00:00Z'),
    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'a1b2c3d4-e5f6-7890-abcd-ef1234567890', '2026-01-16T14:00:00Z')
ON CONFLICT DO NOTHING;

INSERT INTO friends (user_a, user_b, created_at) VALUES
    ('a1b2c3d4-e5f6-7890-abcd-ef1234567890', 'b2e06bd7-fa13-4f05-94cc-8973bcafe892', '2026-01-18T10:00:00Z')
ON CONFLICT DO NOTHING;

INSERT INTO counts (user_id, follower_count, following_count, friend_count, updated_at) VALUES
    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', 2, 2, 1, '2026-02-01T12:00:00Z'),
    ('a1b2c3d4-e5f6-7890-abcd-ef1234567890', 1, 1, 1, '2026-02-01T12:00:00Z'),
    ('c3d4e5f6-a1b2-3456-7890-abcdef123456', 1, 1, 0, '2026-02-01T12:00:00Z')
ON CONFLICT (user_id) DO UPDATE SET follower_count=EXCLUDED.follower_count, following_count=EXCLUDED.following_count, friend_count=EXCLUDED.friend_count, updated_at=EXCLUDED.updated_at;

INSERT INTO media_assets (id, uploader_id, file_type, media_subtype, mime_type, file_size_bytes, storage_bucket, storage_key, processing_status, created_at, updated_at) VALUES
    ('00000000-0000-4000-a000-000000000001', 'b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'image', 'avatar', 'image/jpeg', 52400, 'media', 'avatars/b2e06bd7/avatar.jpg', 'ready', '2026-01-15T10:05:00Z', '2026-01-15T10:05:00Z'),
    ('00000000-0000-4000-a000-000000000002', 'b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'image', 'cover', 'image/jpeg', 204800, 'media', 'covers/b2e06bd7/cover.jpg', 'ready', '2026-01-15T10:06:00Z', '2026-01-15T10:06:00Z'),
    ('00000000-0000-4000-a000-000000000003', 'a1b2c3d4-e5f6-7890-abcd-ef1234567890', 'image', 'avatar', 'image/jpeg', 48000, 'media', 'avatars/a1b2c3d4/avatar.jpg', 'ready', '2026-01-15T10:07:00Z', '2026-01-15T10:07:00Z'),
    ('20000000-0000-4000-a000-000000000001', 'b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'image', 'general', 'image/jpeg', 310000, 'media', 'posts/b2e06bd7/photo1.jpg', 'ready', '2026-01-20T10:00:00Z', '2026-01-20T10:00:00Z'),
    ('20000000-0000-4000-a000-000000000003', 'b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'video', 'general', 'video/mp4', 15000000, 'media', 'posts/b2e06bd7/short1.mp4', 'ready', '2026-01-25T10:00:00Z', '2026-01-25T10:00:00Z'),
    ('20000000-0000-4000-a000-000000000004', 'b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'video', 'general', 'video/mp4', 85000000, 'media', 'posts/b2e06bd7/video1.mp4', 'ready', '2026-02-01T10:00:00Z', '2026-02-01T10:00:00Z')
ON CONFLICT DO NOTHING;

INSERT INTO posts (id, author_id, text, visibility, content_type, is_pinned, created_at, updated_at) VALUES
    ('30000000-0000-4000-a000-000000000001', 'b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'Just shipped a major refactor of our API layer. Clean architecture really pays off!', 'public', 'post', TRUE, '2026-01-20T09:30:00Z', '2026-01-20T09:30:00Z'),
    ('30000000-0000-4000-a000-000000000002', 'b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'Golden Gate Bridge at sunset.', 'public', 'post', FALSE, '2026-01-22T18:45:00Z', '2026-01-22T18:45:00Z'),
    ('30000000-0000-4000-a000-000000000003', 'b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'Quick tip: use cursor-based pagination instead of offset-based.', 'public', 'post', FALSE, '2026-01-24T11:00:00Z', '2026-01-24T11:00:00Z'),
    ('30000000-0000-4000-a000-000000000010', 'a1b2c3d4-e5f6-7890-abcd-ef1234567890', 'New design system is coming together!', 'public', 'post', FALSE, '2026-01-21T14:00:00Z', '2026-01-21T14:00:00Z'),
    ('30000000-0000-4000-a000-000000000020', 'c3d4e5f6-a1b2-3456-7890-abcdef123456', 'New beat dropped! Check out my latest track.', 'public', 'post', FALSE, '2026-02-03T20:00:00Z', '2026-02-03T20:00:00Z')
ON CONFLICT DO NOTHING;

INSERT INTO post_media (post_id, media_id, kind) VALUES
    ('30000000-0000-4000-a000-000000000002', '20000000-0000-4000-a000-000000000001', 'image')
ON CONFLICT DO NOTHING;

INSERT INTO celeb_authors (author_id, is_celeb, updated_at) VALUES
    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', TRUE, '2026-02-01T12:00:00Z')
ON CONFLICT DO NOTHING;
