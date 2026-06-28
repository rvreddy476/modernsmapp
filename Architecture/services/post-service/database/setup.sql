-- Database setup for Architecture/post-service

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

-- Polls
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

-- Comments (threaded: parent_id for replies, soft delete support)
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

-- Post Engagement Counts (denormalized counters for analytics + API reads)
CREATE TABLE IF NOT EXISTS post_engagement_counts (
    post_id         UUID PRIMARY KEY REFERENCES posts(id),
    like_count      INTEGER NOT NULL DEFAULT 0,
    comment_count   INTEGER NOT NULL DEFAULT 0,
    share_count     INTEGER NOT NULL DEFAULT 0,
    bookmark_count  INTEGER NOT NULL DEFAULT 0,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Auto-create engagement counts row when a post is inserted
CREATE OR REPLACE FUNCTION create_engagement_counts()
RETURNS TRIGGER AS $$
BEGIN
    INSERT INTO post_engagement_counts (post_id) VALUES (NEW.id);
    RETURN NEW;
END; $$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_create_engagement_counts ON posts;
CREATE TRIGGER trg_create_engagement_counts
    AFTER INSERT ON posts
    FOR EACH ROW EXECUTE FUNCTION create_engagement_counts();

-- Event Processing Log (consumer dedup — prevents double-counting on replay)
CREATE TABLE IF NOT EXISTS engagement_event_log (
    event_id      TEXT PRIMARY KEY,
    event_type    TEXT NOT NULL,
    target_id     UUID NOT NULL,
    processed_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_event_log_age ON engagement_event_log (processed_at);

-- Idempotent schema upgrades — applied on every boot by BootstrapSchema.
-- migration 005: spam-flagging review_status. Added inline so the
-- bootstrap doesn't depend on migrations/005_review_status.sql running
-- first (the constraint ALTER below referenced the column before it
-- existed on a fresh install).
ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS review_status TEXT NOT NULL DEFAULT 'approved';
-- Full posts-table column reconciliation. internal/store/postgres/posts.go
-- references 28+ columns that weren't in the original CREATE TABLE. Each
-- ADD COLUMN IF NOT EXISTS is idempotent so re-runs on existing DBs are
-- safe. Types match the Post struct in posts.go.
ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS no_comments         BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS no_likes            BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS hashtags            TEXT[]  NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS mentions            UUID[]  NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS location_name       TEXT,
    ADD COLUMN IF NOT EXISTS location_lat        DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS location_lng        DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS post_type           TEXT    NOT NULL DEFAULT 'post',
    ADD COLUMN IF NOT EXISTS app_origin          TEXT    NOT NULL DEFAULT 'postbook',
    ADD COLUMN IF NOT EXISTS share_to_postbook   BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS title               TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS tags                TEXT[]  NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS category            TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS language            TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS seo_title           TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS paid_promotion      BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS altered_content     BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS is_made_for_kids    BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS license             TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS allow_embedding     BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS publish_to_feed     BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS remix_setting       TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS comment_moderation  TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS comment_access      TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS recording_date      TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS recording_location  TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS cover_media_id      UUID,
    ADD COLUMN IF NOT EXISTS original_audio_volume REAL  NOT NULL DEFAULT 1.0,
    ADD COLUMN IF NOT EXISTS overlay_audio_volume  REAL  NOT NULL DEFAULT 0.0,
    ADD COLUMN IF NOT EXISTS tier_required_id    UUID;
CREATE INDEX IF NOT EXISTS idx_posts_review_status
    ON posts(review_status)
    WHERE review_status != 'approved';
-- migration 018: allow the 'pending' review state for the video publish gate.
-- migration 024: allow 'needs_changes' (super-admin requested edits; creator loop).
ALTER TABLE posts DROP CONSTRAINT IF EXISTS posts_review_status_check;
ALTER TABLE posts ADD CONSTRAINT posts_review_status_check
    CHECK (review_status IN ('approved', 'flagged', 'rejected', 'pending', 'needs_changes'));

-- migration 015 backfill: post_reposts (Echo) table.
-- BootstrapSchema doesn't run migrations/, so a fresh install needs this here.
CREATE TABLE IF NOT EXISTS post_reposts (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL,
    original_post_id    UUID NOT NULL,
    repost_type         TEXT NOT NULL CHECK (repost_type IN ('plain', 'quote')),
    quote_text          TEXT,
    visibility          TEXT NOT NULL DEFAULT 'public',
    status              TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'undone')),
    source_context_type TEXT CHECK (source_context_type IS NULL OR source_context_type IN ('feed', 'post_detail', 'profile', 'search', 'stash')),
    source_context_id   UUID,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at          TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_post_reposts_active_unique
    ON post_reposts (user_id, original_post_id) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_post_reposts_user
    ON post_reposts (user_id, created_at DESC) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_post_reposts_original
    ON post_reposts (original_post_id, created_at DESC) WHERE status = 'active';
ALTER TABLE post_engagement_counts
    ADD COLUMN IF NOT EXISTS repost_count INT NOT NULL DEFAULT 0;
