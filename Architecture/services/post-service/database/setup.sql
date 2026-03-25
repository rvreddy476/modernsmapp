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
